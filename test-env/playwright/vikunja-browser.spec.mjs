import { execFileSync } from "node:child_process";
import { expect, test } from "@playwright/test";

// TEST_CASES: VIK-T-007

const username = process.env.ANAS_TEST_USERNAME;
const password = process.env.ANAS_TEST_PASSWORD;
const domain = process.env.ANAS_TEST_DOMAIN || "vikunja.test";
const port = process.env.ANAS_TEST_ENTRY_PORT || "9000";
const pauseContainer = process.env.ANAS_TEST_PAUSE_CONTAINER ?? "anas_vik_authentik";
const base = `https://tasks.${domain}:${port}`;

function requireEnvironment() {
  if (!username) throw new Error("ANAS_TEST_USERNAME is required");
  if (!password) throw new Error("ANAS_TEST_PASSWORD is required");
}

async function installStorageAudit(page) {
  const events = [];
  page.on("console", (message) => {
    const text = message.text();
    if (!text.startsWith("__ANAS_STORAGE__")) return;
    try {
      events.push(JSON.parse(text.slice("__ANAS_STORAGE__".length)));
    } catch {
      events.push({ method: "unparsed", key: null });
    }
  });
  await page.addInitScript(() => {
    for (const method of ["setItem", "removeItem", "clear"]) {
      const original = Storage.prototype[method];
      Storage.prototype[method] = function (...args) {
        if (this === window.localStorage) {
          const key = method === "clear" ? null : String(args[0] ?? "");
          if (key === null || ["token", "desktopOAuthRefreshToken", "loggedInViaProvider"].includes(key)) {
            console.debug(`__ANAS_STORAGE__${JSON.stringify({ method, key })}`);
          }
        }
        return original.apply(this, args);
      };
    }
  });
  return events;
}

async function firstVisible(page, selectors) {
  return (await firstVisibleMatch(page, selectors))?.locator || null;
}

async function firstVisibleMatch(page, selectors) {
  for (const selector of selectors) {
    const locator = page.locator(selector).first();
    if (await locator.isVisible().catch(() => false)) return { locator, selector };
  }
  return null;
}

async function firstVisibleCandidate(candidates) {
  for (const candidate of candidates) {
    if (await candidate.locator.first().isVisible().catch(() => false)) {
      return { ...candidate, locator: candidate.locator.first() };
    }
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

async function loginAuthentik(page) {
  const submittedStages = [];
  const stageTrace = [];
  page.on("request", (request) => {
    if (["GET", "HEAD", "OPTIONS"].includes(request.method())) return;
    try {
      submittedStages.push({
        path: new URL(request.url()).pathname,
        method: request.method(),
        keys: Object.keys(request.postDataJSON()).sort(),
      });
    } catch {
      submittedStages.push({ path: new URL(request.url()).pathname, method: request.method(), keys: ["unparsed"] });
    }
  });
  const user = page.locator('ak-flow-card input[name="uidField"]').last();
  await user.waitFor({ state: "visible", timeout: 20_000 });
  if (new URL(page.url()).hostname === `auth.${domain}`) {
    await user.fill(username);
    const identificationCard = page.locator('ak-flow-card:has(input[name="uidField"])').last();
    stageTrace.push({ stage: "identification", text: (await identificationCard.innerText()).slice(0, 200) });
    await identificationCard.locator('button[type="submit"]').last().click();
    const stagePassword = page.locator('ak-flow-card input[name="password"]').last();
    await stagePassword.waitFor({ state: "visible", timeout: 20_000 });
    await stagePassword.fill(password);
    const passwordCard = page.locator('ak-flow-card:has(input[name="password"])').last();
    stageTrace.push({ stage: "password", text: (await passwordCard.innerText()).slice(0, 200) });
    await passwordCard.locator('button[type="submit"]').last().click();
    await page.waitForTimeout(1_000);
  }

  for (let attempt = 0; attempt < 4 && new URL(page.url()).hostname === `auth.${domain}`; attempt += 1) {
    try {
      await page.waitForURL((url) => url.hostname === `tasks.${domain}`, { timeout: 10_000 });
      break;
    } catch {
      // The flow needs an explicit consent/continue action.
    }
    const consent = await firstVisible(page, [
      'ak-flow-card button:has-text("Continue")',
      'ak-flow-card button:has-text("Authorize")',
      'ak-flow-card button:has-text("Accept")',
      'ak-flow-card button:has-text("Confirm")',
      'ak-flow-card input[type="submit"][value*="Continue" i]',
      'ak-flow-card input[type="submit"][value*="Authorize" i]',
    ]);
    if (!consent) break;
    await consent.click({ force: true });
    await page.waitForTimeout(1_000);
  }
  if (new URL(page.url()).hostname === `auth.${domain}`) {
    await page.waitForTimeout(5_000);
  }
  if (new URL(page.url()).hostname === `auth.${domain}`) {
    const current = new URL(page.url());
    const labels = await page.locator("h1,h2,h3,button").allTextContents();
    const pageText = (await page.locator("body").innerText()).replaceAll(username, "[redacted]");
    const inputLocators = await page.locator("input").all();
    const inputs = [];
    for (const input of inputLocators) {
      inputs.push({
        name: await input.getAttribute("name"),
        type: await input.getAttribute("type"),
        placeholder: await input.getAttribute("placeholder"),
        visible: await input.isVisible(),
        ancestors: await input.evaluate((element) => {
          const tags = [];
          let current = element.parentElement;
          while (current && tags.length < 5) {
            tags.push(current.tagName.toLowerCase());
            current = current.parentElement;
          }
          return tags;
        }),
      });
    }
    throw new Error(
      `Authentik flow stopped at ${current.origin}${current.pathname}; trace=${JSON.stringify(stageTrace)}; submitted=${JSON.stringify(submittedStages)}; controls=${labels.join(" | ").slice(0, 300)}; inputs=${JSON.stringify(inputs)}; text=${pageText.slice(0, 500)}`,
    );
  }
  await page.waitForURL((url) => url.hostname === `tasks.${domain}`, { timeout: 60_000 });
}

async function assertVikunjaSession(page, expected) {
  if (!expected) {
    const storage = await page.context().storageState();
    const vikunjaOrigin = storage.origins.find(({ origin }) => origin === new URL(base).origin);
    const keys = new Set(vikunjaOrigin?.localStorage.map(({ name }) => name) || []);
    expect(keys.has("token")).toBe(false);
    expect(keys.has("desktopOAuthRefreshToken")).toBe(false);
    return;
  }

  const result = await page.evaluate(async () => {
    const token = localStorage.getItem("token") || "";
    if (!token) return { token: false, status: 0, username: "" };
    const response = await fetch("/api/v1/user", {
      headers: { Authorization: `Bearer ${token}` },
    });
    const body = await response.json().catch(() => ({}));
    return { token: true, status: response.status, username: body.username || "" };
  });
  if (expected) {
    expect(result.token).toBe(true);
    expect(result.status).toBe(200);
    expect(result.username).toBe(username);
  }
}

async function waitForVikunjaSessionCleared(page, timeout, storageEvents, networkEvents, logoutControl) {
  try {
    await expect.poll(async () => {
      try {
        await assertVikunjaSession(page, false);
        return true;
      } catch {
        return false;
      }
    }, { timeout }).toBe(true);
  } catch (error) {
    const storage = await page.context().storageState();
    const vikunjaOrigin = storage.origins.find(({ origin }) => origin === new URL(base).origin);
    const keys = vikunjaOrigin?.localStorage.map(({ name }) => name).sort() || [];
    const current = new URL(page.url());
    throw new Error(
      `Vikunja session did not clear; current=${current.origin}${current.pathname}; localStorageKeys=${JSON.stringify(keys)}; storageEvents=${JSON.stringify(storageEvents)}; logoutNetwork=${JSON.stringify(networkEvents)}; logoutControl=${JSON.stringify(logoutControl)}`,
      { cause: error },
    );
  }
}

function trackLocalLogout(page) {
  const events = [];
  const matches = (rawURL) => {
    const url = new URL(rawURL);
    return url.origin === new URL(base).origin && url.pathname === "/api/v1/user/logout";
  };
  page.on("request", (request) => {
    if (matches(request.url())) events.push({ type: "request", method: request.method() });
  });
  page.on("response", (response) => {
    if (matches(response.url())) events.push({ type: "response", status: response.status() });
  });
  page.on("requestfailed", (request) => {
    if (matches(request.url())) events.push({ type: "failed", error: request.failure()?.errorText || "unknown" });
  });
  return events;
}

async function login(page) {
  await startOIDC(page);
  await loginAuthentik(page);
  await expect.poll(async () => {
    try {
      await assertVikunjaSession(page, true);
      return true;
    } catch {
      return false;
    }
  }).toBe(true);
}

async function vikunjaLogout(page) {
  const menu = await firstVisibleMatch(page, [
    'button[aria-label*="user" i]',
    'button[aria-label*="account" i]',
    'button[aria-label*="profile" i]',
    '[data-testid*="user-menu"]',
    '.username-dropdown-trigger',
  ]);
  if (menu) await menu.locator.click();
  const exactLogoutLabel = /^\s*(?:Log\s*out|退出登录|注销)\s*$/i;
  const logout = await firstVisibleCandidate([
    {
      selector: 'a:text-is("Logout")|a:text-is("Log out")|a:text-is("退出登录")|a:text-is("注销")',
      locator: page.locator("a").filter({ hasText: exactLogoutLabel }),
    },
    {
      selector: 'button:not(.username-dropdown-trigger):exact-logout-label',
      locator: page.locator("button:not(.username-dropdown-trigger)").filter({ hasText: exactLogoutLabel }),
    },
    {
      selector: 'a[href*="logout"]',
      locator: page.locator('a[href*="logout"]'),
    },
  ]);
  if (!logout) {
    const text = (await page.locator("body").innerText()).replaceAll(username, "[redacted]").slice(0, 700);
    throw new Error(`Vikunja exposed no logout control; visible page: ${text}`);
  }
  const control = await logout.locator.evaluate((element, matchedSelector) => {
    const rawHref = element.getAttribute("href") || "";
    let href = "";
    if (rawHref) {
      const url = new URL(rawHref, window.location.href);
      href = `${url.origin}${url.pathname}`;
    }
    return {
      selector: matchedSelector,
      tag: element.tagName.toLowerCase(),
      text: (element.textContent || "").trim().slice(0, 120),
      href,
      class: element.getAttribute("class") || "",
      role: element.getAttribute("role") || "",
      ariaLabel: element.getAttribute("aria-label") || "",
    };
  }, logout.selector);
  control.text = control.text.replaceAll(username, "[redacted]");
  // The IAM-down case deliberately makes the navigation target unreachable.
  // Dispatch the click without treating that expected navigation stall as a
  // click failure; the tests assert local cleanup and requests separately.
  await logout.locator.click({ noWaitAfter: true });
  return control;
}

function docker(action, container) {
  if (process.env.ANAS_TEST_DOCKER_API === "true") {
    const socket = process.env.ANAS_TEST_DOCKER_SOCKET || "/var/run/docker.sock";
    execFileSync("curl", [
      "-fsS", "--unix-socket", socket, "-X", "POST", "-o", "/dev/null",
      `http://localhost/v1.41/containers/${container}/${action}`,
    ], { stdio: "ignore" });
    return;
  }
  const args = [];
  if (process.env.ANAS_TEST_DOCKER_SOCKET) {
    args.push("-H", `unix://${process.env.ANAS_TEST_DOCKER_SOCKET}`);
  }
  args.push(action, container);
  if (process.env.ANAS_TEST_DOCKER_REMOTE) {
    execFileSync("ssh", [
      "-o", "StrictHostKeyChecking=no",
      "-o", "UserKnownHostsFile=/dev/null",
      process.env.ANAS_TEST_DOCKER_REMOTE,
      process.env.DOCKER_CMD || "docker",
      ...args,
    ], { stdio: "ignore" });
    return;
  }
  execFileSync(process.env.DOCKER_CMD || "docker", args, { stdio: "ignore" });
}

test("Authentik OIDC login and RP-initiated logout", async ({ page }) => {
  requireEnvironment();
  const storageEvents = await installStorageAudit(page);
  const logoutNetworkEvents = trackLocalLogout(page);
  const providerLogoutURLs = [];
  page.on("request", (request) => {
    const url = request.url();
    if (new URL(url).hostname === `auth.${domain}` && /end.?session|logout/i.test(url)) {
      providerLogoutURLs.push(url);
    }
  });

  await login(page);
  storageEvents.length = 0;
  const logoutControl = await vikunjaLogout(page);
  await waitForVikunjaSessionCleared(page, 20_000, storageEvents, logoutNetworkEvents, logoutControl);

  const rpLogout = providerLogoutURLs.find((url) => {
    const params = new URL(url).searchParams;
    return params.has("id_token_hint") && params.has("post_logout_redirect_uri");
  });
  expect(rpLogout, "Vikunja must issue a standard RP logout request with both required parameters").toBeTruthy();
});

test("local session clears when IAM is unavailable", async ({ page }) => {
  test.skip(!pauseContainer, "requires Docker control of the IAM container");
  requireEnvironment();
  const storageEvents = await installStorageAudit(page);
  const logoutNetworkEvents = trackLocalLogout(page);
  const localLogoutRequests = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.origin === new URL(base).origin && url.pathname === "/api/v1/user/logout") {
      localLogoutRequests.push({ method: request.method(), path: url.pathname });
    }
  });
  await login(page);
  storageEvents.length = 0;
  let paused = false;
  try {
    docker("pause", pauseContainer);
    paused = true;
    const logoutControl = await vikunjaLogout(page);
    await waitForVikunjaSessionCleared(page, 5_000, storageEvents, logoutNetworkEvents, logoutControl);
    expect(localLogoutRequests).toEqual([{ method: "POST", path: "/api/v1/user/logout" }]);
  } finally {
    if (paused) docker("unpause", pauseContainer);
  }
});
