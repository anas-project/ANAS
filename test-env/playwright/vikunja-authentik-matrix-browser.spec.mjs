import { expect, test } from "@playwright/test";

// TEST_CASES: VIK-T-005

const password = process.env.ANAS_TEST_PASSWORD;
const suffix = process.env.ANAS_TEST_MATRIX_SUFFIX;
const domain = process.env.ANAS_TEST_DOMAIN || "vikunja.test";
const port = process.env.ANAS_TEST_ENTRY_PORT || "9000";
const base = `https://tasks.${domain}:${port}`;

const cases = [
  { role: "direct APP_vikunja", username: `vkd${suffix}`, outcome: "allowed" },
  { role: "APP_all", username: `vka${suffix}`, outcome: "allowed" },
  { role: "Admins", username: `vkm${suffix}`, outcome: "allowed" },
  { role: "no application group", username: `vkn${suffix}`, outcome: "policy-denied" },
  { role: "disabled directory user", username: `vkx${suffix}`, outcome: "auth-denied" },
];

function requireEnvironment() {
  if (!password) throw new Error("ANAS_TEST_PASSWORD is required");
  if (!suffix || !/^[0-9A-Za-z._-]+$/.test(suffix)) {
    throw new Error("ANAS_TEST_MATRIX_SUFFIX is required and must be identifier-safe");
  }
}

async function firstVisible(page, selectors) {
  for (const selector of selectors) {
    const locator = page.locator(selector).first();
    if (await locator.isVisible().catch(() => false)) return locator;
  }
  return null;
}

async function startOIDC(page) {
  await page.goto(base);
  if (new URL(page.url()).hostname === `auth.${domain}`) return;
  const provider = await firstVisible(page, [
    'a[href*="openid/anas"]',
    'a[href*="openid-connect"]',
    'button:has-text("ANAS")',
    'a:has-text("ANAS")',
    'button:has-text("OpenID")',
  ]);
  if (!provider) throw new Error("Vikunja exposed no ANAS OIDC control");
  await provider.click();
  await page.waitForURL((url) => url.hostname === `auth.${domain}`, { timeout: 60_000 });
}

async function submitAuthentik(page, username, outcome) {
  const user = page.locator('ak-flow-card input[name="uidField"]').last();
  await user.waitFor({ state: "visible", timeout: 20_000 });
  await user.fill(username);
  await page.locator('ak-flow-card:has(input[name="uidField"]) button[type="submit"]').last().click();

  const secret = page.locator('ak-flow-card input[name="password"]').last();
  await secret.waitFor({ state: "visible", timeout: 20_000 });
  await secret.fill(password);
  await page.locator('ak-flow-card:has(input[name="password"]) button[type="submit"]').last().click();
  await page.waitForTimeout(1_000);
  if (outcome === "auth-denied") return;

  for (let attempt = 0; attempt < 4 && new URL(page.url()).hostname === `auth.${domain}`; attempt += 1) {
    const denied = page.locator("ak-stage-access-denied").last();
    if (await denied.isVisible().catch(() => false)) return;
    const consent = await firstVisible(page, [
      'ak-flow-card button:has-text("Continue")',
      'ak-flow-card button:has-text("Authorize")',
      'ak-flow-card button:has-text("Accept")',
      'ak-flow-card button:has-text("Confirm")',
    ]);
    if (!consent) {
      await page.waitForTimeout(1_000);
      continue;
    }
    await consent.click({ force: true });
    await page.waitForTimeout(1_000);
  }
}

async function assertVikunjaPolicy(page) {
  const info = await page.evaluate(async () => {
    const response = await fetch("/api/v1/info");
    return { status: response.status, body: await response.json() };
  });
  expect(info.status).toBe(200);
  expect(info.body.auth.local.enabled).toBe(false);
  expect(info.body.auth.local.registration_enabled).toBe(false);
}

async function vikunjaSession(page) {
  return page.evaluate(async () => {
    const token = localStorage.getItem("token") || "";
    if (!token) return { hasToken: false, status: 0, username: "" };
    const response = await fetch("/api/v1/user", { headers: { Authorization: `Bearer ${token}` } });
    const body = await response.json().catch(() => ({}));
    return { hasToken: true, status: response.status, username: body.username || "" };
  });
}

async function assertNoVikunjaToken(context) {
  const storage = await context.storageState();
  const origin = storage.origins.find(({ origin }) => origin === new URL(base).origin);
  const keys = new Set(origin?.localStorage.map(({ name }) => name) || []);
  expect(keys.has("token")).toBe(false);
  expect(keys.has("desktopOAuthRefreshToken")).toBe(false);
}

for (const matrixCase of cases) {
  test(`Authentik Vikunja login matrix: ${matrixCase.role} -> ${matrixCase.outcome}`, async ({ page, context }) => {
    requireEnvironment();
    await startOIDC(page);
    await submitAuthentik(page, matrixCase.username, matrixCase.outcome);

    if (matrixCase.outcome === "allowed") {
      await page.waitForURL((url) => url.hostname === `tasks.${domain}`, { timeout: 60_000 });
      await assertVikunjaPolicy(page);
      await expect.poll(async () => vikunjaSession(page), { timeout: 30_000 }).toEqual({
        hasToken: true,
        status: 200,
        username: matrixCase.username,
      });
      return;
    }

    await expect.poll(() => new URL(page.url()).hostname).toBe(`auth.${domain}`);
    if (matrixCase.outcome === "policy-denied") {
      await expect(page.getByRole("heading", { name: /permission denied/i })).toBeVisible();
    } else {
      await expect(page.locator('ak-flow-card input[name="password"]').last()).toBeVisible();
    }
    await assertNoVikunjaToken(context);
  });
}
