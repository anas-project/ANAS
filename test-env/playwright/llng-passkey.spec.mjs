// TEST_CASES: LLNG-READY-T-002
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const portal = 'https://auth.llngtest.invalid:18443';
const app = 'https://tasks.llngtest.invalid:18443';

function directory(action) {
  const host = process.env.ANAS_TEST_SSH_HOST;
  if (!host) throw new Error('explicit isolated SSH host required');
  const result = execFileSync('ssh', ['-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', host,
    'sudo', 'docker', '-H', 'unix:///run/anas-llng-test-1023.sock', 'exec', 'anas_llng_test_1023_samba',
    'python3', '/fixture/llng-passkey-fixture.py', action], { encoding: 'utf8', timeout: 30_000 });
  return action === 'delete' ? {} : JSON.parse(result);
}

test('native Passkey enrollment, AD state boundaries and immutable identity', async ({ page, context }) => {
  const credential = JSON.parse(readFileSync(process.env.ANAS_TEST_CREDENTIAL_FILE, 'utf8')).passkey;
  page.setDefaultTimeout(20_000);
  page.setDefaultNavigationTimeout(30_000);
  await context.addInitScript(() => {
    PublicKeyCredential.isConditionalMediationAvailable = async () => false;
  });
  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  const options = { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true,
    isUserVerified: true, automaticPresenceSimulation: true };
  let active, activeIndex;
  const authenticators = [];
  // Native names are limited to 20 characters.
  const deviceNames = ['Primary', 'Backup'].map(name => name + '-' + Date.now().toString(36));
  async function saveAuthenticator() {
    if (Number.isInteger(activeIndex)) {
      authenticators[activeIndex] = (await cdp.send('WebAuthn.getCredentials', { authenticatorId: active })).credentials;
    }
    if (process.env.ANAS_TEST_PASSKEY_FILE && authenticators.length === 2) {
      writeFileSync(process.env.ANAS_TEST_PASSKEY_FILE, JSON.stringify(authenticators), { mode: 0o600 });
    }
  }
  async function newAuthenticator(index) {
    if (active) {
      await saveAuthenticator();
      await cdp.send('WebAuthn.removeVirtualAuthenticator', { authenticatorId: active });
    }
    active = (await cdp.send('WebAuthn.addVirtualAuthenticator', { options })).authenticatorId;
    activeIndex = index;
    if (Number.isInteger(index)) for (const key of authenticators[index]) {
      await cdp.send('WebAuthn.addCredential', { authenticatorId: active, credential: key });
    }
  }
  async function signedIn() {
    return (await context.cookies(portal)).some(cookie => cookie.name === 'lemonldap');
  }
  async function passwordLogin() {
    await context.clearCookies();
    await page.goto(portal + '/?lmAuth=1_Password');
    const user = page.locator('input[name="user"]').first();
    await user.fill(credential.username);
    await page.locator('input[name="password"]').first().fill(credential.password);
    await page.locator('form').filter({ has: user }).locator('[type="submit"]').first().click();
    await expect.poll(signedIn).toBe(true);
  }
  async function passkeyLogin(allowed) {
    await context.clearCookies();
    await page.goto(portal + '/?lmAuth=2_Passkey');
    const form = page.locator('form').filter({ has: page.locator('#webauthnbutton') });
    const response = page.waitForNavigation({ waitUntil: 'domcontentloaded' });
    await form.locator('#webauthnbutton').click();
    await response;
    if (allowed) await expect.poll(signedIn).toBe(true);
    else {
      await expect(page.locator('[trmsg]:visible').first()).toBeVisible();
      expect(await signedIn()).toBe(false);
    }
    await saveAuthenticator();
  }
  async function applicationIdentity() {
    await cdp.send('Storage.clearDataForOrigin', { origin: app, storageTypes: 'local_storage' });
    await page.goto(app);
    await expect.poll(async () => {
      if (new URL(page.url()).hostname !== 'tasks.llngtest.invalid') return 0;
      return page.evaluate(async () => {
        const token = localStorage.getItem('token');
        if (!token) return 0;
        const r = await fetch('/api/v1/user', { headers: { Authorization: `Bearer ${token}` } });
        return r.ok ? (await r.json()).id : 0;
      }).catch(() => 0); // The app can leave this document during automatic SSO.
    }).toBeGreaterThan(0);
    return page.evaluate(async () => {
      const r = await fetch('/api/v1/user', { headers: { Authorization: `Bearer ${localStorage.getItem('token')}` } });
      return (await r.json()).id;
    });
  }
  directory('normal');
  await passwordLogin();
  for (const name of deviceNames) {
    await newAuthenticator();
    const registered = page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/webauthn/registration'));
    await page.goto(portal + '/2fregisters/webauthn');
    await page.locator('#keyName').fill(name);
    expect((await registered).status()).toBe(200);
    await page.waitForURL(url => url.pathname === '/2fregisters');
    const saved = (await cdp.send('WebAuthn.getCredentials', { authenticatorId: active })).credentials;
    expect(saved).toHaveLength(1);
    authenticators.push(saved);
    activeIndex = authenticators.length - 1;
  }
  // Only the disposable authenticator keys are persisted, outside Git, for the
  // later empty-environment restore test. No SSO cookie or user token is saved.
  if (process.env.ANAS_TEST_PASSKEY_FILE) writeFileSync(process.env.ANAS_TEST_PASSKEY_FILE,
    JSON.stringify(authenticators), { mode: 0o600 });
  console.log('Primary and backup Passkey enrollment passed');
  await newAuthenticator(0);
  await passkeyLogin(true);
  const id = await applicationIdentity();
  console.log('Passkey first-factor and real OIDC callback passed');
  expect(directory('reset').password_reset).toBe(true);
  await passkeyLogin(true);
  expect(await applicationIdentity()).toBe(id);
  directory('normal');
  directory('rename');
  await passkeyLogin(true);
  expect(await applicationIdentity()).toBe(id);
  for (const state of ['disabled', 'locked', 'account-expired']) {
    directory('normal');
    const result = directory(state);
    if (state === 'locked') expect(result.flags & 16).toBe(16);
    await passkeyLogin(false);
    console.log('Directory denial passed: ' + state);
  }
  directory('normal');
  // Use the backup device alone, with the primary authenticator absent.
  await newAuthenticator(1);
  await passkeyLogin(true);
  expect(await applicationIdentity()).toBe(id);
  directory('expired');
  await expect.poll(() => directory('expired').flags & 8388608, { timeout: 75_000, intervals: [5000] }).toBe(8388608);
  await passkeyLogin(true);
  expect(await applicationIdentity()).toBe(id);
  directory('normal');
  await page.goto(portal + '/2fregisters');
  const primaryRow = page.locator('tr').filter({ has: page.locator('input[value="' + deviceNames[0] + '"]') });
  await primaryRow.locator('[data-target="#remove2fModal"]').click();
  await page.locator('#remove2fModal .remove2f').click();
  await expect(primaryRow).toHaveCount(0);
  await newAuthenticator(0);
  await passkeyLogin(false);
  await newAuthenticator(1);
  await passkeyLogin(true);
  expect(await applicationIdentity()).toBe(id);
  await passwordLogin();
  console.log('Revoked device rejected; backup Passkey and AD password recovery passed');
});
