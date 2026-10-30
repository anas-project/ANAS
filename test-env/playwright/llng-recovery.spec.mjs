// TEST_CASES: LLNG-READY-T-005
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const auth = 'https://auth.llngtest.invalid:18443';
const portal = 'https://portal.llngtest.invalid:18443';
const app = 'https://tasks.llngtest.invalid:18443';
const mode = process.env.ANAS_TEST_RECOVERY_MODE;
if (!['baseline', 'restored'].includes(mode)) throw new Error('explicit recovery mode required');
const stateFile = process.env.ANAS_TEST_RECOVERY_STATE_FILE;
if (!stateFile) throw new Error('private recovery baseline file required');
const options = {protocol: 'ctap2', transport: 'internal', hasResidentKey: true,
  hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true};

async function setup(browser, file, index) {
  const saved = JSON.parse(readFileSync(file, 'utf8'));
  const keys = index === undefined ? saved : saved[index];
  const context = await browser.newContext({ignoreHTTPSErrors: true});
  await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
  const page = await context.newPage();
  const cdp = await context.newCDPSession(page); await cdp.send('WebAuthn.enable');
  const authenticatorId = (await cdp.send('WebAuthn.addVirtualAuthenticator', {options})).authenticatorId;
  for (const key of keys) await cdp.send('WebAuthn.addCredential', {authenticatorId, credential: key});
  return {context, page, cdp, authenticatorId, async close() {
    const current = (await cdp.send('WebAuthn.getCredentials', {authenticatorId})).credentials;
    if (index === undefined) writeFileSync(file, JSON.stringify(current), {mode: 0o600});
    else {saved[index] = current; writeFileSync(file, JSON.stringify(saved), {mode: 0o600});}
    await context.close();
  }};
}
async function passkey(page, start) {
  await page.goto(start);
  const link = page.locator('a[href="#id_2_Passkey"]');
  if (await link.count()) await link.click();
  await page.waitForFunction(() => window.jQuery?._data(document.getElementById('webauthnbutton'), 'events')?.click?.length > 0);
  await page.locator('#webauthnbutton').click();
}

test('existing Passkeys, immutable application identity and signing keys survive empty-store recovery', async ({browser}) => {
  const result = {};
  const ordinary = await setup(browser, process.env.ANAS_TEST_PORTAL_PASSKEY_FILE);
  try {
    await passkey(ordinary.page, portal + '/login');
    await ordinary.page.waitForURL(url => url.origin === portal && url.pathname === '/');
    const me = await ordinary.page.evaluate(async () => (await fetch('/api/me')).json());
    expect(me.person.username).toBe('portalnormal');
    expect(me.passkey_authenticated).toBe(true);
    result.portalAnchor = me.person.anchor;
    const response = await ordinary.context.request.get('https://127.0.0.1:18443/oauth2/jwks', {headers: {Host: 'auth.llngtest.invalid:18443'}});
    expect(response.status()).toBe(200);
    const keys = (await response.json()).keys.map(({kty, kid, n, e}) => ({kty, kid, n, e})).sort((a, b) => a.kid.localeCompare(b.kid));
    result.publicKeysDigest = createHash('sha256').update(JSON.stringify(keys)).digest('hex');
  } finally {await ordinary.close();}
  const backup = await setup(browser, process.env.ANAS_TEST_PASSKEY_FILE, 1);
  try {
    await passkey(backup.page, auth + '/?lmAuth=2_Passkey');
    await expect.poll(async () => (await backup.context.cookies(auth)).some(c => c.name === 'lemonldap')).toBe(true);
    await backup.page.goto(app);
    await expect.poll(async () => {
      if (new URL(backup.page.url()).origin !== app) return 0;
      return backup.page.evaluate(async () => {
        const token = localStorage.getItem('token'); if (!token) return 0;
        const response = await fetch('/api/v1/user', {headers: {Authorization: 'Bearer ' + token}});
        return response.ok ? (await response.json()).id : 0;
      }).catch(() => 0);
    }).toBeGreaterThan(0);
    result.applicationId = await backup.page.evaluate(async () => (await (await fetch('/api/v1/user',
      {headers: {Authorization: 'Bearer ' + localStorage.getItem('token')}})).json()).id);
  } finally {await backup.close();}
  if (mode === 'baseline') writeFileSync(stateFile, JSON.stringify(result), {mode: 0o600});
  else expect(result).toEqual(JSON.parse(readFileSync(stateFile, 'utf8')));
  console.log('Existing portal and backup Passkeys, actual application identity and public signing keys verified: ' + mode);
});
