// TEST_CASES: LLNG-READY-T-005
import base from './llng-passkey.config.mjs';
import {defineConfig} from '@playwright/test';
export default defineConfig({...base, testMatch: 'llng-service-rotation.spec.mjs',
  outputDir: '/private/tmp/anas-llng-service-rotation', timeout: 600_000});
