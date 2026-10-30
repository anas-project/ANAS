// TEST_CASES: LLNG-READY-T-008
import base from './llng-passkey.config.mjs';
export default {...base, timeout: 720_000, testMatch: 'llng-upgrade.spec.mjs', outputDir: '/private/tmp/anas-llng-upgrade'};
