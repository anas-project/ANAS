// Synthetic NAS notification endpoint, not an actual NAS consumer.
export default {
  async fetch(request, env) {
    if (Date.now() >= Number(env.EXPIRES)) return new Response(null, { status: 410 });
    if (request.method !== 'POST') return new Response(null, { status: 401 });
    const mode = new URL(request.url).pathname.slice(1);
    if (!['ok', 'offline', 'timeout', 'redirect', 'denied', 'busy'].includes(mode))
      return new Response(null, { status: 404 });
    const body = await request.text();
    if (body.length > 1024) return new Response(null, { status: 413 });
    const timestamp = request.headers.get('X-Mail-Time');
    const signature = request.headers.get('X-Mail-Signature') || '';
    if (!/^\d+$/.test(timestamp || '') || Math.abs(Date.now() - Number(timestamp)) > 660000 ||
        !/^[a-f0-9]{64}$/.test(signature)) return new Response(null, { status: 401 });
    const key = await crypto.subtle.importKey('raw', new TextEncoder().encode(env.NOTIFY_SECRET),
      { name: 'HMAC', hash: 'SHA-256' }, false, ['verify']);
    const mac = Uint8Array.from(signature.match(/../g), x => parseInt(x, 16));
    if (!await crypto.subtle.verify('HMAC', key, mac,
      new TextEncoder().encode(`${timestamp}.${body}`))) return new Response(null, { status: 401 });
    const event = JSON.parse(body);
    if (!/^[a-f0-9-]{36}$/.test(event.id || '') ||
        Object.keys(event).sort().join(',') !== 'id,source,target') return new Response(null, { status: 400 });
    const mail = await env.MAIL.head(`mail/${event.id}`);
    if (!mail || mail.customMetadata.source !== event.source || mail.customMetadata.target !== event.target)
      return new Response(null, { status: 409 });
    // Records that the signed notification arrived AFTER original bytes were stored.
    await env.MAIL.put(`sink/${event.id}/${crypto.randomUUID()}`, JSON.stringify({ mode, at: Date.now() }));
    if (mode === 'timeout') await new Promise(resolve => setTimeout(resolve, 6000));
    if (mode === 'timeout' || mode === 'offline') return new Response(null, { status: 503 });
    if (mode === 'busy') return new Response(null, { status: 429 });
    if (mode === 'denied') return new Response(null, { status: 403 });
    if (mode === 'redirect') return new Response(null, { status: 302,
      headers: { Location: 'https://untrusted.invalid/' } });
    return Response.json({ id: event.id });
  },
};
