// TEST_CASES: LLNG-READY-T-005
import base from './llng-passkey.config.mjs';
import {defineConfig} from '@playwright/test';
export default defineConfig({...base, testMatch: 'llng-recovery-failure.spec.mjs',
  outputDir: '/private/tmp/anas-llng-recovery-failure', timeout: 600_000});
