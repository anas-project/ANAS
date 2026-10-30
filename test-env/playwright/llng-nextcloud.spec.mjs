// TEST_CASES: LLNG-READY-T-006
import { readFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { expect, test } from '@playwright/test';

const auth = 'https://auth.llngtest.invalid:18443';
const cloud = 'https://cloud.llngtest.invalid:18443';
const credentials = JSON.parse(readFileSync(process.env.ANAS_TEST_CREDENTIAL_FILE, 'utf8')).portal;
function ssh(args, input) {
  if (process.env.ANAS_TEST_SSH_HOST !== 'whl@finance.hlong.wang') throw new Error('explicit finance fixture required');
  return execFileSync('ssh', ['-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10',
    process.env.ANAS_TEST_SSH_HOST, ...args], {timeout: 60_000, encoding: 'utf8', input, stdio: ['pipe', 'pipe', 'pipe']});
}
function directory(action, name) {
  return JSON.parse(ssh(['sudo', 'docker', '-H', 'unix:///run/anas-llng-test-1023.sock', 'exec', 'anas_llng_test_1023_samba',
    'python3', '/fixture/llng-directory-fixture.py', action, name]));
}
function eventStatus() {
  return JSON.parse(ssh(['sudo', 'python3', '/home/whl/anas-llng-ready-test-20261023/llng-event-status.py', '--sessions']));
}
async function current(context, cookie) {
  const headers = {Host: 'cloud.llngtest.invalid:18443', 'OCS-APIRequest': 'true'};
  headers.Cookie = cookie || (await context.cookies(cloud)).map(c => c.name + '=' + c.value).join('; ');
  const r = await context.request.get('https://127.0.0.1:18443/ocs/v2.php/cloud/user?format=json', {headers, maxRedirects: 0});
  const data = await r.json().catch(() => ({}));
  return data?.ocs?.data?.id || '';
}
async function login(browser, name) {
  const started = Date.now();
  const context = await browser.newContext({ignoreHTTPSErrors: true});
  await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
  const page = await context.newPage(); page.setDefaultTimeout(30_000); page.setDefaultNavigationTimeout(90_000);
  await page.goto(cloud + '/apps/user_oidc/login/1', {waitUntil: 'domcontentloaded'});
  const user = page.locator('input[name=user]').first();
  await user.fill(credentials[name].username);
  await page.locator('input[name=password]').first().fill(credentials[name].password);
  await page.locator('form').filter({has: user}).locator('[type=submit]').first().click({noWaitAfter: true});
  await expect.poll(() => new URL(page.url()).origin, {timeout: 60_000}).toBe(cloud);
  await expect.poll(() => current(context), {timeout: 60_000}).toMatch(/^[0-9a-f-]{36}$/);
  const anchor = await current(context);
  const cookie = (await context.cookies(cloud)).map(c => c.name + '=' + c.value).join('; ');
  const iamCookies = await context.cookies(auth);
  expect(iamCookies.some(c => c.name === 'lemonldap')).toBe(true);
  console.log('Actual Nextcloud login and user API completed in ' + (Date.now() - started) + ' ms');
  return {page, context, cookie, anchor, iamCookies};
}

for (const origin of ['application', 'provider']) {
  test(`real Nextcloud OIDC ${origin} logout ends the correct application and LLNG session`, async ({browser}) => {
    const first = await login(browser, 'portalnormal');
    const peer = await login(browser, 'portalother');
    const sameUser = await login(browser, 'portalnormal');
    try {
      const nativeSession = () => JSON.parse(ssh(['sudo', 'python3', '/home/whl/anas-llng-ready-test-20261023/llng-event-status.py', '--session-client'],
        JSON.stringify({cookie: first.iamCookies.find(c => c.name === 'lemonldap').value})));
      console.log('Native client registration before logout: ' + JSON.stringify(nativeSession()));
      expect(nativeSession().clients).toContain('nextcloud');
      let logoutStatePresent = false;
      first.page.on('request', request => {
        const url = new URL(request.url());
        if (url.origin === auth && url.searchParams.has('post_logout_redirect_uri')) {
          logoutStatePresent = Boolean(url.searchParams.get('state'));
        }
      });
      await peer.page.goto('about:blank'); await sameUser.page.goto('about:blank');
      if (origin === 'application') {
        await first.page.waitForFunction(() => window.OC?.generateUrl);
        const path = await first.page.evaluate(() => window.OC?.generateUrl('/apps/user_oidc/sls'));
        expect(path).toBeTruthy(); await first.page.goto(new URL(path, cloud).href);
      } else await first.page.goto(auth + '/?logout=1');
      await expect.poll(() => current(first.context, first.cookie), {timeout: 30_000}).toBe('');
      expect(await current(peer.context, peer.cookie)).toBe(peer.anchor);
      expect(await current(sameUser.context, sameUser.cookie)).toBe(first.anchor);
      // Replay the original IAM cookie, rather than only observing its removal
      // in the browser. The old central session must no longer authenticate.
      await first.context.addCookies(first.iamCookies);
      await first.page.goto(auth + '/?lmAuth=1_Password');
      await expect(first.page.locator('input[name=user]').first()).toBeVisible();
      console.log('Real OIDC ' + origin + ' logout revoked the old cookie while another session and user remained authenticated');
      if (origin === 'application') console.log('Native RP logout callback state present: ' + logoutStatePresent);
    } finally {await first.context.close(); await peer.context.close(); await sameUser.context.close();}
  });
}

test('Samba event sends real Nextcloud OIDC background logout while another client is unsupported', async ({browser}) => {
  directory('enable', 'portalnormal');
  const affected = await login(browser, 'portalnormal');
  const peer = await login(browser, 'portalother');
  try {
    await affected.page.goto('about:blank'); await peer.page.goto('about:blank');
    const before = Number(eventStatus().producer.last_published_seq);
    directory('disable', 'portalnormal');
    await expect.poll(() => {
      const s = eventStatus();
      // Native grants must disappear, and this specific receiver must have
      // acknowledged its callback. Unsupported clients remain in the queue.
      return s.producer.last_published_seq > before && s.sso_counts.portalnormal === 0
        && (s.notifications.nextcloud?.portalnormal || 0) === 0;
    }, {timeout: 60_000, intervals: [2000]}).toBe(true);
    directory('enable', 'portalnormal');
    expect(await current(affected.context, affected.cookie)).toBe('');
    expect(await current(peer.context, peer.cookie)).toBe(peer.anchor);
    console.log('Actual Nextcloud old cookie revoked by native background notification; unaffected peer preserved');
  } finally {directory('enable', 'portalnormal'); await affected.context.close(); await peer.context.close();}
});
