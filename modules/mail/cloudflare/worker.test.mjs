// TEST_CASES: MAILMOD-T-009
import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash, createHmac } from 'node:crypto';
import worker, { receive, routeFingerprint, maintain, notify, notifyDue } from './worker.mjs';

const source = '11111111-1111-4111-8111-111111111111';
const generation = '22222222-2222-4222-8222-222222222222';
const box = '33333333-3333-4333-8333-333333333333';
const owner = '44444444-4444-4444-8444-444444444444';
const claim = '55555555-5555-4555-8555-555555555555';
const op = '66666666-6666-4666-8666-666666666666';
const route = {
  address: 'alice@example.test',
  mailbox_id: box,
  owner_anchor: owner,
  claim_version: claim,
};
const sha = (bytes) => createHash('sha256').update(bytes).digest('hex');
class R2Fixture {
  rows = new Map();
  rawGate = null;
  rawStarted = null;
  failRaw = false;
  afterPut = null;
  beforePut = null;
  object(key, body = true) {
    const row = this.rows.get(key);
    if (!row) return null;
    return {
      key,
      etag: row.etag,
      httpEtag: JSON.stringify(row.etag),
      size: row.bytes.byteLength,
      customMetadata: structuredClone(row.metadata),
      ...(body ? { body: new Response(row.bytes.slice()).body } : {}),
    };
  }
  async get(key, options = {}) {
    const object = this.object(key);
    if (object && options.onlyIf?.etagMatches && object.etag !== options.onlyIf.etagMatches)
      delete object.body;
    return object;
  }
  async head(key) {
    return this.object(key, false);
  }
  async put(key, data, options = {}) {
    const bytes = new Uint8Array(await new Response(data).arrayBuffer());
    if (key.includes('/raw/') && options.customMetadata?.kind === 'raw') {
      this.rawStarted?.();
      if (this.rawGate) await this.rawGate;
      if (this.failRaw) throw new Error('private-storage-failure');
    }
    this.beforePut?.(key, bytes);
    const current = this.rows.get(key);
    const create =
      options.onlyIf instanceof Headers
        ? options.onlyIf.get('If-None-Match') === '*'
        : options.onlyIf?.etagDoesNotMatch === '*';
    if (
      (create && current) ||
      (options.onlyIf?.etagMatches && current?.etag !== options.onlyIf.etagMatches)
    )
      return null;
    this.rows.set(key, {
      bytes,
      etag: sha(bytes),
      metadata: structuredClone(options.customMetadata || {}),
    });
    this.afterPut?.(key, bytes);
    return this.object(key, false);
  }
  async list({ prefix, limit, cursor }) {
    const keys = [...this.rows.keys()]
      .filter((key) => key.startsWith(prefix) && (!cursor || key > cursor))
      .sort();
    const selected = keys.slice(0, limit);
    return {
      objects: selected.map((key) => this.object(key, false)),
      truncated: keys.length > selected.length,
      cursor: keys.length > selected.length ? selected.at(-1) : undefined,
    };
  }
}
function fixture(capacity = 2 * 1024 ** 3) {
  return {
    SOURCE_ID: source,
    GENERATION: generation,
    DOMAINS: '["example.test"]',
    READER_TOKEN: 'b'.repeat(64),
    CONTROL_TOKEN: 'c'.repeat(64),
    CAPACITY_BYTES: String(capacity),
    DELIVERED_DAYS: '7',
    MAIL: new R2Fixture(),
  };
}
async function call(env, path, body = undefined, control = false, extra = {}) {
  return worker.fetch(
    new Request('https://source.example.test' + path, {
      method: body === undefined ? 'GET' : 'POST',
      headers: {
        Authorization: 'Bearer ' + (control ? env.CONTROL_TOKEN : env.READER_TOKEN),
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
        ...extra,
      },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    }),
    env
  );
}
async function update(env, routes, expected = 0, operation = op) {
  return call(
    env,
    '/v1/routes',
    {
      source_id: source,
      generation,
      expected_revision: expected,
      operation_id: operation,
      fingerprint: await routeFingerprint(routes),
      routes,
    },
    true
  );
}
function mail(raw, to = route.address) {
  return {
    from: 'sender@example.net',
    to,
    rawSize: raw.byteLength,
    raw,
    rejected: null,
    setReject(reason) {
      this.rejected = reason;
    },
  };
}
const original = Buffer.from(
  'From: sender@example.net\r\nTo: forged@foreign.example\r\nSubject: 原文附件\r\n\r\n\x00original\xff\r\n',
  'utf8'
);

test('reader/control scopes are distinct, no browser or plaintext endpoint access', async () => {
  const env = fixture();
  assert.equal((await update(env, [route])).status, 200);
  const unauthorized = await call(env, '/v1/routes', {}, false);
  assert.equal(unauthorized.status, 401);
  assert.equal((await call(env, '/v1/messages', undefined, true)).status, 401);
  assert.equal(
    (await call(env, '/v1/messages', undefined, false, { Origin: 'https://foreign.example' }))
      .status,
    403
  );
  assert.equal(
    (
      await worker.fetch(
        new Request('http://source.example.test/v1/messages', {
          headers: { Authorization: 'Bearer ' + env.READER_TOKEN },
        }),
        env
      )
    ).status,
    403
  );
  const data = await (await call(env, '/v1/routes')).json();
  assert.equal(data.revision, 1);
  assert.equal(data.bindings.length, 1);
  assert.ok(!JSON.stringify(data).includes(env.CONTROL_TOKEN));
});

async function confirm(env, message, state = 'stored') {
  const id = message.metadata.message_id,
    target = message.metadata.mailbox_id;
  const body = {
    source_id: source,
    generation,
    receipt: {
      id: sha([source, id, target].join('\0')),
      mailbox_id: target,
      sha256: sha(original),
      state,
    },
  };
  return call(env, `/v1/messages/${id}/ack?target=${target}`, body);
}
async function rootState(env) {
  return new Response((await env.MAIL.get(`source/${source}/root.json`)).body).json();
}
test('seven-day cleanup keeps permanent tiny receipts and never prunes undelivered mail', async () => {
  const env = fixture();
  await update(env, [route]);
  const delivered = await receive(mail(original), env),
    undelivered = await receive(mail(original), env);
  assert.equal((await confirm(env, delivered)).status, 200);
  await maintain(env, Date.now() + 6 * 86400000);
  assert.equal(
    (await call(env, `/v1/messages/${delivered.metadata.message_id}/raw?target=${box}`)).status,
    200
  );
  await maintain(env, Date.now() + 8 * 86400000);
  assert.equal(
    (await call(env, `/v1/messages/${delivered.metadata.message_id}/raw?target=${box}`)).status,
    410
  );
  assert.equal(
    (await call(env, `/v1/messages/${undelivered.metadata.message_id}/raw?target=${box}`)).status,
    200
  );
  const state = await rootState(env);
  assert.equal(state.used_bytes, 4 * 1024 ** 2 + 2048 + 4096 + original.byteLength);
  const key = `source/${source}/control/${delivered.metadata.message_id}/${box}`,
    tomb = await new Response((await env.MAIL.get(key)).body).json();
  assert.equal(tomb.state, 'tombstone');
  assert.ok(!JSON.stringify(tomb).includes(owner));
  assert.ok(!JSON.stringify(tomb).includes(route.address));
  assert.equal(tomb.cleanup.root_released, true);
  await maintain(env, Date.now() + 8 * 86400000);
  assert.equal((await rootState(env)).used_bytes, state.used_bytes);
});
test('pressure reclaims oldest delivered originals before refusing the remaining undelivered data', async () => {
  const capacity = 4 * 1024 ** 2 + 3 * (4096 + original.byteLength) + 2048,
    env = fixture(capacity);
  await update(env, [route]);
  const oldest = await receive(mail(original), env),
    undelivered = await receive(mail(original), env),
    newest = await receive(mail(original), env);
  await confirm(env, newest);
  await confirm(env, oldest);
  const replacement = await receive(mail(original), env);
  assert.ok(replacement.metadata.message_id);
  assert.equal(
    (await call(env, `/v1/messages/${oldest.metadata.message_id}/raw?target=${box}`)).status,
    410
  );
  assert.equal(
    (await call(env, `/v1/messages/${newest.metadata.message_id}/raw?target=${box}`)).status,
    200
  );
  assert.equal(
    (await call(env, `/v1/messages/${undelivered.metadata.message_id}/raw?target=${box}`)).status,
    200
  );
  assert.equal((await rootState(env)).used_bytes, capacity);
  await maintain(env);
  assert.equal(
    (await call(env, `/v1/messages/${newest.metadata.message_id}/raw?target=${box}`)).status,
    200
  );
});
for (const stage of [
  'raw-fence',
  'compact-intent',
  'root-release',
  'control-proof',
  'journal-removal',
  'minimal-tombstone',
]) {
  test(`cleanup recovers a lost write acknowledgement at ${stage} without releasing quota twice`, async () => {
    const env = fixture();
    await update(env, [route]);
    const message = await receive(mail(original), env);
    await confirm(env, message);
    let injected = false;
    env.MAIL.afterPut = (key, bytes) => {
      if (injected) return;
      let value;
      try {
        value = JSON.parse(new TextDecoder().decode(bytes));
      } catch {
        return;
      }
      const match =
        (stage === 'raw-fence' &&
          key.includes('/raw/') &&
          value.schema === 'anas.mail-source-tombstone/v1') ||
        (stage === 'compact-intent' &&
          key.includes('/control/') &&
          value.state === 'deleting' &&
          !value.message?.metadata?.owner_anchor &&
          !value.cleanup?.root_released) ||
        (stage === 'root-release' && key.endsWith('/root.json') && value.releases?.length === 1) ||
        (stage === 'control-proof' &&
          key.includes('/control/') &&
          value.state === 'deleting' &&
          value.cleanup?.root_released) ||
        (stage === 'journal-removal' &&
          key.endsWith('/root.json') &&
          value.releases?.length === 0 &&
          value.used_bytes === 4 * 1024 ** 2 + 2048) ||
        (stage === 'minimal-tombstone' && key.includes('/control/') && value.state === 'tombstone');
      if (match) {
        injected = true;
        throw new Error('lost-after-commit');
      }
    };
    await assert.rejects(() => maintain(env, Date.now() + 8 * 86400000), /storage-unavailable/);
    assert.equal(injected, true);
    env.MAIL.afterPut = null;
    for (let retry = 0; retry < 3; retry++) await maintain(env, Date.now() + 8 * 86400000);
    const state = await rootState(env);
    assert.equal(state.used_bytes, 4 * 1024 ** 2 + 2048);
    assert.equal(state.releases?.length || 0, 0);
    assert.equal(
      (await call(env, `/v1/messages/${message.metadata.message_id}/raw?target=${box}`)).status,
      410
    );
  });
}
test('an expired reservation is fenced before quota release, even if its old upload later completes', async () => {
  const env = fixture();
  await update(env, [route]);
  let release, started;
  env.MAIL.rawGate = new Promise((resolve) => {
    release = resolve;
  });
  const waiting = new Promise((resolve) => {
    started = resolve;
  });
  env.MAIL.rawStarted = started;
  const pending = receive(mail(original), env);
  await waiting;
  await maintain(env, Date.now() + 301000);
  release();
  await assert.rejects(() => pending, /raw-not-confirmed/);
  const root = await rootState(env);
  assert.equal(root.pending.length, 0);
  assert.equal(root.used_bytes, 4 * 1024 ** 2 + 2048);
  assert.deepEqual((await (await call(env, '/v1/messages')).json()).messages, []);
  const raw = [...env.MAIL.rows].find(([key]) => key.includes('/raw/'))[1];
  assert.equal(raw.metadata.kind, 'tombstone');
});
test('concurrent cleanup workers keep one quota release and cannot revive a cleaned original', async () => {
  const env = fixture();
  await update(env, [route]);
  const message = await receive(mail(original), env);
  await confirm(env, message);
  const results = await Promise.allSettled(
    Array.from({ length: 4 }, () => maintain(env, Date.now() + 8 * 86400000))
  );
  assert.ok(results.some((v) => v.status === 'fulfilled'));
  for (let retry = 0; retry < 3; retry++) await maintain(env, Date.now() + 8 * 86400000);
  assert.equal((await rootState(env)).used_bytes, 4 * 1024 ** 2 + 2048);
  const rawKey = `source/${source}/raw/${message.metadata.message_id}/${box}`;
  assert.equal(
    await env.MAIL.put(rawKey, original, { onlyIf: new Headers({ 'If-None-Match': '*' }) }),
    null
  );
  assert.equal(
    (await call(env, `/v1/messages/${message.metadata.message_id}/raw?target=${box}`)).status,
    410
  );
});
test('raw persisted before record failure is recovered with its original identity', async () => {
  const env = fixture();
  await update(env, [route]);
  let failed = false;
  env.MAIL.beforePut = (key) => {
    if (!failed && key.includes('/control/')) {
      failed = true;
      throw new Error('record-failure');
    }
  };
  await assert.rejects(() => receive(mail(original), env), /storage-unavailable/);
  env.MAIL.beforePut = null;
  const before = await rootState(env),
    id = before.pending[0].metadata.message_id;
  await maintain(env);
  const page = await (await call(env, '/v1/messages')).json();
  assert.equal(page.messages[0].metadata.message_id, id);
  assert.equal((await rootState(env)).pending.length, 0);
  assert.equal((await rootState(env)).used_bytes, before.used_bytes);
});
test('route CAS repeats its own operation and rejects stale snapshots/reopening', async () => {
  const env = fixture();
  assert.equal((await update(env, [route])).status, 200);
  assert.equal((await update(env, [route])).status, 200);
  const next = '77777777-7777-4777-8777-777777777777';
  assert.equal((await update(env, [], 0, next)).status, 409);
  assert.equal((await update(env, [], 1, next)).status, 200);
  const state = await (await call(env, '/v1/routes')).json();
  assert.equal(state.bindings[0].closed_sequence, 0);
  assert.equal((await update(env, [route], 2, '88888888-8888-4888-8888-888888888888')).status, 409);
});
test('complete original and immutable trusted target precede listing/acknowledgement', async () => {
  const env = fixture();
  await update(env, [route]);
  const accepted = await receive(mail(original), env);
  const page = await (await call(env, '/v1/messages?limit=100')).json();
  assert.equal(page.messages.length, 1);
  const message = page.messages[0];
  assert.equal(message.metadata.mailbox_id, box);
  assert.equal(message.metadata.owner_anchor, owner);
  assert.equal(message.metadata.accepted_address, route.address);
  const path = `/v1/messages/${accepted.metadata.message_id}/raw?target=${box}`;
  const raw = await call(env, path);
  assert.equal(raw.status, 200);
  assert.deepEqual(Buffer.from(await raw.arrayBuffer()), original);
  assert.equal(raw.headers.get('ETag'), JSON.stringify(message.etag));
  const id = sha([source, accepted.metadata.message_id, box].join('\0'));
  const ack = {
    source_id: source,
    generation,
    receipt: { id, mailbox_id: box, sha256: sha(original), state: 'queued' },
  };
  const ackPath = `/v1/messages/${accepted.metadata.message_id}/ack?target=${box}`;
  assert.equal((await call(env, ackPath, ack)).status, 400);
  ack.receipt.state = 'stored';
  const first = await (await call(env, ackPath, ack)).json();
  assert.equal(first.delivery.receipt_id, id);
  const second = await (await call(env, ackPath, ack)).json();
  assert.deepEqual(first.delivery, second.delivery);
  assert.equal((await call(env, path.replace(box, generation))).status, 404);
  ack.receipt.sha256 = 'a'.repeat(64);
  assert.equal((await call(env, ackPath, ack)).status, 409);
  const root = await (await env.MAIL.get(`source/${source}/root.json`)).body;
  const saved = await new Response(root).json();
  assert.equal(saved.pending.length, 0);
  assert.equal(saved.used_bytes, 4 * 1024 ** 2 + 4096 + original.byteLength);
});
test('reservation fences preserve the old owner while routes change during raw upload', async () => {
  const env = fixture();
  await update(env, [route]);
  let release, started;
  env.MAIL.rawGate = new Promise((resolve) => {
    release = resolve;
  });
  const waiting = new Promise((resolve) => {
    started = resolve;
  });
  env.MAIL.rawStarted = started;
  const pending = receive(mail(original), env);
  await waiting;
  const replacement = {
    ...route,
    mailbox_id: '77777777-7777-4777-8777-777777777777',
    owner_anchor: '88888888-8888-4888-8888-888888888888',
    claim_version: '99999999-9999-4999-8999-999999999999',
  };
  assert.equal(
    (await update(env, [replacement], 1, 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')).status,
    200
  );
  release();
  const old = await pending;
  assert.equal(old.metadata.mailbox_id, box);
  env.MAIL.rawGate = null;
  const next = await receive(mail(original), env);
  assert.equal(next.metadata.mailbox_id, replacement.mailbox_id);
  const state = await (await call(env, '/v1/routes')).json();
  assert.equal(state.bindings[0].closed_sequence, 1);
  assert.equal(state.bindings[1].first_sequence, 2);
});
test('concurrent admissions cannot overspend or lose accepted reservations', async () => {
  const env = fixture();
  await update(env, [route]);
  const messages = await Promise.all(Array.from({ length: 4 }, () => receive(mail(original), env)));
  assert.equal(new Set(messages.map((v) => v.metadata.message_id)).size, 4);
  const root = await new Response((await env.MAIL.get(`source/${source}/root.json`)).body).json();
  assert.equal(root.sequence, 4);
  assert.equal(root.pending.length, 0);
  assert.equal(root.used_bytes, 4 * 1024 ** 2 + 4 * (4096 + original.byteLength));
});
test('storage failure escapes email and leaves a charged, unlisted recovery intent', async () => {
  const env = fixture();
  await update(env, [route]);
  env.MAIL.failRaw = true;
  await assert.rejects(() => worker.email(mail(original), env), /storage-unavailable/);
  const page = await (await call(env, '/v1/messages')).json();
  assert.deepEqual(page.messages, []);
  const root = await new Response((await env.MAIL.get(`source/${source}/root.json`)).body).json();
  assert.equal(root.pending.length, 1);
  assert.ok(root.used_bytes > 4 * 1024 ** 2);
});
test('unknown recipients never reserve and capacity refusal preserves undelivered originals', async () => {
  const env = fixture(4 * 1024 ** 2 + 4096 + original.byteLength);
  await update(env, [route]);
  const unknown = mail(original, 'other@example.test');
  assert.equal(await receive(unknown, env), null);
  assert.equal(unknown.rejected, 'Unknown recipient');
  await receive(mail(original), env);
  await assert.rejects(() => receive(mail(original), env), /source-capacity/);
  const page = await (await call(env, '/v1/messages')).json();
  assert.equal(page.messages.length, 1);
  assert.equal(page.messages[0].delivery, undefined);
  const root = await new Response((await env.MAIL.get(`source/${source}/root.json`)).body).json();
  assert.equal(root.used_bytes, Number(env.CAPACITY_BYTES));
  assert.equal(root.alert.code, 'capacity-or-admission-limit');
});

test(
  'one-write-per-second storage backs off within a bound and preserves the receipt',
  { timeout: 20000 },
  async () => {
    const env = fixture(),
      writes = new Map();
    let rateLimited = 0;
    env.MAIL.beforePut = (key) => {
      if (Date.now() - (writes.get(key) || 0) < 1000) {
        rateLimited++;
        throw Object.assign(new Error('429 private-rate-details'), { status: 429 });
      }
    };
    env.MAIL.afterPut = (key) => writes.set(key, Date.now());
    assert.equal((await update(env, [route])).status, 200);
    const message = await receive(mail(original), env);
    assert.equal((await confirm(env, message)).status, 200);
    await maintain(env, Date.now() + 8 * 86400000);
    assert.equal((await rootState(env)).used_bytes, 4 * 1024 ** 2 + 2048);
    assert.ok(rateLimited >= 3);
    assert.equal(
      (await call(env, `/v1/messages/${message.metadata.message_id}/raw?target=${box}`)).status,
      410
    );
  }
);

function notificationFixture(t, fetcher) {
  const env = fixture();
  env.NOTIFY_ORIGIN = 'https://nas.example.test';
  env.NOTIFY_TOKEN = 'd'.repeat(64);
  const previous = globalThis.fetch;
  globalThis.fetch = fetcher;
  t.after(() => {
    globalThis.fetch = previous;
  });
  return env;
}
function noticeReceipt(body) {
  const value = JSON.parse(body);
  return Response.json(
    {
      schema: 'anas.mail-notification/v1',
      source_id: value.source_id,
      generation: value.generation,
      notification_id: value.notification_id,
      accepted: true,
    },
    { status: 202 }
  );
}
const fetchSourceRaw = (env, m) =>
  call(env, `/v1/messages/${m.metadata.message_id}/raw?target=${m.metadata.mailbox_id}`);
test('authenticated notification contains no original and persists success independently of delivery', async (t) => {
  let calls = 0,
    last;
  const env = notificationFixture(t, async (url, options) => {
    calls++;
    assert.equal(url, 'https://nas.example.test/api/ingress/notify');
    assert.equal(options.redirect, 'manual');
    const timestamp = options.headers['X-ANAS-Mail-Time'];
    const expected = createHmac('sha256', Buffer.from(env.NOTIFY_TOKEN, 'hex'))
      .update(timestamp + '.' + options.body)
      .digest('hex');
    assert.equal(options.headers['X-ANAS-Mail-Signature'], expected);
    last = JSON.parse(options.body);
    assert.deepEqual(Object.keys(last).sort(), [
      'generation',
      'mailbox_id',
      'message_id',
      'notification_id',
      'schema',
      'source_id',
    ]);
    assert.ok(!options.body.includes('Subject:'));
    assert.ok(!options.body.includes(env.NOTIFY_TOKEN));
    return noticeReceipt(options.body);
  });
  await update(env, [route]);
  const m = await receive(mail(original), env);
  const results = await Promise.all([
    notify(env, m.metadata.message_id, box),
    notify(env, m.metadata.message_id, box),
  ]);
  assert.equal(calls, 1);
  assert.ok(results.some((v) => v.state === 'notified'));
  assert.equal(last.mailbox_id, box);
  const page = await (await call(env, '/v1/messages')).json();
  assert.equal(page.messages[0].notification.state, 'notified');
  assert.equal(page.messages[0].notification.attempts, 1);
  assert.equal(page.messages[0].delivery, undefined);
  assert.equal((await fetchSourceRaw(env, m)).status, 200);
});
test('temporary notification failures use three delayed attempts and keep undelivered mail', async (t) => {
  let calls = 0;
  const env = notificationFixture(t, async () => {
    calls++;
    return new Response('temporary', { status: 503 });
  });
  await update(env, [route]);
  const m = await receive(mail(original), env),
    now = Date.now();
  assert.equal((await notify(env, m.metadata.message_id, box, now)).state, 'failed');
  assert.equal((await notify(env, m.metadata.message_id, box, now + 59999)).state, 'waiting');
  assert.equal((await notify(env, m.metadata.message_id, box, now + 60001)).state, 'failed');
  assert.equal((await notify(env, m.metadata.message_id, box, now + 300001)).state, 'exhausted');
  assert.equal((await notify(env, m.metadata.message_id, box, now + 900000)).state, 'exhausted');
  assert.equal(calls, 3);
  const page = await (await call(env, '/v1/messages')).json();
  assert.equal(page.messages[0].notification.attempts, 3);
  assert.equal(page.messages[0].delivery, undefined);
  assert.equal((await fetchSourceRaw(env, m)).status, 200);
});
test('configuration errors do not retry blindly and explicit binding changes resume notification', async (t) => {
  let calls = 0,
    healthy = false;
  const env = notificationFixture(t, async (_url, options) => {
    calls++;
    return healthy ? noticeReceipt(options.body) : new Response('denied', { status: 401 });
  });
  await update(env, [route]);
  const m = await receive(mail(original), env);
  assert.equal((await notify(env, m.metadata.message_id, box)).state, 'configuration-error');
  assert.equal(
    (await notify(env, m.metadata.message_id, box, Date.now() + 900000)).state,
    'configuration-error'
  );
  assert.equal(calls, 1);
  env.NOTIFY_TOKEN = 'e'.repeat(64);
  healthy = true;
  assert.equal((await notify(env, m.metadata.message_id, box)).state, 'notified');
  assert.equal(calls, 2);
});
test('redirects and forged success receipts cannot establish a notified mark', async (t) => {
  let mode = 'redirect';
  const env = notificationFixture(t, async () =>
    mode === 'redirect'
      ? new Response('', { status: 302, headers: { Location: 'https://foreign.example' } })
      : Response.json({ accepted: true, notification_id: 'wrong' })
  );
  await update(env, [route]);
  const m = await receive(mail(original), env);
  assert.equal((await notify(env, m.metadata.message_id, box)).state, 'configuration-error');
  mode = 'bad-receipt';
  env.NOTIFY_TOKEN = 'e'.repeat(64);
  assert.equal((await notify(env, m.metadata.message_id, box)).state, 'configuration-error');
  const page = await (await call(env, '/v1/messages')).json();
  assert.equal(page.messages[0].notification.check, 'invalid-receipt');
  assert.equal(page.messages[0].notification.notified_at, undefined);
});
test('notification receipts require unambiguous fields and never mark mail delivered', async (t) => {
  for (const mode of ['valid', 'escaped-key', 'duplicate', 'escaped-duplicate', 'case-variant', 'extra']) {
    await t.test(mode, async (t) => {
      const env = notificationFixture(t, async (_url, options) => {
        const input = JSON.parse(options.body);
        let raw = JSON.stringify({ schema: 'anas.mail-notification/v1', source_id: input.source_id,
          generation: input.generation, notification_id: input.notification_id, accepted: true });
        if (mode === 'duplicate') raw = raw.replace('"accepted":true', '"accepted":false,"accepted":true');
        if (mode === 'escaped-key') raw = raw.replace('"accepted"', '"\\u0061ccepted"');
        if (mode === 'escaped-duplicate') raw = raw.replace('"accepted":true', '"accepted":false,"\\u0061ccepted":true');
        if (mode === 'case-variant') raw = raw.replace('"accepted"', '"Accepted"');
        if (mode === 'extra') raw = raw.replace('"accepted":true', '"accepted":true,"extra":1');
        return new Response(raw, { status: 202, headers: { 'Content-Type': 'application/json' } });
      });
      await update(env, [route]);
      const m = await receive(mail(original), env);
      const result = await notify(env, m.metadata.message_id, box);
      const valid = mode === 'valid' || mode === 'escaped-key';
      assert.equal(result.state, valid ? 'notified' : 'configuration-error');
      const page = await (await call(env, '/v1/messages')).json();
      if (valid) assert.ok(page.messages[0].notification.notified_at);
      else {
        assert.equal(page.messages[0].notification.check, 'invalid-receipt');
        assert.equal(page.messages[0].notification.notified_at, undefined);
      }
      assert.equal(page.messages[0].delivery, undefined);
      assert.equal((await fetchSourceRaw(env, m)).status, 200);
    });
  }
});
test(
  'notification timeout leaves source mail available for polling',
  { timeout: 10000 },
  async (t) => {
    const keepAlive = setTimeout(() => {}, 7000);
    t.after(() => clearTimeout(keepAlive));
    const env = notificationFixture(
      t,
      async (_url, options) =>
        new Promise((_resolve, reject) =>
          options.signal.addEventListener(
            'abort',
            () => reject(new Error('private-network-details')),
            { once: true }
          )
        )
    );
    await update(env, [route]);
    const m = await receive(mail(original), env);
    assert.equal((await notify(env, m.metadata.message_id, box)).state, 'failed');
    const page = await (await call(env, '/v1/messages')).json();
    assert.equal(page.messages[0].notification.check, 'timeout');
    assert.equal((await fetchSourceRaw(env, m)).status, 200);
  }
);
test('a late notification receipt cannot overwrite the native delivery receipt', async (t) => {
  let release, started;
  const pending = new Promise((resolve) => {
    started = resolve;
  });
  const env = notificationFixture(t, async (_url, options) => {
    started();
    await new Promise((resolve) => {
      release = resolve;
    });
    return noticeReceipt(options.body);
  });
  await update(env, [route]);
  const m = await receive(mail(original), env);
  const sending = notify(env, m.metadata.message_id, box);
  await pending;
  assert.equal((await confirm(env, m)).status, 200);
  release();
  assert.equal((await sending).state, 'notified');
  const page = await (await call(env, '/v1/messages')).json();
  assert.equal(page.messages[0].delivery.state, 'stored');
  assert.equal(page.messages[0].notification.state, 'notified');
});
test('bad or missing notification configuration never blocks receiving and polling', async () => {
  const env = fixture();
  env.NOTIFY_ORIGIN = 'http://insecure.example.test';
  env.NOTIFY_TOKEN = 'd'.repeat(64);
  await update(env, [route]);
  const m = await receive(mail(original), env);
  assert.equal((await notifyDue(env)).state, 'configuration-error');
  assert.equal((await call(env, '/v1/status')).status, 200);
  assert.equal((await fetchSourceRaw(env, m)).status, 200);
  delete env.NOTIFY_ORIGIN;
  delete env.NOTIFY_TOKEN;
  assert.equal((await notify(env, m.metadata.message_id, box)).state, 'unconfigured');
});

test('an earlier slow publication cannot fall behind a later saved pagination cursor', async () => {
  const env = fixture();
  await update(env, [route]);
  let release, started;
  env.MAIL.rawGate = new Promise((resolve) => {
    release = resolve;
  });
  const waiting = new Promise((resolve) => {
    started = resolve;
  });
  env.MAIL.rawStarted = started;
  const held = receive(mail(original), env);
  await waiting;
  env.MAIL.rawGate = null;
  for (let index = 0; index < 25; index++) await receive(mail(original), env);
  const originalList = env.MAIL.list.bind(env.MAIL);
  let interleaved = false;
  env.MAIL.list = async (args) => {
    const result = await originalList(args);
    if (!interleaved) {
      interleaved = true;
      release();
      await held;
    }
    return result;
  };
  const blocked = await (await call(env, '/v1/messages')).json();
  assert.deepEqual(blocked.messages, []);
  assert.equal(blocked.cursor, null);
  let cursor = '',
    messages = [];
  do {
    const page = await (
      await call(env, '/v1/messages' + (cursor ? '?cursor=' + encodeURIComponent(cursor) : ''))
    ).json();
    messages.push(...page.messages);
    cursor = page.cursor;
  } while (cursor);
  const first = await held;
  assert.equal(messages.length, 26);
  assert.ok(messages.some((value) => value.metadata.message_id === first.metadata.message_id));
});
