// TEST_CASES: LLNG-READY-T-003
import base from './llng-passkey.config.mjs';
export default { ...base, use: { ...base.use, serviceWorkers: 'block' }, testMatch: 'llng-public-oidc.spec.mjs', outputDir: '/private/tmp/anas-llng-public-oidc' };
