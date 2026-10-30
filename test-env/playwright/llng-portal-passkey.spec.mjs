// TEST_CASES: LLNG-READY-T-002
import {execFileSync} from 'node:child_process';
import {readFileSync, writeFileSync} from 'node:fs';
import {expect, test} from '@playwright/test';

const auth = 'https://auth.llngtest.invalid:18443';
const portal = 'https://portal.llngtest.invalid:18443';
function directory(action) {
  if (process.env.ANAS_TEST_SSH_HOST !== 'whl@finance.hlong.wang') throw new Error('explicit finance test host required');
  return JSON.parse(execFileSync('ssh', ['-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10',
    process.env.ANAS_TEST_SSH_HOST, 'sudo', 'docker', '-H', 'unix:///run/anas-llng-test-1023.sock', 'exec',
    'anas_llng_test_1023_samba', 'python3', '/fixture/llng-directory-fixture.py', action, 'portalnormal'],
    {encoding: 'utf8', timeout: 30_000, stdio: ['ignore', 'pipe', 'pipe']}));
}
async function api(page, path, body) {
  return page.evaluate(async ({path, body}) => {
    const bootstrap = await fetch('/api/bootstrap').then(r => r.json());
    const response = await fetch(path, {method: body === undefined ? 'GET' : 'POST',
      headers: {'Content-Type': 'application/json', 'X-CSRF-Token': bootstrap.csrf},
      body: body === undefined ? undefined : JSON.stringify(body)});
    return {status: response.status, data: await response.json()};
  }, {path, body});
}

test('actual portal accepts verified Passkey during mandatory change and expiry, including fresh authentication', async ({page, context}) => {
  const credential = JSON.parse(readFileSync(process.env.ANAS_TEST_CREDENTIAL_FILE, 'utf8')).portal.portalnormal;
  page.setDefaultTimeout(30_000);
  await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  const {authenticatorId} = await cdp.send('WebAuthn.addVirtualAuthenticator', {options: {
    protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true,
    isUserVerified: true, automaticPresenceSimulation: true,
  }});
  async function passkeyInteraction() {
    await page.locator('a[href="#id_2_Passkey"]').click();
    // The native portal attaches this handler only after its asynchronous
    // portalLoaded event, later than the form becoming visible on renewsession.
    await page.waitForFunction(() => (window.jQuery?._data(document.getElementById('webauthnbutton'), 'events')?.click || []).length > 0);
    await page.locator('#webauthnbutton').click();
    await page.waitForURL(url => url.origin === portal && url.pathname === '/', {waitUntil: 'domcontentloaded'});
    const me = await api(page, '/api/me');
    expect(me.status).toBe(200); expect(me.data.passkey_authenticated).toBe(true);
    return me.data;
  }
  async function login() {
    await context.clearCookies();
    await page.goto(portal + '/login');
    return passkeyInteraction();
  }
  directory('normal_password_age'); directory('restore_password'); directory('password_current');
  directory('enable'); directory('unlocked'); directory('add_portal');
  try {
    await page.goto(auth + '/?lmAuth=1_Password');
    const user = page.locator('input[name=user]').first(); await user.fill(credential.username);
    await page.locator('input[name=password]').first().fill(credential.password);
    await page.locator('form').filter({has: user}).locator('[type=submit]').first().click();
    await expect.poll(async () => (await context.cookies(auth)).some(c => c.name === 'lemonldap')).toBe(true);
    const registered = page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/webauthn/registration'));
    await page.goto(auth + '/2fregisters/webauthn');
    await page.locator('#keyName').fill('Portal-' + Date.now().toString(36));
    expect((await registered).status()).toBe(200);
    await page.waitForURL(url => url.pathname === '/2fregisters');
    const original = await login();
    console.log('Verified Passkey reached the actual portal');
    expect(directory('force_change').password_reset).toBe(true);
    const reset = await login();
    expect(reset.person.anchor).toBe(original.person.anchor);
    expect(reset.person.must_change_password).toBe(true);
    console.log('AD mandatory password change still permits the portal Passkey');
    const profile = {given_name: 'Passkey', surname: 'Acceptance', display_name: reset.person.display_name};
    expect((await api(page, '/api/me/profile', {profile, profile_version: reset.profile_version})).status).toBe(200);
    await expect.poll(async () => (await api(page, '/api/me')).data.recent_authentication,
      {timeout: 40_000, intervals: [2000]}).toBe(false);
    await page.evaluate(async () => {
      const b = await fetch('/api/bootstrap').then(r => r.json());
      const form = document.createElement('form'); form.method = 'POST'; form.action = '/api/reauth';
      const csrf = document.createElement('input'); csrf.name = 'csrf'; csrf.value = b.csrf;
      form.append(csrf); document.body.append(form); form.submit();
    });
    const fresh = await passkeyInteraction();
    expect(fresh.person.anchor).toBe(original.person.anchor); expect(fresh.recent_authentication).toBe(true);
    directory('password_current'); directory('short_password_age');
    await expect.poll(() => directory('state').flags & 8388608, {timeout: 75_000, intervals: [5000]}).toBe(8388608);
    const expired = await login();
    expect(expired.person.password_expired).toBe(true);
    expect(expired.person.anchor).toBe(original.person.anchor);
    console.log('Actual portal signed Passkey evidence, forced-change/expiry admission, profile write and fresh Passkey authentication passed');
  } finally {
    directory('normal_password_age'); directory('password_current'); directory('restore_profile');
    if (process.env.ANAS_TEST_PASSKEY_FILE) {
      const keys = (await cdp.send('WebAuthn.getCredentials', {authenticatorId})).credentials;
      writeFileSync(process.env.ANAS_TEST_PASSKEY_FILE, JSON.stringify(keys), {mode: 0o600});
    }
  }
});
