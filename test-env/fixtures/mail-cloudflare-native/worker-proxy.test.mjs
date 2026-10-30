// TEST_CASES: MAILMOD-T-010
import assert from 'node:assert/strict';
import net from 'node:net';
import {once} from 'node:events';
import {test} from 'node:test';
import {createWorkerProxy, dialWorkerThroughSocks} from './worker-proxy.mjs';

const host = 'anas-mail-smtp-live-123456abcdef.test.workers.dev';
const connect = port => net.createConnection({host:'127.0.0.1',port});
async function listen(server) { server.listen(0,'127.0.0.1'); await once(server,'listening'); return server.address().port; }
async function close(server) { await new Promise((resolve,reject) => server.close(error=>error?reject(error):resolve())); }

test('proxy allows only the disposable Worker and forwards opaque bytes', async () => {
  assert.throws(() => createWorkerProxy('example.com'));
  const target = net.createServer(socket => socket.on('data', data => socket.write(data)));
  const targetPort = await listen(target);
  const calls = [];
  const proxy = createWorkerProxy(host, (name, port) => { calls.push([name,port]); return connect(targetPort); });
  const port = await listen(proxy);
  try {
    const rejected = connect(port);
    rejected.write('CONNECT example.com:443 HTTP/1.1\r\nHost: example.com\r\n\r\n');
    let refusal = '';
    rejected.on('data', data => { refusal += data; });
    await once(rejected, 'end');
    assert.match(refusal, /^HTTP\/1\.1 403 /);
    assert.equal(calls.length, 0);

    const accepted = connect(port), raw = Buffer.from([0, 1, 2, 255]);
    accepted.write(`CONNECT ${host}:443 HTTP/1.1\r\nHost: ${host}:443\r\n\r\n`);
    let received = Buffer.alloc(0);
    const full = new Promise((resolve,reject) => {
      accepted.on('error', reject);
      accepted.on('data', data => {
        received = Buffer.concat([received,data]);
        if (received.includes('\r\n\r\n') && received.length >= Buffer.byteLength('HTTP/1.1 200 Connection Established\r\n\r\n') + raw.length) resolve();
      });
    });
    await once(accepted, 'data');
    accepted.write(raw);
    await full;
    assert.deepEqual(received.subarray(-raw.length), raw);
    assert.deepEqual(calls, [[host,443]]);
    accepted.destroy();
  } finally { await close(proxy); await close(target); }
});

test('SOCKS tunnel keeps the Worker hostname and TLS bytes unchanged', async () => {
  let requested = '';
  const socks = net.createServer(socket => {
    let state = 0, input = Buffer.alloc(0);
    socket.on('data', chunk => {
      input = Buffer.concat([input, chunk]);
      if (state === 0 && input.length >= 3) {
        assert.deepEqual(input.subarray(0,3), Buffer.from([5,1,0]));
        input = input.subarray(3); state = 1; socket.write(Buffer.from([5,0]));
      }
      if (state === 1 && input.length >= 5 && input.length >= 7+input[4]) {
        const size = input[4];
        assert.deepEqual(input.subarray(0,4), Buffer.from([5,1,0,3]));
        requested = input.subarray(5,5+size).toString('ascii');
        assert.equal(input.readUInt16BE(5+size),443);
        input = input.subarray(7+size); state = 2;
        socket.write(Buffer.from([5,0,0,1,127,0,0,1,0,1]));
      }
      if (state === 2 && input.length) { socket.write(input); input = Buffer.alloc(0); }
    });
  });
  const port = await listen(socks);
  try {
    await assert.rejects(dialWorkerThroughSocks('other.example',443,port));
    const conn = await dialWorkerThroughSocks(host,443,port);
    const received = once(conn,'data');
    conn.write(Buffer.from([22,3,3,0,255]));
    assert.deepEqual((await received)[0],Buffer.from([22,3,3,0,255]));
    assert.equal(requested,host);
    conn.destroy();
    const api = await dialWorkerThroughSocks('api.cloudflare.com',443,port,true);
    assert.equal(requested,'api.cloudflare.com');
    api.destroy();
  } finally { await close(socks); }
});

test('Cloudflare API access is explicit and still rejects other hosts', async () => {
  const target = net.createServer(socket => socket.on('data', data => socket.write(data)));
  const targetPort = await listen(target), calls = [];
  const dial = (name,port) => { calls.push([name,port]); return connect(targetPort); };
  const denied = createWorkerProxy(host, dial), enabled = createWorkerProxy(host, dial, {allowCloudflareApi:true});
  const deniedPort = await listen(denied), enabledPort = await listen(enabled);
  async function request(port,authority) {
    const socket=connect(port);socket.write(`CONNECT ${authority}:443 HTTP/1.1\r\nHost: ${authority}:443\r\n\r\n`);
    const [data]=await once(socket,'data');socket.destroy();return data.toString();
  }
  try {
    assert.match(await request(deniedPort,'api.cloudflare.com'),/^HTTP\/1\.1 403 /);
    assert.match(await request(enabledPort,'example.com'),/^HTTP\/1\.1 403 /);
    assert.match(await request(enabledPort,'api.cloudflare.com'),/^HTTP\/1\.1 200 /);
    assert.deepEqual(calls,[['api.cloudflare.com',443]]);
    await assert.rejects(dialWorkerThroughSocks('api.cloudflare.com',443,1));
  } finally { await close(denied);await close(enabled);await close(target); }
});
