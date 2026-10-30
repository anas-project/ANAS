// Explicitly enabled, expiring SMTP experiment on mail-poc.bengbu.cc.cd only.
import fixture from './cloud-fixture.mjs';
import worker from './worker.mjs';
export default {
  async fetch(request, env) {
    if (new URL(request.url).pathname === '/__smtp-faults') {
      if (Date.now() >= Number(env.EXPIRES) || request.headers.get('Authorization') !== `Bearer ${env.TEST_TOKEN}`)
        return new Response('Unauthorized', {status:401});
      const page = await env.MAIL.list({prefix:'smtp-fault/'});
      return Response.json({observed:page.objects.length});
    }
    return fixture.fetch(request, env);
  },
  async email(message, env, ctx) {
    if (Date.now() >= Number(env.EXPIRES) || message.rawSize > 32768 || message.from !== 'probe@sender.mail-poc.bengbu.cc.cd') {
      message.setReject('Isolated test unavailable');
      return;
    }
    if (message.to === 'fail@mail-poc.bengbu.cc.cd') {
      await env.MAIL.put('smtp-fault/' + crypto.randomUUID(), JSON.stringify({at:Date.now(),to:message.to}));
      throw new Error('Injected raw-storage failure for isolated SMTP test');
    }
    return worker.email(message, env, ctx);
  },
};
