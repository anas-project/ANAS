// TEST_CASES: MAILMOD-T-010
// Test-only HTTPS through a local SSH SOCKS tunnel. Authorization stays in a
// 0600 local header file, never in curl arguments or the remote SSH host.
import {spawn} from 'node:child_process';
import {mkdtemp, writeFile, rm} from 'node:fs/promises';
import {join} from 'node:path';

export async function cloudflareFetch(proxyPort, target, options = {}) {
  const url = new URL(target);
  if (!Number.isInteger(proxyPort) || proxyPort < 1 || proxyPort > 65535 || url.protocol !== 'https:' || url.username || url.password || url.port || url.hash || !(url.hostname === 'api.cloudflare.com' || /^anas-mail-smtp-live-[a-f0-9]{12}\.[a-z0-9-]+\.workers\.dev$/.test(url.hostname))) throw new Error('unsupported isolated Cloudflare target');
  const method = options.method || 'GET';
  if (!['GET','POST','PUT','DELETE'].includes(method) || options.redirect && options.redirect !== 'error') throw new Error('unsupported isolated Cloudflare request');
  const headers = options.headers || {};
  if (Object.keys(headers).some(key => !['Authorization','Content-Type'].includes(key)) || !/^Bearer [A-Za-z0-9_-]{20,256}$/.test(headers.Authorization || '')) throw new Error('invalid isolated Cloudflare authorization');
  let body = options.body, contentType = headers['Content-Type'];
  if (body instanceof FormData) {
    const encoded = new Response(body);
    contentType = encoded.headers.get('content-type');
    body = Buffer.from(await encoded.arrayBuffer());
  } else if (body !== undefined && body !== null) body = Buffer.from(body);
  if (body && body.length > 16<<20 || contentType && /[\r\n]/.test(contentType)) throw new Error('invalid isolated Cloudflare request body');
  const dir = await mkdtemp('/private/tmp/anas-cf-tunnel-');
  const file = join(dir, 'headers');
  try {
    await writeFile(file, 'Authorization: '+headers.Authorization+'\n', {mode:0o600,flag:'wx'});
    const args = ['--silent','--show-error','--proto','=https','--socks5-hostname',`127.0.0.1:${proxyPort}`,'--noproxy','',
      '--max-time','50','--request',method,'--header','@'+file,'--write-out','%{http_code}'];
    if (contentType) args.push('--header','Content-Type: '+contentType);
    if (body) args.push('--data-binary','@-');
    args.push(url.href);
    const response = await new Promise((resolve,reject) => {
      const child = spawn('/usr/bin/curl',args,{stdio:['pipe','pipe','pipe']});
      const chunks=[]; let size=0, failed=false;
      const fail = error => { if (!failed) {failed=true;child.kill('SIGTERM');reject(error);} };
      const abort = () => fail(new Error('isolated Cloudflare request cancelled'));
      if (options.signal?.aborted) abort();
      else options.signal?.addEventListener('abort',abort,{once:true});
      child.on('error',()=>fail(new Error('isolated Cloudflare transport unavailable')));
      child.stdout.on('data',chunk=>{size+=chunk.length;if(size>8<<20)fail(new Error('isolated Cloudflare response too large'));else chunks.push(chunk);});
      child.stderr.resume();
      child.stdin.on('error',()=>{});
      child.on('close',code=>{
        options.signal?.removeEventListener('abort',abort);
        if (failed) return;
        if (code !== 0 || size < 3) { reject(new Error('isolated Cloudflare transport failed')); return; }
        const raw=Buffer.concat(chunks), status=Number(raw.subarray(-3).toString('ascii'));
        if (!Number.isInteger(status) || status<100 || status>599) { reject(new Error('isolated Cloudflare status invalid')); return; }
        resolve(new Response(raw.subarray(0,-3),{status}));
      });
      child.stdin.end(body || undefined);
    });
    return response;
  } finally { await rm(dir,{recursive:true,force:true}); }
}
