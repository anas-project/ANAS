// Candidate managed source. Deployment/retention verification remains gated;
// this is separate from the archived test-env protocol prototype.
import {
  notificationConfiguration,
  notificationBinding,
  sendNotification,
} from './notification.mjs';
const SCHEMA = 'anas.mail-source/v1';
const MAX_RAW = 25 * 1024 * 1024;
const ROOT_RESERVE = 4 * 1024 * 1024;
const MESSAGE_RESERVE = 4096;
const RETAINED_RESERVE = 2048;
const UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/;
const DIGEST = /^[a-f0-9]{64}$/;
const MESSAGE_ID = /^[0-9]{16}-[a-f0-9-]{36}$/;
const encoder = new TextEncoder();
const createOnly = { onlyIf: new Headers({ 'If-None-Match': '*' }) };
const response = (value, status = 200) =>
  Response.json(value, { status, headers: { 'Cache-Control': 'no-store' } });
class SourceError extends Error {
  constructor(code, status = 503) {
    super(code);
    this.status = status;
  }
}
const fail = (code, status) => {
  throw new SourceError(code, status);
};
const exact = (value, required, optional = []) =>
  value &&
  typeof value === 'object' &&
  !Array.isArray(value) &&
  required.every((key) => Object.hasOwn(value, key)) &&
  Object.keys(value).every((key) => required.includes(key) || optional.includes(key));
const safeInt = (value) => Number.isSafeInteger(value) && value >= 0;
const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
// R2 limits writes to one object key. Replayable control documents can back
// off; an already consumed email stream cannot be retried in this invocation.
async function r2Put(env, key, body, options) {
  const replayable =
    typeof body === 'string' || body instanceof ArrayBuffer || ArrayBuffer.isView(body);
  for (let attempt = 0; attempt < 4; attempt++) {
    try {
      return await env.MAIL.put(key, body, options);
    } catch (error) {
      const limited =
        error?.status === 429 ||
        /(?:^|\D)429(?:\D|$)/.test(String(error?.message || '').slice(0, 1024));
      if (!limited) fail('storage-unavailable');
      if (!replayable || attempt === 3) fail('storage-rate-limited');
      await wait(1100);
    }
  }
}
async function hash(value) {
  return Array.from(
    new Uint8Array(await crypto.subtle.digest('SHA-256', encoder.encode(value))),
    (b) => b.toString(16).padStart(2, '0')
  ).join('');
}
function address(value) {
  if (typeof value !== 'string' || value.length > 254 || value !== value.toLowerCase())
    return false;
  const parts = value.split('@');
  return (
    parts.length === 2 &&
    /^[a-z0-9_][a-z0-9._-]{0,63}$/.test(parts[0]) &&
    !parts[0].includes('..') &&
    !parts[0].endsWith('.') &&
    domain(parts[1])
  );
}
function domain(value) {
  return (
    typeof value === 'string' &&
    value.length <= 253 &&
    value.split('.').length >= 2 &&
    value.split('.').every((label) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))
  );
}
function settings(env) {
  let domains;
  try {
    domains = JSON.parse(env.DOMAINS);
  } catch {
    fail('configuration-invalid');
  }
  const capacity = Number(env.CAPACITY_BYTES),
    days = Number(env.DELIVERED_DAYS);
  if (
    !UUID.test(env.SOURCE_ID) ||
    !UUID.test(env.GENERATION) ||
    !DIGEST.test(env.READER_TOKEN) ||
    !DIGEST.test(env.CONTROL_TOKEN) ||
    env.READER_TOKEN === env.CONTROL_TOKEN ||
    !Array.isArray(domains) ||
    !domains.length ||
    domains.length > 100 ||
    domains.some((v) => !domain(v)) ||
    new Set(domains).size !== domains.length ||
    !safeInt(capacity) ||
    capacity < ROOT_RESERVE + MESSAGE_RESERVE + 1 ||
    !safeInt(days) ||
    days < 1 ||
    days > 3650 ||
    !env.MAIL
  )
    fail('configuration-invalid');
  return { source: env.SOURCE_ID, generation: env.GENERATION, capacity, days, domains };
}
function routeValid(value, cfg) {
  return (
    exact(value, ['address', 'mailbox_id', 'owner_anchor', 'claim_version']) &&
    address(value.address) &&
    cfg.domains.includes(value.address.split('@')[1]) &&
    [value.mailbox_id, value.owner_anchor, value.claim_version].every((v) => UUID.test(v))
  );
}
function intent(value) {
  return {
    address: value.address,
    mailbox_id: value.mailbox_id,
    owner_anchor: value.owner_anchor,
    claim_version: value.claim_version,
  };
}
export async function routeFingerprint(routes) {
  return hash(
    JSON.stringify(
      routes
        .map(intent)
        .sort((a, b) => (a.address < b.address ? -1 : a.address > b.address ? 1 : 0))
    )
  );
}
async function bindingID(cfg, route) {
  return hash(
    [
      cfg.source,
      cfg.generation,
      route.address,
      route.claim_version,
      route.mailbox_id,
      route.owner_anchor,
    ].join('\0')
  );
}
const rootKey = (cfg) => `source/${cfg.source}/root.json`;
const rawKey = (cfg, id, target) => `source/${cfg.source}/raw/${id}/${target}`;
const controlKey = (cfg, id, target) => `source/${cfg.source}/control/${id}/${target}`;
async function boundedJSON(body, maximum) {
  const reader = body?.getReader?.();
  if (!reader) fail('invalid-json', 400);
  const chunks = [];
  let length = 0;
  try {
    for (;;) {
      const next = await reader.read();
      if (next.done) break;
      length += next.value.byteLength;
      if (length > maximum) {
        await reader.cancel();
        fail('input-too-large', 413);
      }
      chunks.push(next.value);
    }
    const bytes = new Uint8Array(length);
    let offset = 0;
    for (const chunk of chunks) {
      bytes.set(chunk, offset);
      offset += chunk.byteLength;
    }
    return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
  } catch (error) {
    if (error instanceof SourceError) throw error;
    fail('invalid-json', 400);
  }
}
async function initial(cfg) {
  return {
    schema: SCHEMA,
    source_id: cfg.source,
    generation: cfg.generation,
    nonce: crypto.randomUUID(),
    route_revision: 0,
    operation_id: '',
    fingerprint: await routeFingerprint([]),
    sequence: 0,
    bindings: [],
    used_bytes: ROOT_RESERVE,
    pending: [],
    alert: null,
  };
}
async function readRoot(env, cfg) {
  const object = await env.MAIL.get(rootKey(cfg));
  if (!object) return { value: await initial(cfg), etag: null };
  const value = await boundedJSON(object.body, ROOT_RESERVE);
  if (
    value.schema !== SCHEMA ||
    value.source_id !== cfg.source ||
    value.generation !== cfg.generation ||
    !UUID.test(value.nonce) ||
    !safeInt(value.route_revision) ||
    !safeInt(value.sequence) ||
    !Array.isArray(value.bindings) ||
    value.bindings.length > 5000 ||
    !Array.isArray(value.pending) ||
    value.pending.length > 64 ||
    !safeInt(value.used_bytes) ||
    value.used_bytes < ROOT_RESERVE ||
    value.used_bytes > cfg.capacity
  )
    fail('source-state-invalid');
  const pendingIDs = new Set();
  for (const item of value.pending) {
    const m = item?.metadata;
    if (
      !m ||
      m.source_id !== cfg.source ||
      !routeValid(
        {
          address: m.accepted_address,
          mailbox_id: m.mailbox_id,
          owner_anchor: m.owner_anchor,
          claim_version: m.claim_version,
        },
        cfg
      ) ||
      !safeInt(item.sequence) ||
      item.sequence < 1 ||
      item.sequence > value.sequence ||
      !MESSAGE_ID.test(m.message_id) ||
      !m.message_id.startsWith(String(item.sequence).padStart(16, '0') + '-') ||
      pendingIDs.has(m.message_id) ||
      !safeInt(item.bytes) ||
      item.bytes < 1 ||
      item.bytes > MAX_RAW ||
      !Number.isFinite(Date.parse(item.received_at))
    )
      fail('source-state-invalid');
    pendingIDs.add(m.message_id);
  }
  return { value, etag: object.etag };
}
async function changeRoot(env, cfg, mutate) {
  for (let attempt = 0; attempt < 8; attempt++) {
    const current = await readRoot(env, cfg);
    const result = await mutate(current.value);
    current.value.nonce = crypto.randomUUID();
    const body = JSON.stringify(current.value);
    if (encoder.encode(body).byteLength > ROOT_RESERVE) fail('source-control-capacity');
    const stored = await r2Put(
      env,
      rootKey(cfg),
      body,
      current.etag ? { onlyIf: { etagMatches: current.etag } } : createOnly
    );
    if (stored) return { value: current.value, result };
    await wait(1100);
  }
  fail('source-busy');
}
function publicRoutes(root) {
  return {
    schema: SCHEMA,
    source_id: root.source_id,
    generation: root.generation,
    revision: root.route_revision,
    sequence: root.sequence,
    operation_id: root.operation_id,
    fingerprint: root.fingerprint,
    bindings: root.bindings,
  };
}
async function replaceRoutes(request, env, cfg) {
  const input = await boundedJSON(request.body, ROOT_RESERVE);
  if (
    !exact(input, [
      'source_id',
      'generation',
      'expected_revision',
      'operation_id',
      'fingerprint',
      'routes',
    ]) ||
    input.source_id !== cfg.source ||
    input.generation !== cfg.generation ||
    !safeInt(input.expected_revision) ||
    input.expected_revision === Number.MAX_SAFE_INTEGER ||
    !UUID.test(input.operation_id) ||
    !Array.isArray(input.routes) ||
    input.routes.length > 5000 ||
    input.routes.some((route) => !routeValid(route, cfg)) ||
    new Set(input.routes.map((route) => route.address)).size !== input.routes.length ||
    input.fingerprint !== (await routeFingerprint(input.routes))
  )
    fail('invalid-routes', 400);
  const changed = await changeRoot(env, cfg, async (root) => {
    if (
      root.operation_id === input.operation_id &&
      root.route_revision === input.expected_revision + 1 &&
      root.fingerprint === input.fingerprint
    )
      return;
    if (root.route_revision !== input.expected_revision || root.operation_id === input.operation_id)
      fail('revision-conflict', 409);
    const desired = new Map(
      await Promise.all(input.routes.map(async (route) => [await bindingID(cfg, route), route]))
    );
    for (const binding of root.bindings) {
      if (binding.closed_sequence === null && !desired.has(binding.id))
        binding.closed_sequence = root.sequence;
      if (binding.closed_sequence !== null && desired.has(binding.id)) fail('closed-binding', 409);
      desired.delete(binding.id);
    }
    for (const [id, route] of desired)
      root.bindings.push({
        ...intent(route),
        id,
        first_sequence: root.sequence + 1,
        closed_sequence: null,
      });
    if (root.bindings.length > 5000) fail('source-control-capacity');
    root.route_revision++;
    root.operation_id = input.operation_id;
    root.fingerprint = input.fingerprint;
  });
  return response(publicRoutes(changed.value));
}
async function putControl(env, key, value, etag = null) {
  value.nonce = crypto.randomUUID();
  const body = JSON.stringify(value);
  if (encoder.encode(body).byteLength > 3072) fail('source-record-capacity');
  return r2Put(env, key, body, etag ? { onlyIf: { etagMatches: etag } } : createOnly);
}
function initialNotification(env, started) {
  const config = notificationConfiguration(env);
  return {
    state: config.state === 'configured' ? 'pending' : config.state,
    attempts: 0,
    id: crypto.randomUUID(),
    binding: '',
    started_at: started,
  };
}
function publicMessage(record) {
  const n = record.notification;
  return {
    ...record.message,
    ...(n
      ? {
          notification: {
            state: n.state,
            attempts: n.attempts,
            ...(n.check ? { check: n.check } : {}),
            ...(n.http_status ? { http_status: n.http_status } : {}),
            ...(n.notified_at ? { notified_at: n.notified_at } : {}),
            ...(n.next_at ? { next_attempt_at: n.next_at } : {}),
          },
        }
      : {}),
  };
}
export async function notify(env, id, target, now = Date.now()) {
  const cfg = settings(env),
    config = notificationConfiguration(env);
  if (config.state !== 'configured') return { state: config.state };
  const binding = await notificationBinding(config);
  let claim;
  for (let attempt = 0; attempt < 8; attempt++) {
    const current = await getControl(env, cfg, id, target),
      record = current.value;
    if (!['received', 'delivered'].includes(record.state)) return { state: 'expired' };
    if (record.message.delivery) return { state: 'delivered' };
    let n = record.notification;
    if (!n || n.binding !== binding)
      n = {
        state: 'pending',
        attempts: 0,
        id: crypto.randomUUID(),
        binding,
        started_at: new Date(now).toISOString(),
      };
    if (['notified', 'exhausted', 'configuration-error'].includes(n.state))
      return { state: n.state };
    if (!Number.isInteger(n.attempts) || n.attempts < 0 || n.attempts >= 3 || !UUID.test(n.id))
      fail('notification-state-invalid');
    const due = Date.parse(n.started_at) + [0, 60000, 300000][n.attempts];
    if (!Number.isFinite(due)) fail('notification-state-invalid');
    if (now < due) return { state: 'waiting' };
    n = { ...n, state: 'sending', attempts: n.attempts + 1, check: '', http_status: 0 };
    if (n.attempts < 3)
      n.next_at = new Date(Date.parse(n.started_at) + [0, 60000, 300000][n.attempts]).toISOString();
    else delete n.next_at;
    record.notification = n;
    if (await putControl(env, controlKey(cfg, id, target), record, current.etag)) {
      claim = n;
      break;
    }
  }
  if (!claim) fail('source-busy');
  const result = await sendNotification(
    config,
    {
      source_id: cfg.source,
      generation: cfg.generation,
      notification_id: claim.id,
      message_id: id,
      mailbox_id: target,
    },
    now
  );
  for (let attempt = 0; attempt < 8; attempt++) {
    const current = await getControl(env, cfg, id, target),
      record = current.value,
      n = record.notification;
    if (
      !['received', 'delivered'].includes(record.state) ||
      n?.id !== claim.id ||
      n?.binding !== binding
    )
      return { state: 'stale', attempted: true };
    if (n.state === 'notified') return { state: 'notified', attempted: true };
    // An old failure cannot overwrite a later attempt. Any authenticated
    // success for this same notice can establish the durable notified mark.
    if (result.state !== 'notified' && n.attempts !== claim.attempts)
      return { state: n.state, attempted: true };
    record.notification = { ...n, ...result };
    if (result.state === 'failed' && n.attempts >= 3) record.notification.state = 'exhausted';
    if (['notified', 'exhausted', 'configuration-error'].includes(record.notification.state))
      delete record.notification.next_at;
    if (await putControl(env, controlKey(cfg, id, target), record, current.etag))
      return { state: record.notification.state, attempted: true };
  }
  fail('source-busy');
}
export async function notifyDue(env, now = Date.now()) {
  const config = notificationConfiguration(env);
  if (config.state !== 'configured') return { state: config.state };
  const cfg = settings(env),
    root = await readRoot(env, cfg),
    binding = await notificationBinding(config);
  const page = await env.MAIL.list({
    prefix: `source/${cfg.source}/control/`,
    limit: 20,
    cursor: root.value.notification_cursor || undefined,
  });
  let attempts = 0;
  for (const object of page.objects) {
    const parts = object.key.split('/'),
      target = parts.at(-1),
      id = parts.at(-2),
      record = (await getControl(env, cfg, id, target)).value;
    if (record.state !== 'received' || record.message.delivery) continue;
    if (
      record.notification?.binding === binding &&
      ['notified', 'exhausted', 'configuration-error'].includes(record.notification.state)
    )
      continue;
    const result = await notify(env, id, target, now);
    if (result.attempted) attempts++;
    if (attempts >= 2) return { attempts };
  }
  await changeRoot(env, cfg, (value) => {
    value.notification_cursor = page.truncated ? page.cursor : '';
  });
  return { attempts };
}
function sameMessage(a, b) {
  return (
    JSON.stringify(a.metadata) === JSON.stringify(b.metadata) &&
    a.sequence === b.sequence &&
    a.bytes === b.bytes &&
    a.etag === b.etag &&
    a.received_at === b.received_at
  );
}
async function finishReservation(env, cfg, accepted) {
  const id = accepted.metadata.message_id,
    target = accepted.metadata.mailbox_id;
  await changeRoot(env, cfg, async (root) => {
    const current = await getControl(env, cfg, id, target);
    if (
      !['received', 'delivered'].includes(current.value.state) ||
      !sameMessage(current.value.message, accepted)
    )
      fail('reservation-changed');
    const index = root.pending.findIndex(
      (v) => v.metadata.message_id === id && v.metadata.mailbox_id === target
    );
    if (index >= 0) root.pending.splice(index, 1);
  });
}

// A permanent tiny tombstone prevents a delayed create-only upload from
// resurrecting a removed original. Its budget is retained, including control
// metadata; undelivered complete originals never enter this cleanup path.
async function fenceRaw(env, cfg, message, aborting = false) {
  const id = message.metadata.message_id,
    target = message.metadata.mailbox_id,
    key = rawKey(cfg, id, target);
  const existing = await env.MAIL.head(key);
  if (existing?.customMetadata?.kind === 'tombstone') return true;
  if (aborting && existing?.customMetadata?.kind === 'raw') return false;
  if (
    !aborting &&
    (!existing ||
      existing.etag !== message.etag ||
      existing.size !== message.bytes ||
      existing.customMetadata?.kind !== 'raw')
  )
    fail('raw-not-confirmed');
  const body = JSON.stringify({
    schema: 'anas.mail-source-tombstone/v1',
    nonce: crypto.randomUUID(),
  });
  const options = {
    ...(existing ? { onlyIf: { etagMatches: existing.etag } } : createOnly),
    customMetadata: { kind: 'tombstone' },
  };
  const stored = await r2Put(env, key, body, options);
  if (stored) return true;
  const after = await env.MAIL.head(key);
  return after?.customMetadata?.kind === 'tombstone';
}
function compactControl(record, state) {
  const m = record.message;
  const value = {
    schema: SCHEMA,
    source_id: record.source_id,
    generation: record.generation,
    nonce: crypto.randomUUID(),
    state,
    message: {
      metadata: {
        source_id: record.source_id,
        message_id: m.metadata.message_id,
        mailbox_id: m.metadata.mailbox_id,
      },
      sequence: m.sequence,
      bytes: m.bytes,
      ...(m.delivery ? { delivery: m.delivery } : {}),
    },
    cleanup: record.cleanup,
    ...(record.notification
      ? {
          notification: {
            state: record.notification.state,
            attempts: record.notification.attempts,
            ...(record.notification.notified_at
              ? { notified_at: record.notification.notified_at }
              : {}),
          },
        }
      : {}),
  };
  if (encoder.encode(JSON.stringify(value)).byteLength > 1024) fail('cleanup-record-capacity');
  return value;
}
async function finishCleanup(env, cfg, id, target) {
  let current = await getControl(env, cfg, id, target);
  if (
    !['deleting', 'aborting', 'tombstone', 'aborted'].includes(current.value.state) ||
    !current.value.cleanup
  )
    fail('cleanup-not-confirmed');
  const proof = current.value.cleanup,
    token = proof.token;
  if (!UUID.test(token) || !safeInt(proof.amount) || proof.amount < 1)
    fail('cleanup-not-confirmed');
  if (
    !['tombstone', 'aborted'].includes(current.value.state) &&
    !(await fenceRaw(env, cfg, current.value.message, current.value.state === 'aborting'))
  )
    fail('cleanup-not-confirmed');
  // Compact the control record BEFORE reducing its reservation. During a
  // crash the remaining 2 KiB must cover both tiny objects and their keys,
  // not an unbounded original sender/address plus a cleanup intent.
  for (let attempt = 0; attempt < 8; attempt++) {
    current = await getControl(env, cfg, id, target);
    if (current.value.cleanup?.token !== token) fail('cleanup-changed');
    if (!current.value.message.metadata.owner_anchor) break;
    if (
      await putControl(
        env,
        controlKey(cfg, id, target),
        compactControl(current.value, current.value.state),
        current.etag
      )
    )
      break;
    if (attempt === 7) fail('source-busy');
  }
  // Read root BEFORE reading the control proof inside every CAS attempt. If
  // another worker records/releases/removes this journal entry meanwhile, the
  // root ETag changes; a stale callback must reread the completed proof rather
  // than decrementing capacity twice.
  await changeRoot(env, cfg, async (root) => {
    const actual = (await getControl(env, cfg, id, target)).value;
    if (actual.cleanup?.token !== token) fail('cleanup-changed');
    if (actual.cleanup.root_released) return;
    root.releases ??= [];
    if (root.releases.some((v) => v.token === token)) return;
    if (root.releases.length >= 64 || root.used_bytes - proof.amount < ROOT_RESERVE)
      fail('cleanup-accounting-invalid');
    root.used_bytes -= proof.amount;
    root.releases.push({ token, id, target });
    if (root.pressure && cfg.capacity - root.used_bytes >= root.pressure.needed_bytes)
      root.pressure = null;
  });
  for (let attempt = 0; attempt < 8; attempt++) {
    current = await getControl(env, cfg, id, target);
    if (current.value.cleanup?.token !== token) fail('cleanup-changed');
    if (current.value.cleanup.root_released) break;
    current.value.cleanup.root_released = true;
    if (await putControl(env, controlKey(cfg, id, target), current.value, current.etag)) break;
    if (attempt === 7) fail('source-busy');
  }
  await changeRoot(env, cfg, async (root) => {
    const actual = (await getControl(env, cfg, id, target)).value;
    if (actual.cleanup?.token !== token || !actual.cleanup.root_released)
      fail('cleanup-not-confirmed');
    root.releases = (root.releases || []).filter((v) => v.token !== token);
    root.pending = root.pending.filter(
      (v) => v.metadata.message_id !== id || v.metadata.mailbox_id !== target
    );
  });
  for (let attempt = 0; attempt < 8; attempt++) {
    current = await getControl(env, cfg, id, target);
    if (['tombstone', 'aborted'].includes(current.value.state)) return;
    if (current.value.cleanup?.token !== token || !current.value.cleanup.root_released)
      fail('cleanup-not-confirmed');
    const minimal = compactControl(
      current.value,
      current.value.state === 'aborting' ? 'aborted' : 'tombstone'
    );
    if (await putControl(env, controlKey(cfg, id, target), minimal, current.etag)) return;
  }
  fail('source-busy');
}
async function cleanDelivered(env, cfg, current) {
  const record = current.value,
    m = record.message;
  if (record.state === 'delivered') {
    if (!m.delivery || !['stored', 'processed'].includes(m.delivery.state))
      fail('cleanup-not-confirmed');
    record.state = 'deleting';
    record.cleanup = {
      token: crypto.randomUUID(),
      amount: m.bytes + MESSAGE_RESERVE - RETAINED_RESERVE,
      root_released: false,
    };
    if (
      !(await putControl(
        env,
        controlKey(cfg, m.metadata.message_id, m.metadata.mailbox_id),
        record,
        current.etag
      ))
    )
      return false;
  }
  await finishCleanup(env, cfg, m.metadata.message_id, m.metadata.mailbox_id);
  return true;
}
async function repairReservation(env, cfg, pending, now) {
  const id = pending.metadata.message_id,
    target = pending.metadata.mailbox_id;
  const raw = await env.MAIL.head(rawKey(cfg, id, target));
  if (raw?.customMetadata?.kind === 'raw' && raw.size === pending.bytes) {
    const accepted = { ...pending, etag: raw.etag };
    const record = {
      schema: SCHEMA,
      source_id: cfg.source,
      generation: cfg.generation,
      nonce: crypto.randomUUID(),
      state: 'received',
      message: accepted,
      notification: initialNotification(env, pending.received_at),
    };
    await putControl(env, controlKey(cfg, id, target), record);
    await finishReservation(env, cfg, accepted);
    return true;
  }
  if (now - Date.parse(pending.received_at) < 300000) return false;
  if (!(await fenceRaw(env, cfg, pending, true))) return false;
  const record = {
    schema: SCHEMA,
    source_id: cfg.source,
    generation: cfg.generation,
    nonce: crypto.randomUUID(),
    state: 'aborting',
    message: pending,
    cleanup: {
      token: crypto.randomUUID(),
      amount: pending.bytes + MESSAGE_RESERVE - RETAINED_RESERVE,
      root_released: false,
    },
  };
  await putControl(env, controlKey(cfg, id, target), record);
  await finishCleanup(env, cfg, id, target);
  return true;
}

// Each invocation does bounded work. A capacity-pressure traversal is durable
// and visits messages in acceptance order; an incomplete scan remains visible
// and temporary admission failure never claims all delivered copies were tried.
export async function maintain(env, now = Date.now()) {
  const cfg = settings(env);
  let root = await readRoot(env, cfg);
  if (root.value.releases?.length) {
    const entry = root.value.releases[0];
    await finishCleanup(env, cfg, entry.id, entry.target);
    return { recovered: true };
  }
  if (root.value.pending.length) {
    if (await repairReservation(env, cfg, root.value.pending[0], now)) return { repaired: true };
  }
  const pressure = Boolean(root.value.pressure),
    cursor = (pressure ? root.value.pressure.cursor : root.value.retention_cursor) || undefined;
  const page = await env.MAIL.list({ prefix: `source/${cfg.source}/control/`, limit: 20, cursor });
  for (const object of page.objects) {
    const parts = object.key.split('/'),
      target = parts.at(-1),
      id = parts.at(-2),
      current = await getControl(env, cfg, id, target),
      record = current.value;
    if (['deleting', 'aborting'].includes(record.state)) {
      await finishCleanup(env, cfg, id, target);
      return { recovered: true };
    }
    if (
      record.state === 'delivered' &&
      (pressure || now - Date.parse(record.message.delivery.acknowledged_at) >= cfg.days * 86400000)
    ) {
      await cleanDelivered(env, cfg, current);
      return { cleaned: true };
    }
  }
  await changeRoot(env, cfg, (value) => {
    // A newly confirmed older copy resets pressure scanning. A stale scan
    // cannot advance that reset or silently skip its earlier messages.
    if (pressure) {
      if (value.pressure?.nonce === root.value.pressure.nonce) {
        if (page.truncated) value.pressure.cursor = page.cursor;
        else {
          value.pressure = null;
          value.pressure_checked_at = new Date(now).toISOString();
        }
      }
    } else value.retention_cursor = page.truncated ? page.cursor : '';
  });
  return { scanning: page.truncated, pressure };
}
async function getControl(env, cfg, id, target) {
  const object = await env.MAIL.get(controlKey(cfg, id, target));
  if (!object) fail('copy-not-found', 404);
  const value = await boundedJSON(object.body, 3072);
  if (
    value.schema !== SCHEMA ||
    value.source_id !== cfg.source ||
    value.generation !== cfg.generation ||
    value.message?.metadata?.message_id !== id ||
    value.message?.metadata?.mailbox_id !== target ||
    !UUID.test(value.nonce)
  )
    fail('source-record-invalid');
  return { value, etag: object.etag };
}
export async function receive(message, env, retried = false) {
  const cfg = settings(env),
    recipient = typeof message.to === 'string' ? message.to.toLowerCase() : '';
  if (!address(recipient) || !cfg.domains.includes(recipient.split('@')[1])) {
    message.setReject('Unknown recipient');
    return null;
  }
  if (!safeInt(message.rawSize) || message.rawSize < 1 || message.rawSize > MAX_RAW) {
    message.setReject('Message too large or empty');
    return null;
  }
  if (
    typeof message.from !== 'string' ||
    message.from.length > 254 ||
    /[\0\r\n]/.test(message.from)
  ) {
    message.setReject('Invalid envelope sender');
    return null;
  }
  const uuid = crypto.randomUUID();
  let reserved;
  try {
    reserved = await changeRoot(env, cfg, (root) => {
      const binding = root.bindings.find(
        (v) => v.address === recipient && v.closed_sequence === null
      );
      if (!binding) fail('unknown-recipient', 404);
      if (root.pending.length >= 64) fail('admission-busy');
      if (root.sequence >= Number.MAX_SAFE_INTEGER - 1) fail('source-sequence-exhausted');
      if (root.used_bytes + message.rawSize + MESSAGE_RESERVE > cfg.capacity)
        fail('source-capacity');
      root.sequence++;
      const id = String(root.sequence).padStart(16, '0') + '-' + uuid;
      const accepted = {
        metadata: {
          source_id: cfg.source,
          message_id: id,
          mailbox_id: binding.mailbox_id,
          owner_anchor: binding.owner_anchor,
          accepted_address: recipient,
          claim_version: binding.claim_version,
          envelope_from: message.from,
          sha256: '',
        },
        sequence: root.sequence,
        bytes: message.rawSize,
        etag: '',
        received_at: new Date().toISOString(),
      };
      root.pending.push(accepted);
      root.used_bytes += message.rawSize + MESSAGE_RESERVE;
      root.alert = null;
      return accepted;
    });
  } catch (error) {
    if (error instanceof SourceError && error.message === 'unknown-recipient') {
      message.setReject('Unknown recipient');
      return null;
    }
    // Persist only a bounded diagnostic, never the sender or raw mail. Storage
    // exceptions still escape email(), so no successful SMTP acceptance occurs.
    if (error instanceof SourceError && error.message === 'source-capacity') {
      await changeRoot(env, cfg, (root) => {
        root.alert = { code: 'capacity-or-admission-limit', at: new Date().toISOString() };
        root.pressure ??= {
          nonce: crypto.randomUUID(),
          cursor: '',
          started_at: new Date().toISOString(),
          needed_bytes: message.rawSize + MESSAGE_RESERVE,
        };
        root.pressure.needed_bytes = Math.max(
          root.pressure.needed_bytes,
          message.rawSize + MESSAGE_RESERVE
        );
      });
      if (!retried) {
        const result = await maintain(env);
        if (result.cleaned || result.repaired) return receive(message, env, true);
      }
    }
    throw error;
  }
  const accepted = reserved.result,
    id = accepted.metadata.message_id,
    target = accepted.metadata.mailbox_id;
  const transfer = new AbortController();
  let body = message.raw,
    pumping = Promise.resolve();
  if (typeof body?.getReader === 'function') {
    const fixed = new FixedLengthStream(message.rawSize);
    pumping = body.pipeTo(fixed.writable, { signal: transfer.signal });
    body = fixed.readable;
  } else if (
    !(body instanceof ArrayBuffer || ArrayBuffer.isView(body)) ||
    body.byteLength !== message.rawSize
  )
    fail('invalid-raw-stream');
  let stored;
  try {
    [stored] = await Promise.all([
      r2Put(env, rawKey(cfg, id, target), body, {
        ...createOnly,
        customMetadata: { kind: 'raw' },
        httpMetadata: { contentType: 'message/rfc822' },
      }),
      pumping,
    ]);
  } finally {
    transfer.abort();
  }
  if (!stored || stored.size !== message.rawSize) fail('raw-not-confirmed');
  accepted.etag = stored.etag;
  const control = {
    schema: SCHEMA,
    source_id: cfg.source,
    generation: cfg.generation,
    nonce: crypto.randomUUID(),
    state: 'received',
    message: accepted,
    notification: initialNotification(env, accepted.received_at),
  };
  if (!(await putControl(env, controlKey(cfg, id, target), control))) {
    const existing = await getControl(env, cfg, id, target);
    if (
      !['received', 'delivered'].includes(existing.value.state) ||
      !sameMessage(existing.value.message, accepted)
    )
      fail('record-not-confirmed');
  }
  await finishReservation(env, cfg, accepted);
  return accepted;
}
async function listMessages(url, env, cfg) {
  if (
    [...url.searchParams.keys()].some((key) => !['limit', 'cursor'].includes(key)) ||
    url.searchParams.getAll('limit').length > 1 ||
    url.searchParams.getAll('cursor').length > 1
  )
    fail('invalid-query', 400);
  const cursor = url.searchParams.get('cursor') || undefined;
  if (cursor && (cursor.length > 2048 || /[\0\r\n]/.test(cursor))) fail('invalid-cursor', 400);
  // Capture admission progress BEFORE listing. A slow earlier upload must
  // publish or be fenced before a cursor can advance beyond it. Admissions
  // begun after this snapshot are likewise left for the next traversal.
  const root = await readRoot(env, cfg);
  const barrier = Math.min(
    root.value.sequence + 1,
    ...root.value.pending.map((value) => value.sequence)
  );
  // Keep this request below the free Workers subrequest budget. The Go client
  // accepts a smaller page and persists every cursor instead of assuming 100.
  const page = await env.MAIL.list({ prefix: `source/${cfg.source}/control/`, limit: 20, cursor });
  const messages = [];
  let stopped = false;
  for (const object of page.objects) {
    const parts = object.key.split('/'),
      target = parts.at(-1),
      id = parts.at(-2);
    const current = await getControl(env, cfg, id, target);
    if (current.value.message.sequence >= barrier) {
      stopped = true;
      break;
    }
    if (current.value.state === 'received' || current.value.state === 'delivered')
      messages.push(publicMessage(current.value));
  }
  return response({
    schema: SCHEMA,
    source_id: cfg.source,
    generation: cfg.generation,
    route_revision: root.value.route_revision,
    messages,
    cursor: !stopped && page.truncated ? page.cursor : null,
  });
}
async function rawMessage(env, cfg, id, target) {
  const current = await getControl(env, cfg, id, target);
  if (!['received', 'delivered'].includes(current.value.state)) fail('copy-expired', 410);
  const object = await env.MAIL.get(rawKey(cfg, id, target), {
    onlyIf: { etagMatches: current.value.message.etag },
  });
  if (
    !object?.body ||
    object.size !== current.value.message.bytes ||
    object.customMetadata?.kind !== 'raw'
  )
    fail('raw-not-confirmed');
  return new Response(object.body, {
    headers: {
      'Content-Type': 'message/rfc822',
      'Content-Length': String(object.size),
      ETag: object.httpEtag,
      'Cache-Control': 'no-store',
    },
  });
}
async function acknowledge(request, env, cfg, id, target) {
  const input = await boundedJSON(request.body, 2048),
    receipt = input?.receipt;
  const key = await hash([cfg.source, id, target].join('\0'));
  if (
    !exact(input, ['source_id', 'generation', 'receipt']) ||
    input.source_id !== cfg.source ||
    input.generation !== cfg.generation ||
    !exact(receipt, ['id', 'mailbox_id', 'sha256', 'state']) ||
    receipt.id !== key ||
    receipt.mailbox_id !== target ||
    !DIGEST.test(receipt.sha256) ||
    !['stored', 'processed'].includes(receipt.state)
  )
    fail('invalid-receipt', 400);
  for (let attempt = 0; attempt < 8; attempt++) {
    const current = await getControl(env, cfg, id, target),
      record = current.value;
    if (!['received', 'delivered'].includes(record.state)) fail('copy-expired', 410);
    if (record.message.delivery) {
      if (
        record.message.delivery.receipt_id !== key ||
        record.message.delivery.sha256 !== receipt.sha256
      )
        fail('receipt-conflict', 409);
    } else {
      const raw = await env.MAIL.head(rawKey(cfg, id, target));
      if (
        !raw ||
        raw.etag !== record.message.etag ||
        raw.size !== record.message.bytes ||
        raw.customMetadata?.kind !== 'raw'
      )
        fail('raw-not-confirmed');
      record.message.delivery = {
        receipt_id: key,
        sha256: receipt.sha256,
        state: receipt.state,
        acknowledged_at: new Date().toISOString(),
      };
      record.state = 'delivered';
      if (!(await putControl(env, controlKey(cfg, id, target), record, current.etag))) continue;
    }
    // Most receipts do not change shared capacity. Avoid consuming the
    // per-key write allowance when there is no pressure traversal to reset.
    if ((await readRoot(env, cfg)).value.pressure) {
      await changeRoot(env, cfg, (root) => {
        if (root.pressure)
          root.pressure = {
            nonce: crypto.randomUUID(),
            cursor: '',
            started_at: root.pressure.started_at,
            needed_bytes: root.pressure.needed_bytes,
          };
      });
    }
    return response({
      schema: SCHEMA,
      source_id: cfg.source,
      generation: cfg.generation,
      message_id: id,
      mailbox_id: target,
      delivery: record.message.delivery,
    });
  }
  fail('source-busy');
}
async function authenticated(request, env) {
  const cfg = settings(env),
    url = new URL(request.url);
  if (url.protocol !== 'https:' || request.headers.has('Origin') || request.headers.has('Cookie'))
    return response({ error: 'forbidden' }, 403);
  const control =
    request.method === 'POST' && ['/v1/routes', '/v1/maintenance'].includes(url.pathname);
  if (
    request.headers.get('Authorization') !==
    `Bearer ${control ? env.CONTROL_TOKEN : env.READER_TOKEN}`
  )
    return response({ error: 'unauthorized' }, 401);
  if (url.pathname === '/v1/routes' && !url.search) {
    if (request.method === 'GET') return response(publicRoutes((await readRoot(env, cfg)).value));
    if (request.method === 'POST') return replaceRoutes(request, env, cfg);
  }
  if (url.pathname === '/v1/messages' && request.method === 'GET')
    return listMessages(url, env, cfg);
  const direct = /^\/v1\/messages\/([^/]+)$/.exec(url.pathname);
  if (
    direct &&
    MESSAGE_ID.test(direct[1]) &&
    UUID.test(url.searchParams.get('target')) &&
    [...url.searchParams.keys()].length === 1 &&
    request.method === 'GET'
  ) {
    const record = (await getControl(env, cfg, direct[1], url.searchParams.get('target'))).value;
    if (!['received', 'delivered'].includes(record.state)) fail('copy-expired', 410);
    return response({
      schema: SCHEMA,
      source_id: cfg.source,
      generation: cfg.generation,
      route_revision: (await readRoot(env, cfg)).value.route_revision,
      message: publicMessage(record),
    });
  }
  if (url.pathname === '/v1/maintenance' && request.method === 'POST' && !url.search)
    return response(await maintain(env));
  if (url.pathname === '/v1/status' && request.method === 'GET' && !url.search) {
    const root = (await readRoot(env, cfg)).value;
    return response({
      schema: SCHEMA,
      source_id: cfg.source,
      generation: cfg.generation,
      capacity_bytes: cfg.capacity,
      used_bytes: root.used_bytes,
      delivered_days: cfg.days,
      pending_reservations: root.pending.length,
      pressure_scan: !!root.pressure,
      pressure_checked_at: root.pressure_checked_at || null,
      alert: root.alert,
      account_quota: 'unknown',
      notifications: notificationConfiguration(env).state,
    });
  }
  const match = /^\/v1\/messages\/([^/]+)\/(raw|ack)$/.exec(url.pathname);
  if (
    match &&
    MESSAGE_ID.test(match[1]) &&
    UUID.test(url.searchParams.get('target')) &&
    [...url.searchParams.keys()].length === 1
  ) {
    if (match[2] === 'raw' && request.method === 'GET')
      return rawMessage(env, cfg, match[1], url.searchParams.get('target'));
    if (match[2] === 'ack' && request.method === 'POST')
      return acknowledge(request, env, cfg, match[1], url.searchParams.get('target'));
  }
  return response({ error: 'not-found' }, 404);
}
export default {
  async email(message, env, ctx) {
    const accepted = await receive(message, env);
    if (accepted && ctx)
      ctx.waitUntil(notify(env, accepted.metadata.message_id, accepted.metadata.mailbox_id));
  },
  async scheduled(_controller, env, ctx) {
    ctx.waitUntil(Promise.all([maintain(env), notifyDue(env)]));
  },
  async fetch(request, env) {
    try {
      return await authenticated(request, env);
    } catch (error) {
      return response(
        { error: error instanceof SourceError ? error.message : 'source-unavailable' },
        error instanceof SourceError ? error.status : 503
      );
    }
  },
};
