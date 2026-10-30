// TEST_CASES: MAILMOD-T-027
// Tests only a random empty R2 bucket. Never deletes a bucket unless its
// returned creation metadata still matches and a fresh object listing is empty.
import { constants } from 'node:fs';
import { open } from 'node:fs/promises';
import { randomBytes, createHash } from 'node:crypto';
import { isAbsolute } from 'node:path';

const credentialPath = process.env.MAIL_CLOUDFLARE_TEST_CREDENTIAL_FILE;
const journalPath = process.env.MAIL_CLOUDFLARE_R2_TEST_JOURNAL;
if (!credentialPath || !journalPath || !isAbsolute(credentialPath) || !isAbsolute(journalPath))
  throw Error('explicit private credential and new journal paths required');
const input = await open(credentialPath, constants.O_RDONLY | constants.O_NOFOLLOW);
let credential;
try {
  const info = await input.stat();
  if (!info.isFile() || info.nlink !== 1 || info.size > 16384 || info.mode & 0o077 || info.uid !== process.getuid())
    throw Error('private credential file required');
  credential = JSON.parse(await input.readFile('utf8'));
} finally { await input.close(); }
if (typeof credential.account_id !== 'string' || !/^[a-f0-9]{32}$/.test(credential.account_id) ||
    typeof credential.token !== 'string' || !/^[\x21-\x7e]{16,512}$/.test(credential.token))
  throw Error('invalid credential file');
const journal = await open(journalPath, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, 0o600);
const state = { name: `anas-r2-poc-${randomBytes(8).toString('hex')}`, phase: 'prepared', checks: [], cleanup: [], unknown: false };
async function record(phase) { state.phase = phase; await journal.writeFile(JSON.stringify(state) + '\n'); await journal.sync(); }
const api = `https://api.cloudflare.com/client/v4/accounts/${credential.account_id}/r2/buckets`;
async function request(path, method, body) {
  const response = await fetch(api + path, { method, headers: { Authorization: `Bearer ${credential.token}`,
    ...(body ? { 'Content-Type': 'application/json' } : {}) }, body: body ? JSON.stringify(body) : undefined,
    redirect: 'error', signal: AbortSignal.timeout(30000) });
  const raw = await response.text();
  if (raw.length > 65536) throw Error('provider response too large');
  const data = JSON.parse(raw);
  return { status: response.status, ok: response.ok && data?.success === true, result: data?.result,
    resultInfo: data?.result_info, codes: Array.isArray(data?.errors) ? data.errors.map((v) => v?.code).filter(Number.isInteger) : [] };
}
function identity(bucket) {
  if (!bucket || bucket.name !== state.name || typeof bucket.creation_date !== 'string' || !Number.isFinite(Date.parse(bucket.creation_date)))
    throw Error('bucket identity incomplete');
  const fields = [bucket.name, bucket.creation_date, bucket.jurisdiction ?? '', bucket.location ?? '', bucket.storage_class ?? ''];
  if (fields.some((v) => typeof v !== 'string')) throw Error('bucket identity invalid');
  return createHash('sha256').update(JSON.stringify(fields)).digest('hex');
}
let created = false, failure = false;
try {
  await record('prepared');
  const before = await request('/' + state.name, 'GET');
  state.precheck_status = before.status;
  state.precheck_codes = before.codes;
  await record('prechecked');
  if (before.status !== 404) throw Error('name availability not established');
  await record('create-issued');
  let result;
  try { result = await request('', 'POST', {name: state.name}); }
  catch { state.unknown = true; throw Error('create outcome unknown'); }
  if (!result.ok) { if (result.status >= 500) state.unknown = true; throw Error('create not confirmed'); }
  created = true;
  try { state.fingerprint = identity(result.result); }
  catch { state.unknown = true; throw Error('created bucket identity unknown'); }
  await record('created');
  const observed = await request('/' + state.name, 'GET');
  if (!observed.ok || identity(observed.result) !== state.fingerprint) throw Error('bucket readback mismatch');
  state.checks.push('creation-metadata-matches');
  await record('duplicate-issued');
  let duplicate;
  try { duplicate = await request('', 'POST', {name: state.name}); }
  catch { state.unknown = true; throw Error('duplicate outcome unknown'); }
  state.duplicate_status = duplicate.status;
  state.duplicate_codes = duplicate.codes;
  if (duplicate.ok || duplicate.status !== 409 || !duplicate.codes.includes(10004)) {
    state.unknown = true;
    throw Error('duplicate did not give a definite rejection');
  }
  const after = await request('/' + state.name, 'GET');
  if (!after.ok || identity(after.result) !== state.fingerprint) throw Error('bucket changed after duplicate');
  state.checks.push('duplicate-rejected-original-retained');
  await record('checked');
} catch { failure = true; state.failed_at = state.phase; await record('failed'); }
finally {
  if (created && state.fingerprint) {
    try {
      const current = await request('/' + state.name, 'GET');
      const objects = await request('/' + state.name + '/objects?per_page=1', 'GET');
      const info = objects.resultInfo;
      const invalidInfo = info != null && (typeof info !== 'object' || Array.isArray(info) ||
        ('is_truncated' in info && info.is_truncated !== false) || ('cursor' in info && info.cursor !== ''));
      if (!current.ok || identity(current.result) !== state.fingerprint || !objects.ok ||
          !Array.isArray(objects.result) || objects.result.length !== 0 || invalidInfo)
        state.cleanup.push('ownership-or-emptiness-unknown');
      else {
        await record('delete-issued');
        const deleted = await request('/' + state.name, 'DELETE');
        if (!deleted.ok) state.cleanup.push('delete-unconfirmed');
        else {
          const verify = await request('/' + state.name, 'GET');
          state.cleanup.push(verify.status === 404 ? 'deleted-confirmed' : 'delete-unconfirmed');
        }
      }
    } catch { state.cleanup.push('delete-unknown'); }
  } else if (created || state.unknown) state.cleanup.push('ownership-unknown');
  state.passed = !failure && !state.unknown && state.checks.length === 2 && state.cleanup.length === 1 && state.cleanup[0] === 'deleted-confirmed';
  await record('finished');
  await journal.close();
}
if (!state.passed) { process.stderr.write('FAIL: isolated R2 identity test; inspect private journal\n'); process.exitCode = 1; }
else process.stdout.write('PASS: R2 creation metadata read back, duplicate rejected, empty test bucket deleted\n');
