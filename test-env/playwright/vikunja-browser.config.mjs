import { defineConfig } from "@playwright/test";

const domain = process.env.ANAS_TEST_DOMAIN || "vikunja.test";
const entryIP = process.env.ANAS_TEST_ENTRY_IP || "127.0.0.1";

export default defineConfig({
  testDir: ".",
  testMatch: "vikunja-browser.spec.mjs",
  timeout: Number(process.env.ANAS_TEST_BROWSER_TIMEOUT || 180_000),
  expect: { timeout: 20_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [
    ["line"],
    ["./sanitized-reporter.mjs", { outputFile: process.env.ANAS_TEST_REPORT_FILE }],
  ],
  use: {
    browserName: "chromium",
    headless: process.env.ANAS_TEST_HEADLESS !== "false",
    ignoreHTTPSErrors: true,
    actionTimeout: 20_000,
    navigationTimeout: 60_000,
    screenshot: "off",
    trace: "off",
    video: "off",
    launchOptions: {
      args: [`--host-resolver-rules=MAP *.${domain} ${entryIP}`],
    },
  },
  outputDir: process.env.ANAS_TEST_PLAYWRIGHT_OUTPUT || "/private/tmp/anas-vikunja-playwright",
});
