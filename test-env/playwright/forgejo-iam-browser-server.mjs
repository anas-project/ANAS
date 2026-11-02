// Workstation Chrome for the owned finance fixture, reached only through SSH.
// Usage: node forgejo-iam-browser-server.mjs <public-leaf.pem> <private-output.json>
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import { chromium } from 'playwright-core';

const [certificatePath, outputPath] = process.argv.slice(2);
assert.ok(certificatePath && outputPath, 'public certificate and private output paths are required');
const certificate = new crypto.X509Certificate(await fs.readFile(certificatePath));
assert.ok(certificate.checkHost('git.fj-iam.test') && certificate.checkHost('auth.fj-iam.test'));
const pin = crypto.createHash('sha256').update(certificate.publicKey.export({ type: 'spki', format: 'der' }))
  .digest('base64');
const server = await chromium.launchServer({ channel: 'chrome', headless: true,
  host: '127.0.0.1', port: 19998, args: [
    '--no-proxy-server', '--host-resolver-rules=MAP *.fj-iam.test 127.0.0.1',
    '--ignore-certificate-errors-spki-list=' + pin,
  ] });
try {
  const endpoint = server.wsEndpoint();
  await fs.writeFile(outputPath, JSON.stringify({ local_endpoint: endpoint,
    remote_endpoint: endpoint.replace(':19998/', ':19999/'),
    certificate_spki_sha256: pin, pid: process.pid }) + '\n', { mode: 0o600 });
  await fs.chmod(outputPath, 0o600);
  console.log('Dedicated Chrome ready; SSH loopback only; test certificate pinned.');
  await new Promise(resolve => {
    process.once('SIGTERM', resolve);
    process.once('SIGINT', resolve);
  });
} finally {
  await server.close();
}
