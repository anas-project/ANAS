import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

// TEST_CASES: LLNG-PWD-T-002, LLNG-PWD-T-003
// Credentials belong to disposable users in an isolated Samba domain only.
// Never enable Playwright traces, screenshots or storage-state export here.
const domain = process.env.ANAS_TEST_DOMAIN || 'llngtest.invalid';
const port = process.env.ANAS_TEST_ENTRY_PORT || '18443';
const app = `https://tasks.${domain}:${port}`;
const mode = process.env.ANAS_TEST_PASSWORD_FLOW;

async function submitLogin(page, user, password) {
  const field = page.locator('input[name="user"]').first();
  await field.fill(user);
  await page.locator('input[name="password"]').first().fill(password);
  await page.locator('form').filter({ has: field }).locator('button[type="submit"],input[type="submit"]').first().click();
}

async function applicationSession(page) {
  return page.evaluate(async () => {
    const token = localStorage.getItem('token');
    if (!token) return { status: 0, username: '' };
    const response = await fetch('/api/v1/user', { headers: { Authorization: `Bearer ${token}` } });
    const body = await response.json().catch(() => ({}));
    return { status: response.status, username: body.username || '' };
  });
}

async function assertNoSession(context) {
  expect((await context.cookies()).some(cookie => cookie.name === 'lemonldap')).toBe(false);
  const storage = await context.storageState();
  expect(storage.origins.some(origin => origin.localStorage.some(item => item.name === 'token'))).toBe(false);
}

test('Samba password change preserves the original Vikunja OIDC transaction', async ({ page, context }) => {
  if (!['reset', 'expired'].includes(mode)) throw new Error('ANAS_TEST_PASSWORD_FLOW must be reset or expired');
  if (!process.env.ANAS_TEST_CREDENTIAL_FILE) throw new Error('isolated credential file is required');
  const credentials = JSON.parse(readFileSync(process.env.ANAS_TEST_CREDENTIAL_FILE, 'utf8'))[mode];
  let originalState;
  let callbackState;
  let callbackCount = 0;
  page.on('request', request => {
    const url = new URL(request.url());
    if (url.hostname === `auth.${domain}` && url.pathname === '/oauth2/authorize' && !originalState) {
      originalState = url.searchParams.get('state');
    }
    if (url.hostname === `tasks.${domain}` && url.pathname === '/auth/openid/anas' && url.searchParams.has('code')) {
      callbackState = url.searchParams.get('state');
      callbackCount++;
    }
  });
  await page.goto(app);
  const provider = page.locator('a[href*="openid/anas"],a:has-text("ANAS"),button:has-text("ANAS")').first();
  // With a single provider and local login disabled, Vikunja redirects itself.
  await expect.poll(async () => new URL(page.url()).hostname === `auth.${domain}` || await provider.isVisible()).toBe(true);
  if (new URL(page.url()).hostname !== `auth.${domain}`) await provider.click();
  await page.waitForURL(url => url.hostname === `auth.${domain}`);
  expect(Boolean(originalState)).toBe(true);

  // An incorrect current password must not open the password-change form.
  await submitLogin(page, credentials.username, credentials.old + '-incorrect');
  await expect(page.locator('[trmsg="5"]')).toBeVisible();
  await expect(page.locator('input[name="password"]')).toBeVisible();
  await expect(page.locator('input[name="newpassword"]')).toHaveCount(0);
  await assertNoSession(context);

  await submitLogin(page, credentials.username, credentials.old);
  await expect(page.locator('input[name="newpassword"]')).toBeVisible();
  await expect(page.locator(`[trmsg="${mode === 'reset' ? '25' : '22'}"]`)).toBeVisible();
  await assertNoSession(context);
  const newPassword = page.locator('input[name="newpassword"]');
  await newPassword.fill(credentials.new);
  await page.locator('input[name="confirmpassword"]').fill(credentials.new);
  await page.locator('form').filter({ has: newPassword }).locator('button[type="submit"],input[type="submit"]').first().click();
  await expect(page.locator('[trmsg="35"]')).toBeVisible();
  await assertNoSession(context);

  // Upstream deliberately requires a new authentication after changing a
  // password. Continue the displayed form, without restarting app login.
  await submitLogin(page, credentials.username, credentials.new);
  await page.waitForURL(url => url.hostname === `tasks.${domain}`);
  await expect.poll(() => applicationSession(page)).toEqual({ status: 200, username: credentials.username });
  expect(callbackCount).toBe(1);
  expect(callbackState === originalState).toBe(true);
});
