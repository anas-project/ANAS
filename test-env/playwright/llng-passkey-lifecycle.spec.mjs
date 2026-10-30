// TEST_CASES: LLNG-READY-T-002
import {readFileSync} from 'node:fs';
import {execFileSync} from 'node:child_process';
import {expect, test} from '@playwright/test';

const auth = 'https://auth.llngtest.invalid:18443';
const app = 'https://tasks.llngtest.invalid:18443';
const credential = JSON.parse(readFileSync(process.env.ANAS_TEST_CREDENTIAL_FILE, 'utf8')).passkey;
function ssh(args) {
  if (process.env.ANAS_TEST_SSH_HOST !== 'whl@finance.hlong.wang') throw new Error('explicit isolated finance host required');
  return execFileSync('ssh', ['-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10',
    process.env.ANAS_TEST_SSH_HOST, ...args], {encoding: 'utf8', timeout: 60_000, stdio: 'pipe'});
}
function directory(action) {
  ssh(['sudo', 'docker', '-H', 'unix:///run/anas-llng-test-1023.sock', 'exec', 'anas_llng_test_1023_samba',
    'python3', '/fixture/llng-passkey-fixture.py', action]);
}
function status() {
  return JSON.parse(ssh(['sudo', 'python3', '/home/whl/anas-llng-ready-test-20261023/llng-event-status.py']));
}

test('directory deletion rejects the old Passkey and a reused username must enroll a new credential', async ({page, context}) => {
  await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
  const cdp = await context.newCDPSession(page); await cdp.send('WebAuthn.enable');
  const options = {protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true,
    isUserVerified: true, automaticPresenceSimulation: true};
  let device = (await cdp.send('WebAuthn.addVirtualAuthenticator', {options})).authenticatorId;
  const signedIn = async () => (await context.cookies(auth)).some(c => c.name === 'lemonldap');
  async function enroll(prefix) {
    await context.clearCookies(); await page.goto(auth + '/?lmAuth=1_Password');
    const user = page.locator('input[name=user]').first(); await user.fill(credential.username);
    await page.locator('input[name=password]').first().fill(credential.password);
    await page.locator('form').filter({has: user}).locator('[type=submit]').first().click();
    await expect.poll(signedIn).toBe(true);
    const registered = page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/webauthn/registration'));
    await page.goto(auth + '/2fregisters/webauthn');
    await page.locator('#keyName').fill(prefix + '-' + Date.now().toString(36));
    expect((await registered).status()).toBe(200);
    await page.waitForURL(url => url.pathname === '/2fregisters');
  }
  async function passkey(allowed) {
    await context.clearCookies(); await page.goto(auth + '/?lmAuth=2_Passkey');
    await page.waitForFunction(() => window.jQuery?._data(document.getElementById('webauthnbutton'), 'events')?.click?.length > 0);
    await Promise.all([page.waitForNavigation({waitUntil: 'domcontentloaded'}), page.locator('#webauthnbutton').click()]);
    if (allowed) await expect.poll(signedIn).toBe(true);
    else expect(await signedIn()).toBe(false);
  }
  async function identity() {
    await cdp.send('Storage.clearDataForOrigin', {origin: app, storageTypes: 'local_storage'});
    await page.goto(app);
    const current = () => page.evaluate(async () => {
      const token = localStorage.getItem('token');
      if (!token) return 0;
      const r = await fetch('/api/v1/user', {headers: {Authorization: 'Bearer ' + token}});
      return r.ok ? (await r.json()).id : 0;
    }).catch(() => 0);
    await expect.poll(current, {timeout: 30_000}).toBeGreaterThan(0);
    return current();
  }
  // The producer subscribes to OU=People. The original password-only fixture
  // lived in CN=Users and must not be used to assert this event subscription.
  directory('scope'); directory('normal');
  await enroll('BeforeDelete');
  await passkey(true);
  const oldIdentity = await identity();
  const oldCookies = await context.cookies(auth);
  await page.goto('about:blank');
  const before = status().producer.last_published_seq;
  console.log('Scoped account and newly enrolled Passkey baseline accepted');
  let deleted = false;
  try {
    directory('delete'); deleted = true;
    await expect.poll(() => {
      const s = status();
      return s.producer.last_published_seq > before && s.consumer.pending === 0
        && Number(s.cursor.cursor) >= s.producer.last_published_seq;
    }, {timeout: 60_000, intervals: [2000]}).toBe(true);
    console.log('Real deletion event acknowledged');
    await context.addCookies(oldCookies);
    await page.goto(auth + '/?lmAuth=1_Password');
    await expect(page.locator('input[name=user]').first()).toBeVisible();
    await passkey(false);
    directory('create'); deleted = false;
    await passkey(false);
    await cdp.send('WebAuthn.removeVirtualAuthenticator', {authenticatorId: device});
    device = (await cdp.send('WebAuthn.addVirtualAuthenticator', {options})).authenticatorId;
    await enroll('Replacement');
    await passkey(true);
    expect(await identity()).not.toBe(oldIdentity);
    console.log('Real AD deletion revoked the central session; old key rejected before and after username reuse; fresh enrollment has a new application identity');
  } finally {
    if (deleted) directory('create');
    directory('normal');
  }
});
