// TEST_CASES: MAILPOC-T-001, MAILPOC-T-002, MAILPOC-T-003
// Only synthetic users in the isolated mail PoC. Never point at a production IAM.
import { chromium } from 'playwright';
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';

const root = process.env.MAIL_POC_EVIDENCE;
if (!root || !path.isAbsolute(root) || !root.includes('mail-webmail-poc')) {
  throw new Error('MAIL_POC_EVIDENCE must be an absolute private experiment directory');
}
await fs.mkdir(root, { recursive: true, mode: 0o700 });
const input = JSON.parse(await fs.readFile(path.join(root, 'secrets.json'), 'utf8'));
const mode = process.env.MAIL_POC_MODE || 'login';
const user = process.env.MAIL_POC_USER || 'alice';
assert.ok(['alice', 'bob'].includes(user));
const origin = 'https://webmail.mailtest.invalid:18883';
const iam = 'https://auth.mailtest.invalid:18883';
const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || chromium.executablePath(), args: ['--host-resolver-rules=MAP *.mailtest.invalid 127.0.0.1'] });
const context = await browser.newContext({ ignoreHTTPSErrors: true, serviceWorkers: 'block' });
// The browser trusts this experiment's short-lived self-signed endpoint. The
// application-to-application and IMAP/SMTP paths independently verify its CA.
const page = await context.newPage();
const clientErrors = [];
const requestShapes = [];
page.on('request', req => { const u = new URL(req.url()); if (u.hostname === 'webmail.mailtest.invalid' && !u.pathname.includes('/static.php')) requestShapes.push({ path: u.pathname, host: u.host, method: req.method(), queryKeys: [...u.searchParams.keys()] }); });
page.on('pageerror', error => clientErrors.push(String(error.message).replace(/\?[^\s"']+/g, '?[redacted]')));
page.setDefaultTimeout(30000);
let passwordPosts = 0;
context.on('request', req => {
  if (req.method() === 'POST' && req.url().startsWith(iam) && /(?:^|&)password=/.test(req.postData() || '')) passwordPosts++;
});
const result = { mode, user, checks: [], success: false };
const record = (name, passed, detail = undefined) => result.checks.push({ name, passed, ...(detail ? { detail } : {}) });
try {
  for (let attempt = 0; attempt < 12; attempt++) {
    const response = await page.goto(iam + '/', { waitUntil: 'domcontentloaded' });
    if (response?.status() === 200) break;
    if (attempt === 11) throw new Error('IAM did not become ready');
    await new Promise(resolve => setTimeout(resolve, 2000));
  }
  await page.locator('input[name="user"]').fill(user);
  await page.locator('input[name="password"]').fill(input[user]);
  await page.locator('button[type="submit"], input[type="submit"]').first().click();
  await page.waitForFunction(() => !document.querySelector('input[name="password"]'));
  record('real_ad_iam_login', true);
  const before = passwordPosts;
  await page.goto(origin + '/', { waitUntil: 'domcontentloaded' });
  await page.locator('#messagelist, #messagelist-content').first().waitFor({ state: 'visible', timeout: 60000 });
  assert.equal(passwordPosts, before, 'Roundcube required another password submission');
  assert.ok(page.url().startsWith(origin));
  record('existing_iam_session_opens_mailbox_without_password', true);
  await page.screenshot({ path: path.join(root, `${mode}-${user}-mailbox.png`), fullPage: true });

  if (mode === 'identities') {
    for (const alias of ['shopping', 'work']) {
      await page.goto(origin + '/?_task=settings&_action=identities', { waitUntil: 'networkidle' });
      await page.locator('a').filter({ hasText: /^Create$/ }).click();
      const form = page.frameLocator('iframe[name="preferences-frame"]');
      await form.locator('input[name="_email"]').fill(alias + '@mailtest.invalid');
      await form.locator('input[name="_name"]').fill('PoC ' + alias);
      await form.locator('textarea[name="_signature"]').fill('Recovery signature ' + alias);
      await page.frame({ name: 'preferences-frame' }).waitForLoadState('networkidle');
      await form.locator('button#rcmbtnfrm100').click();
      await page.frame({ name: 'preferences-frame' }).waitForFunction(() => Number(document.querySelector('input[name="_iid"]')?.value) > 0);
      await page.waitForLoadState('networkidle');
      record('identity_saved_' + alias, true);
    }
  }

  if (mode === 'send-aliases') {
    for (const alias of ['shopping', 'work']) {
      await page.goto(origin + '/?_task=mail&_action=compose', { waitUntil: 'networkidle' });
      await page.locator('#_from').selectOption({ label: 'PoC ' + alias + ' <' + alias + '@mailtest.invalid>' });
      // The native recipient widget keeps its textarea hidden behind a token input.
      const recipient = page.locator('#_to').locator('..').locator('input').first();
      await recipient.fill('bob@mailtest.invalid');
      await recipient.press('Enter');
      await page.locator('#compose-subject').fill('ANAS UI alias ' + alias);
      await page.locator('#composebody').fill('Sent through the real Roundcube UI as ' + alias);
      await page.locator('button#rcmbtn111').click();
      await page.locator('#messagelist, #messagelist-content').first().waitFor({ state: 'visible' });
      record('ui_sent_' + alias, true);
    }
  }

  if (mode === 'preferences') {
    await page.waitForLoadState('networkidle');
    await page.locator('#listmenulink').click();
    await page.locator('select[name="sort_col"]:visible').selectOption({ label: 'Subject' });
    await page.locator('button:visible').filter({ hasText: 'Save' }).click();
    await page.waitForLoadState('networkidle');
    record('nondefault_sort_preference_saved', true);
  }

  if (mode === 'inspect') {
    if (process.env.MAIL_POC_INSPECT === 'identity') {
      await page.goto(origin + '/?_task=settings&_action=identities', { waitUntil: 'networkidle' });
      await page.locator('a').filter({ hasText: /^Create$/ }).click();
      await page.frameLocator('iframe[name="preferences-frame"]').locator('input[name="_email"]').waitFor();
    } else if (process.env.MAIL_POC_INSPECT === 'compose') {
      await page.goto(origin + '/?_task=mail&_action=compose', { waitUntil: 'networkidle' });
    }
    await page.waitForLoadState('networkidle');
    const fields = await page.locator('a, input, select, textarea, button').evaluateAll(nodes => nodes.map(n => ({
      tag: n.tagName, id: n.id, name: n.getAttribute('name'), text: (n.innerText || '').slice(0, 70),
      href: n.getAttribute('href')?.replace(/([?&](?:code|state|token|_token)=)[^&]*/g, '$1[redacted]'),
    })));
    const frames = [];
    for (const frame of page.frames().slice(1)) frames.push({ name: frame.name(), fields: await frame.locator('a, input, select, textarea, button').evaluateAll(nodes => nodes.map(n => ({ tag: n.tagName, id: n.id, name: n.getAttribute('name'), text: (n.innerText || '').slice(0, 80) }))) });
    await fs.writeFile(path.join(root, 'ui-fields.json'), JSON.stringify({ fields, frames }, null, 2), { mode: 0o600 });
    const clientState = await page.evaluate(() => ({ exists: typeof window.rcmail, busy: window.rcmail?.busy, enabledCommands: Object.entries(window.rcmail?.commands || {}).filter(([,v])=>v).map(([k])=>k) }));
    await fs.writeFile(path.join(root, 'ui-client-state.json'), JSON.stringify(clientState, null, 2), { mode: 0o600 });
  }

  if (mode === 'token' || mode === 'login') {
    // Obtain a separate genuine authorization-code token, without intercepting
    // Roundcube's own login exchange or fabricating an identity-provider response.
    const verifier = crypto.randomBytes(32).toString('base64url');
    const challenge = crypto.createHash('sha256').update(verifier).digest('base64url');
    const state = crypto.randomBytes(20).toString('hex');
    const redirect = origin + '/index.php/login/oauth';
    const params = new URLSearchParams({ client_id: 'mail-poc-roundcube', response_type: 'code', response_mode: 'query', redirect_uri: redirect,
      scope: 'openid email profile', state, code_challenge: challenge, code_challenge_method: 'S256' });
    const cookie = (await context.cookies(iam)).map(c => `${c.name}=${c.value}`).join('; ');
    const authorization = await context.request.get('https://127.0.0.1:18883/oauth2/authorize?' + params, {
      headers: { Host: 'auth.mailtest.invalid:18883', Cookie: cookie }, maxRedirects: 0,
    });
    assert.equal(authorization.status(), 302);
    const callback = new URL(authorization.headers().location);
    assert.equal(callback.origin + callback.pathname, redirect);
    assert.equal(callback.searchParams.get('state'), state);
    const code = callback.searchParams.get('code');
    assert.ok(code, 'No real authorization code was returned');
    const response = await context.request.post('https://127.0.0.1:18883/oauth2/token', { headers: { Host: 'auth.mailtest.invalid:18883' }, form: {
      grant_type: 'authorization_code', client_id: 'mail-poc-roundcube', client_secret: input.client,
      code, redirect_uri: redirect, code_verifier: verifier,
    } });
    assert.equal(response.status(), 200);
    const tokens = await response.json();
    assert.ok(tokens.access_token);
    await fs.writeFile(path.join(root, `tokens-${user}.json`), JSON.stringify(tokens), { mode: 0o600 });
    const info = await context.request.get('https://127.0.0.1:18883/oauth2/userinfo', { headers: { Host: 'auth.mailtest.invalid:18883', Authorization: 'Bearer ' + tokens.access_token } });
    assert.equal(info.status(), 200);
    const claims = await info.json();
    await fs.writeFile(path.join(root, `claims-${user}.json`), JSON.stringify(claims), { mode: 0o600 });
    record('genuine_authorization_code_with_pkce', true);
  }
  result.success = true;
} catch (error) {
  // Never put authentication URLs, cookies, tokens, or passwords in stdout.
  const diagnostic = String(error.stack || error)
    .replace(/([?&](?:code|state|token|_token)=)[^\s&"']*/g, '$1[redacted]');
  await fs.writeFile(path.join(root, `${mode}-${user}-error.txt`), diagnostic, { mode: 0o600 });
  await page.screenshot({ path: path.join(root, `${mode}-${user}-failure.png`), fullPage: true }).catch(() => {});
  result.error = 'See private diagnostic file';
} finally {
  await fs.writeFile(path.join(root, `${mode}-${user}-client-errors.json`), JSON.stringify(clientErrors, null, 2), { mode: 0o600 });
  await fs.writeFile(path.join(root, `${mode}-${user}-requests.json`), JSON.stringify(requestShapes, null, 2), { mode: 0o600 });
  result.password_submissions = passwordPosts;
  await fs.writeFile(path.join(root, `${mode}-${user}-result.json`), JSON.stringify(result, null, 2), { mode: 0o600 });
  await browser.close();
  console.log(JSON.stringify(result));
}
if (!result.success) process.exitCode = 1;
