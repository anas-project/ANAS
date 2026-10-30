// TEST_CASES: MAILMOD-T-010
// Real SMTP -> production Worker/R2 -> formal Go source client on finance.
// No synthetic EmailEvent, fake delivery receipt or production enable action.
import assert from 'node:assert/strict';
import { readFile, writeFile, stat, lstat, mkdir, unlink, mkdtemp, rmdir } from 'node:fs/promises';
import { appendFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { randomBytes, randomUUID, createHash } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { createWorkerProxy } from '../fixtures/mail-cloudflare-native/worker-proxy.mjs';
const managementFile = process.env.MAIL_CLOUDFLARE_TEST_CREDENTIAL_FILE;
const routingFile = process.env.MAIL_CLOUDFLARE_ROUTING_CREDENTIAL_FILE;
const smtpFile = process.env.MAIL_CLOUDFLARE_SMTP_CREDENTIAL_FILE;
const binary = process.env.MAIL_CLOUDFLARE_SMTP_TEST_BINARY;
const report = process.env.MAIL_CLOUDFLARE_TEST_REPORT_DIR;
assert.ok(managementFile && routingFile && smtpFile && binary && report, 'Explicit private inputs/report/binary required');
for (const p of [managementFile, routingFile, smtpFile]) {
    assert.equal((await lstat(p)).isSymbolicLink(), false);
    assert.equal((await stat(p)).mode & 0o077, 0);
}
const management = JSON.parse(await readFile(managementFile, 'utf8'));
const routing = JSON.parse(await readFile(routingFile, 'utf8'));
const smtp = JSON.parse(await readFile(smtpFile, 'utf8'));
assert.ok(['resend', 'eusend'].includes(smtp.provider) && smtp.domain === 'mail-poc.hlong.wang');
const accountID = 'aa7aa9753a4fffabb1d6536bcf62fb95', zone = routing.zone_id;
const domains = {'0d84033826bc3cd0c283efdb57a90f7a':'mail-poc.bengbu.cc.cd','cb14d4de75d3286765c7864476f9f4fb':'hlong.wang'};
const domain = domains[zone];
assert.equal(management.account_id, accountID);
assert.ok(domain && routing.allowed_domain === domain);
await mkdir(report, { mode: 0o700 }); // Never mix evidence from separate runs.
const suffix = randomBytes(6).toString('hex'), name = 'anas-mail-smtp-live-' + suffix;
const source = randomUUID(), generation = randomUUID(), owner = randomUUID(), boxes = [randomUUID(), randomUUID()];
const reader = randomBytes(32).toString('hex'), control = randomBytes(32).toString('hex');
const routes = ['main', 'alias', 'extra'].map((kind, n) => ({ address: `smtp-${suffix}-${kind}@${domain}`, mailbox_id: boxes[n === 2 ? 1 : 0], owner_anchor: owner, claim_version: randomUUID() }));
const sshTarget = process.env.MAIL_CLOUDFLARE_SSH_TARGET || 'whl@finance.hlong.wang';
assert.ok(['whl@finance.hlong.wang','whl@ln.hlong.wang'].includes(sshTarget));
const root = sshTarget === 'whl@ln.hlong.wang' ? '/home/whl/anas-mail-go-test-ln-20261030' : '/home/whl/anas-mail-runtime-20261026';
const remote = root + '/cloud-smtp-' + suffix;
const binding = join(resolve(report), 'private-binding.json');
const state = { name, source, generation, routes, smtp_provider: smtp.provider, started: new Date().toISOString(), created: {}, cleaned: {}, cleanup_errors: [] };
const hash = v => createHash('sha256').update(v).digest('hex');
const save = () => writeFile(join(resolve(report), 'result.json'), JSON.stringify(state, null, 2), { mode: 0o600 });
const wait = ms => new Promise(r => setTimeout(r, ms));
async function api(path, method = 'GET', body) {
    const apiToken = path.startsWith('/zones/') ? routing.token : management.token;
    const r = await fetch('https://api.cloudflare.com/client/v4' + path, { method, headers: { Authorization: 'Bearer ' + apiToken, ...(body && !(body instanceof FormData) ? { 'Content-Type': 'application/json' } : {}) }, body: body instanceof FormData ? body : body ? JSON.stringify(body) : undefined, redirect: 'error', signal: AbortSignal.timeout(45000) });
    const j = await r.json();
    assert.ok(r.ok && j.success, 'Cloudflare ' + method + ' ' + r.status + ' codes ' + (j.errors || []).map(v => v.code).join(','));
    return j.result;
}
const account = '/accounts/' + accountID, ruleBase = '/zones/' + zone + '/email/routing/rules';
async function allRules() { const rows = []; for (let page = 1; page <= 100; page++) {
    const r = await fetch(`https://api.cloudflare.com/client/v4${ruleBase}?per_page=50&page=${page}`, { headers: { Authorization: 'Bearer ' + routing.token }, redirect: 'error', signal: AbortSignal.timeout(45000) });
    const j = await r.json();
    assert.ok(r.ok && j.success && Array.isArray(j.result));
    assert.equal(j.result_info.page, page);
    assert.equal(j.result_info.per_page, 50);
    assert.ok(Number.isInteger(j.result_info.total_count) && j.result_info.total_count <= 5000);
    rows.push(...j.result);
    if (page >= Math.max(1, Math.ceil(j.result_info.total_count / 50))) {
        assert.equal(rows.length, j.result_info.total_count);
        return rows;
    }
} throw Error('Incomplete rules'); }
const prefix = `ANAS Mail ${source}/${generation}/`;
const own = r => r.name?.startsWith(prefix) && r.source === 'api' && r.priority === 0 && r.enabled === true && r.actions?.length === 1 && r.actions[0].type === 'worker' && r.actions[0].value?.length === 1 && r.actions[0].value[0] === name && r.matchers?.length === 1 && r.matchers[0].type === 'literal' && r.matchers[0].field === 'to' && routes.some(v => v.address === r.matchers[0].value) && r.name === prefix + hash(r.matchers[0].value);
const digest = rows => hash(JSON.stringify(rows.filter(r => !own(r)).map(r => [r.id || r.tag, r.name, r.source, r.priority, r.enabled, r.actions, r.matchers]).sort((a, b) => a[0].localeCompare(b[0]))));
function local(command, args, timeout = 60000) {
    const result = spawnSync(command, args, { encoding: 'utf8', timeout });
    if (result.status !== 0 || result.error) {
        // These calls contain fixed SSH/SCP paths, never command-line keys.
        appendFileSync(join(report, 'local-command-error.log'),
            (result.stdout || '') + (result.stderr || ''), { mode: 0o600 });
        throw Error(command + ' failed; private command log preserved');
    }
    return result.stdout;
}
// OpenSSH appends a temporary suffix while creating its socket. Keep this
// owned private path short even when the evidence directory is deeply nested.
const controlDirectory = await mkdtemp('/tmp/anas-mail-smtp-');
const controlSocket = join(controlDirectory, 'ssh.sock');
const sshOptions = [
    '-o', 'Port=' + (sshTarget === 'whl@ln.hlong.wang' ? '2200' : '22'),
    '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=15',
    '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=6',
    '-o', 'ControlMaster=auto', '-o', 'ControlPersist=60',
    '-o', 'ControlPath=' + controlSocket,
];
const sshArgs = [...sshOptions, sshTarget];
const originalSender = sshTarget === 'whl@ln.hlong.wang';
const senderRemote = '/home/whl/anas-mail-runtime-20261026/cloud-smtp-send-' + suffix;
const senderOptions = ['-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','ConnectTimeout=15','-o','ControlMaster=no','-o','ControlPath=none'];
let senderCreated = false;
let base, before, remoteCreated = false, proxyServer, proxySSH;
async function upload(code, notification, cleanup = false) { const form = new FormData(); form.set('metadata', new Blob([JSON.stringify({ main_module: 'worker.mjs', compatibility_date: '2026-07-01', tags: ['anas-mail-isolated-smtp-test'], bindings: [{ name: 'MAIL', type: 'r2_bucket', bucket_name: name }, ...Object.entries({ SOURCE_ID: source, GENERATION: generation, DOMAINS: JSON.stringify([domain]), CAPACITY_BYTES: String(2 * 1024 ** 3), DELIVERED_DAYS: '7' }).map(([name, text]) => ({ name, type: 'plain_text', text })), { name: 'READER_TOKEN', type: 'secret_text', text: reader }, { name: 'CONTROL_TOKEN', type: 'secret_text', text: control }] })], { type: 'application/json' })); form.set('worker.mjs', new Blob([code], { type: 'application/javascript+module' }), 'worker.mjs'); if (!cleanup)
    form.set('notification.mjs', new Blob([notification], { type: 'application/javascript+module' }), 'notification.mjs'); return api(account + '/workers/scripts/' + name, 'PUT', form); }
await save();
try {
    before = digest(await allRules());
    const child = await api('/zones/' + zone + '/dns_records?type=MX&name='+encodeURIComponent(domain)+'&per_page=100');
    assert.equal(child.length, 3, 'Enabled child MX required');
    assert.ok(!(await api(account + '/workers/scripts')).some(v => v.id === name));
    assert.ok(!(await api(account + '/r2/buckets')).buckets.some(v => v.name === name));
    const sub = await api(account + '/workers/subdomain');
    base = `https://${name}.${sub.subdomain}.workers.dev`;
    await api(account + '/r2/buckets', 'POST', { name, storageClass: 'Standard' });
    state.created.bucket = true;
    await save();
    assert.equal((await api(account + '/r2/buckets/' + name + '/domains/managed')).enabled, false);
    const code = await readFile('modules/mail/cloudflare/worker.mjs', 'utf8'), notification = await readFile('modules/mail/cloudflare/notification.mjs', 'utf8');
    state.source_sha256 = {};
    for (const p of ['modules/mail/cloudflare/worker.mjs', 'modules/mail/cloudflare/notification.mjs', 'modules/mail/ingress/cloudflare_smtp_live_test.go', 'modules/mail/ingress/source.go', 'modules/mail/ingress/routes.go', 'modules/mail/ingress/cloudflare_rules.go', 'test-env/scripts/mail-cloudflare-smtp-e2e.mjs', 'test-env/scripts/mail-cloudflare-smtp-send.py'])
        state.source_sha256[p] = hash(await readFile(p));
    state.binary_sha256 = hash(await readFile(binary));
    const version = await upload(code, notification);
    state.created.worker = true;
    state.worker_version = version.deployment_id || version.version_id || version.etag;
    await save();
    await api(account + '/workers/scripts/' + name + '/subdomain', 'POST', { enabled: true, previews_enabled: false });
    let ready = false;
    const readinessDeadline = Date.now() + 120000;
    while (Date.now() < readinessDeadline) {
        try {
            const r = await fetch(base + '/v1/status', { headers: { Authorization: 'Bearer ' + reader }, redirect: 'error', signal: AbortSignal.timeout(Math.min(10000, Math.max(1, readinessDeadline - Date.now()))) });
            if (r.status === 200) {
                const s = await r.json();
                if (s.source_id === source && s.generation === generation) {
                    ready = true;
                    break;
                }
            }
        }
        catch { }
        await wait(Math.min(2000, Math.max(0, readinessDeadline - Date.now())));
    }
    assert.ok(ready, 'Production Worker readiness');
    let proxy = process.env.MAIL_CLOUDFLARE_SMTP_PROXY || '';
    if (sshTarget === 'whl@ln.hlong.wang' && !proxy) {
        proxyServer = createWorkerProxy(new URL(base).hostname, undefined, {allowCloudflareApi:true});
        await new Promise((resolve,reject)=>{proxyServer.once('error',reject);proxyServer.listen(0,'127.0.0.1',resolve)});
        const localPort = proxyServer.address().port;
        const remotePort = Number(local('ssh',[...sshArgs,`python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'`]).trim());
        assert.ok(Number.isInteger(remotePort) && remotePort > 0 && remotePort < 65536);
        proxySSH = spawn('ssh',[...sshOptions,'-o','ExitOnForwardFailure=yes','-N','-R',`127.0.0.1:${remotePort}:127.0.0.1:${localPort}`,sshTarget],{stdio:['ignore','ignore','pipe']});
        proxySSH.stderr.resume();
        proxy = `http://127.0.0.1:${remotePort}`;
        state.worker_tls_tunnel = true;
    }
    if (proxy)
        assert.ok(/^(?:socks5h?|http):\/\/127\.0\.0\.1:[0-9]{1,5}$/.test(proxy), 'Explicit loopback test proxy required');
    state.explicit_test_proxy = !!proxy;
    const visibleTo = process.env.MAIL_CLOUDFLARE_SMTP_VISIBLE_TO || 'foreign';
    assert.ok(['foreign', 'main'].includes(visibleTo), 'Explicit visible recipient mode required');
    state.visible_to_mode = visibleTo;
    const cfg = {
        Source: {
            ID: source, Generation: generation, Origin: base,
            Token: reader, ControlToken: control,
            Domains: [domain], IPv4Only: true, Proxy: proxy,
        },
        Zone: zone, Worker: name, RoutingToken: routing.token,
        Operation: randomUUID(), Routes: routes,
        Subject: 'ANAS cloud SMTP ' + source,
        VisibleTo: visibleTo === 'main' ? routes[0].address : 'forged@foreign.example',
    };
    await writeFile(binding, JSON.stringify(cfg), { mode: 0o600, flag: 'wx' });
    local('ssh', [...sshArgs, `umask 077; mkdir -- ${remote}`]);
    remoteCreated = true;
    local('scp', ['-q', ...sshOptions, binding, `${sshTarget}:${remote}/binding.json`]);
    local('scp', ['-q', ...sshOptions, resolve(binary), `${sshTarget}:${remote}/ingress.test`]);
    local('scp', ['-q', ...sshOptions, 'test-env/scripts/mail-cloudflare-smtp-send.py', `${sshTarget}:${remote}/smtp-send.py`]);
    if (originalSender) {
        local('ssh', [...senderOptions,'whl@finance.hlong.wang',`umask 077; mkdir ${senderRemote}`]);
        senderCreated = true;
        const publicBinding = join(resolve(report),'sender-public.json');
        await writeFile(publicBinding,JSON.stringify({Source:{ID:source},Subject:cfg.Subject,VisibleTo:cfg.VisibleTo,Routes:routes.map(r=>({address:r.address}))}),{mode:0o600,flag:'wx'});
        local('scp',['-q',...senderOptions,publicBinding,`whl@finance.hlong.wang:${senderRemote}/binding.json`]);
        local('scp',['-q',...senderOptions,'test-env/scripts/mail-cloudflare-smtp-send.py',`whl@finance.hlong.wang:${senderRemote}/smtp-send.py`]);
        local('scp',['-q',...senderOptions,'test-env/scripts/mail-cloudflare-rcpt-check.py',`whl@finance.hlong.wang:${senderRemote}/rcpt-check.py`]);
    }
    local('ssh', [...sshArgs, `chmod 600 ${remote}/binding.json; chmod 700 ${remote}/ingress.test`]);
    if (!originalSender) local('scp',['-q',...sshOptions,'test-env/scripts/mail-cloudflare-rcpt-check.py',`${sshTarget}:${remote}/rcpt-check.py`]);
    const goLog = join(report, 'go-test.log');
    await writeFile(goLog, '', { mode: 0o600, flag: 'wx' });
    let output = '', triggered = false, submission, logFailed = false;
    const childProcess = spawn('ssh', [...sshArgs, `env MAIL_CLOUDFLARE_SMTP_CONFIG=${remote}/binding.json ${remote}/ingress.test -test.v -test.run '^TestCloudflareSMTPSourceLive$' -test.timeout 390s`], { stdio: ['ignore', 'pipe', 'pipe'] });
    const submitSMTP = async () => {
        await wait(5000);
        let accepted = false;
        const probes = [];
        const deadline = Date.now() + 90000;
        while (!accepted && Date.now() < deadline) {
            for (const mx of ['route1.mx.cloudflare.net','route2.mx.cloudflare.net','route3.mx.cloudflare.net']) {
                const root = originalSender ? senderRemote : remote;
                const command = `python3 ${root}/rcpt-check.py ${root}/binding.json ${mx}`;
                const args = originalSender ? [...senderOptions,'whl@finance.hlong.wang',command] : [...sshArgs,command];
                const probe = spawnSync('ssh',args,{encoding:'utf8',timeout:25000});
                let result;
                try { result = JSON.parse(probe.stdout); } catch { result = {phase:'unavailable'}; }
                probes.push({mx,...result});
                if (probe.status === 0 && result.phase === 'complete' && JSON.stringify(result.codes) === '[250,250,250]') { accepted = true; break; }
                if (Date.now() >= deadline) break;
            }
            if (!accepted) await wait(Math.min(5000,Math.max(0,deadline-Date.now())));
        }
        state.recipient_probes = probes;
        state.recipient_probe_accepted = accepted;
        await save();
        assert.ok(accepted,'Public recipient preflight did not accept all targets; no DATA submitted');
        const senderArgs = originalSender ? [...senderOptions,'whl@finance.hlong.wang',`python3 ${senderRemote}/smtp-send.py /home/whl/anas-mail-runtime-20261026/${smtp.provider}-sending-key.json ${senderRemote}/binding.json`] : [...sshArgs,`python3 ${remote}/smtp-send.py ${root}/${smtp.provider}-sending-key.json ${remote}/binding.json`];
        const result = spawnSync('ssh', senderArgs, { encoding: 'utf8', timeout: 150000 });
        await writeFile(join(report, 'smtp-submit.log'),
            (result.stdout || '') + (result.stderr || ''), { mode: 0o600 });
        assert.equal(result.status, 0, 'SMTP submission failed; private log preserved');
        state.smtp_submission = JSON.parse(result.stdout);
        state.smtp_submit_host = originalSender ? 'whl@finance.hlong.wang' : sshTarget;
        await save();
        console.log('Real SMTP submitted to three controlled targets');
    };
    const collect = chunk => {
        output += chunk.toString();
        try { appendFileSync(goLog, chunk); }
        catch {
            logFailed = true;
            childProcess.kill('SIGTERM');
            return;
        }
        if (!triggered && output.includes('ANAS_CLOUD_SMTP_ROUTES_READY')) {
            triggered = true;
            submission = submitSMTP();
            submission.catch(() => childProcess.kill('SIGTERM'));
        }
    };
    childProcess.stdout.on('data', collect);
    childProcess.stderr.on('data', collect);
    const status = await new Promise((resolve, reject) => {
        const timer = setTimeout(() => {
            childProcess.kill('SIGTERM');
            reject(Error('Remote source test timeout'));
        }, 420000);
        childProcess.on('error', error => { clearTimeout(timer); reject(error); });
        childProcess.on('close', code => { clearTimeout(timer); resolve(code); });
    });
    await writeFile(join(report, 'go-test.log'), output, { mode: 0o600 });
    assert.equal(logFailed, false, 'Private Go log write failed');
    if (submission)
        await submission;
    assert.ok(triggered && status === 0, 'Formal Go real SMTP/source test failed; private log preserved');
    state.go_test_passed = true;
}
catch (error) {
    state.failure = error.message;
}
finally {
    if (proxySSH && proxySSH.exitCode === null) proxySSH.kill('SIGTERM');
    if (proxyServer) { state.tunnel_stats = {...proxyServer.stats}; proxyServer.close(); }
    if (senderCreated) {
        try { local('ssh',[...senderOptions,'whl@finance.hlong.wang',`rm -f ${senderRemote}/binding.json ${senderRemote}/smtp-send.py ${senderRemote}/rcpt-check.py && rmdir ${senderRemote}`]); state.cleaned.sender_inputs = true; }
        catch (e) { state.cleanup_errors.push(e.message); }
    }
    await unlink(binding).catch(e => { if (e.code !== 'ENOENT')
        state.cleanup_errors.push('Local binding removal failed'); });
    if (remoteCreated) {
        try {
            local('ssh', [...sshArgs, `rm -f -- ${remote}/binding.json`]);
            state.cleaned.remote_binding = true;
        }
        catch (e) {
            state.cleanup_errors.push(e.message);
        }
    }
    try {
        const rows = await allRules();
        assert.ok(!rows.some(r => r.name?.startsWith(prefix) && !own(r)), 'Owned rule drift');
        for (const row of rows.filter(own)) {
            const id = row.id || row.tag;
            assert.match(id, /^[a-f0-9]{32}$/);
            assert.ok(own(await api(ruleBase + '/' + id)));
            await api(ruleBase + '/' + id, 'DELETE');
        }
        const after = await allRules();
        assert.ok(!after.some(own));
        state.cleaned.rules = true;
        state.unrelated_rules_unchanged = before === digest(after);
    }
    catch (e) {
        state.cleanup_errors.push(e.message);
    }
    // Only after recipient removal, replace this disposable Worker with a
    // bounded bucket cleanup handler. The SMTP test used only production code.
    if (state.created.worker && state.cleaned.rules) {
        try {
            const code = 'export default {async email(m){m.setReject("Isolated test completed");},async fetch(r,e){if(r.method!=="POST"||r.headers.get("Authorization")!=="Bearer "+e.CONTROL_TOKEN)return new Response("Denied",{status:403});const p=await e.MAIL.list({limit:1000});if(p.objects.length)await e.MAIL.delete(p.objects.map(o=>o.key));return Response.json({deleted:p.objects.length});}};';
            await upload(code, '', true);
            let done = false;
            for (let n = 0; n < 60; n++) {
                try {
                    const r = await fetch(base, { method: 'POST', headers: { Authorization: 'Bearer ' + control }, redirect: 'error', signal: AbortSignal.timeout(15000) });
                    if (r.status === 200 && (await r.json()).deleted === 0) {
                        done = true;
                        break;
                    }
                }
                catch { }
                await wait(2000);
            }
            assert.ok(done, 'Disposable R2 object cleanup incomplete');
            state.cleaned.objects = true;
        }
        catch (e) {
            state.cleanup_errors.push(e.message);
        }
    }
    if (state.created.worker && state.cleaned.objects) {
        try {
            await api(account + '/workers/scripts/' + name, 'DELETE');
            state.cleaned.worker = true;
        }
        catch (e) {
            state.cleanup_errors.push(e.message);
        }
    }
    if (state.created.bucket && (!state.created.worker || state.cleaned.worker)) {
        try {
            await api(account + '/r2/buckets/' + name, 'DELETE');
            state.cleaned.bucket = true;
        }
        catch (e) {
            state.cleanup_errors.push(e.message);
        }
    }
    try {
        const socket = await lstat(controlSocket).catch(error => {
            if (error.code !== 'ENOENT') throw error;
        });
        if (socket) {
            assert.ok(socket.isSocket(), 'Unexpected SSH control path');
            local('ssh', [...sshOptions, '-O', 'exit', sshTarget]);
        }
        await rmdir(controlDirectory);
        state.cleaned.ssh_master = true;
    } catch (error) { state.cleanup_errors.push(error.message); }
    state.finished = new Date().toISOString();
    await save();
    console.log(JSON.stringify({ passed: state.go_test_passed === true, failure: state.failure || null, cleaned: state.cleaned, unrelated_rules_unchanged: state.unrelated_rules_unchanged, cleanup_errors: state.cleanup_errors }));
}
if (state.failure || state.cleanup_errors.length || !state.unrelated_rules_unchanged)
    process.exitCode = 1;
