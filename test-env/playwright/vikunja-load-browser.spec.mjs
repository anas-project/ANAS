import fs from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";

// TEST_CASES: VIK-T-012

const username = process.env.ANAS_TEST_USERNAME;
const password = process.env.ANAS_TEST_PASSWORD;
const domain = process.env.ANAS_TEST_DOMAIN || "vikunja.test";
const port = process.env.ANAS_TEST_ENTRY_PORT || "9000";
const metricFile = process.env.ANAS_TEST_METRIC_FILE;
const base = `https://tasks.${domain}:${port}`;

async function firstVisible(page, selectors) {
  for (const selector of selectors) {
    const locator = page.locator(selector).first();
    if (await locator.isVisible().catch(() => false)) return locator;
  }
  return null;
}

async function login(page) {
  await page.goto(base);
  if (new URL(page.url()).hostname !== `auth.${domain}`) {
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

  const user = page.locator('ak-flow-card input[name="uidField"]').last();
  await user.waitFor({ state: "visible" });
  await user.fill(username);
  await page.locator('ak-flow-card:has(input[name="uidField"]) button[type="submit"]').last().click();
  const secret = page.locator('ak-flow-card input[name="password"]').last();
  await secret.waitFor({ state: "visible" });
  await secret.fill(password);
  await page.locator('ak-flow-card:has(input[name="password"]) button[type="submit"]').last().click();

  for (let attempt = 0; attempt < 4 && new URL(page.url()).hostname === `auth.${domain}`; attempt += 1) {
    try {
      await page.waitForURL((url) => url.hostname === `tasks.${domain}`, { timeout: 10_000 });
      break;
    } catch {
      // A consent stage may still be present.
    }
    const consent = await firstVisible(page, [
      'ak-flow-card button:has-text("Continue")',
      'ak-flow-card button:has-text("Authorize")',
      'ak-flow-card button:has-text("Accept")',
      'ak-flow-card button:has-text("Confirm")',
    ]);
    if (consent) await consent.click({ force: true });
  }
  await page.waitForURL((url) => url.hostname === `tasks.${domain}`, { timeout: 60_000 });
  await expect.poll(() => page.evaluate(() => Boolean(localStorage.getItem("token")))).toBe(true);
}

test("10k task authenticated cold first screen", async ({ page }) => {
  if (!username || !password) throw new Error("ANAS_TEST_USERNAME and ANAS_TEST_PASSWORD are required");
  if (!metricFile) throw new Error("ANAS_TEST_METRIC_FILE is required");
  await login(page);

  const started = Date.now();
  await page.goto(base, { waitUntil: "domcontentloaded" });
  await page.locator("main").waitFor({ state: "visible", timeout: 60_000 });
  const firstScreenMs = Date.now() - started;
  const metrics = await page.evaluate(async () => {
    const token = localStorage.getItem("token") || "";
    const probeStarted = performance.now();
    const response = await fetch("/api/v2/tasks?per_page=1&page=1", {
      headers: { Authorization: `Bearer ${token}` },
    });
    const body = await response.json();
    const navigation = performance.getEntriesByType("navigation")[0];
    return {
      api_status: response.status,
      api_probe_ms: Math.round((performance.now() - probeStarted) * 100) / 100,
      task_total: Number(
        response.headers.get("x-pagination-total") || body.total || body.pagination?.total || 0,
      ),
      navigation_response_start_ms: Math.round(navigation.responseStart * 100) / 100,
      dom_content_loaded_ms: Math.round(navigation.domContentLoadedEventEnd * 100) / 100,
      load_event_ms: Math.round(navigation.loadEventEnd * 100) / 100,
    };
  });
  expect(metrics.api_status).toBe(200);
  expect(metrics.task_total).toBe(10_000);

  const report = {
    schema: "anas.vikunja-load-browser/v1",
    measured_at: new Date().toISOString(),
    first_screen_ms: firstScreenMs,
    ...metrics,
  };
  fs.mkdirSync(path.dirname(metricFile), { recursive: true });
  fs.writeFileSync(metricFile, `${JSON.stringify(report, null, 2)}\n`, { mode: 0o600 });
  fs.chmodSync(metricFile, 0o600);
});
