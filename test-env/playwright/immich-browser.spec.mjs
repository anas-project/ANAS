import { expect, test } from '@playwright/test';
import { createHash } from 'node:crypto';
import { appendFileSync, readFileSync } from 'node:fs';

const username = process.env.ANAS_TEST_USERNAME;
const password = process.env.ANAS_TEST_PASSWORD;
const iam = new URL(process.env.ANAS_TEST_IAM_URL || 'https://auth.invalid');
const app = new URL(process.env.ANAS_TEST_APP_URL || 'https://photos.invalid');
const provider = process.env.ANAS_TEST_IAM_PROVIDER || 'authentik';
const originals = [process.env.ANAS_TEST_PHOTO, process.env.ANAS_TEST_VIDEO];
const checksum = (value) => createHash('sha256').update(value).digest('hex');
const phase = (name) => appendFileSync(`${process.env.ANAS_TEST_REPORT_FILE}.phases.log`, `${new Date().toISOString()} ${name}\n`, { mode: 0o600 });

function loginUsername(page) {
  return provider === 'casdoor' ? page.locator('form#normal_login input#input') : page.locator('ak-flow-card input[name="uidField"]').last();
}

async function signIn(page) {
  await expect(loginUsername(page)).toBeVisible();
  await loginUsername(page).fill(username);
  if (provider === 'casdoor') {
    await page.locator('#normal_login_password').fill(password);
    await page.locator('form#normal_login button[type="submit"]').click();
    return;
  }
  await page.locator('ak-flow-card:has(input[name="uidField"]) button[type="submit"]').last().click();
  await page.locator('ak-flow-card input[name="password"]').last().fill(password);
  await page.locator('ak-flow-card:has(input[name="password"]) button[type="submit"]').last().click();
}

async function acknowledgeVersionNotice(page) {
  const notice = page.getByRole('dialog').filter({ hasText: 'NEW VERSION AVAILABLE' });
  if (await notice.waitFor({ state: 'visible', timeout: 5_000 }).then(() => true, () => false)) {
    await notice.getByRole('button', { name: 'Acknowledge', exact: true }).click();
    await expect(notice).toBeHidden();
  }
}

test('native OIDC browser login, onboarding, photo/video upload, album, RP logout and IAM-initiated logout', async ({ page, context }) => {
  if (!['authentik', 'casdoor'].includes(provider) || !['admin', 'user'].includes(process.env.ANAS_TEST_ROLE) || !username || !password || !process.env.ANAS_TEST_REPORT_FILE || originals.some((path) => !path) || iam.protocol !== 'https:' || iam.port !== app.port || iam.hostname !== `auth.${app.hostname.slice('photos.'.length)}`) {
    throw new Error('Complete isolated AD/IAM/media fixture inputs are required');
  }
  phase('login-page');
  await expect.poll(async () => (await page.goto('/auth/login?autoLaunch=0', { waitUntil: 'domcontentloaded' })).status(), {
    timeout: 90_000, intervals: [2_000],
  }).toBe(200);
  await expect(page.locator('input[type="password"]')).toHaveCount(0);
  phase('native-oidc-navigation');
  await expect(page.getByRole('button', { name: 'ANAS', exact: true })).toBeVisible({ timeout: 90_000 });
  await expect(page.getByRole('button', { name: 'ANAS', exact: true })).toBeEnabled({ timeout: 90_000 });
  await page.getByRole('button', { name: 'ANAS', exact: true }).click();
  await page.waitForURL((url) => url.hostname === iam.hostname);
  await signIn(page);
  await page.waitForURL((url) => url.hostname === app.hostname && !url.searchParams.has('code'), { timeout: 90_000 });
  phase('version-notice');
  await acknowledgeVersionNotice(page);
  phase('onboarding');
  for (let step = 0; step < 12 && new URL(page.url()).pathname === '/auth/onboarding'; step++) {
    const current = page.url();
    await page.locator('#onboarding-card > div.flex.pt-4 > div').last().getByRole('button').click();
    await page.waitForURL((url) => url.href !== current);
  }
  await expect(page).toHaveURL(/\/photos$/);
  const identity = await page.evaluate(async () => {
    const response = await fetch('/api/users/me'); const user = await response.json();
    return { status: response.status, isAdmin: user.isAdmin };
  });
  expect(identity).toEqual({ status: 200, isAdmin: process.env.ANAS_TEST_ROLE === 'admin' });

  phase('media-upload');
  for (const file of originals) {
    const upload = page.waitForResponse((response) => response.url().endsWith('/api/assets') && response.request().method() === 'POST');
    const chooser = page.waitForEvent('filechooser');
    await page.getByRole('button', { name: 'Upload', exact: true }).filter({ visible: true }).click();
    await (await chooser).setFiles(file);
    const response = await upload;
    expect(response.status()).toBe(201);
    const asset = await response.json();
    expect(asset.status).toBe('created');
    const restored = await page.evaluate(async (id) => {
      const response = await fetch(`/api/assets/${id}/original`);
      const bytes = await response.arrayBuffer();
      const hash = await crypto.subtle.digest('SHA-256', bytes);
      return { status: response.status, hash: Array.from(new Uint8Array(hash), (value) => value.toString(16).padStart(2, '0')).join('') };
    }, asset.id);
    expect(restored.status).toBe(200);
    expect(restored.hash).toBe(checksum(readFileSync(file)));
    await expect.poll(() => page.evaluate(async (id) => (await fetch(`/api/assets/${id}/thumbnail?size=thumbnail`)).status, asset.id), { timeout: 90_000 }).toBe(200);
  }
  phase('album-management');
  await page.goto('/albums');
  await page.getByRole('button', { name: 'Create album', exact: true }).click();
  const title = page.getByTitle('Edit Title', { exact: true });
  await title.fill('ANAS browser album');
  const renamed = page.waitForResponse((response) => /\/api\/albums\/[0-9a-f-]+$/.test(response.url()) && response.request().method() === 'PATCH');
  await title.press('Enter');
  expect((await renamed).status()).toBe(200);
  await page.reload();
  await expect(title).toHaveValue('ANAS browser album');

  const oldSession = (await context.cookies()).find((cookie) => cookie.name === 'immich_access_token')?.value;
  expect(Boolean(oldSession)).toBe(true);
  phase('rp-logout');
  await page.goto('/auth/logout');
  await page.waitForURL((url) => url.hostname === iam.hostname, { timeout: 60_000 });
  for (let step = 0; step < 3; step++) {
    const submit = page.locator('ak-flow-card button[type="submit"]').last();
    if (!(await submit.isVisible().catch(() => false))) break;
    await submit.click();
    await page.waitForTimeout(1000);
  }
  await page.goto('/auth/login?autoLaunch=0');
  const oldStatus = await page.evaluate(async (token) => (await fetch('/api/users/me', { credentials: 'omit', headers: { Authorization: `Bearer ${token}` } })).status, oldSession);
  expect(oldStatus).toBe(401);
  phase('post-logout-native-auto-oidc');
  await page.goto('/auth/login?autoLaunch=1');
  await page.waitForURL((url) => url.hostname === iam.hostname, { timeout: 90_000 });
  await signIn(page);
  await page.waitForURL((url) => url.hostname === app.hostname && url.pathname === '/photos', { timeout: 90_000 });
  await acknowledgeVersionNotice(page);
  const iamSession = (await context.cookies()).find((cookie) => cookie.name === 'immich_access_token')?.value;
  expect(Boolean(iamSession)).toBe(true);
  phase('iam-initiated-logout');
  const iamPage = await context.newPage();
  await iamPage.goto(provider === 'casdoor' ? `${iam.origin}/api/logout` : `${iam.origin}/if/flow/default-invalidation-flow/`);
  for (let step = 0; step < 3; step++) {
    const submit = iamPage.locator('ak-flow-card button[type="submit"]').last();
    if (!(await submit.isVisible().catch(() => false))) break;
    await submit.click();
    await iamPage.waitForTimeout(1000);
  }
  await page.goto('/auth/login?autoLaunch=0');
  await expect.poll(() => page.evaluate(async (token) => (await fetch('/api/users/me', { credentials: 'omit', headers: { Authorization: `Bearer ${token}` } })).status, iamSession), { timeout: 90_000 }).toBe(401);
  expect(await page.evaluate(async () => (await fetch('/api/users/me')).status)).toBe(401);
  phase('post-logout-native-auto-oidc');
  await page.goto('/auth/login?autoLaunch=1');
  await page.waitForURL((url) => url.hostname === iam.hostname, { timeout: 90_000 });
  await expect(loginUsername(page)).toBeVisible();
});
