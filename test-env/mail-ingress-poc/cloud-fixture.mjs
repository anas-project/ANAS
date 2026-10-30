// Authenticated, short-lived synthetic fixture. Never install as a mail service.
import worker, { receive, notify } from './worker.mjs';
const json = (body, status = 200) => Response.json(body, { status });
const validID = id => /^[a-f0-9-]{36}$/.test(id || '');

export default {
  async fetch(request, env) {
    if (Date.now() >= Number(env.EXPIRES)) return json({ error: 'expired' }, 410);
    const url = new URL(request.url);
    if (!url.pathname.startsWith('/__')) return worker.fetch(request, env);
    if (request.headers.get('Authorization') !== `Bearer ${env.TEST_TOKEN}`)
      return json({ error: 'unauthorized' }, 401);
    if (url.pathname === '/__health') return json({ revision: env.REVISION });
    if (url.pathname === '/__receive' && request.method === 'POST') {
      const bytes = await request.arrayBuffer();
      if (bytes.byteLength > 32768) return json({ error: 'fixture-size' }, 413);
      let rejected;
      const id = await receive({ to: url.searchParams.get('to'), from: 'sender@example.net',
        raw: bytes, rawSize: bytes.byteLength, setReject: reason => { rejected = reason; } }, env);
      return json({ id, rejected });
    }
    if (url.pathname === '/__cleanup' && request.method === 'POST') {
      // The deployment script binds only a freshly created, unique test bucket.
      const page = await env.MAIL.list({ limit: 100 });
      if (page.objects.length) await env.MAIL.delete(page.objects.map(x => x.key));
      return json({ deleted: page.objects.length });
    }
    const id = url.searchParams.get('id');
    if (!validID(id)) return json({ error: 'invalid-id' }, 400);
    if (url.pathname === '/__notify' && request.method === 'POST') {
      const mode = url.searchParams.get('mode') || 'ok';
      if (!['ok', 'offline', 'timeout', 'redirect', 'denied', 'busy', 'nas'].includes(mode))
        return json({ error: 'mode' }, 400);
      const advance = Math.max(0, Math.min(600000, Number(url.searchParams.get('advance')) || 0));
      if (mode === 'nas' && !env.NOTIFY_URL) return json({ error: 'no-nas-endpoint' }, 400);
      return json({ state: await notify({ ...env, NOTIFY_URL: mode === 'nas' ? env.NOTIFY_URL : `${env.SINK_URL}/${mode}` },
        id, Date.now() + advance) });
    }
    if (url.pathname === '/__inspect' && request.method === 'GET') {
      const state = {};
      for (const key of [`ack/${id}`, `notified/${id}`, `blocked/${id}`,
        ...[0, 1, 2].map(n => `attempt/${id}/${n}`)]) {
        const object = await env.MAIL.get(key);
        state[key] = object ? await object.json() : null;
      }
      const sink = await env.MAIL.list({ prefix: `sink/${id}/` });
      state.sinkCalls = sink.objects.length;
      state.rawPresent = !!(await env.MAIL.head(`mail/${id}`));
      return json(state);
    }
    return json({ error: 'not-found' }, 404);
  },
};
