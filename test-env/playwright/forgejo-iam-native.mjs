// Real browser login against the dedicated Forgejo IAM acceptance fixture.
// Credentials and full browser artifacts stay in the private fixture directory.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import crypto from 'node:crypto';
import { pathToFileURL } from 'node:url';

const harness = process.env.ANAS_FORGEJO_TEST_HARNESS_ROOT || '/harness';
const fixture = process.env.ANAS_FORGEJO_TEST_FIXTURE_ROOT || '/fixture';
const { chromium } = await import(pathToFileURL(harness + '/playwright-core/index.mjs'));
const input = JSON.parse(await fs.readFile(fixture + '/browser-input.json', 'utf8'));
assert.equal(input.origin, 'https://git.fj-iam.test:19000');
assert.equal(input.issuer, 'https://auth.fj-iam.test:19000');
assert.ok(['casdoor', 'llng'].includes(input.provider));
assert.match(input.username, /^fj(?:user|admin|deny)[a-f0-9]{8}$/);
assert.ok(typeof input.password === 'string' && input.password.length >= 20);
assert.ok(['allowed', 'denied', 'unavailable'].includes(input.expect));
assert.match(input.case, /^[a-z][a-z0-9-]{0,63}$/);
const pin = crypto.createHash('sha256').update(new crypto.X509Certificate(input.leaf_pem)
  .publicKey.export({ type: 'spki', format: 'der' })).digest('base64');
const result = { status: 'failed', provider: input.provider, case: input.case, password_posts: 0 };
let browser;
let page;
try {
  const endpoint = process.env.ANAS_FORGEJO_BROWSER_WS_ENDPOINT;
  if (endpoint) {
    assert.match(endpoint, /^ws:\/\/127\.0\.0\.1:19999\/[a-f0-9-]{32,64}$/);
    browser = await chromium.connect(endpoint);
    result.browser_execution = 'SSH loopback browser server';
  } else {
    browser = await chromium.launch({ headless: true, args: [
      '--no-proxy-server', '--disable-dev-shm-usage', '--ignore-certificate-errors-spki-list=' + pin,
    ] });
    result.browser_execution = 'local test container';
  }
  const context = await browser.newContext({ serviceWorkers: 'block' });
  page = await context.newPage();
  page.setDefaultTimeout(60_000);
  page.setDefaultNavigationTimeout(60_000);
  let passwordLeak = false;
  const issuerReplies = [];
  context.on('response', response => {
    const url = new URL(response.url());
    if (url.origin === input.issuer && url.pathname === '/api/login' && response.request().method() === 'POST') {
      issuerReplies.push(response.json().then(value => ({ http_status: response.status(), status: value.status,
        message: String(value.msg || '').replaceAll(input.password, '[redacted]').slice(0, 512) }))
        .catch(() => ({ http_status: response.status() })));
    }
  });
  context.on('request', request => {
    const body = request.postData() || '';
    if (body.includes(input.password) || [...new URLSearchParams(body).values()].includes(input.password)) {
      result.password_posts++;
      if (new URL(request.url()).origin !== input.issuer) passwordLeak = true;
    }
  });
  let rejected = false;
  let providerResponse;
  try {
    const response = await page.goto(input.origin + '/user/oauth2/anas', { waitUntil: 'domcontentloaded' });
    providerResponse = response;
    if (input.expect === 'unavailable') {
      result.provider_http_status = response?.status();
    }
    if (input.expect !== 'unavailable') {
      const selectors = input.provider === 'llng' ? ['input[name=user]'] : [
        '.login-username-input input', 'input.login-username-input', 'input[name=username]', 'input[type=text]',
      ];
      let user;
      const deadline = Date.now() + 60_000;
      while (!user && Date.now() < deadline) {
        for (const selector of selectors) {
          const field = page.locator(selector).first();
          if (await field.isVisible().catch(() => false)) { user = field; break; }
        }
        if (!user) await page.waitForTimeout(200);
      }
      assert.ok(user, 'real IAM login form unavailable');
      assert.equal(new URL(page.url()).origin, input.issuer);
      await user.fill(input.username);
      await page.locator('input[type=password]').first().fill(input.password);
      const form = user.locator('xpath=ancestor::form[1]');
      const submit = input.provider === 'casdoor' ? page.locator('button.login-button').first()
        : form.locator('button[type=submit],input[type=submit]').first();
      await submit.click({ noWaitAfter: true });
      try {
        await page.waitForURL(url => url.origin === input.origin, { timeout: 45_000, waitUntil: 'domcontentloaded' });
      } catch (error) {
        if (input.expect !== 'denied') throw error;
      }
      if (input.expect === 'denied') {
        const body = await page.locator('body').innerText();
        result.issuer_login_responses = await Promise.all(issuerReplies);
        result.iam_outcome_origin = new URL(page.url()).origin;
        result.iam_outcome_pathname = new URL(page.url()).pathname;
        await fs.writeFile(fixture + '/browser-' + input.case + '-iam-response.txt',
          body.replaceAll(input.password, '[redacted]'), { mode: 0o600 });
        rejected = /denied|not.?authori[sz]ed|not.*permission|not.*allowed|拒绝|不允许|无权|权限/i
          .test(body)
          || new URL(page.url()).searchParams.get('error') === 'access_denied'
          || (input.provider === 'casdoor' && result.issuer_login_responses.some(reply =>
            reply.http_status === 200 && reply.status === 'error' && reply.message === 'Unauthorized operation'));
      }
    }
  } catch (error) {
    if (input.expect !== 'unavailable') throw error;
    result.provider_navigation_failed = true;
  }
  if (input.expect === 'unavailable') {
    const removedIssuerRoute = providerResponse?.status() === 404
      && new URL(providerResponse.url()).origin === input.issuer;
    result.issuer_route_unregistered = removedIssuerRoute;
    assert.ok(result.provider_navigation_failed || removedIssuerRoute
      || (providerResponse && providerResponse.status() >= 500),
      'stopped IAM did not produce an availability error');
  }
  await page.goto(input.origin + '/user/settings', { waitUntil: 'domcontentloaded' });
  const settingsPath = new URL(page.url()).pathname;
  const profileName = page.locator('input[name=name]');
  const authenticated = (settingsPath === '/user/settings' || settingsPath.startsWith('/user/settings/'))
    && await profileName.count() === 1 && await profileName.inputValue() === input.username
    && await page.locator('input[name=full_name]').count() === 1;
  result.authenticated_settings = authenticated;
  if (input.expect === 'allowed') {
    assert.ok(authenticated, 'OIDC login did not establish authenticated settings');
    assert.ok(result.password_posts > 0, 'browser did not authenticate at the real IAM provider');
  } else {
    assert.ok(!authenticated, 'rejected or unavailable IAM established an application session');
    if (input.expect === 'denied') assert.ok(rejected, 'expected an explicit IAM authorization rejection');
  }
  assert.ok(!passwordLeak, 'password was submitted outside the IAM origin');
  result.status = 'passed';
  await page.screenshot({ path: fixture + '/browser-' + input.case + '.png', fullPage: true,
    mask: [page.locator('input')] });
} catch (error) {
  result.error = String(error.message).replaceAll(input.password, '[redacted]')
    .replaceAll(encodeURIComponent(input.password), '[redacted]').replace(/(?:https?|wss?):\/\/[^\s"')]+/g, '[url]');
  process.exitCode = 1;
  if (page) {
    await page.screenshot({ path: fixture + '/browser-' + input.case + '-failed.png', fullPage: true,
      mask: [page.locator('input')] }).catch(() => {});
    const markup = await page.content().catch(() => null);
    if (markup !== null) await fs.writeFile(fixture + '/browser-' + input.case + '-failed.html', markup,
      { mode: 0o600 }).catch(() => {});
  }
} finally {
  if (browser) await browser.close();
  await fs.writeFile(fixture + '/browser-result.json', JSON.stringify(result, null, 2) + '\n', { mode: 0o600 });
  console.log(JSON.stringify(result));
}
