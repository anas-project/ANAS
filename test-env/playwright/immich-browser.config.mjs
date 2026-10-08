import { defineConfig } from '@playwright/test';

const origin = new URL(process.env.ANAS_TEST_APP_URL || 'https://photos.invalid');
const hostAddress = process.env.ANAS_TEST_HOST_ADDRESS || '127.0.0.1';
if (!/^(127\.0\.0\.1|10\.(?:\d{1,3}\.){2}\d{1,3})$/.test(hostAddress) || hostAddress.split('.').some((part) => Number(part) > 255)) {
  throw new Error('Immich browser acceptance requires a loopback or isolated private host address');
}
if (!/^photos\.iw[a-f0-9]{8}\.immich\.test$/.test(origin.hostname) || origin.protocol !== 'https:') {
  throw new Error('Immich browser acceptance requires the isolated workspace fixture origin');
}
export default defineConfig({
  testDir: '.', testMatch: 'immich-browser.spec.mjs', workers: 1, retries: 0,
  timeout: 300_000, expect: { timeout: 30_000 },
  reporter: [['./sanitized-reporter.mjs', { outputFile: process.env.ANAS_TEST_REPORT_FILE }]],
  use: {
    baseURL: origin.origin, headless: true, ignoreHTTPSErrors: true, locale: 'en-US',
    screenshot: 'off', trace: 'off', video: 'off', actionTimeout: 30_000,
    launchOptions: {
      channel: process.env.ANAS_TEST_BROWSER_EXECUTABLE ? undefined : (process.env.ANAS_TEST_BROWSER_CHANNEL || 'chrome'),
      executablePath: process.env.ANAS_TEST_BROWSER_EXECUTABLE || undefined,
      args: ['--no-proxy-server', `--host-resolver-rules=MAP *.${origin.hostname.slice('photos.'.length)} ${hostAddress}`],
    },
  },
  outputDir: process.env.ANAS_TEST_PLAYWRIGHT_OUTPUT || '/private/tmp/anas-immich-browser',
});
