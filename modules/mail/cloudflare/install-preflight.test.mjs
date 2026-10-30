// TEST_CASES: MAILMOD-T-023
import test from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, writeFile, symlink, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import { cloudflareInstallPreflight } from './install-preflight.mjs';

const input = { account: 'a'.repeat(32), token: 'synthetic-token-value', worker: 'anas-worker-test', bucket: 'anas-bucket-test' };
const envelope = (result, result_info) => Response.json({ success: true, result, ...(result_info && { result_info }) });
function fixture({ conflict = false, loop = false } = {}) {
  const calls = [];
  return { calls, fetcher: async (url, init) => {
    calls.push({ url, method: init.method, authorization: init.headers.Authorization });
    if (url.endsWith('/user/tokens/verify')) return envelope({ status: 'active' });
    if (url.endsWith('/workers/scripts')) return envelope([{ id: conflict ? input.worker : 'another-worker' }]);
    if (url.endsWith('/workers/subdomain')) return envelope({ subdomain: 'example' });
    if (url.includes('/r2/buckets')) {
      if (!url.includes('cursor=')) return envelope({ buckets: [{ name: 'other-bucket' }] }, { cursor: 'second/page' });
      return envelope({ buckets: [{ name: conflict ? input.bucket : 'new-bucket' }] }, { cursor: loop ? 'second/page' : '' });
    }
    throw Error('unexpected URL');
  }};
}

test('preflight checks every R2 page and reports uncertainty without mutating', async () => {
  const f = fixture();
  const result = await cloudflareInstallPreflight({ ...input, fetcher: f.fetcher });
  assert.deepEqual(result, { worker: 'available', bucket: 'available', workers_subdomain: 'example',
    write_permissions: 'unknown', billing_and_quota: 'unknown', installation: 'not-started' });
  assert.equal(f.calls.length, 5);
  assert.ok(f.calls.every((c) => c.method === 'GET' && c.authorization === `Bearer ${input.token}`));
  assert.ok(f.calls.some((c) => c.url.includes('cursor=second%2Fpage')));
  assert.ok(!JSON.stringify(result).includes(input.token));
});

test('existing Worker and later-page bucket are conflicts', async () => {
  const f = fixture({ conflict: true });
  const result = await cloudflareInstallPreflight({ ...input, fetcher: f.fetcher });
  assert.equal(result.worker, 'conflict');
  assert.equal(result.bucket, 'conflict');
  assert.equal(result.installation, 'not-started');
});

test('pagination loops and provider errors fail closed without exposing secrets', async () => {
  const f = fixture({ loop: true });
  await assert.rejects(cloudflareInstallPreflight({ ...input, fetcher: f.fetcher }), /provider-pagination-invalid/);
  await assert.rejects(cloudflareInstallPreflight({ ...input, fetcher: async () => { throw Error(input.token); } }), (error) =>
    error.message === 'provider-request-failed' && !error.message.includes(input.token));
  await assert.rejects(cloudflareInstallPreflight({ ...input, bucket: '../foreign', fetcher: f.fetcher }), /invalid-preflight-input/);
  await assert.rejects(cloudflareInstallPreflight({ ...input, account: [input.account], fetcher: f.fetcher }), /invalid-preflight-input/);
  await assert.rejects(cloudflareInstallPreflight({ ...input, token: `${input.token}\n`, fetcher: f.fetcher }), /invalid-preflight-input/);
});

test('unexpected provider response cannot be reported available', async () => {
  const f = fixture();
  await assert.rejects(cloudflareInstallPreflight({ ...input, fetcher: async (url, init) =>
    url.includes('/r2/buckets') ? envelope({ buckets: [] }) : f.fetcher(url, init) }), /provider-response-invalid/);
});

test('CLI rejects readable-by-others files and symlinks before any provider call', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'anas-mail-preflight-'));
  try {
    const path = join(dir, 'credentials.json');
    await writeFile(path, JSON.stringify({account_id: input.account, token: input.token}), {mode: 0o644});
    const script = fileURLToPath(new URL('./install-preflight.mjs', import.meta.url));
    const run = promisify(execFile);
    await assert.rejects(run(process.execPath, [script, path, input.worker, input.bucket]), (error) =>
      error.stderr.includes('private-credential-file-required') && !error.stderr.includes(input.token));
    const link = join(dir, 'link');
    await symlink(path, link);
    await assert.rejects(run(process.execPath, [script, link, input.worker, input.bucket]), (error) =>
      !error.stderr.includes(input.token));
  } finally {
    await rm(dir, {recursive: true, force: true});
  }
});
