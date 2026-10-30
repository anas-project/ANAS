// TEST_CASES: LLNG-READY-T-005
import base from './llng-passkey.config.mjs';
export default {...base, timeout: 360_000, testMatch: 'llng-key-rotation.spec.mjs', outputDir: '/private/tmp/anas-llng-key-rotation'};
