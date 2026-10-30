// TEST_CASES: LLNG-READY-T-004
import base from './llng-passkey.config.mjs';
import { defineConfig } from '@playwright/test';
export default defineConfig({...base, testMatch: 'llng-theme.spec.mjs',
  outputDir: '/private/tmp/anas-llng-theme', timeout: 60_000});
