// TEST_CASES: TEMP-T-021
import { defineConfig } from "@playwright/test";

const domain = process.env.ANAS_TEST_DOMAIN;
const entryIP = process.env.ANAS_TEST_ENTRY_IP;
const channel = process.env.ANAS_TEST_BROWSER_CHANNEL;
if (channel && channel !== "chrome") throw new Error("only installed Chrome is supported as a browser override");
if (!domain || !entryIP || !process.env.ANAS_TEST_REPORT_FILE) {
  throw new Error("explicit test domain, entry IP, and report path are required");
}

export default defineConfig({
  testDir: ".",
  testMatch: "workspace-temp-storage-collabora.spec.mjs",
  timeout: 3_600_000,
  expect: { timeout: 30_000 },
  workers: 1,
  fullyParallel: false,
  retries: 0,
  reporter: [["./sanitized-reporter.mjs", { outputFile: process.env.ANAS_TEST_REPORT_FILE }]],
  use: {
    browserName: "chromium",
    ...(channel ? { channel } : {}),
    headless: process.env.ANAS_TEST_HEADED !== "1",
    ignoreHTTPSErrors: true,
    actionTimeout: 30_000,
    navigationTimeout: 120_000,
    screenshot: "off",
    trace: "off",
    video: "off",
    launchOptions: { args: [`--host-resolver-rules=MAP *.${domain} ${entryIP}`] },
  },
  outputDir: process.env.ANAS_TEST_PLAYWRIGHT_OUTPUT || "/tmp/anas-temp-storage-playwright",
});
