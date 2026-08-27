import { expect, test } from "@playwright/test";

// TEST_CASES: VIK-T-006

const password = process.env.ANAS_TEST_PASSWORD;
const suffix = process.env.ANAS_TEST_MATRIX_SUFFIX;
const domain = process.env.ANAS_TEST_DOMAIN || "vikunja-llng.test";
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
  if (!provider) {
    const text = (await page.locator("body").innerText()).slice(0, 500);
    throw new Error(`Vikunja exposed no ANAS OIDC control; visible page: ${text}`);
  }
  await provider.click();
  await page.waitForURL((url) => url.hostname === `auth.${domain}`, { timeout: 60_000 });
}

async function assertVikunjaPolicy(page) {
  const info = await page.evaluate(async () => {
    const response = await fetch("/api/v1/info", { headers: { Accept: "application/json" } });
    return { status: response.status, body: await response.json() };
  });
  expect(info.status).toBe(200);
  expect(info.body.auth.local.enabled).toBe(false);
  expect(info.body.auth.local.registration_enabled).toBe(false);
}

async function submitLLNG(page, username) {
  const user = page.locator('input[name="user"]').first();
  const secret = page.locator('input[name="password"]').first();
  await user.waitFor({ state: "visible", timeout: 20_000 });
  await secret.waitFor({ state: "visible", timeout: 20_000 });
  await user.fill(username);
  await secret.fill(password);
  const form = page.locator("form").filter({ has: user }).first();
  await form.locator('button[type="submit"],input[type="submit"]').first().click();
}

async function vikunjaSession(page) {
  return page.evaluate(async () => {
    const token = localStorage.getItem("token") || "";
    if (!token) return { hasToken: false, status: 0, username: "" };
    const response = await fetch("/api/v1/user", {
      headers: { Authorization: `Bearer ${token}` },
    });
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
  test(`LLNG Vikunja login matrix: ${matrixCase.role} -> ${matrixCase.outcome}`, async ({ page, context }) => {
    requireEnvironment();
    await startOIDC(page);
    await submitLLNG(page, matrixCase.username);

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
      await expect(page.locator("form")).toHaveCount(0);
      await expect(page.getByRole("alert")).toContainText(/not authorized|denied|forbidden/i);
    } else {
      await expect(page.locator('input[name="user"]')).toHaveValue(matrixCase.username);
      await expect(page.locator('input[name="password"]')).toHaveValue("");
    }
    await assertNoVikunjaToken(context);
  });
}
