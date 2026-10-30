// TEST_CASES: MAILMOD-T-024
// Real read-only Cloudflare preflight; no resource creation or DNS changes.
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { isAbsolute, resolve } from 'node:path';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';

const credential = process.env.MAIL_CLOUDFLARE_TEST_CREDENTIAL_FILE;
if (!credential || !isAbsolute(credential)) throw Error('explicit private credential file required');
const preflight = resolve(fileURLToPath(new URL('../../modules/mail/cloudflare/install-preflight.mjs', import.meta.url)));
const name = `anas-preflight-${randomBytes(8).toString('hex')}`;
try {
  const run = promisify(execFile);
  const noCredential = { ...process.env };
  delete noCredential.MAIL_CLOUDFLARE_TEST_CREDENTIAL_FILE;
  await assert.rejects(run(process.execPath, [fileURLToPath(import.meta.url)], { env: noCredential, timeout: 5000 }),
    (error) => error.stdout === '' && error.stderr.includes('explicit private credential file required'));
  const result = await run(process.execPath, [preflight, credential, name, name], { timeout: 90000, maxBuffer: 65536 });
  const value = JSON.parse(result.stdout);
  assert.equal(value.worker, 'available');
  assert.equal(value.bucket, 'available');
  assert.equal(value.write_permissions, 'unknown');
  assert.equal(value.billing_and_quota, 'unknown');
  assert.equal(value.installation, 'not-started');
  process.stdout.write('PASS: read-only Cloudflare preflight; installation not started\n');
} catch {
  // No provider error body, token, credentials, or child command is exposed.
  process.stderr.write('FAIL: read-only Cloudflare preflight\n');
  process.exitCode = 1;
}
