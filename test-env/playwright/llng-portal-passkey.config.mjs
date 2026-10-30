// TEST_CASES: LLNG-READY-T-002
import base from './llng-passkey.config.mjs';
import {defineConfig} from '@playwright/test';
export default defineConfig({...base, testMatch: 'llng-portal-passkey.spec.mjs',
  outputDir: '/private/tmp/anas-llng-portal-passkey', timeout: 300_000});
