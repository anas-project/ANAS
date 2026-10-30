// TEST_CASES: MAILMOD-T-025
// Creates one random isolated Worker, checks duplicate creation, and removes
// only Worker IDs whose name and identity were independently read back.
import { constants } from 'node:fs';
import { open } from 'node:fs/promises';
import { randomBytes } from 'node:crypto';
import { isAbsolute } from 'node:path';

const credentialPath = process.env.MAIL_CLOUDFLARE_TEST_CREDENTIAL_FILE;
const journalPath = process.env.MAIL_CLOUDFLARE_CREATE_TEST_JOURNAL;
if (!credentialPath || !journalPath || !isAbsolute(credentialPath) || !isAbsolute(journalPath))
  throw Error('explicit private credential and new journal paths required');
let credential;
const input = await open(credentialPath, constants.O_RDONLY | constants.O_NOFOLLOW);
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
const state = { name: `anas-create-poc-${randomBytes(8).toString('hex')}`, phase: 'prepared', ids: [], checks: [], cleanup: [], unknown: false };
async function record(phase) {
  state.phase = phase;
  await journal.writeFile(JSON.stringify(state) + '\n');
  await journal.sync();
}
const api = `https://api.cloudflare.com/client/v4/accounts/${credential.account_id}/workers/workers`;
async function request(path, method, body) {
  const response = await fetch(api + path, {
    method, headers: { Authorization: `Bearer ${credential.token}`, ...(body ? { 'Content-Type': 'application/json' } : {}) },
    body: body ? JSON.stringify(body) : undefined, redirect: 'error', signal: AbortSignal.timeout(30000),
  });
  const text = await response.text();
  if (text.length > 65536) throw Error('provider response too large');
  const json = JSON.parse(text);
  return { status: response.status, ok: response.ok && json?.success === true, result: json?.result,
    codes: Array.isArray(json?.errors) ? json.errors.map((item) => item?.code).filter(Number.isInteger) : [] };
}
const validID = (value) => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
let failure = false;
try {
  await record('prepared');
  await record('create-issued');
  let first;
  try { first = await request('', 'POST', { name: state.name }); }
  catch { state.unknown = true; throw Error('create outcome unknown'); }
  if (!first.ok) {
    if (first.status >= 500) state.unknown = true;
    throw Error('create not confirmed');
  }
  if (!validID(first.result?.id) || first.result?.name !== state.name) {
    state.unknown = true;
    throw Error('create response malformed');
  }
  state.ids.push(first.result.id);
  await record('created');
  const observed = await request('/' + encodeURIComponent(first.result.id), 'GET');
  if (!observed.ok || observed.result?.id !== first.result.id || observed.result?.name !== state.name)
    throw Error('ownership readback failed');
  state.checks.push('created-id-readback');
  await record('duplicate-issued');
  let second;
  try { second = await request('', 'POST', { name: state.name }); }
  catch { state.unknown = true; throw Error('duplicate outcome unknown'); }
  state.duplicate_status = second.status;
  state.duplicate_codes = second.codes;
  if (second.ok) {
    if (validID(second.result?.id) && second.result?.name === state.name && !state.ids.includes(second.result.id))
      state.ids.push(second.result.id);
    else if (!validID(second.result?.id) || second.result?.name !== state.name) state.unknown = true;
    throw Error('duplicate name unexpectedly accepted');
  }
  if (second.status !== 409 || !second.codes.includes(10040)) {
    state.unknown = true;
    throw Error('duplicate outcome unknown');
  }
  const after = await request('/' + encodeURIComponent(first.result.id), 'GET');
  if (!after.ok || after.result?.id !== first.result.id || after.result?.name !== state.name)
    throw Error('original changed after duplicate');
  state.checks.push('duplicate-rejected-original-retained');
  await record('checked');
} catch {
  failure = true;
  await record('failed');
} finally {
  for (const id of state.ids) {
    try {
      const current = await request('/' + encodeURIComponent(id), 'GET');
      if (!current.ok || current.result?.id !== id || current.result?.name !== state.name) {
        state.cleanup.push('ownership-unknown');
      } else {
        await record('delete-issued');
        const deleted = await request('/' + encodeURIComponent(id), 'DELETE');
        if (!deleted.ok) state.cleanup.push('delete-unconfirmed');
        else {
          const verify = await request('/' + encodeURIComponent(id), 'GET');
          state.cleanup.push(verify.status === 404 ? 'deleted-confirmed' : 'delete-unconfirmed');
        }
      }
    } catch { state.cleanup.push('delete-unknown'); }
    await record('cleanup-observed');
  }
  state.passed = !failure && !state.unknown && state.ids.length === 1 && state.cleanup.length === 1 && state.cleanup[0] === 'deleted-confirmed';
  await record('finished');
  await journal.close();
}
if (!state.passed) { process.stderr.write('FAIL: isolated Worker create-only test; inspect private journal\n'); process.exitCode = 1; }
else process.stdout.write('PASS: duplicate Worker creation rejected; isolated Worker deleted\n');
