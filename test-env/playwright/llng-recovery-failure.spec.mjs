// TEST_CASES: LLNG-READY-T-005
import {execFileSync} from 'node:child_process';
import {readFileSync, writeFileSync} from 'node:fs';
import {expect, test} from '@playwright/test';

const portal = 'https://portal.llngtest.invalid:18443';
const keyFile = process.env.ANAS_TEST_PORTAL_PASSKEY_FILE;
async function identity(browser) {
  const context = await browser.newContext({ignoreHTTPSErrors: true});
  await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
  const page = await context.newPage(); page.setDefaultTimeout(30_000); page.setDefaultNavigationTimeout(60_000);
  const cdp = await context.newCDPSession(page); await cdp.send('WebAuthn.enable');
  const authenticatorId = (await cdp.send('WebAuthn.addVirtualAuthenticator', {options: {
    protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true,
    isUserVerified: true, automaticPresenceSimulation: true}})).authenticatorId;
  for (const key of JSON.parse(readFileSync(keyFile, 'utf8')))
    await cdp.send('WebAuthn.addCredential', {authenticatorId, credential: key});
  try {
    await page.goto(portal + '/login');
    await page.locator('a[href="#id_2_Passkey"]').click();
    await page.waitForFunction(() => window.jQuery?._data(document.getElementById('webauthnbutton'), 'events')?.click?.length > 0);
    await page.locator('#webauthnbutton').click();
    await page.waitForURL(url => url.origin === portal && url.pathname === '/');
    const me = await page.evaluate(async () => (await fetch('/api/me')).json());
    expect(me.person.username).toBe('portalnormal'); expect(me.passkey_authenticated).toBe(true);
    const response = await context.request.get('https://127.0.0.1:18443/oauth2/jwks', {headers: {Host: 'auth.llngtest.invalid:18443'}});
    expect(response.status()).toBe(200);
    return {anchor: me.person.anchor, keys: (await response.json()).keys.map(({kid, n, e, kty}) => ({kid, n, e, kty}))};
  } finally {
    writeFileSync(keyFile, JSON.stringify((await cdp.send('WebAuthn.getCredentials', {authenticatorId})).credentials), {mode: 0o600});
    await context.close();
  }
}

test('damaged recovery archives leave the original identity, signing key and existing Passkey usable', async ({browser}) => {
  const before = await identity(browser);
  if (process.env.ANAS_TEST_SSH_HOST !== 'whl@finance.hlong.wang') throw new Error('isolated finance fixture required');
  const report = JSON.parse(execFileSync('ssh', ['-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10',
    process.env.ANAS_TEST_SSH_HOST, 'sudo', 'python3', '/home/whl/anas-llng-ready-test-20261023/llng-recovery-fixture.py', 'failed-restore'],
    {encoding: 'utf8', timeout: 480_000, stdio: 'pipe'}));
  expect(report).toMatchObject({checksum_rejected: true, native_import_rejected: true,
    existing_target_rejected: true, original_tables_preserved: true, original_configuration_preserved: true});
  expect(await identity(browser)).toEqual(before);
  console.log('Checksum guard and native import rejected damage; original tables/configuration and actual portal Passkey/identity/signing key remained usable');
});
