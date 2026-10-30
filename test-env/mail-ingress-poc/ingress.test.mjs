import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHmac } from 'node:crypto';
import { Miniflare } from 'miniflare';
import { receive } from './worker.mjs';

const token = 'fixture-only-pull-token';
const secret = 'fixture-only-notification-secret';
const raw = 'From: sender@example.net\r\nTo: forged@example.org\r\nMessage-ID: <same@sender>\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n你好\r\n--x\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n\r\nAAECA/8=\r\n--x--\r\n';
const wrapper = `import worker, {receive, notify} from './worker.mjs';
export default {async fetch(req, env) {
 const u = new URL(req.url);
 if (u.pathname === '/__receive') {
   let rejected;
   const bytes = await req.arrayBuffer();
   const streamed = u.searchParams.has('stream') ? new ReadableStream({start(controller) {
     controller.enqueue(new Uint8Array(bytes).slice(0,17));
     controller.enqueue(new Uint8Array(bytes).slice(17)); controller.close();
   }}) : bytes;
   const id = await receive({from:'sender@example.net', to:u.searchParams.get('to'),
     raw:streamed, rawSize:bytes.byteLength + Number(u.searchParams.get('sizeDelta') || 0), setReject:reason=>{rejected=reason}},env);
   return Response.json({id,rejected});
 }
 if (u.pathname === '/__notify') return Response.json({state:await notify(env,u.searchParams.get('id'),Number(u.searchParams.get('now'))||Date.now())});
 return worker.fetch(req,env);
}}`;
async function fixture(t, responder = async req => Response.json({ id: (await req.json()).id })) {
  const dir = await mkdtemp(join(tmpdir(), 'anas-mail-ingress-'));
  const code = await readFile(new URL('./worker.mjs', import.meta.url), 'utf8');
  let calls = [];
  let mf;
  async function start() {
    mf = new Miniflare({ compatibilityDate: '2026-07-01', host: '127.0.0.1',
      modules: [{ type: 'ESModule', path: 'main.mjs', contents: wrapper },
        { type: 'ESModule', path: 'worker.mjs', contents: code }],
      r2Buckets: ['MAIL'], r2Persist: dir,
      bindings: { SOURCE: 'source-a', PULL_TOKEN: token, NOTIFY_URL: 'https://nas.invalid/notice',
        NOTIFY_SECRET: secret, ROUTES: JSON.stringify({ 'a@example.org': 'box-a', 'b@example.org': 'box-b' }) },
      outboundService: async req => {
        assert.equal(req.url, 'https://nas.invalid/notice');
        const body = await req.clone().text();
        assert.equal(req.headers.get('x-mail-signature'), createHmac('sha256', secret)
          .update(`${req.headers.get('x-mail-time')}.${body}`).digest('hex'));
        const message = JSON.parse(body);
        assert.deepEqual(Object.keys(message).sort(), ['id', 'source', 'target']);
        const bucket = await mf.getR2Bucket('MAIL');
        assert.ok(await bucket.head(`mail/${message.id}`), 'persist before notification');
        calls.push(message);
        return responder(req);
      } });
    await mf.ready;
  }
  await start();
  t.after(async () => { await mf.dispose(); await rm(dir, { recursive: true, force: true }); });
  const api = (path, init = {}) => mf.dispatchFetch(`http://localhost${path}`, {
    ...init, headers: { Authorization: `Bearer ${token}`, ...init.headers } });
  return { api, calls, restart: async () => { await mf.dispose(); await start(); },
    bucket: () => mf.getR2Bucket('MAIL'),
    receive: async (to = 'a@example.org') => (await api(`/__receive?to=${to}`, { method: 'POST', body: raw })).json(),
    notify: async (id, now = Date.now()) => (await api(`/__notify?id=${id}&now=${now}`)).json(),
    ack: (id, target = 'box-a') => api(`/messages/${id}/ack`, { method: 'POST',
      body: JSON.stringify({ source: 'source-a', target, receipt: 'nas-durable-receipt' }) }) };
}

test('R2 failure propagates before acceptance; no success is invented', async () => {
  let wrote = 0;
  await assert.rejects(receive({ to: 'a@example.org', from: 's@example.net', raw: 'bytes', rawSize: 5 },
    { ROUTES: '{"a@example.org":"box-a"}', SOURCE: 'a', MAIL: { put: async () => { wrote++; throw Error('disk failure'); } } }), /disk failure/);
  assert.equal(wrote, 1);
});

test('NAS offline: R2 survives runtime restart, complete bytes and envelope remain retrievable', async t => {
  const f = await fixture(t, () => new Response('offline', { status: 503 }));
  const { id } = await f.receive();
  assert.equal((await f.notify(id)).state, 'failed');
  await f.restart();
  const page = await (await f.api('/messages')).json();
  assert.equal(page.messages[0].target, 'box-a');
  assert.equal(page.messages[0].recipient, 'a@example.org');
  assert.equal(page.messages[0].delivered, false);
  assert.equal(await (await f.api(`/messages/${id}/raw`)).text(), raw);
  assert.equal((await f.ack(id)).status, 200);
});

test('Email Worker raw stream persists complete bytes without a known stream length', async t => {
  const f = await fixture(t);
  const response = await f.api('/__receive?to=a@example.org&stream=1', {method:'POST',body:raw});
  assert.equal(response.status,200,response.status === 200 ? '' : await response.text());
  const {id} = await response.json();
  assert.equal(await (await f.api(`/messages/${id}/raw`)).text(),raw);
});

test('Truncated and oversized raw streams cannot create a stored mail object', async t => {
  const f = await fixture(t);
  for (const delta of [-1,1]) {
    const response = await f.api(`/__receive?to=a@example.org&stream=1&sizeDelta=${delta}`, {method:'POST',body:raw});
    assert.equal(response.status,500);
    assert.equal((await (await f.api('/messages')).json()).messages.length,0);
  }
});

test('notification success is NOT delivery; duplicate and late acknowledgements cannot overwrite completion', async t => {
  const f = await fixture(t);
  const { id } = await f.receive();
  assert.equal((await f.notify(id)).state, 'notified');
  const bucket = await f.bucket();
  assert.ok(await bucket.head(`notified/${id}`));
  assert.equal(await bucket.head(`ack/${id}`), null);
  assert.ok(await bucket.head(`mail/${id}`));
  await Promise.all([f.ack(id), f.ack(id)]);
  const before = await (await bucket.get(`ack/${id}`)).text();
  await f.ack(id);
  assert.equal((await f.notify(id, Date.now() + 400000)).state, 'delivered');
  assert.equal(await (await bucket.get(`ack/${id}`)).text(), before);
  assert.equal(f.calls.length, 1);
  assert.ok(await bucket.head(`mail/${id}`));
});

test('timed-out notification has bounded persistent retries and does not block polling', async t => {
  let first = true;
  const f = await fixture(t, async () => {
    if (first) { first = false; await new Promise(resolve => setTimeout(resolve, 5500)); }
    return new Response('offline', {status: 503});
  });
  const { id } = await f.receive();
  const now = Date.now();
  assert.equal((await f.notify(id, now)).state, 'failed');
  const timeoutAttempt = await (await (await f.bucket()).get(`attempt/${id}/0`)).json();
  assert.equal(timeoutAttempt.failure, 'timeout');
  assert.equal(timeoutAttempt.status, undefined);
  assert.equal((await f.notify(id, now + 10)).state, 'waiting');
  await f.restart();
  assert.equal((await f.notify(id, now + 61000)).state, 'failed');
  assert.equal((await f.notify(id, now + 301000)).state, 'failed');
  assert.equal((await f.notify(id, now + 601000)).state, 'exhausted');
  assert.equal(f.calls.length, 3);
  assert.equal(await (await f.api(`/messages/${id}/raw`)).text(), raw);
  await f.ack(id);
  assert.equal((await f.notify(id, now + 701000)).state, 'delivered');
});

test('pagination and restored-client reconciliation include completed retained mail', async t => {
  const f = await fixture(t);
  const ids = [];
  for (let i = 0; i < 5; i++) ids.push((await f.receive()).id);
  await f.ack(ids[0]);
  await f.restart();
  let cursor, messages = [];
  do {
    const page = await (await f.api('/messages?limit=2' + (cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''))).json();
    messages.push(...page.messages); cursor = page.cursor;
  } while (cursor);
  assert.deepEqual(messages.map(x => x.id).sort(), ids.sort());
  assert.equal(messages.filter(x => x.delivered).length, 1);
});

test('forged headers, equal Message-ID, cross-target receipt and unauthenticated API stay isolated', async t => {
  const f = await fixture(t);
  const a = await f.receive(), b = await f.receive('b@example.org');
  assert.notEqual(a.id, b.id);
  assert.equal((await f.receive('removed@example.org')).rejected, 'Unknown recipient');
  assert.equal((await f.ack(a.id, 'box-b')).status, 400);
  assert.equal((await f.api('/messages', { headers: { Authorization: 'Bearer wrong' } })).status, 401);
  assert.equal((await f.api(`/messages/${a.id}/raw`, { headers: { Authorization: '' } })).status, 401);
  assert.equal((await f.api('/messages/../../private/raw')).status, 404);
  const messages = (await (await f.api('/messages')).json()).messages;
  assert.equal(messages.find(x => x.id === a.id).target, 'box-a');
  assert.equal(messages.find(x => x.id === b.id).target, 'box-b');
});

test('redirects do not expose signed notifications; configuration failure stops retries', async t => {
  const f = await fixture(t, () => new Response(null, { status: 302, headers: { Location: 'https://untrusted.invalid/' } }));
  const { id } = await f.receive();
  assert.equal((await f.notify(id)).state, 'configuration-error');
  assert.equal((await f.notify(id, Date.now()+400000)).state, 'configuration-error');
  assert.equal(f.calls.length, 1);
  assert.equal(await (await f.api(`/messages/${id}/raw`)).text(), raw);
});


test('in-flight notification finishing after delivery cannot regress its durable receipt', async t => {
  let entered, release;
  const started = new Promise(resolve => { entered = resolve; });
  const blocked = new Promise(resolve => { release = resolve; });
  const f = await fixture(t, async req => {
    const message = await req.json();
    entered(); await blocked;
    return Response.json({id: message.id});
  });
  const {id} = await f.receive();
  const pending = f.notify(id);
  await started;
  try {
    assert.equal((await f.ack(id)).status, 200);
  } finally { release(); }
  assert.equal((await pending).state, 'notified');
  const message = (await (await f.api('/messages')).json()).messages[0];
  assert.equal(message.delivered, true);
  assert.equal((await f.notify(id)).state, 'delivered');
  assert.equal(f.calls.length, 1);
});
