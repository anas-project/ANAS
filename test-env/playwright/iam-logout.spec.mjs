import { execFileSync } from "node:child_process";
import { expect, test } from "@playwright/test";

const provider = process.env.ANAS_TEST_IAM_PROVIDER;
const protocol = process.env.ANAS_TEST_IAM_PROTOCOL || "oidc";
const app = process.env.ANAS_TEST_APP;
const username = process.env.ANAS_TEST_USERNAME;
const password = process.env.ANAS_TEST_PASSWORD;
const domain = process.env.ANAS_TEST_DOMAIN || "nas.test";
const port = process.env.ANAS_TEST_ENTRY_PORT || "9000";
const origin = process.env.ANAS_TEST_LOGOUT_ORIGIN || "module";
const pauseContainer = process.env.ANAS_TEST_PAUSE_CONTAINER || "";

const bases = {
  iam: `https://auth.${domain}:${port}`,
  nextcloud: `https://nc.${domain}:${port}`,
  meshcentral: `https://meshcentral.${domain}:${port}`,
  netbird: `https://netbird.${domain}:${port}`,
  oauth2_proxy: process.env.ANAS_TEST_PROTECTED_URL || `https://oauth2-proxy.${domain}:${port}`,
};

function requireEnvironment() {
  for (const [name, value] of Object.entries({ ANAS_TEST_IAM_PROVIDER: provider, ANAS_TEST_APP: app, ANAS_TEST_USERNAME: username, ANAS_TEST_PASSWORD: password })) {
    if (!value) throw new Error(`${name} is required`);
  }
  if (!["authentik", "llng", "casdoor"].includes(provider)) throw new Error(`unsupported IAM provider ${provider}`);
  if (!["oidc", "saml"].includes(protocol)) throw new Error(`unsupported IAM protocol ${protocol}`);
  if (!bases[app]) throw new Error(`unsupported application ${app}`);
}

function startURL() {
  switch (app) {
    case "nextcloud":
      return protocol === "saml"
        ? `${bases.nextcloud}/apps/user_saml/saml/login?idp=1`
        : `${bases.nextcloud}/apps/user_oidc/login/1`;
    case "meshcentral": return `${bases.meshcentral}/auth-oidc`;
    case "netbird": return bases.netbird;
    case "oauth2_proxy": return `${bases.oauth2_proxy}/oauth2/start?rd=%2F`;
    default: throw new Error(`unsupported application ${app}`);
  }
}

async function firstVisible(page, selectors) {
  for (const selector of selectors) {
    const locator = page.locator(selector).first();
    if (await locator.isVisible().catch(() => false)) return locator;
  }
  return null;
}

async function submit(page) {
  const button = await firstVisible(page, [
    'button[type="submit"]',
    'input[type="submit"]',
    'button:has-text("Continue")',
    'button:has-text("Sign in")',
    'button:has-text("Login")',
  ]);
  if (!button) throw new Error(`provider ${provider} exposed no visible submit control`);
  await button.click();
}

async function loginProvider(page) {
  if (provider === "authentik") {
    const user = await firstVisible(page, ['input[name="uidField"]', 'input[name="uid_field"]', 'input[name="username"]']);
    if (!user) throw new Error("authentik identification field was not visible");
    await user.fill(username);
    await submit(page);
    const pass = await firstVisible(page, ['input[name="password"]', 'input[type="password"]']);
    if (!pass) throw new Error("authentik password field was not visible");
    await pass.fill(password);
    await submit(page);
  } else if (provider === "llng") {
    await page.locator('input[name="user"]').fill(username);
    await page.locator('input[name="password"]').fill(password);
    await submit(page);
  } else {
    const user = await firstVisible(page, ['input[name="username"]', 'input[placeholder*="Username" i]', 'input[type="text"]']);
    const pass = await firstVisible(page, ['input[name="password"]', 'input[placeholder*="Password" i]', 'input[type="password"]']);
    if (!user || !pass) throw new Error("casdoor login fields were not visible");
    await user.fill(username);
    await pass.fill(password);
    await submit(page);
  }

  // Some provider policies display an explicit consent/continue stage.
  for (let attempt = 0; attempt < 3 && new URL(page.url()).hostname === `auth.${domain}`; attempt += 1) {
    const consent = await firstVisible(page, ['button:has-text("Continue")', 'button:has-text("Authorize")', 'button:has-text("Accept")']);
    if (!consent) break;
    await consent.click();
  }
  await page.waitForURL((url) => url.hostname !== `auth.${domain}`, { timeout: 60_000 });
}

async function assertNextcloudSession(context, expected) {
  const response = await context.request.get(`${bases.nextcloud}/ocs/v2.php/cloud/user?format=json`, {
    headers: { "OCS-APIRequest": "true" },
  });
  const body = await response.json().catch(() => ({}));
  const current = body?.ocs?.data?.id || "";
  if (expected) expect(current).toBe(username);
  else expect(current).not.toBe(username);
}

async function assertLocalSession(page, context, expected) {
	if (app === "nextcloud") return assertNextcloudSession(context, expected);
	if (app === "netbird") {
		const present = await page.evaluate(() => {
			const entries = [...Object.entries(localStorage), ...Object.entries(sessionStorage)];
			return entries.some(([key, value]) => /oidc\.user|access_token|id_token|user:/i.test(`${key}:${value}`));
		});
		expect(present).toBe(expected);
		return;
	}
	const cookies = await context.cookies(bases[app]);
	const relevant = cookies.filter((cookie) => {
		if (app === "oauth2_proxy") return cookie.name.startsWith("_oauth2_proxy");
		if (app === "meshcentral") return cookie.httpOnly || /mesh|session|xid|auth/i.test(cookie.name);
		return /netbird|auth|token/i.test(cookie.name);
  });
  if (expected) expect(relevant.length).toBeGreaterThan(0);
  else expect(relevant.length).toBe(0);
}

async function moduleLogout(page) {
  if (app === "nextcloud") {
    const logout = page.locator('a[href*="logout"], a#logout').last();
    const href = await logout.getAttribute("href").catch(() => null);
    if (href) await page.goto(new URL(href, bases.nextcloud).toString());
    else {
      const generated = await page.evaluate(() => globalThis.OC?.generateUrl?.("/logout") || "");
      if (!generated) throw new Error("Nextcloud exposed no logout URL");
      await page.goto(new URL(generated, bases.nextcloud).toString());
    }
  } else if (app === "meshcentral") {
    await page.goto(`${bases.meshcentral}/logout`);
  } else if (app === "netbird") {
    const menu = await firstVisible(page, ['button[aria-label*="account" i]', 'button[aria-label*="user" i]', '[data-testid*="user-menu"]']);
    if (menu) await menu.click();
    const logout = page.getByRole("menuitem", { name: /log\s*out|sign\s*out/i }).or(page.getByRole("button", { name: /log\s*out|sign\s*out/i })).first();
    await expect(logout).toBeVisible();
    await logout.click();
  } else {
    await page.goto(`${bases.oauth2_proxy}/oauth2/sign_out`);
  }
}

async function providerLogout(page) {
  if (provider === "authentik") await page.goto(`${bases.iam}/if/flow/default-invalidation-flow/`);
  else if (provider === "llng") await page.goto(`${bases.iam}/?logout=1`);
  else await page.goto(`${bases.iam}/api/logout`);
}

function docker(action, container) {
  const args = [];
  if (process.env.ANAS_TEST_DOCKER_SOCKET) args.push("-H", `unix://${process.env.ANAS_TEST_DOCKER_SOCKET}`);
  args.push(action, container);
  execFileSync(process.env.DOCKER_CMD || "docker", args, { stdio: "ignore" });
}

test(`${provider || "unset"}/${protocol}/${app || "unset"} logout`, async ({ page, context }) => {
  requireEnvironment();
  const logoutNavigations = [];
  page.on("request", (request) => {
    const url = request.url();
    if (/end.?session|logout|SAMLRequest|SAMLResponse/i.test(url)) logoutNavigations.push(url);
  });

  await page.goto(startURL());
  if (new URL(page.url()).hostname === `auth.${domain}`) await loginProvider(page);
  await expect.poll(() => new URL(page.url()).hostname).not.toBe(`auth.${domain}`);
	await assertLocalSession(page, context, true);

  let paused = false;
  try {
    if (pauseContainer) {
      docker("pause", pauseContainer);
      paused = true;
    }
    if (origin === "iam") await providerLogout(page);
    else await moduleLogout(page);
		await expect.poll(async () => {
			try { await assertLocalSession(page, context, false); return true; } catch { return false; }
		}, { timeout: 30_000 }).toBe(true);
		if (app === "oauth2_proxy" && pauseContainer) {
			const protectedResponse = await context.request.get(bases.oauth2_proxy, { maxRedirects: 0 });
			expect([301, 302, 303, 307, 308, 401, 403]).toContain(protectedResponse.status());
		}
  } finally {
    if (paused) docker("unpause", pauseContainer);
  }

  if (protocol === "saml" && provider === "casdoor") {
    expect(logoutNavigations.some((url) => /SAMLRequest|SAMLResponse/.test(url))).toBe(false);
    return;
  }
  if (app === "oauth2_proxy") return;

  if (protocol === "saml") {
    expect(logoutNavigations.some((url) => /SAMLRequest=/.test(url))).toBe(true);
  }
  if (protocol === "oidc" && ["meshcentral", "netbird"].includes(app)) {
    const rpLogout = logoutNavigations.find((url) => /end.?session|logout/i.test(url) && new URL(url).hostname === `auth.${domain}`);
    expect(rpLogout, "application must navigate to the discovery/provider logout endpoint").toBeTruthy();
    expect(new URL(rpLogout).searchParams.get("state"), "RP logout must bind its callback with state").toBeTruthy();
  }

  await page.goto(startURL());
  await expect.poll(() => new URL(page.url()).hostname).toBe(`auth.${domain}`);
});
