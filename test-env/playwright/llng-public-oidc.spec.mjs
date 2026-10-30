// TEST_CASES: LLNG-READY-T-003
import { createHash, createPublicKey, randomBytes, verify } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const issuer = 'https://auth.llngtest.invalid:18443/';
const origin = issuer.slice(0, -1);
const application = 'https://tasks.llngtest.invalid:18443';

test('native public S256, signed authentication time, mandatory interaction and live self-account', async ({ page, context }) => {
  page.setDefaultTimeout(20_000);
  page.setDefaultNavigationTimeout(30_000);
  const credential = JSON.parse(readFileSync(process.env.ANAS_TEST_CREDENTIAL_FILE, 'utf8')).passkey;
  await context.addInitScript(() => { PublicKeyCredential.isConditionalMediationAvailable = async () => false; });
  await context.route('**/*', route => {
    const url = new URL(route.request().url());
    if (url.origin !== application) return route.continue();
    return route.fulfill({ status: 200, contentType: 'text/html', body: '<p>Protocol callback received</p>' });
  });
  function directory(action) {
    if (!process.env.ANAS_TEST_SSH_HOST) throw new Error('explicit isolated SSH host required');
    execFileSync('ssh', ['-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', process.env.ANAS_TEST_SSH_HOST,
      'sudo', 'docker', '-H', 'unix:///run/anas-llng-test-1023.sock', 'exec', 'anas_llng_test_1023_samba',
      'python3', '/fixture/llng-passkey-fixture.py', action], { timeout: 30_000, stdio: 'pipe' });
  }
  const api = async (path, options = {}) => context.request.fetch('https://127.0.0.1:18443' + path,
    { ...options, headers: { Host: 'auth.llngtest.invalid:18443', ...options.headers } });
  const discoveryResponse = await api('/.well-known/openid-configuration');
  expect(discoveryResponse.status()).toBe(200);
  const discovery = await discoveryResponse.json();
  expect(discovery.issuer).toBe(issuer);
  const keysResponse = await api(new URL(discovery.jwks_uri).pathname);
  const keys = (await keysResponse.json()).keys;
  function flow(reauth = false) {
    const f = { client: reauth ? 'user-portal-reauth' : 'user-portal', reauth,
      redirect: application + '/oidc/' + (reauth ? 'reauth-callback' : 'callback'),
      state: randomBytes(20).toString('hex'), nonce: randomBytes(20).toString('hex'), verifier: randomBytes(32).toString('base64url') };
    f.query = new URLSearchParams({ client_id: f.client, redirect_uri: f.redirect, response_type: 'code', scope: 'openid profile email',
      state: f.state, nonce: f.nonce, code_challenge_method: 'S256', code_challenge: createHash('sha256').update(f.verifier).digest('base64url'), lmAuth: '1_Password' });
    if (reauth) { f.query.set('prompt', 'login'); f.query.set('max_age', '0'); }
    return f;
  }
  async function authorize(f, interact) {
    await page.goto(origin + '/oauth2/authorize?' + f.query);
    if (interact) {
      // A fresh cookie must not skip this real credential interaction.
      const user = page.locator('input[name="user"]').first();
      await expect(user).toBeVisible();
      expect(new URL(page.url()).hostname).toBe('auth.llngtest.invalid');
      await user.fill(credential.username);
      await page.locator('input[name="password"]').first().fill(credential.password);
      await page.locator('form').filter({ has: user }).locator('[type="submit"]').first().click();
    }
    await page.waitForURL(url => url.origin + url.pathname === f.redirect && url.searchParams.has('code'));
    const callback = new URL(page.url());
    expect(callback.searchParams.get('state') === f.state).toBe(true);
    return callback.searchParams.get('code');
  }
  async function tokens(f, code, verifier = f.verifier) {
    return api('/oauth2/token', { method: 'POST', form: { client_id: f.client, code, grant_type: 'authorization_code', redirect_uri: f.redirect, code_verifier: verifier } });
  }
  async function signedClaims(response, f) {
    expect(response.status()).toBe(200);
    const data = await response.json();
    expect(typeof data.id_token).toBe('string');
    const [header, payload, signature] = data.id_token.split('.');
    const h = JSON.parse(Buffer.from(header, 'base64url'));
    const claims = JSON.parse(Buffer.from(payload, 'base64url'));
    expect(h.alg).toBe('RS256');
    const key = keys.find(key => key.kid === h.kid);
    expect(Boolean(key)).toBe(true);
    expect(verify('RSA-SHA256', Buffer.from(header + '.' + payload), createPublicKey({ key, format: 'jwk' }), Buffer.from(signature, 'base64url'))).toBe(true);
    expect(claims.iss).toBe(issuer);
    expect([claims.aud].flat()).toContain(f.client);
    expect(claims.nonce).toBe(f.nonce);
    expect(claims.auth_time).toBeGreaterThan(0);
    return { claims, access: data.access_token };
  }
  directory('normal');
  const initial = flow();
  const first = await signedClaims(await tokens(initial, await authorize(initial, true)), initial);
  const renew = flow(true), started = Math.floor(Date.now() / 1000);
  const renewed = await signedClaims(await tokens(renew, await authorize(renew, true)), renew);
  expect(renewed.claims.sub).toBe(first.claims.sub);
  expect(renewed.claims.auth_time).toBeGreaterThanOrEqual(started - 1);
  const account = await api('/anas-account', { headers: { Authorization: 'Bearer ' + renewed.access } });
  expect(account.status()).toBe(200);
  const facts = await account.json();
  expect(facts.schema).toBe('llng-account/v1');
  expect(facts.subject).toBe(first.claims.sub);
  expect(facts.enabled).toBe(true);
  expect(facts.name).toBe(credential.username);
  expect((await api('/anas-account')).status()).toBe(401);
  console.log('Real fresh-password interaction, signed auth_time and restricted self-account passed');
  for (const method of ['plain', 'missing']) {
    const bad = flow();
    if (method === 'plain') bad.query.set('code_challenge_method', 'plain');
    else { bad.query.delete('code_challenge_method'); bad.query.delete('code_challenge'); }
    await page.goto(origin + '/oauth2/authorize?' + bad.query);
    expect(new URL(page.url()).hostname).toBe('auth.llngtest.invalid');
    expect(new URL(page.url()).searchParams.has('code')).toBe(false);
    await expect(page.locator('[trmsg]:visible').first()).toBeVisible();
  }
  const wrong = flow();
  expect((await tokens(wrong, await authorize(wrong, false), randomBytes(32).toString('base64url'))).status()).toBe(400);
  directory('disabled');
  try {
    expect((await api('/anas-account', { headers: { Authorization: 'Bearer ' + renewed.access } })).status()).toBe(403);
    const denied = flow();
    await page.goto(origin + '/oauth2/authorize?' + denied.query);
    expect(new URL(page.url()).hostname).toBe('auth.llngtest.invalid');
    await expect(page.locator('[trmsg]:visible').first()).toBeVisible();
  } finally { directory('normal'); }
  console.log('PKCE downgrade/wrong verifier and disabled-directory old-cookie boundaries passed');
});
