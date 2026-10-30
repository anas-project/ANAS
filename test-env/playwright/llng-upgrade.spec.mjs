// TEST_CASES: LLNG-READY-T-008
import {execFileSync} from 'node:child_process';
import {readFileSync} from 'node:fs';
import {expect, test} from '@playwright/test';

const auth = 'https://auth.llngtest.invalid:18443';
const app = 'https://tasks.llngtest.invalid:18443';
const credential = JSON.parse(readFileSync(process.env.ANAS_TEST_CREDENTIAL_FILE, 'utf8')).portal.portalnormal;
function fixture(action) {
  if (process.env.ANAS_TEST_SSH_HOST !== 'whl@finance.hlong.wang') throw new Error('isolated finance fixture required');
  return JSON.parse(execFileSync('ssh', ['-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10',
    process.env.ANAS_TEST_SSH_HOST, 'sudo', 'python3', '/home/whl/anas-llng-ready-test-20261023/llng-upgrade-fixture.py', action],
    {encoding: 'utf8', timeout: 240_000, stdio: 'pipe'}));
}

test('historical 2.23.2 Module upgrades native persistent state to 2.23.4 with the same real application identity', async ({page, context}) => {
  page.setDefaultTimeout(30_000); page.setDefaultNavigationTimeout(60_000);
  await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
  const cdp = await context.newCDPSession(page);
  async function identity() {
    await context.clearCookies();
    await cdp.send('Storage.clearDataForOrigin', {origin: app, storageTypes: 'local_storage'});
    await page.goto(app);
    console.log('Application login landed at ' + new URL(page.url()).origin + new URL(page.url()).pathname);
    const user = page.locator('input[name=user]').first(); await user.fill(credential.username);
    await page.locator('input[name=password]').first().fill(credential.password);
    await page.locator('form').filter({has: user}).locator('[type=submit]').first().click({noWaitAfter: true});
    await page.waitForURL(url => url.origin === app);
    const current = () => page.evaluate(async () => {
      const token = localStorage.getItem('token');
      if (!token) return 0;
      const r = await fetch('/api/v1/user', {headers: {Authorization: 'Bearer ' + token}});
      return r.ok ? (await r.json()).id : 0;
    }).catch(() => 0);
    await expect.poll(current, {timeout: 45_000}).toBeGreaterThan(0);
    return current();
  }
  async function publicKeys() {
    const response = await context.request.get('https://127.0.0.1:18443/oauth2/jwks', {headers: {Host: 'auth.llngtest.invalid:18443'}});
    expect(response.status()).toBe(200);
    return (await response.json()).keys.map(({kid, n, e, kty}) => ({kid, n, e, kty}));
  }
  let started = false;
  try {
    started = true;
    const old = fixture('start-old'); expect(old.version).toBe('2.23.2');
    console.log('Historical Module baseline: ' + JSON.stringify(old));
    const oldID = await identity(), keys = await publicKeys();
    console.log('Old-version application identity established');
    const iamCookies = await context.cookies(auth);
    expect(iamCookies.some(c => c.name === 'lemonldap')).toBe(true);
    await page.goto('about:blank');
    const promoted = fixture('promote');
    expect(promoted.version).toBe('2.23.4'); expect(promoted.tables_preserved).toBe(true);
    expect(await publicKeys()).toEqual(keys);
    await context.addCookies(iamCookies);
    await page.goto(auth + '/?lmAuth=1_Password');
    await expect(page.locator('input[name=user]')).toHaveCount(0);
    expect(await identity()).toBe(oldID);
    console.log('Upgrade preserved table contents, old central session, public signing key and actual Vikunja identity: ' + JSON.stringify(promoted));
  } finally {
    if (started) console.log(JSON.stringify(fixture('restore')));
  }
});
