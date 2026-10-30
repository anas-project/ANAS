import { defineConfig } from '@playwright/test';

// TEST_CASES: LLNG-PWD-T-002, LLNG-PWD-T-003

const domain = process.env.ANAS_TEST_DOMAIN || 'llngtest.invalid';
export default defineConfig({
  testDir: '.',
  testMatch: 'llng-password-oidc.spec.mjs',
  timeout: 120_000,
  expect: { timeout: 20_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['line'], ['./sanitized-reporter.mjs', { outputFile: process.env.ANAS_TEST_REPORT_FILE }]],
  use: {
    browserName: 'chromium',
    ignoreHTTPSErrors: true,
    screenshot: 'off', trace: 'off', video: 'off',
    launchOptions: { args: [`--host-resolver-rules=MAP *.${domain} ${process.env.ANAS_TEST_ENTRY_IP || '127.0.0.1'}`] },
  },
  outputDir: process.env.ANAS_TEST_PLAYWRIGHT_OUTPUT || '/private/tmp/anas-llng-password-oidc',
});
