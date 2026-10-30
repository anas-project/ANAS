// TEST_CASES: MAILMOD-T-013
// Real API contract only. The Cloudflare management key stays on this host.
import assert from 'node:assert/strict';
import { readFile, writeFile, stat, lstat, mkdir, unlink } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { randomBytes, randomUUID, createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';

const credentialFile = process.env.MAIL_CLOUDFLARE_TEST_CREDENTIAL_FILE;
const report = process.env.MAIL_CLOUDFLARE_TEST_REPORT_DIR;
const binary = process.env.MAIL_CLOUDFLARE_RULE_TEST_BINARY;
assert.ok(credentialFile && report && binary, 'Explicit private input/report/binary required');
assert.equal((await lstat(credentialFile)).isSymbolicLink(), false);
assert.equal((await stat(credentialFile)).mode & 0o077, 0);
const credential = JSON.parse(await readFile(credentialFile, 'utf8'));
assert.equal(credential.account_id, 'aa7aa9753a4fffabb1d6536bcf62fb95');
let zone = '0d84033826bc3cd0c283efdb57a90f7a', domain = 'mail-poc.bengbu.cc.cd';
// Worker setup and independent cleanup keep using the local management key.
// Only the separately authorized, zone-scoped routing key reaches Go.
let routingToken = credential.token;
const routingFile = process.env.MAIL_CLOUDFLARE_ROUTING_CREDENTIAL_FILE;
if (routingFile) {
  assert.equal((await lstat(routingFile)).isSymbolicLink(), false);
  assert.equal((await stat(routingFile)).mode & 0o077, 0);
  const routing = JSON.parse(await readFile(routingFile, 'utf8'));
  const domains = {'0d84033826bc3cd0c283efdb57a90f7a':'mail-poc.bengbu.cc.cd','cb14d4de75d3286765c7864476f9f4fb':'hlong.wang'};
  assert.ok(domains[routing.zone_id] && routing.allowed_domain === domains[routing.zone_id]);
  zone = routing.zone_id; domain = routing.allowed_domain;
  assert.ok(typeof routing.token === 'string' && routing.token.length >= 32);
  routingToken = routing.token;
}
await mkdir(report, { mode: 0o700 }); // Fresh evidence directory; never reuse a run.
assert.equal((await stat(report)).mode & 0o077, 0);
const suffix = randomBytes(6).toString('hex');
const worker = `anas-mail-rule-live-${suffix}`;
const address = `rule-live-${suffix}@${domain}`;
const source = randomUUID(), generation = randomUUID();
const hash = value => createHash('sha256').update(value).digest('hex');
const ruleName = `ANAS Mail ${source}/${generation}/${hash(address)}`;
const state = { worker, address, source, generation, separate_routing_credential: !!routingFile, started: new Date().toISOString(), cleanup_errors: [] };
const keyFile = join(resolve(report), 'private-binding.json');
const save = () => writeFile(join(report, 'result.json'), JSON.stringify(state, null, 2), { mode: 0o600 });
async function api(path, method = 'GET', body) {
  const response = await fetch('https://api.cloudflare.com/client/v4' + path, {
    method,
    headers: { Authorization: 'Bearer ' + (path.startsWith('/zones/') ? routingToken : credential.token),
      ...(body && !(body instanceof FormData) ? { 'Content-Type': 'application/json' } : {}) },
    body: body instanceof FormData ? body : body ? JSON.stringify(body) : undefined,
    redirect: 'error', signal: AbortSignal.timeout(45000),
  });
  const value = await response.json();
  if (!response.ok || !value.success) throw Error(`Provider ${method} failed: ${response.status}, codes ${(value.errors || []).map(v => v.code).join(',')}`);
  return value.result;
}
const base = `/zones/${zone}/email/routing/rules`;
async function rules() {
  const rows = [];
  for (let page = 1; page <= 100; page++) {
    const response = await fetch(`https://api.cloudflare.com/client/v4${base}?per_page=50&page=${page}`, {
      headers: { Authorization: 'Bearer ' + routingToken }, redirect: 'error', signal: AbortSignal.timeout(45000),
    });
    const value = await response.json();
    assert.ok(response.ok && value.success && Array.isArray(value.result), 'Independent rule list unavailable');
    assert.equal(value.result_info.page, page);
    assert.equal(value.result_info.per_page, 50);
    assert.ok(Number.isInteger(value.result_info.total_count) && value.result_info.total_count >= 0 && value.result_info.total_count <= 5000);
    rows.push(...value.result);
    if (page >= Math.max(1, Math.ceil(value.result_info.total_count / 50))) {
      assert.equal(rows.length, value.result_info.total_count);
      return rows;
    }
  }
  throw Error('Independent rule list incomplete');
}
const own = r => r.name === ruleName && r.source === 'api' && r.priority === 0 && r.enabled === true &&
  r.actions?.length === 1 && r.actions[0].type === 'worker' && r.actions[0].value?.length === 1 &&
  r.actions[0].value[0] === worker && r.matchers?.length === 1 && r.matchers[0].type === 'literal' &&
  r.matchers[0].field === 'to' && r.matchers[0].value === address;
const digest = rows => hash(JSON.stringify(rows.filter(r => !own(r)).map(r =>
  [r.id || r.tag, r.name, r.source, r.priority, r.enabled, r.actions, r.matchers]).sort((a,b) => a[0].localeCompare(b[0]))));
let before, uploaded = false;
await save();
try {
  before = digest(await rules());
  const scripts = await api(`/accounts/${credential.account_id}/workers/scripts`);
  assert.ok(!scripts.some(v => v.id === worker), 'Unique Worker name already exists');
  const code = 'export default {async email(m){m.setReject("Isolated configuration test");},async fetch(){return new Response("Not found",{status:404});}};';
  const form = new FormData();
  form.set('metadata', new Blob([JSON.stringify({ main_module: 'worker.mjs', compatibility_date: '2026-07-01', tags: ['anas-mail-isolated-rule-test'] })], { type: 'application/json' }));
  form.set('worker.mjs', new Blob([code], { type: 'application/javascript+module' }), 'worker.mjs');
  await api(`/accounts/${credential.account_id}/workers/scripts/${worker}`, 'PUT', form);
  uploaded = state.worker_created = true;
  state.source_sha256 = {};
  for (const file of ['modules/mail/ingress/cloudflare_rules.go', 'modules/mail/ingress/cloudflare_live_test.go', 'test-env/scripts/mail-cloudflare-rules-e2e.mjs'])
    state.source_sha256[file] = hash(await readFile(file));
  state.binary_sha256 = hash(await readFile(binary));
  await writeFile(keyFile, JSON.stringify({ Source: source, Generation: generation, Zone: zone, Worker: worker, Address: address, Token: routingToken }), { mode: 0o600, flag: 'wx' });
  await save();
  const result = spawnSync(resolve(binary), ['-test.v', '-test.run', '^TestCloudflareRulesLiveIsolatedRoundTrip$', '-test.timeout', '180s'], {
    env: { ...process.env, MAIL_CLOUDFLARE_RULE_TEST_CONFIG: keyFile }, encoding: 'utf8', timeout: 200000,
  });
  await writeFile(join(report, 'go-test.log'), (result.stdout || '') + (result.stderr || ''), { mode: 0o600 });
  state.go_test_passed = result.status === 0 && !result.error;
  assert.equal(state.go_test_passed, true, 'Formal Go client test failed; private log preserved');
} catch (error) {
  state.failure = error.message;
} finally {
  await unlink(keyFile).catch(error => { if (error.code !== 'ENOENT') state.cleanup_errors.push('private binding removal failed'); });
  state.private_binding_removed = await stat(keyFile).then(() => false, error => error.code === 'ENOENT');
  try {
    for (const rule of (await rules()).filter(own)) {
      const id = rule.id || rule.tag;
      assert.match(id, /^[a-f0-9]{32}$/);
      assert.ok(own(await api(`${base}/${id}`)), 'Owned rule drifted; preserve for reconciliation');
      await api(`${base}/${id}`, 'DELETE');
    }
    const after = await rules();
    state.rules_removed = !after.some(own);
    state.unrelated_rules_unchanged = before === digest(after);
  } catch (error) { state.cleanup_errors.push(error.message); }
  if (uploaded && state.rules_removed) {
    try {
      await api(`/accounts/${credential.account_id}/workers/scripts/${worker}`, 'DELETE');
      state.worker_removed = true;
    } catch (error) { state.cleanup_errors.push(error.message); }
  }
  state.finished = new Date().toISOString();
  await save();
  console.log(JSON.stringify({ passed: state.go_test_passed === true, failure: state.failure || null,
    rules_removed: state.rules_removed, worker_removed: state.worker_removed,
    unrelated_rules_unchanged: state.unrelated_rules_unchanged,
    private_binding_removed: state.private_binding_removed, cleanup_errors: state.cleanup_errors }));
}
if (state.failure || state.cleanup_errors.length || !state.unrelated_rules_unchanged || !state.private_binding_removed) process.exitCode = 1;
