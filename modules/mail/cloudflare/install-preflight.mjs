// Read-only Cloudflare installer preflight. It does not create resources,
// prove write permissions, enable Email Routing, or establish mail delivery.
import { constants } from 'node:fs';
import { open } from 'node:fs/promises';
import { isAbsolute } from 'node:path';
import { pathToFileURL } from 'node:url';

const API = 'https://api.cloudflare.com/client/v4';
const NAME = /^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])$/;
const SUBDOMAIN = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;
const ACCOUNT = /^[a-f0-9]{32}$/;
const MAX_BODY = 2 * 1024 * 1024;

async function bodyJSON(response) {
  if (!response.body) throw Error('provider-response-invalid');
  const reader = response.body.getReader();
  const chunks = [];
  let length = 0;
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      length += value.byteLength;
      if (length > MAX_BODY) throw Error('provider-response-too-large');
      chunks.push(value);
    }
    return JSON.parse(Buffer.concat(chunks).toString('utf8'));
  } finally {
    await reader.cancel().catch(() => {});
  }
}

export async function cloudflareInstallPreflight({ account, token, worker, bucket, fetcher = fetch }) {
  if (typeof account !== 'string' || !ACCOUNT.test(account) || typeof token !== 'string' ||
      !/^[\x21-\x7e]{16,512}$/.test(token) || typeof worker !== 'string' || typeof bucket !== 'string' ||
      !NAME.test(worker) || !NAME.test(bucket)) throw Error('invalid-preflight-input');
  async function get(path) {
    let response;
    try {
      response = await fetcher(API + path, {
        method: 'GET', headers: { Authorization: `Bearer ${token}`, Accept: 'application/json' },
        redirect: 'error', signal: AbortSignal.timeout(15000),
      });
      const json = await bodyJSON(response);
      if (!response.ok || json?.success !== true) throw Error('provider-request-failed');
      return json;
    } catch {
      // Do not return provider bodies, Authorization values, or raw errors.
      throw Error('provider-request-failed');
    }
  }
  const accountPath = `/accounts/${account}`;
  const verified = await get('/user/tokens/verify');
  if (verified.result?.status !== 'active') throw Error('token-not-active');
  const scripts = await get(`${accountPath}/workers/scripts`);
  if (!Array.isArray(scripts.result) || scripts.result.some((s) => typeof s?.id !== 'string'))
    throw Error('provider-response-invalid');
  let bucketConflict = false, cursor = '';
  const seen = new Set();
  for (let page = 0; page < 100; page++) {
    const suffix = cursor ? `?per_page=1000&cursor=${encodeURIComponent(cursor)}` : '?per_page=1000';
    const result = await get(`${accountPath}/r2/buckets${suffix}`);
    if (!Array.isArray(result.result?.buckets) || !result.result_info ||
        result.result.buckets.some((b) => typeof b?.name !== 'string')) throw Error('provider-response-invalid');
    bucketConflict ||= result.result.buckets.some((b) => b.name === bucket);
    const next = result.result_info.cursor ?? '';
    if (typeof next !== 'string' || next.length > 2048 || seen.has(next)) throw Error('provider-pagination-invalid');
    if (!next) {
      cursor = '';
      break;
    }
    seen.add(next);
    cursor = next;
  }
  if (cursor) throw Error('provider-pagination-limit');
  const subdomain = await get(`${accountPath}/workers/subdomain`);
  if (typeof subdomain.result?.subdomain !== 'string' || !SUBDOMAIN.test(subdomain.result.subdomain))
    throw Error('provider-response-invalid');
  return {
    worker: scripts.result.some((s) => s.id === worker) ? 'conflict' : 'available',
    bucket: bucketConflict ? 'conflict' : 'available',
    workers_subdomain: subdomain.result.subdomain,
    write_permissions: 'unknown', billing_and_quota: 'unknown', installation: 'not-started',
  };
}

async function privateCredentials(path) {
  if (!isAbsolute(path)) throw Error('private-credential-file-required');
  const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  let data;
  try {
    const info = await file.stat();
    if (!info.isFile() || info.nlink !== 1 || info.size > 16384 || (info.mode & 0o077) ||
        (typeof process.getuid === 'function' && info.uid !== process.getuid())) throw Error('private-credential-file-required');
    const buffer = Buffer.alloc(16385);
    let offset = 0;
    while (offset < buffer.length) {
      const { bytesRead } = await file.read(buffer, offset, buffer.length - offset, null);
      if (!bytesRead) break;
      offset += bytesRead;
    }
    if (offset > 16384) throw Error('private-credential-file-required');
    data = JSON.parse(buffer.subarray(0, offset).toString('utf8'));
  } finally {
    await file.close();
  }
  if (!data || typeof data !== 'object' || Array.isArray(data) || typeof data.account_id !== 'string' || !ACCOUNT.test(data.account_id) || typeof data.token !== 'string')
    throw Error('private-credential-file-invalid');
  return data;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.argv.length !== 5) throw Error('usage: node install-preflight.mjs PRIVATE_CREDENTIAL_FILE WORKER_NAME BUCKET_NAME');
    const credential = await privateCredentials(process.argv[2]);
    const result = await cloudflareInstallPreflight({account: credential.account_id, token: credential.token, worker: process.argv[3], bucket: process.argv[4]});
    process.stdout.write(JSON.stringify(result, null, 2) + '\n');
  } catch (error) {
    const allowed = new Set(['invalid-preflight-input', 'private-credential-file-required', 'private-credential-file-invalid',
      'provider-request-failed', 'provider-response-invalid', 'provider-pagination-invalid', 'provider-pagination-limit', 'token-not-active']);
    const message = String(error?.message || '');
    process.stderr.write((allowed.has(message) || message.startsWith('usage:') ? message : 'preflight-failed') + '\n');
    process.exitCode = 1;
  }
}
