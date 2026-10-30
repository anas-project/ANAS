// TEST_CASES: MAILMOD-T-010
// Real Worker/R2 contract subset. Does not change DNS or claim SMTP/DMS success.
import assert from 'node:assert/strict';
import { readFile, writeFile, stat, mkdir } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { randomBytes, randomUUID, createHash } from 'node:crypto';

const credentialFile = process.env.MAIL_CLOUDFLARE_TEST_CREDENTIAL_FILE;
const reportDir = process.env.MAIL_CLOUDFLARE_TEST_REPORT_DIR;
if (!credentialFile || !reportDir) throw Error('Explicit private credential/report paths required');
if ((await stat(credentialFile)).mode & 0o077) throw Error('Private credential file required');
const credential = JSON.parse(await readFile(credentialFile, 'utf8'));
assert.match(credential.account_id, /^[a-f0-9]{32}$/);
await mkdir(reportDir, { recursive: true, mode: 0o700 });
if ((await stat(reportDir)).mode & 0o077) throw Error('Private report directory required');
const name = `anas-mail-runtime-20261026-${randomBytes(4).toString('hex')}`;
const state = { name, created: {}, cleaned: {}, tests: [], started: new Date().toISOString() };
const source = randomUUID(),
  generation = randomUUID(),
  owner = randomUUID(),
  boxA = randomUUID(),
  boxB = randomUUID();
const readerToken = randomBytes(32).toString('hex'),
  controlToken = randomBytes(32).toString('hex'),
  testToken = randomBytes(32).toString('hex');
const domain = 'mail-poc.bengbu.cc.cd',
  addressA = `a@${domain}`,
  addressB = `b@${domain}`;
const routes = [
  { address: addressA, mailbox_id: boxA, owner_anchor: owner, claim_version: randomUUID() },
  { address: addressB, mailbox_id: boxB, owner_anchor: owner, claim_version: randomUUID() },
];
const raw = Buffer.from(
  'From: probe@sender.mail-poc.bengbu.cc.cd\r\nTo: forged@foreign.example\r\nMessage-ID: <same-source-test@sender>\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n你好\r\n--x\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n\r\nAAECA/8=\r\n--x--\r\n'
);
const hash = (value) => createHash('sha256').update(value).digest('hex');
const capacity = 4 * 1024 ** 2 + 8 * (raw.byteLength + 4096) + 2048;
const account = `/accounts/${credential.account_id}`;
const save = () =>
  writeFile(join(resolve(reportDir), 'result.json'), JSON.stringify(state, null, 2), {
    mode: 0o600,
  });
const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function api(path, method = 'GET', body) {
  const response = await fetch('https://api.cloudflare.com/client/v4' + path, {
    method,
    headers: {
      Authorization: 'Bearer ' + credential.token,
      ...(body && !(body instanceof FormData) ? { 'Content-Type': 'application/json' } : {}),
    },
    body: body instanceof FormData ? body : body ? JSON.stringify(body) : undefined,
    redirect: 'error',
    signal: AbortSignal.timeout(45000),
  });
  const data = await response.json();
  if (!response.ok || !data.success)
    throw Error(
      `Cloudflare ${method} ${path.replace(credential.account_id, 'ACCOUNT')}: ${
        response.status
      }, codes ${(data.errors || []).map((v) => v.code).join(',')}`
    );
  return data.result;
}
let base;
async function request(
  path,
  { method = 'GET', body, token = path.startsWith('/__') ? testToken : readerToken } = {}
) {
  return fetch(base + path, {
    method,
    body,
    headers: { Authorization: 'Bearer ' + token },
    redirect: 'error',
    signal: AbortSignal.timeout(45000),
  });
}
async function json(path, options) {
  const r = await request(path, options);
  assert.equal(r.status, 200, 'Fixture status at ' + path.split('?')[0]);
  return r.json();
}
const inject = (to = addressA, fault = '') =>
  request(`/__receive?to=${encodeURIComponent(to)}${fault ? '&fault=' + fault : ''}`, {
    method: 'POST',
    body: raw,
  });
const all = async () => {
  const result = [];
  let cursor = '';
  for (let pageNumber = 0; pageNumber < 20; pageNumber++) {
    const page = await json(
      '/v1/messages?limit=100' + (cursor ? '&cursor=' + encodeURIComponent(cursor) : '')
    );
    assert.equal(page.source_id, source);
    assert.equal(page.generation, generation);
    result.push(...page.messages);
    if (!page.cursor) return result;
    cursor = page.cursor;
  }
  throw Error('Pagination did not finish');
};
const ack = (message) =>
  request(`/v1/messages/${message.metadata.message_id}/ack?target=${message.metadata.mailbox_id}`, {
    method: 'POST',
    body: JSON.stringify({
      source_id: source,
      generation,
      receipt: {
        id: hash([source, message.metadata.message_id, message.metadata.mailbox_id].join('\0')),
        mailbox_id: message.metadata.mailbox_id,
        sha256: hash(raw),
        state: 'stored',
      },
    }),
  });
const fetchRaw = (message) =>
  request(`/v1/messages/${message.metadata.message_id}/raw?target=${message.metadata.mailbox_id}`);
async function check(title, work) {
  const started = Date.now();
  try {
    await work();
    state.tests.push({ title, result: 'pass', elapsed_ms: Date.now() - started });
  } catch (error) {
    state.tests.push({ title, result: 'fail', error: error.message });
    throw error;
  } finally {
    await save();
    console.log(`${state.tests.at(-1).result.toUpperCase()} ${title}`);
  }
}
await save();
try {
  assert.equal((await api('/user/tokens/verify')).status, 'active');
  const existing = await api(`${account}/workers/scripts`);
  assert.ok(!existing.some((v) => v.id === name), 'Existing Worker cannot be overwritten');
  const buckets = await api(`${account}/r2/buckets`);
  assert.ok(!buckets.buckets.some((v) => v.name === name), 'Existing bucket cannot be reused');
  const subdomain = await api(`${account}/workers/subdomain`);
  base = `https://${name}.${subdomain.subdomain}.workers.dev`;
  await api(`${account}/r2/buckets`, 'POST', { name, storageClass: 'Standard' });
  state.created.bucket = true;
  await save();
  const main = await readFile(
    new URL('../fixtures/mail-cloudflare-runtime/fixture.mjs', import.meta.url),
    'utf8'
  );
  const code = await readFile(
    new URL('../../modules/mail/cloudflare/worker.mjs', import.meta.url),
    'utf8'
  );
  const notification = await readFile(
    new URL('../../modules/mail/cloudflare/notification.mjs', import.meta.url),
    'utf8'
  );
  state.source_sha256 = {
    worker: hash(code),
    fixture: hash(main),
    notification: hash(notification),
  };
  state.source_sha256.runner = hash(await readFile(new URL(import.meta.url)));
  state.capacity_bytes = capacity;
  const binding = (name, text, secret = false) => ({
    name,
    type: secret ? 'secret_text' : 'plain_text',
    text,
  });
  const form = new FormData();
  form.set(
    'metadata',
    new Blob(
      [
        JSON.stringify({
          main_module: 'fixture.mjs',
          compatibility_date: '2026-07-01',
          tags: ['anas-mail-isolated-runtime'],
          bindings: [
            { name: 'MAIL', type: 'r2_bucket', bucket_name: name },
            binding('SOURCE_ID', source),
            binding('GENERATION', generation),
            binding('DOMAINS', JSON.stringify([domain])),
            binding('CAPACITY_BYTES', String(capacity)),
            binding('DELIVERED_DAYS', '7'),
            binding('READER_TOKEN', readerToken, true),
            binding('CONTROL_TOKEN', controlToken, true),
            binding('TEST_TOKEN', testToken, true),
            binding('EXPIRES', String(Date.now() + 3 * 3600000)),
            binding('REVISION', state.source_sha256.worker),
          ],
        }),
      ],
      { type: 'application/json' }
    )
  );
  form.set(
    'fixture.mjs',
    new Blob([main], { type: 'application/javascript+module' }),
    'fixture.mjs'
  );
  form.set('worker.mjs', new Blob([code], { type: 'application/javascript+module' }), 'worker.mjs');
  form.set(
    'notification.mjs',
    new Blob([notification], { type: 'application/javascript+module' }),
    'notification.mjs'
  );
  const uploaded = await api(`${account}/workers/scripts/${name}`, 'PUT', form);
  state.created.worker = true;
  state.worker_version = uploaded.deployment_id || uploaded.version_id || uploaded.etag;
  await save();
  await api(`${account}/workers/scripts/${name}/subdomain`, 'POST', {
    enabled: true,
    previews_enabled: false,
  });
  let ready = false;
  for (let attempt = 0; attempt < 60; attempt++) {
    try {
      const health = await request('/__health');
      if (health.status === 200 && (await health.json()).revision === state.source_sha256.worker) {
        ready = true;
        break;
      }
    } catch {}
    await wait(2000);
  }
  assert.equal(ready, true, 'New expiring Worker readiness');
  await check('private R2 and distinct reader/control permissions', async () => {
    assert.equal((await api(`${account}/r2/buckets/${name}/domains/managed`)).enabled, false);
    assert.equal((await request('/v1/messages', { token: 'wrong' })).status, 401);
    const fingerprint = hash(
      JSON.stringify([...routes].sort((a, b) => a.address.localeCompare(b.address)))
    );
    const update = JSON.stringify({
      source_id: source,
      generation,
      expected_revision: 0,
      operation_id: randomUUID(),
      fingerprint,
      routes,
    });
    assert.equal((await request('/v1/routes', { method: 'POST', body: update })).status, 401);
    assert.equal(
      (await request('/v1/routes', { method: 'POST', body: update, token: controlToken })).status,
      200
    );
    const readback = await json('/v1/routes');
    assert.equal(readback.revision, 1);
    assert.equal(readback.fingerprint, fingerprint);
    assert.equal(
      (await request('/v1/routes', { method: 'POST', body: update, token: controlToken })).status,
      200
    );
  });
  let first, second;
  await check('actual R2 original bytes and trusted independent targets', async () => {
    const a = await inject(),
      b = await inject(addressB);
    assert.equal(a.status, 200);
    assert.equal(b.status, 200);
    first = (await a.json()).result;
    second = (await b.json()).result;
    assert.equal(first.metadata.mailbox_id, boxA);
    assert.equal(second.metadata.mailbox_id, boxB);
    assert.notEqual(first.metadata.message_id, second.metadata.message_id);
    const downloaded = await fetchRaw(first);
    assert.equal(downloaded.status, 200);
    assert.equal(hash(Buffer.from(await downloaded.arrayBuffer())), hash(raw));
    assert.equal((await json('/v1/status')).notifications, 'unconfigured');
  });
  await check('four concurrent real reservations preserve every accepted original', async () => {
    const responses = await Promise.all(Array.from({ length: 4 }, () => inject()));
    for (const r of responses) assert.equal(r.status, 200, 'Concurrent source admission');
    const messages = await all();
    assert.equal(messages.length, 6);
    assert.equal(new Set(messages.map((v) => v.metadata.message_id)).size, 6);
    state.concurrent_elapsed_messages = 4;
  });
  await check('persisted raw survives control failure and resumes with the same key', async () => {
    assert.equal((await inject(addressA, 'record')).status, 503);
    assert.equal((await all()).length, 6);
    await json('/__maintenance', { method: 'POST' });
    assert.equal((await all()).length, 7);
  });
  await check('full capacity retains undelivered data and exposes pressure status', async () => {
    assert.equal((await inject()).status, 200);
    assert.equal((await inject()).status, 503);
    assert.equal((await all()).length, 8);
    assert.equal((await fetchRaw(first)).status, 200);
    const status = await json('/v1/status');
    assert.ok(status.used_bytes <= capacity);
    assert.ok(status.alert);
    assert.equal(status.account_quota, 'unknown');
  });
  await check(
    'lost acknowledgement replay, pressure cleanup and bounded metadata budget',
    async () => {
      const a = await ack(first);
      assert.equal(a.status, 200);
      const receipt = await a.json();
      const repeat = await ack(first);
      assert.equal(repeat.status, 200);
      assert.deepEqual((await repeat.json()).delivery, receipt.delivery);
      assert.equal(
        (await inject()).status,
        200,
        'Reclaim delivered copy before accepting replacement'
      );
      assert.equal((await fetchRaw(first)).status, 410);
      assert.equal((await fetchRaw(second)).status, 200);
      const status = await json('/v1/status');
      assert.equal(status.used_bytes, capacity);
    }
  );
  await check('seven-day delivery retention preserves all unconfirmed originals', async () => {
    assert.equal((await ack(second)).status, 200);
    await json('/__maintenance?advance=' + 8 * 86400000, { method: 'POST' });
    assert.equal((await fetchRaw(second)).status, 410);
    assert.equal((await all()).length, 7);
  });
} catch (error) {
  state.failure = error.message;
} finally {
  state.cleanup_errors = [];
  if (state.created.worker && state.created.bucket && base) {
    try {
      for (let page = 0; page < 20; page++) {
        const result = await json('/__cleanup', { method: 'POST' });
        if (!result.deleted) break;
        if (page === 19) throw Error('Object cleanup did not finish');
      }
      state.cleaned.objects = true;
    } catch (error) {
      state.cleanup_errors.push(error.message);
    }
  }
  if (state.created.worker) {
    try {
      await api(`${account}/workers/scripts/${name}`, 'DELETE');
      state.cleaned.worker = true;
    } catch (error) {
      state.cleanup_errors.push(error.message);
    }
  }
  if (state.created.bucket && (state.cleaned.objects || !state.created.worker)) {
    try {
      await api(`${account}/r2/buckets/${name}`, 'DELETE');
      state.cleaned.bucket = true;
    } catch (error) {
      state.cleanup_errors.push(error.message);
    }
  }
  state.finished = new Date().toISOString();
  await save();
  console.log(
    JSON.stringify({
      tests: state.tests.length,
      failure: state.failure || null,
      cleanup_errors: state.cleanup_errors,
      cleaned: state.cleaned,
    })
  );
}
if (state.failure || state.cleanup_errors.length) process.exitCode = 1;
