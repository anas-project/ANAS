// Isolated protocol prototype, NOT a deployable mail service. See README.md.
const json = (data, status = 200) => Response.json(data, { status });
const createOnly = { onlyIf: { etagDoesNotMatch: '*' } };
const mailKey = id => `mail/${id}`;
const idPattern = /^[a-f0-9-]{36}$/;

export async function receive(message, env) {
  const target = JSON.parse(env.ROUTES)[message.to];
  if (!target) { message.setReject('Unknown recipient'); return null; }
  if (!Number.isSafeInteger(message.rawSize) || message.rawSize < 0 || message.rawSize > 25 * 1024 * 1024) {
    message.setReject('Message too large'); return null;
  }
  // One atomic R2 object holds BOTH original bytes and envelope metadata.
  // A new SMTP invocation gets a new ID: Message-ID is sender-controlled.
  const id = crypto.randomUUID();
  // Email raw streams do not expose a known length to R2. Preserve streaming
  // and enforce the provider's byte count instead of buffering the full email.
  const transfer = new AbortController();
  let body = message.raw;
  let pumping = Promise.resolve();
  if (typeof body?.getReader === 'function') {
    const fixed = new FixedLengthStream(message.rawSize);
    pumping = body.pipeTo(fixed.writable, {signal:transfer.signal});
    body = fixed.readable;
  }
  try {
    const [stored] = await Promise.all([
      Promise.resolve().then(() => env.MAIL.put(mailKey(id), body, {
        ...createOnly,
        customMetadata: { target, recipient: message.to, from: message.from,
          source: env.SOURCE, created: String(Date.now()) },
      })), pumping,
    ]);
    if (!stored) throw new Error('Storage did not confirm creation');
  } finally { transfer.abort(); }
  return id;
}

export async function notify(env, id, now = Date.now()) {
  if (await env.MAIL.head(`ack/${id}`)) return 'delivered';
  if (await env.MAIL.head(`notified/${id}`)) return 'notified';
  if (await env.MAIL.head(`blocked/${id}`)) return 'configuration-error';
  if (!env.NOTIFY_URL || !env.NOTIFY_SECRET) return 'poll-only';
  if (!env.NOTIFY_URL.startsWith('https://')) throw new Error('HTTPS required');
  const mail = await env.MAIL.head(mailKey(id));
  if (!mail) return 'missing';
  const delays = [0, 60_000, 300_000];
  let attempt = 0;
  for (; attempt < delays.length; attempt++) {
    if (!(await env.MAIL.head(`attempt/${id}/${attempt}`))) break;
  }
  if (attempt === delays.length) return 'exhausted';
  if (now < Number(mail.customMetadata.created) + delays[attempt]) return 'waiting';
  // A claimed attempt remains consumed after interruption; polling is independent.
  const claim = await env.MAIL.put(`attempt/${id}/${attempt}`,
    JSON.stringify({ at: now, state: 'started' }), createOnly);
  if (!claim) return 'claimed';
  const body = JSON.stringify({ source: env.SOURCE, id, target: mail.customMetadata.target });
  const timestamp = String(now);
  const key = await crypto.subtle.importKey('raw', new TextEncoder().encode(env.NOTIFY_SECRET),
    { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
  const mac = await crypto.subtle.sign('HMAC', key, new TextEncoder().encode(`${timestamp}.${body}`));
  const signature = Array.from(new Uint8Array(mac), x => x.toString(16).padStart(2, '0')).join('');
  let state = 'failed';
  let status, failure;
  const timeout = AbortSignal.timeout(5000);
  const started = Date.now();
  try {
    const response = await fetch(env.NOTIFY_URL, { method: 'POST', body, redirect: 'manual',
      headers: { 'Content-Type': 'application/json', 'X-Mail-Time': timestamp, 'X-Mail-Signature': signature },
      signal: timeout });
    status = response.status;
    if (response.ok && (await response.json()).id === id) {
      await env.MAIL.put(`notified/${id}`, JSON.stringify({ at: now }), createOnly);
      state = 'notified';
    } else if (response.status >= 300 && response.status < 500 && response.status !== 429) {
      await env.MAIL.put(`blocked/${id}`, JSON.stringify({ at: now, status: response.status }), createOnly);
      state = 'configuration-error';
    }
  } catch { state = 'failed'; failure = timeout.aborted ? 'timeout' : 'request-or-response'; }
  await env.MAIL.put(`attempt/${id}/${attempt}`, JSON.stringify({ at: now, state,
    status, failure, request_ms: Date.now() - started }));
  // Delivery receipts are separate immutable objects: late notification cannot regress them.
  return state;
}

export default {
  async email(message, env, ctx) {
    const id = await receive(message, env); // Do not swallow storage failure.
    if (id) ctx.waitUntil(notify(env, id));
  },
  async fetch(request, env) {
    if (!env.PULL_TOKEN || request.headers.get('Authorization') !== `Bearer ${env.PULL_TOKEN}`)
      return json({ error: 'unauthorized' }, 401);
    const url = new URL(request.url);
    if (url.pathname === '/messages' && request.method === 'GET') {
      const limit = Math.max(1, Math.min(100, Number(url.searchParams.get('limit')) || 20));
      const page = await env.MAIL.list({ prefix: 'mail/', limit,
        cursor: url.searchParams.get('cursor') || undefined, include: ['customMetadata'] });
      const messages = [];
      for (const object of page.objects) {
        const id = object.key.slice(5);
        messages.push({ id, ...object.customMetadata,
          delivered: !!(await env.MAIL.head(`ack/${id}`)) });
      }
      // Include completed items so a restored NAS can reconcile retained cloud copies.
      return json({ source: env.SOURCE, messages, cursor: page.truncated ? page.cursor : null });
    }
    const match = url.pathname.match(/^\/messages\/([^/]+)\/(raw|ack)$/);
    if (!match || !idPattern.test(match[1])) return json({ error: 'not-found' }, 404);
    const [, id, action] = match;
    const object = await env.MAIL.get(mailKey(id));
    if (!object) return json({ error: 'not-found' }, 404);
    if (action === 'raw' && request.method === 'GET')
      return new Response(object.body, { headers: { 'Content-Type': 'message/rfc822', 'Cache-Control': 'no-store' } });
    if (action === 'ack' && request.method === 'POST') {
      let receipt;
      try { receipt = await request.json(); } catch { return json({ error: 'bad-receipt' }, 400); }
      if (receipt.source !== env.SOURCE || receipt.target !== object.customMetadata.target ||
          typeof receipt.receipt !== 'string' || !/^[a-zA-Z0-9-]{1,80}$/.test(receipt.receipt))
        return json({ error: 'wrong-receipt' }, 400);
      await env.MAIL.put(`ack/${id}`, JSON.stringify({ ...receipt, at: Date.now() }), createOnly);
      return json({ id, delivered: true });
    }
    return json({ error: 'method' }, 405);
  },
};
