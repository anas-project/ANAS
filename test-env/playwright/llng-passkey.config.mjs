// TEST_CASES: LLNG-READY-T-002
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.', testMatch: 'llng-passkey.spec.mjs',
  timeout: 240_000, expect: { timeout: 20_000 },
  workers: 1, retries: 0,
  reporter: [['line'], ['./sanitized-reporter.mjs', { outputFile: process.env.ANAS_TEST_REPORT_FILE }]],
  use: {
    browserName: 'chromium', ignoreHTTPSErrors: true,
    screenshot: 'off', trace: 'off', video: 'off',
    launchOptions: { args: ['--host-resolver-rules=MAP *.llngtest.invalid 127.0.0.1'] },
  },
  outputDir: '/private/tmp/anas-llng-passkey',
});
