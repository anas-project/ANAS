// TEST_CASES: LLNG-READY-T-001
import base from './llng-passkey.config.mjs';
import { defineConfig } from '@playwright/test';
export default defineConfig({...base, testMatch: 'llng-portal.spec.mjs',
  outputDir: '/private/tmp/anas-llng-portal', timeout: 240_000});
