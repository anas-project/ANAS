// Explicitly invoked cloud integration experiment; creates and removes only new fixtures.
import assert from 'node:assert/strict';
import { readFile, writeFile, stat, mkdir } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { randomBytes, createHash } from 'node:crypto';

const credentialPath = process.env.MAIL_POC_CREDENTIAL_FILE;
const reportDir = process.env.MAIL_POC_REPORT_DIR;
if (!credentialPath || !reportDir) throw Error('Set MAIL_POC_CREDENTIAL_FILE and MAIL_POC_REPORT_DIR');
if ((await stat(credentialPath)).mode & 0o077) throw Error('Credential file must be private');
const credential = JSON.parse(await readFile(credentialPath, 'utf8'));
assert.match(credential.account_id, /^[a-f0-9]{32}$/);
await mkdir(reportDir, { recursive: true, mode: 0o700 });
if ((await stat(reportDir)).mode & 0o077) throw Error('Report directory must be private');
const suffix = randomBytes(4).toString('hex');
const name = `anas-mail-poc-20261024-${suffix}`;
const state = { name, bucket: name, worker: name, sink: `${name}-sink`,
  created: {}, cleaned: {}, started: new Date().toISOString(), tests: [], versions: [] };
const statePath = join(resolve(reportDir), `${name}.json`);
const testToken = randomBytes(32).toString('hex'), pullToken = randomBytes(32).toString('hex');
const notifySecret = randomBytes(32).toString('hex');
const source = `poc-${suffix}`;
const expires = String(Date.now() + 2 * 3600000);
const account = `/accounts/${credential.account_id}`;
const save = () => writeFile(statePath, JSON.stringify(state, null, 2), { mode: 0o600 });
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
async function api(path, method = 'GET', body) {
  const response = await fetch(`https://api.cloudflare.com/client/v4${path}`, {
    method, headers: { Authorization: `Bearer ${credential.token}`,
      ...(body && !(body instanceof FormData) ? { 'Content-Type': 'application/json' } : {}) },
    body: body instanceof FormData ? body : body ? JSON.stringify(body) : undefined,
    redirect: 'error', signal: AbortSignal.timeout(45000),
  });
  const data = await response.json();
  if (!response.ok || !data.success) {
    // Never log raw response objects, credentials, binding metadata or request headers.
    throw Error(`Cloudflare ${method} ${path.replace(credential.account_id, 'ACCOUNT')}: HTTP ${response.status}, codes ${(data.errors || []).map(x => x.code).join(',')}`);
  }
  return data.result;
}
const binding = (name, text, secret = false) => ({ name, type: secret ? 'secret_text' : 'plain_text', text });
const commonBindings = () => [ { name: 'MAIL', type: 'r2_bucket', bucket_name: state.bucket },
  binding('EXPIRES', expires), binding('NOTIFY_SECRET', notifySecret, true) ];
const files = {};
for (const file of ['worker.mjs', 'cloud-fixture.mjs', 'cloud-sink.mjs', 'cloud-test.mjs'])
  files[file] = await readFile(new URL(file, import.meta.url), 'utf8');
state.hashes = Object.fromEntries(Object.entries(files).map(([file, code]) =>
  [file, createHash('sha256').update(code).digest('hex')]));
let base, sinkURL;
async function upload(script, main, bindings, revision) {
  const form = new FormData();
  form.set('metadata', new Blob([JSON.stringify({ main_module: main, bindings,
    compatibility_date: '2026-07-01', compatibility_flags: ['global_fetch_strictly_public'],
    tags: ['anas-mail-isolated-poc'] })], { type: 'application/json' }));
  for (const file of main === 'cloud-fixture.mjs' ? [main, 'worker.mjs'] : [main])
    form.set(file, new Blob([files[file]], { type: 'application/javascript+module' }), file);
  const result = await api(`${account}/workers/scripts/${script}`, 'PUT', form);
  state.created[script] = true;
  state.versions.push({ script, revision, id: result.deployment_id || result.version_id || result.etag });
  await save();
}
async function deployMain(revision) {
  await upload(state.worker, 'cloud-fixture.mjs', [...commonBindings(), binding('SOURCE', source),
    binding('ROUTES', JSON.stringify({ 'a@example.org': 'box-a', 'b@example.org': 'box-b' })),
    binding('PULL_TOKEN', pullToken, true), binding('TEST_TOKEN', testToken, true),
    binding('REVISION', revision), binding('SINK_URL', sinkURL)], revision);
}
async function request(path, { method = 'GET', body, token = path.startsWith('/__') ? testToken : pullToken } = {}) {
  state.httpRequests = (state.httpRequests || 0) + 1;
  return fetch(base + path, { method, body, headers: { Authorization: `Bearer ${token}` },
    redirect: 'error', signal: AbortSignal.timeout(20000) });
}
async function json(path, options) {
  const r = await request(path, options);
  assert.equal(r.status, 200, `Fixture HTTP failure at ${path.split('?')[0]}`);
  return r.json();
}
const raw = 'From: sender@example.net\r\nTo: forged@example.org\r\nMessage-ID: <same@sender>\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n你好\r\n--x\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n\r\nAAECA/8=\r\n--x--\r\n';
const receive = (to = 'a@example.org') => json(`/__receive?to=${encodeURIComponent(to)}`, { method: 'POST', body: raw });
const notify = async (id, mode = 'ok', advance = 0) => {
  const result = await json(`/__notify?id=${id}&mode=${mode}&advance=${advance}`, { method: 'POST' });
  (state.notifications ||= []).push({ id, mode, advance, result, detail: await inspect(id) });
  await save();
  return result;
};
const inspect = id => json(`/__inspect?id=${id}`);
const ack = (id, receipt = 'simulated-nas-receipt', target = 'box-a') => request(`/messages/${id}/ack`,
  { method: 'POST', body: JSON.stringify({ source, target, receipt }) });
const verifyRaw = async id => assert.equal(await (await request(`/messages/${id}/raw`)).text(), raw);
async function allMessages() {
  let cursor, messages = [], pages = 0;
  do {
    const page = await json('/messages?limit=2' + (cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''));
    assert.equal(page.source, source);
    messages.push(...page.messages); cursor = page.cursor;
    assert.ok(++pages <= 30, 'pagination must terminate');
  } while (cursor);
  return messages;
}
async function waitRevision(revision) {
  for (let n = 0; n < 60; n++) {
    try { if ((await json('/__health')).revision === revision) return; } catch { /* propagation */ }
    await sleep(2000);
  }
  throw Error(`Worker revision ${revision} did not become ready`);
}
async function check(title, fn) {
  const started = Date.now();
  try { await fn(); state.tests.push({ title, result: 'pass', elapsed_ms: Date.now() - started }); }
  catch (e) { state.tests.push({ title, result: 'fail', error: e.message }); throw e; }
  finally { await save(); console.log(`${state.tests.at(-1).result.toUpperCase()} ${title}`); }
}
await save();
try {
  const tokenStatus = await api('/user/tokens/verify');
  assert.equal(tokenStatus.status, 'active');
  state.token_expires = tokenStatus.expires_on;
  const existing = await api(`${account}/workers/scripts`);
  assert.ok(!existing.some(x => [state.worker, state.sink].includes(x.id)), 'Never overwrite an existing Worker');
  const buckets = await api(`${account}/r2/buckets`);
  assert.ok(!buckets.buckets.some(x => x.name === state.bucket), 'Never reuse an existing bucket');
  const subdomain = await api(`${account}/workers/subdomain`);
  base = `https://${state.worker}.${subdomain.subdomain}.workers.dev`;
  sinkURL = `https://${state.sink}.${subdomain.subdomain}.workers.dev`;
  await api(`${account}/r2/buckets`, 'POST', { name: state.bucket, storageClass: 'Standard' });
  state.created.bucket = true; await save();
  await upload(state.sink, 'cloud-sink.mjs', commonBindings(), 'sink-1');
  await deployMain('1');
  for (const script of [state.sink, state.worker])
    await api(`${account}/workers/scripts/${script}/subdomain`, 'POST', { enabled: true, previews_enabled: false });
  await waitRevision('1');
  // Each workers.dev route propagates independently; main readiness says nothing about the sink.
  let sinkReady = false;
  state.sinkReadiness = [];
  for (let n = 0; n < 60; n++) {
    const r = await fetch(sinkURL + '/ok', { method: 'POST', body: '{}', redirect: 'error',
      signal: AbortSignal.timeout(10000) });
    state.sinkReadiness.push({ status: r.status, at: new Date().toISOString() });
    await save();
    if (r.status === 401) { sinkReady = true; break; }
    await sleep(2000);
  }
  assert.equal(sinkReady, true, 'Notification fixture must become ready independently');
  console.log(`Cloud fixtures ready: ${name}`);
  await check('Private R2 and authenticated HTTP boundaries', async () => {
    const managed = await api(`${account}/r2/buckets/${state.bucket}/domains/managed`);
    assert.equal(managed.enabled, false);
    assert.equal((await request('/messages', { token: 'wrong' })).status, 401, 'Pull API rejects wrong token');
    assert.equal((await request('/__receive?to=a@example.org', { method: 'POST', body: raw, token: pullToken })).status, 401, 'Test injection rejects pull token');
    assert.equal((await fetch(sinkURL + '/ok', { method: 'POST', body: '{}', redirect: 'error' })).status, 401, 'Sink rejects missing HMAC');
  });
  let first;
  await check('Envelope target isolation, forged headers and identical Message-ID', async () => {
    first = (await receive()).id;
    const b = (await receive('b@example.org')).id;
    assert.notEqual(first, b);
    assert.equal((await receive('removed@example.org')).rejected, 'Unknown recipient');
    const messages = await allMessages();
    assert.equal(messages.find(x => x.id === first).target, 'box-a');
    assert.equal(messages.find(x => x.id === b).target, 'box-b');
    assert.equal((await ack(first, 'cross-target', 'box-b')).status, 400);
    await verifyRaw(b);
  });
  await check('NAS 503 and Worker redeployment preserve R2 bytes and pending delivery', async () => {
    assert.equal((await notify(first, 'offline')).state, 'failed');
    await deployMain('2'); await waitRevision('2');
    await verifyRaw(first);
    const messages = await allMessages();
    assert.equal(messages.find(x => x.id === first).delivered, false);
    assert.equal((await inspect(first)).sinkCalls, 1);
  });
  await check('Successful signed notification is distinct from final delivery', async () => {
    const id = (await receive()).id;
    assert.equal((await notify(id)).state, 'notified');
    const view = await inspect(id);
    assert.ok(view[`notified/${id}`]); assert.equal(view[`ack/${id}`], null);
    assert.equal(view.rawPresent, true); assert.equal(view.sinkCalls, 1);
  });
  await check('Concurrent and repeated receipts are immutable and retain raw bytes', async () => {
    const id = (await receive()).id;
    const responses = await Promise.all([ack(id, 'receipt-a'), ack(id, 'receipt-b')]);
    assert.ok(responses.every(x => x.status === 200));
    const before = (await inspect(id))[`ack/${id}`];
    assert.ok(['receipt-a', 'receipt-b'].includes(before.receipt));
    assert.equal((await ack(id, 'receipt-c')).status, 200);
    assert.deepEqual((await inspect(id))[`ack/${id}`], before);
    assert.equal((await notify(id)).state, 'delivered');
    await verifyRaw(id);
  });
  await check('Actual HTTPS timeout, durable bounded attempts and polling after failure', async () => {
    const id = (await receive()).id;
    const start = Date.now();
    assert.equal((await notify(id, 'timeout')).state, 'failed');
    const elapsed = Date.now() - start;
    assert.ok(elapsed >= 4900 && elapsed < 12000, `timeout elapsed ${elapsed}`);
    state.timeout_roundtrip_with_inspection_ms = elapsed;
    const attempt = (await inspect(id))[`attempt/${id}/0`];
    assert.equal(attempt.failure, 'timeout', 'Must distinguish timeout from eventual HTTP 503');
    assert.equal(attempt.status, undefined);
    assert.ok(attempt.request_ms >= 4900 && attempt.request_ms < 6000);
    state.timeout_request_ms = attempt.request_ms;
    assert.equal((await notify(id)).state, 'waiting');
    // Retry spacing uses injected time; this is not a five-minute wall-clock test.
    assert.equal((await notify(id, 'busy', 61000)).state, 'failed');
    assert.equal((await notify(id, 'offline', 301000)).state, 'failed');
    assert.equal((await notify(id, 'ok', 601000)).state, 'exhausted');
    assert.equal((await inspect(id)).sinkCalls, 3);
    await verifyRaw(id);
    assert.equal((await ack(id)).status, 200);
    assert.equal((await notify(id)).state, 'delivered');
  });
  await check('Redirect and authentication failure stop; 429 can recover', async () => {
    for (const mode of ['redirect', 'denied']) {
      const id = (await receive()).id;
      assert.equal((await notify(id, mode)).state, 'configuration-error');
      assert.equal((await notify(id, 'ok', 301000)).state, 'configuration-error');
      assert.equal((await inspect(id)).sinkCalls, 1);
      await verifyRaw(id);
    }
    const id = (await receive()).id;
    assert.equal((await notify(id, 'busy')).state, 'failed');
    assert.equal((await notify(id, 'ok', 61000)).state, 'notified');
    assert.equal((await inspect(id)).sinkCalls, 2);
  });
  await check('Paginated replay includes retained completed messages', async () => {
    const messages = await allMessages();
    assert.equal(messages.length, 8);
    assert.equal(new Set(messages.map(x => x.id)).size, 8);
    assert.equal(messages.filter(x => x.delivered).length, 2);
    state.messages = messages.length;
    state.raw_bytes = Buffer.byteLength(raw) * messages.length;
    state.raw_sha256 = createHash('sha256').update(raw).digest('hex');
  });
} catch (error) {
  state.error = error.message; process.exitCode = 1;
  console.error(error.message);
} finally {
  // Tear down only the resources this run created; keep evidence and the approved API token.
  if (state.created.bucket && state.created[state.worker]) {
    try {
      for (let i = 0; i < 20; i++) {
        const page = await json('/__cleanup', { method: 'POST' });
        state.deleted_objects = (state.deleted_objects || 0) + page.deleted;
        if (page.deleted === 0) { state.cleaned.objects = true; break; }
      }
    } catch (error) { state.cleanup_error = error.message; process.exitCode = 1; }
  }
  for (const script of [state.sink, state.worker]) {
    if (!state.created[script]) continue;
    try {
      await api(`${account}/workers/scripts/${script}`, 'DELETE');
      state.cleaned[script] = true;
    } catch (error) { state.cleanup_error = error.message; process.exitCode = 1; }
  }
  if (state.created.bucket) {
    try { await api(`${account}/r2/buckets/${state.bucket}`, 'DELETE'); state.cleaned.bucket = true; }
    catch (error) { state.cleanup_error = error.message; process.exitCode = 1; }
  }
  state.finished = new Date().toISOString(); await save();
  console.log(JSON.stringify({ passed: state.tests.filter(x => x.result === 'pass').length,
    failed: state.tests.filter(x => x.result === 'fail').length,
    cleanup: state.cleaned, evidence: statePath }));
}
