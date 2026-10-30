// TEST_CASES: LLNG-READY-T-006
import base from './llng-passkey.config.mjs';
import { defineConfig } from '@playwright/test';
export default defineConfig({...base, testMatch: 'llng-nextcloud.spec.mjs',
  outputDir: '/private/tmp/anas-llng-nextcloud', timeout: 240_000});
