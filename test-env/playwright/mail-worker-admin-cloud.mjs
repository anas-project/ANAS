// TEST_CASES: MAILMOD-T-081
// Explicit isolated fixture only: the browser uses a synthetic login and local
// TLS endpoint. Credential values must never be logged, persisted or traced.
import { chromium, expect } from '@playwright/test';
import { lstat, readFile } from 'node:fs/promises';

async function privateJSON(path) {
  const st = await lstat(path);
  if (!st.isFile() || st.isSymbolicLink() || (st.mode & 0o077) !== 0 || st.uid !== process.getuid() || st.nlink !== 1 || st.size > 16384) throw Error('private credential unavailable');
  return JSON.parse(await readFile(path, 'utf8'));
}
let browser, context, step = 'fixture', pageErrors = 0;
const responses = [];
try {
  const origin = new URL(process.env.MAIL_WORKER_BROWSER_ORIGIN);
  if (origin.protocol !== 'https:' || origin.hostname !== '127.0.0.1' || origin.pathname !== '/' || origin.search || origin.hash) throw Error('invalid isolated origin');
  const deployment = await privateJSON(process.env.MAIL_CLOUDFLARE_TEST_CREDENTIAL_FILE);
  const routing = await privateJSON(process.env.MAIL_CLOUDFLARE_ROUTING_TEST_CREDENTIAL_FILE);
  const worker = process.env.MAIL_WORKER_TEST_NAME, operation = process.env.MAIL_WORKER_TEST_OPERATION;
  if (routing.allowed_domain !== 'mail-poc.bengbu.cc.cd' || routing.zone_id !== '0d84033826bc3cd0c283efdb57a90f7a' || deployment.token === routing.token || !/^anas-reserve-poc-[a-f0-9]{24}$/.test(worker) || !/^cfg-[a-f0-9]{32}$/.test(operation)) throw Error('invalid isolated inputs');
  step = 'browser-start';
  browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH });
  context = await browser.newContext({ ignoreHTTPSErrors: true });
  await context.route('**/*', async route => {
    if (new URL(route.request().url()).origin !== origin.origin) return route.abort();
    return route.continue();
  });
  await context.addCookies([{ name: '__Host-anas-mail-session', value: 'session-secret', url: origin.origin + '/', secure: true, httpOnly: true, sameSite: 'Lax' }]);
  const page = await context.newPage();
  page.on('pageerror', () => { pageErrors++; });
  page.on('response', response => {
    const url = new URL(response.url());
    if (url.origin === origin.origin && ['/setup', '/assets/setup.js', '/api/setup'].includes(url.pathname)) responses.push([url.pathname, response.status()]);
  });
  step = 'open-page';
  await page.goto(origin.origin + '/setup', { waitUntil: 'domcontentloaded' });
  step = 'load-form';
  await expect(page.locator('#setup-form')).toBeVisible();
  await expect(page.locator('#worker-installer')).toBeVisible();
  step = 'fill-inputs';
  for (const [id, value] of Object.entries({ 'worker-operation': operation, 'worker-account': deployment.account_id, 'worker-zone': routing.zone_id, 'worker-name': worker, 'worker-bucket': worker, 'worker-capacity': '16777216' })) await page.locator('#' + id).fill(value);
  let source = '';
  for (const action of ['prepare', 'commit', 'commit']) {
    step = action;
    await page.locator('#worker-routing-token').fill(routing.token);
    if (action === 'prepare') await page.locator('#worker-deploy-token').fill(deployment.token);
    const pending = page.waitForResponse(response => response.url() === origin.origin + '/api/setup/worker-install' && response.request().method() === 'POST', { timeout: 215000 });
    await page.locator('#worker-' + action).click();
    const response = await pending;
    if (response.status() !== 200) throw Error('installation returned a non-success status');
    const value = await response.json();
    if (value.state !== (action === 'prepare' ? 'prepared' : 'configured') || value.enabled !== false || !/^[a-f0-9-]{36}$/.test(value.source_id) || (source && value.source_id !== source)) throw Error('installation result mismatch');
    source = value.source_id;
    await expect(page.locator('#worker-routing-token')).toBeEmpty();
    await expect(page.locator('#worker-deploy-token')).toBeEmpty();
    await expect(page.locator('#worker-result')).toContainText(action === 'prepare' ? 'Prepared;' : 'Configuration saved;');
    await expect(page.locator('#enable')).toBeDisabled();
  }
  step = 'privacy';
  const visible = await page.locator('body').innerText();
  const stored = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
  if (visible.includes(deployment.token) || visible.includes(routing.token) || stored.includes(deployment.token) || stored.includes(routing.token)) throw Error('credential persisted');
  console.log('isolated browser prepare, commit and repeat completed; credentials cleared');
} catch {
  console.error('isolated browser test failed at ' + step);
  console.error('diagnostics ' + JSON.stringify({ pageErrors, responses }));
  process.exitCode = 1;
} finally {
  await context?.close();
  await browser?.close();
}
