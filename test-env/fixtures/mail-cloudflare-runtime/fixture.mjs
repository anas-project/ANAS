// TEST_CASES: MAILMOD-T-010
// Uploaded as fixture.mjs alongside the production worker.mjs; expires after
// three hours. Never use this synthetic entry point for production email.
import worker, { receive, maintain } from './worker.mjs';

const json = (value, status = 200) => Response.json(value, { status });
export default {
  async fetch(request, env) {
    if (Date.now() >= Number(env.EXPIRES)) return json({ error: 'fixture-expired' }, 410);
    const url = new URL(request.url);
    if (!url.pathname.startsWith('/__')) return worker.fetch(request, env);
    if (request.headers.get('Authorization') !== `Bearer ${env.TEST_TOKEN}`)
      return json({ error: 'unauthorized' }, 401);
    if (url.pathname === '/__health') return json({ revision: env.REVISION });
    if (url.pathname === '/__cleanup' && request.method === 'POST') {
      const page = await env.MAIL.list({ limit: 100 });
      if (page.objects.length) await env.MAIL.delete(page.objects.map((object) => object.key));
      return json({ deleted: page.objects.length });
    }
    if (url.pathname === '/__maintenance' && request.method === 'POST') {
      const advance = Number(url.searchParams.get('advance') || 0);
      if (!Number.isSafeInteger(advance) || advance < 0 || advance > 8 * 86400000)
        return json({ error: 'invalid-time' }, 400);
      try {
        return json(await maintain(env, Date.now() + advance));
      } catch {
        return json({ error: 'maintenance-not-confirmed' }, 503);
      }
    }
    if (url.pathname === '/__receive' && request.method === 'POST') {
      const chunks = [];
      let size = 0;
      const reader = request.body?.getReader();
      if (!reader) return json({ error: 'body-required' }, 400);
      for (;;) {
        const next = await reader.read();
        if (next.done) break;
        size += next.value.byteLength;
        if (size > 32768) {
          await reader.cancel();
          return json({ error: 'fixture-size' }, 413);
        }
        chunks.push(next.value);
      }
      const raw = new Uint8Array(size);
      let offset = 0;
      for (const chunk of chunks) {
        raw.set(chunk, offset);
        offset += chunk.byteLength;
      }
      let binding = env,
        rejected;
      if (url.searchParams.get('fault') === 'record') {
        const bucket = env.MAIL;
        binding = {
          ...env,
          MAIL: {
            get: (...args) => bucket.get(...args),
            head: (...args) => bucket.head(...args),
            list: (...args) => bucket.list(...args),
            put: (key, ...args) => {
              if (key.includes('/control/')) throw Error('injected-record-write');
              return bucket.put(key, ...args);
            },
          },
        };
      }
      try {
        const result = await receive(
          {
            from: 'probe@sender.mail-poc.bengbu.cc.cd',
            to: url.searchParams.get('to'),
            raw,
            rawSize: size,
            setReject: (reason) => {
              rejected = reason;
            },
          },
          binding
        );
        return json({ result, rejected });
      } catch {
        return json({ error: 'receive-not-confirmed' }, 503);
      }
    }
    return json({ error: 'not-found' }, 404);
  },
};
