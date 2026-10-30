// TEST_CASES: MAILMOD-T-010
// Ephemeral test-only CONNECT proxy. It forwards opaque TLS to exactly one
// disposable Worker hostname, and optionally Cloudflare's API; it is not
// installed in the product or NAS. The API uses a separate zone-limited token.
import net from 'node:net';
import {once} from 'node:events';

const workerHost = /^anas-mail-smtp-live-[a-f0-9]{12}\.[a-z0-9-]+\.workers\.dev$/;

// The optional test-only upstream is a local SSH SOCKS tunnel. DNS resolution
// happens through that tunnel and TLS still terminates at the Worker itself.
export async function dialWorkerThroughSocks(host, port, proxyPort, allowCloudflareApi = false) {
  if (!(workerHost.test(host) || allowCloudflareApi === true && host === 'api.cloudflare.com') || port !== 443 || !Number.isInteger(proxyPort) || proxyPort < 1 || proxyPort > 65535) throw new Error('invalid test SOCKS target');
  const socket = net.createConnection({host:'127.0.0.1',port:proxyPort});
  socket.setTimeout(10000, () => socket.destroy(new Error('test SOCKS timeout')));
  try {
    await once(socket, 'connect');
    const read = async size => {
      let value;
      while ((value = socket.read(size)) === null) {
        if (socket.destroyed || socket.readableEnded) throw new Error('test SOCKS closed');
        await once(socket, 'readable');
      }
      return value;
    };
    socket.write(Buffer.from([5,1,0]));
    if (!(await read(2)).equals(Buffer.from([5,0]))) throw new Error('test SOCKS authentication rejected');
    const name = Buffer.from(host, 'ascii');
    socket.write(Buffer.concat([Buffer.from([5,1,0,3,name.length]),name,Buffer.from([1,187])]));
    const head = await read(4);
    if (head[0] !== 5 || head[1] !== 0 || head[2] !== 0) throw new Error('test SOCKS connection rejected');
    let size = head[3] === 1 ? 4 : head[3] === 4 ? 16 : 0;
    if (head[3] === 3) size = (await read(1))[0];
    if (!size) throw new Error('test SOCKS reply invalid');
    await read(size+2);
    socket.setTimeout(0);
    return socket;
  } catch (error) { socket.destroy(); throw error; }
}

export function createWorkerProxy(host, dial = (name, port) => net.createConnection({host: name, port}), {allowCloudflareApi = false} = {}) {
  if (!workerHost.test(host)) throw new Error('isolated Worker hostname required');
  if (typeof allowCloudflareApi !== 'boolean') throw new Error('invalid API permission');
  let active = 0;
  const stats = {accepted:0,rejected:0,authorized:0,upstream_connected:0,upstream_failed:0};
  const server = net.createServer(client => {
    if (client.remoteAddress !== '127.0.0.1' || active >= 16) { stats.rejected++; client.destroy(); return; }
    stats.accepted++;
    active++;
    let upstream, finished = false, input = Buffer.alloc(0), connected = false;
    const end = () => {
      if (finished) return;
      finished = true;
      active--;
      client.destroy();
      upstream?.destroy();
    };
    client.on('error', end);
    client.on('close', end);
    client.setTimeout(10000, end);
    client.on('data', chunk => {
      if (connected || finished) return;
      input = Buffer.concat([input, chunk]);
      const boundary = input.indexOf('\r\n\r\n');
      if (boundary < 0) { if (input.length > 8192) end(); return; }
      if (boundary > 8192) { end(); return; }
      const lines = input.subarray(0, boundary).toString('latin1').split('\r\n');
      const destination = lines[0] === `CONNECT ${host}:443 HTTP/1.1` ? host : allowCloudflareApi && lines[0] === 'CONNECT api.cloudflare.com:443 HTTP/1.1' ? 'api.cloudflare.com' : '';
      if (!destination || lines.slice(1).some(line => !/^[A-Za-z0-9-]+: [^\r\n]*$/.test(line))) {
        stats.rejected++;
        client.end('HTTP/1.1 403 Forbidden\r\nConnection: close\r\n\r\n');
        return;
      }
      connected = true;
      stats.authorized++;
      client.pause();
      const remainder = input.subarray(boundary + 4);
      input = null;
      const ready = socket => {
        upstream = socket;
        if (finished) { upstream.destroy(); return; }
        stats.upstream_connected++;
        upstream.on('error', end);
        upstream.on('close', end);
        upstream.setTimeout(30000, end);
        if (finished) return;
        client.setTimeout(30000, end);
        client.write('HTTP/1.1 200 Connection Established\r\n\r\n');
        if (remainder.length) upstream.write(remainder);
        client.pipe(upstream);
        upstream.pipe(client);
        client.resume();
      };
      try {
        const result = dial(destination, 443);
        if (result && typeof result.then === 'function') result.then(ready, () => { stats.upstream_failed++; end(); });
        else { upstream = result; upstream.on('error', () => { stats.upstream_failed++; end(); }); upstream.once('connect', () => ready(upstream)); }
      } catch { stats.upstream_failed++; end(); }
    });
  });
  server.stats = stats;
  return server;
}
