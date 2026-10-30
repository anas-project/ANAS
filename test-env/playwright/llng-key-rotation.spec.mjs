// TEST_CASES: LLNG-READY-T-005
import {createHash, createPublicKey, randomBytes, verify} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {readFileSync} from 'node:fs';
import {expect, test} from '@playwright/test';

const auth = 'https://auth.llngtest.invalid:18443';
const credential = JSON.parse(readFileSync(process.env.ANAS_TEST_CREDENTIAL_FILE, 'utf8')).portal.portalnormal;
const newKid = 'llng-test-rotated-20261023';
function rotate(action) {
  if (process.env.ANAS_TEST_SSH_HOST !== 'whl@finance.hlong.wang') throw new Error('isolated finance fixture required');
  execFileSync('ssh', ['-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10',
    process.env.ANAS_TEST_SSH_HOST, 'sudo', 'python3', '/home/whl/anas-llng-ready-test-20261023/llng-rotation-fixture.py', action],
    {timeout: 60_000, stdio: 'pipe'});
}
async function password(page) {
  const user = page.locator('input[name=user]').first();
  await user.fill(credential.username);
  await page.locator('input[name=password]').first().fill(credential.password);
  await page.locator('form').filter({has: user}).locator('[type=submit]').first().click({noWaitAfter: true});
}
function valid(token, keys) {
  const [header, payload, signature] = token.split('.');
  const h = JSON.parse(Buffer.from(header, 'base64url'));
  const key = keys.find(k => k.kid === h.kid);
  return h.alg === 'RS256' && Boolean(key) && verify('RSA-SHA256', Buffer.from(header + '.' + payload),
    createPublicKey({key, format: 'jwk'}), Buffer.from(signature, 'base64url'));
}

test('native OIDC key publication, promotion, retirement boundary and rollback preserve real consumers', async ({browser}) => {
  const context = await browser.newContext({ignoreHTTPSErrors: true, serviceWorkers: 'block'});
  await context.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
  const page = await context.newPage();
  await page.route('https://tasks.llngtest.invalid:18443/**', route => route.fulfill({contentType: 'text/html', body: '<p>Protocol callback received</p>'}));
  const api = (path, options = {}) => context.request.fetch('https://127.0.0.1:18443' + path,
    {...options, headers: {Host: 'auth.llngtest.invalid:18443', ...options.headers}});
  const jwks = async () => (await (await api('/oauth2/jwks')).json()).keys;
  async function issue() {
    await context.clearCookies();
    const verifier = randomBytes(32).toString('base64url'), state = randomBytes(20).toString('hex'), nonce = randomBytes(20).toString('hex');
    const redirect = 'https://tasks.llngtest.invalid:18443/oidc/callback';
    const query = new URLSearchParams({client_id: 'user-portal', redirect_uri: redirect, response_type: 'code',
      scope: 'openid profile', state, nonce, lmAuth: '1_Password', code_challenge_method: 'S256',
      code_challenge: createHash('sha256').update(verifier).digest('base64url')});
    const observe = request => {
      const url = new URL(request.url());
      if (url.pathname === '/oauth2/authorize' && url.searchParams.has('state')) console.log('Authorization state matches requested flow: ' + (url.searchParams.get('state') === state));
    };
    page.on('request', observe);
    await page.goto(auth + '/oauth2/authorize?' + query); await password(page);
    await page.waitForURL(url => url.origin + url.pathname === redirect && url.searchParams.has('code'));
    const callback = new URL(page.url());
    page.off('request', observe);
    console.log('OIDC callback state check: ' + JSON.stringify({present: callback.searchParams.has('state'),
      matches: callback.searchParams.get('state') === state, occurrences: callback.searchParams.getAll('state').length}));
    expect(callback.searchParams.get('state') === state).toBe(true);
    const response = await api('/oauth2/token', {method: 'POST', form: {client_id: 'user-portal',
      grant_type: 'authorization_code', code: callback.searchParams.get('code'), redirect_uri: redirect, code_verifier: verifier}});
    expect(response.status()).toBe(200);
    const token = (await response.json()).id_token;
    expect(valid(token, await jwks())).toBe(true);
    const claims = JSON.parse(Buffer.from(token.split('.')[1], 'base64url'));
    expect(claims.nonce === nonce && claims.iss === auth + '/' && [claims.aud].flat().includes('user-portal')).toBe(true);
    return {token, kid: JSON.parse(Buffer.from(token.split('.')[0], 'base64url')).kid, sub: claims.sub};
  }
  async function consumer(app, subject) {
    console.log('Checking actual consumer: ' + app);
    const other = await browser.newContext({ignoreHTTPSErrors: true});
    await other.addInitScript(() => {PublicKeyCredential.isConditionalMediationAvailable = async () => false;});
    try {
      const target = `https://${app}.llngtest.invalid:18443`;
      const p = await other.newPage();
      await p.goto(target + (app === 'portal' ? '/login' : '/apps/user_oidc/login/1'));
      await password(p);
      await expect.poll(() => new URL(p.url()).origin, {timeout: 60_000}).toBe(target);
      let lastStatus;
      await expect.poll(async () => {
        const cookies = (await other.cookies(target)).map(c => c.name + '=' + c.value).join('; ');
        const response = await other.request.get('https://127.0.0.1:18443' + (app === 'portal' ? '/api/me' : '/ocs/v2.php/cloud/user?format=json'),
          {headers: {Host: `${app}.llngtest.invalid:18443`, Cookie: cookies, 'OCS-APIRequest': 'true'}});
        const data = await response.json().catch(() => ({}));
        const status = JSON.stringify({app, status: response.status(), path: new URL(p.url()).pathname,
          responseFields: Object.keys(data), userPresent: Boolean(app === 'portal' ? data.person : data.ocs?.data?.id)});
        if (status !== lastStatus) {console.log(status); lastStatus = status;}
        return app === 'portal' ? data.person?.anchor : data.ocs?.data?.id;
      }, {timeout: 60_000}).toBe(subject);
    } finally {await other.close();}
  }
  let prepared = false;
  try {
    const baseline = await issue();
    prepared = true; rotate('prepare');
    await expect.poll(async () => (await jwks()).some(k => k.kid === newKid)).toBe(true);
    expect((await issue()).kid).toBe(baseline.kid);
    rotate('refresh-consumer-cache');
    await consumer('cloud', baseline.sub);
    rotate('promote');
    const current = await issue(); expect(current.kid).toBe(newKid); expect(current.sub).toBe(baseline.sub);
    expect(valid(baseline.token, await jwks())).toBe(true);
    for (const app of ['portal', 'cloud']) await consumer(app, baseline.sub);
    rotate('retire');
    await expect.poll(async () => (await jwks()).some(k => k.kid === baseline.kid)).toBe(false);
    expect(valid(baseline.token, await jwks())).toBe(false);
    expect(valid(current.token, await jwks())).toBe(true);
    rotate('rollback'); prepared = false;
    expect((await issue()).kid).toBe(baseline.kid);
    console.log('Native key overlap retained old-token verification; new signing key worked with actual portal and Nextcloud; early retirement rejected old tokens; rollback restored signing');
  } finally {
    if (prepared) rotate('rollback');
    await context.close();
  }
});
