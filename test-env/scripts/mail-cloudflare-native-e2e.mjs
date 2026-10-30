// TEST_CASES: MAILMOD-T-010
// Real SMTP -> production Worker/R2 -> isolated finance Go/PG/DMS/Roundcube.
// No synthetic EmailEvent, fake delivery receipt or production enable action.
import assert from 'node:assert/strict';
import { readFile, writeFile, stat, lstat, mkdir, unlink, mkdtemp, rmdir } from 'node:fs/promises';
import { appendFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { randomBytes, randomUUID, createHash } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import net from 'node:net';
import https from 'node:https';
import { createWorkerProxy, dialWorkerThroughSocks } from '../fixtures/mail-cloudflare-native/worker-proxy.mjs';
import { cloudflareFetch } from '../fixtures/mail-cloudflare-native/socks-fetch.mjs';
const managementFile = process.env.MAIL_CLOUDFLARE_TEST_CREDENTIAL_FILE;
const routingFile = process.env.MAIL_CLOUDFLARE_ROUTING_CREDENTIAL_FILE;
const smtpFile = process.env.MAIL_CLOUDFLARE_SMTP_CREDENTIAL_FILE;
const binary = process.env.MAIL_CLOUDFLARE_NATIVE_BUNDLE;
const bundleHash = process.env.MAIL_CLOUDFLARE_NATIVE_BUNDLE_SHA256;
const proxyPort = Number(process.env.MAIL_CLOUDFLARE_NATIVE_PROXY_PORT);
const rcptSocks = process.env.MAIL_CLOUDFLARE_NATIVE_RCPT_SOCKS_PORT;
const rcptSocksPort = rcptSocks === undefined ? null : Number(rcptSocks);
const report = process.env.MAIL_CLOUDFLARE_TEST_REPORT_DIR;
assert.ok(managementFile && routingFile && smtpFile && binary && /^[a-f0-9]{64}$/.test(bundleHash || '') && report && Number.isInteger(proxyPort) && proxyPort > 0 && proxyPort < 65536, 'Explicit private inputs/report/bundle/loopback proxy required');
assert.ok(rcptSocksPort === null || (/^[0-9]{1,5}$/.test(rcptSocks) && Number.isInteger(rcptSocksPort) && rcptSocksPort > 0 && rcptSocksPort < 65536), 'Invalid loopback SMTP probe proxy');
const visibleTo = process.env.MAIL_CLOUDFLARE_NATIVE_VISIBLE_TO || 'main';
const externalTo = process.env.MAIL_CLOUDFLARE_NATIVE_EXTERNAL_TO || '';
const sendMode = process.env.MAIL_CLOUDFLARE_NATIVE_SEND_MODE || 'provider';
assert.ok(['main','foreign','controlled-external'].includes(visibleTo), 'Explicit visible recipient mode required');
assert.ok(visibleTo !== 'controlled-external' ? !externalTo : /^[a-z0-9][a-z0-9._-]{0,63}@126\.com$/.test(externalTo), 'Explicit controlled 126 address required only for the external mode');
assert.ok(sendMode === 'provider' || sendMode === 'direct-mx' && visibleTo === 'controlled-external' && rcptSocksPort !== null, 'Invalid controlled sender mode');
const fetch = (target, options) => cloudflareFetch(proxyPort, target, options);
assert.equal(createHash('sha256').update(await readFile(binary)).digest('hex'), bundleHash, 'Native fixture bundle changed');
for (const p of [managementFile, routingFile, smtpFile]) {
    assert.equal((await lstat(p)).isSymbolicLink(), false);
    assert.equal((await stat(p)).mode & 0o077, 0);
}
const management = JSON.parse(await readFile(managementFile, 'utf8'));
const routing = JSON.parse(await readFile(routingFile, 'utf8'));
const smtp = JSON.parse(await readFile(smtpFile, 'utf8'));
assert.ok(['resend', 'eusend'].includes(smtp.provider) && smtp.domain === 'mail-poc.hlong.wang');
const accountID = 'aa7aa9753a4fffabb1d6536bcf62fb95', zone = '0d84033826bc3cd0c283efdb57a90f7a';
assert.equal(management.account_id, accountID);
assert.equal(routing.zone_id, zone);
assert.equal(routing.allowed_domain, 'mail-poc.bengbu.cc.cd');
await mkdir(report, { mode: 0o700 }); // Never mix evidence from separate runs.
const suffix = randomBytes(6).toString('hex'), name = 'anas-mail-smtp-live-' + suffix;
const source = randomUUID(), generation = randomUUID(), owner = randomUUID(), boxes = [randomUUID(), randomUUID()];
const reader = randomBytes(32).toString('hex'), control = randomBytes(32).toString('hex');
const withNotification = process.env.MAIL_CLOUDFLARE_NATIVE_NOTIFICATION === '1';
const withProductRules = process.env.MAIL_CLOUDFLARE_NATIVE_PUBLIC_SYNC === '1';
const withOffline = process.env.MAIL_CLOUDFLARE_NATIVE_OFFLINE === '1';
const withScheduledRetry = process.env.MAIL_CLOUDFLARE_NATIVE_SCHEDULED_RETRY === '1';
const skipNativeIntake = process.env.MAIL_CLOUDFLARE_NATIVE_SKIP_INTAKE === '1';
assert.ok(!withOffline || withNotification, 'Offline test requires a real notification binding');
assert.ok(!withScheduledRetry || withOffline, 'Scheduled retry requires the offline fixture');
const notify = withNotification ? randomBytes(32).toString('hex') : '';
const routes = ['main', 'alias', 'extra'].map((kind, n) => ({ address: `smtp-${suffix}-${kind}@mail-poc.bengbu.cc.cd`, mailbox_id: boxes[n === 2 ? 1 : 0], owner_anchor: owner, claim_version: randomUUID() }));
const root = '/home/whl/anas-mail-runtime-20261026';
const remote = '/home/whl/anas-vps-native-' + suffix;
const binding = join(resolve(report), 'private-binding.json');
const state = { name, source, generation, routes, smtp_provider: sendMode === 'provider' ? smtp.provider : 'direct-mx', native_intake_executed: skipNativeIntake ? false : null, started: new Date().toISOString(), issued: {}, created: {}, cleaned: {}, cleanup_errors: [] };
const hash = v => createHash('sha256').update(v).digest('hex');
const save = () => writeFile(join(resolve(report), 'result.json'), JSON.stringify(state, null, 2), { mode: 0o600 });
const wait = ms => new Promise(r => setTimeout(r, ms));
async function bounded(promise, ms, fallback) {
    let timer;
    try { return await Promise.race([promise,new Promise(resolve => { timer=setTimeout(()=>resolve(fallback),ms); })]); }
    finally { clearTimeout(timer); }
}
async function api(path, method = 'GET', body) {
    const r = await fetch('https://api.cloudflare.com/client/v4' + path, { method, headers: { Authorization: 'Bearer ' + management.token, ...(body && !(body instanceof FormData) ? { 'Content-Type': 'application/json' } : {}) }, body: body instanceof FormData ? body : body ? JSON.stringify(body) : undefined, redirect: 'error', signal: AbortSignal.timeout(45000) });
    const j = await r.json();
    assert.ok(r.ok && j.success, 'Cloudflare ' + method + ' ' + r.status + ' codes ' + (j.errors || []).map(v => v.code).join(','));
    return j.result;
}
const account = '/accounts/' + accountID, ruleBase = '/zones/' + zone + '/email/routing/rules';
async function allRules() { const rows = []; for (let page = 1; page <= 100; page++) {
    const r = await fetch(`https://api.cloudflare.com/client/v4${ruleBase}?per_page=50&page=${page}`, { headers: { Authorization: 'Bearer ' + management.token }, redirect: 'error', signal: AbortSignal.timeout(45000) });
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
    '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=15',
    '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=6',
    '-o', 'ControlMaster=auto', '-o', 'ControlPersist=60',
    '-o', 'ControlPath=' + controlSocket,
];
const sshArgs = [...sshOptions, 'whl@finance.hlong.wang'];
let base, before, remoteCreated = false, proxyServer, proxySSH, nativeChild, notifySSH, cloudTunnel, notifyOrigin, notifyCert, notifyKey, notifyPort;
function nextTunnelLine(child) { return new Promise((resolve,reject) => {
    let pending=''; const timer=setTimeout(()=>finish(Error('notification tunnel readiness timeout')),120000);
    const receive=chunk=>{pending+=chunk.toString(); if(pending.length>65536) return finish(Error('notification tunnel output exceeded limit')); const found=pending.match(/https:\/\/[a-z0-9-]+\.trycloudflare\.com/); if(found) finish(null,found[0]);};
    const ended=()=>finish(Error('notification tunnel stopped before ready'));
    function finish(err,value){clearTimeout(timer);child.stdout?.off('data',receive);child.stderr?.off('data',receive);child.off('exit',ended);err?reject(err):resolve(value);}
    child.stdout.on('data',receive);child.stderr.on('data',receive);child.once('exit',ended);
}); }
async function freePort() { return new Promise((resolve,reject)=>{const s=net.createServer();s.once('error',reject);s.listen(0,'127.0.0.1',()=>{const port=s.address().port;s.close(()=>resolve(port));});}); }
function health(port,ca) { return new Promise((resolve,reject)=>{const r=https.get({hostname:'127.0.0.1',port,path:'/__.health',ca,timeout:3000},response=>{response.resume();resolve(response.statusCode)});r.on('error',reject);r.on('timeout',()=>r.destroy(Error('health timeout')));}); }
async function startNotificationTunnel() {
    notifySSH=spawn('ssh',['-o','BatchMode=yes','-o','ControlMaster=no','-o','ConnectTimeout=15','-o','ExitOnForwardFailure=yes','-o','ServerAliveInterval=15','-N','-L',`127.0.0.1:${notifyPort}:${remote}/bridge/notify.sock`,'whl@finance.hlong.wang'],{stdio:['ignore','ignore','pipe']});
    notifySSH.stderr.on('data',chunk=>appendFileSync(join(report,'notify-ssh.log'),chunk,{mode:0o600}));
    let originReady=false;
    for(let i=0;i<30 && notifySSH.exitCode===null;i++){try{if(await health(notifyPort,notifyCert)===204){originReady=true;break;}}catch{}await wait(1000);}
    assert.ok(originReady,'Isolated HTTPS notification receiver unavailable');
}
async function upload(code, notification, cleanup = false) { const form = new FormData(); form.set('metadata', new Blob([JSON.stringify({ main_module: 'worker.mjs', compatibility_date: '2026-07-01', compatibility_flags:['global_fetch_strictly_public'], tags: ['anas-mail-isolated-smtp-test'], bindings: [{ name: 'MAIL', type: 'r2_bucket', bucket_name: name }, ...Object.entries({ SOURCE_ID: source, GENERATION: generation, DOMAINS: JSON.stringify(['mail-poc.bengbu.cc.cd']), CAPACITY_BYTES: String(2 * 1024 ** 3), DELIVERED_DAYS: '7' }).map(([name, text]) => ({ name, type: 'plain_text', text })), { name: 'READER_TOKEN', type: 'secret_text', text: reader }, { name: 'CONTROL_TOKEN', type: 'secret_text', text: control }, ...(withNotification && !cleanup ? [{name:'NOTIFY_ORIGIN',type:'plain_text',text:notifyOrigin},{name:'NOTIFY_TOKEN',type:'secret_text',text:notify}] : [])] })], { type: 'application/json' })); form.set('worker.mjs', new Blob([code], { type: 'application/javascript+module' }), 'worker.mjs'); if (!cleanup)
    form.set('notification.mjs', new Blob([notification], { type: 'application/javascript+module' }), 'notification.mjs'); return api(account + '/workers/scripts/' + name, 'PUT', form); }
await save();
try {
    if (withNotification) {
        local('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-keyout',join(controlDirectory,'notify-key.pem'),'-out',join(controlDirectory,'notify-cert.pem'),'-days','1','-subj','/CN=127.0.0.1','-addext','subjectAltName=IP:127.0.0.1']);
        notifyCert = await readFile(join(controlDirectory,'notify-cert.pem'),'utf8');
        notifyKey = await readFile(join(controlDirectory,'notify-key.pem'),'utf8');
        await writeFile(join(controlDirectory,'cloudflared.yml'),'{}\n',{mode:0o600});
        notifyPort=await freePort();
        cloudTunnel=spawn('cloudflared',['--config',join(controlDirectory,'cloudflared.yml'),'tunnel','--no-autoupdate','--metrics','127.0.0.1:0','--protocol','http2','--url',`https://127.0.0.1:${notifyPort}`,'--origin-ca-pool',join(controlDirectory,'notify-cert.pem')],{stdio:['ignore','pipe','pipe']});
        notifyOrigin=await nextTunnelLine(cloudTunnel);
        cloudTunnel.stdout.resume();cloudTunnel.stderr.resume();
        state.notification_tunnel = 'temporary HTTPS with pinned isolated origin certificate';
    }
    before = digest(await allRules());
    const child = await api('/zones/' + zone + '/dns_records?type=MX&name=mail-poc.bengbu.cc.cd&per_page=100');
    assert.equal(child.length, 3, 'Enabled child MX required');
    assert.ok(!(await api(account + '/workers/scripts')).some(v => v.id === name));
    assert.ok(!(await api(account + '/r2/buckets')).buckets.some(v => v.name === name));
    const sub = await api(account + '/workers/subdomain');
    base = `https://${name}.${sub.subdomain}.workers.dev`;
    state.issued.bucket = true;
    await save();
    await api(account + '/r2/buckets', 'POST', { name, storageClass: 'Standard' });
    state.created.bucket = true;
    await save();
    assert.equal((await api(account + '/r2/buckets/' + name + '/domains/managed')).enabled, false);
    const code = await readFile('modules/mail/cloudflare/worker.mjs', 'utf8'), notification = await readFile('modules/mail/cloudflare/notification.mjs', 'utf8');
    state.source_sha256 = {};
    for (const p of ['modules/mail/cloudflare/worker.mjs', 'modules/mail/cloudflare/notification.mjs', 'modules/mail/service/ingress_worker_native_test.go', 'modules/mail/service/ingress_worker_tunnel_test.go', 'modules/mail/service/roundcube_native_test.go', 'test-env/fixtures/mail-cloudflare-native/worker-proxy.mjs', 'test-env/fixtures/mail-vps-native/run.py', 'test-env/scripts/mail-cloudflare-native-e2e.mjs', 'test-env/scripts/mail-cloudflare-rcpt-check.py', 'test-env/scripts/mail-cloudflare-smtp-send.py', 'test-env/scripts/mail-cloudflare-direct-smtp-send.py'])
        state.source_sha256[p] = hash(await readFile(p));
    state.bundle_sha256 = hash(await readFile(binary));
    state.issued.worker = true;
    await save();
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
    state.visible_to_mode = visibleTo;
    const cfg = {
        Source: {
            ID: source, Generation: generation, Origin: base,
            Token: reader, ControlToken: control,
            Domains: ['mail-poc.bengbu.cc.cd'], IPv4Only: true, Proxy: '',
        },
        Routes: routes,
        Subject: 'ANAS cloud SMTP ' + source,
        VisibleTo: visibleTo === 'main' ? routes[0].address : visibleTo === 'foreign' ? 'forged@foreign.example' : externalTo,
        ...(withNotification ? {NotifyToken:notify,NotifyCert:notifyCert,NotifyKey:notifyKey} : {}),
        ...(withProductRules ? {RuleToken:routing.token} : {}),
        ...(withOffline ? {Offline:true} : {}),
        ...(withScheduledRetry ? {ScheduledRetry:true} : {}),
    };
    await writeFile(binding, JSON.stringify(cfg), { mode: 0o600, flag: 'wx' });
    local('ssh', [...sshArgs, `umask 077; mkdir -- ${remote}`]);
    remoteCreated = true;
    local('ssh', [...sshArgs, `mkdir -m 700 -- ${remote}/bridge`]);
    local('scp', ['-q', ...sshOptions, binding, `whl@finance.hlong.wang:${remote}/worker-native.json`]);
    local('scp', ['-q', ...sshOptions, resolve(binary), `whl@finance.hlong.wang:${remote}/bundle.tar`]);
    local('scp', ['-q', ...sshOptions, 'test-env/fixtures/mail-vps-native/run.py', `whl@finance.hlong.wang:${remote}/run.py`]);
    local('scp', ['-q', ...sshOptions, 'test-env/scripts/mail-cloudflare-rcpt-check.py', `whl@finance.hlong.wang:${remote}/rcpt-check.py`]);
    local('scp', ['-q', ...sshOptions, 'test-env/scripts/mail-cloudflare-smtp-send.py', `whl@finance.hlong.wang:${remote}/smtp-send.py`]);
    local('ssh', [...sshArgs, `chmod 600 ${remote}/worker-native.json`]);
    const goLog = join(report, 'go-test.log');
    await writeFile(goLog, '', { mode: 0o600, flag: 'wx' });
    proxyServer = createWorkerProxy(new URL(base).hostname, (host, port) => dialWorkerThroughSocks(host, port, proxyPort, withProductRules), {allowCloudflareApi:withProductRules});
    await new Promise((resolve,reject) => { proxyServer.once('error', reject); proxyServer.listen(0, '127.0.0.1', resolve); });
    const workerProxyPort = proxyServer.address().port;
    proxySSH = spawn('ssh', ['-o', 'BatchMode=yes', '-o', 'ControlMaster=no', '-o', 'ConnectTimeout=15',
        '-o', 'ExitOnForwardFailure=yes', '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=6',
        '-N', '-R', `${remote}/bridge/cloud.sock:127.0.0.1:${workerProxyPort}`, 'whl@finance.hlong.wang'], {stdio:['ignore','ignore','pipe']});
    proxySSH.stderr.on('data', chunk => appendFileSync(join(report, 'proxy-ssh.log'), chunk, {mode:0o600}));
    proxySSH.on('error', error => { state.proxy_error = error.message; });
    let socketReady = false;
    for (let i=0; i<30 && proxySSH.exitCode === null; i++) {
        const check = spawnSync('ssh', [...sshArgs, `test -S ${remote}/bridge/cloud.sock`], {timeout:20000});
        if (check.status === 0) { socketReady = true; break; }
        await wait(1000);
    }
    assert.ok(socketReady, 'Restricted SSH Worker tunnel did not start');
    nativeChild = spawn('ssh', [...sshArgs, `env MAIL_NATIVE_WORKER_TEST=1 MAIL_NATIVE_WORKER_SKIP_INTAKE=${skipNativeIntake ? '1' : '0'} MAIL_NATIVE_WORKER_RETRY_TEST=${withScheduledRetry ? '1' : '0'} python3 ${remote}/run.py ${remote} ${bundleHash}`], {stdio:['ignore','pipe','pipe']});
    let logFailed = false;
    for (const stream of [nativeChild.stdout, nativeChild.stderr]) stream.on('data', chunk => {
        try { appendFileSync(goLog, chunk); } catch { logFailed = true; }
    });
    const nativeResult = new Promise(resolve => {
        nativeChild.on('error', error => resolve({error: error.message}));
        nativeChild.on('close', code => resolve({code}));
    });
    let routeReady = false;
    const routeDeadline = Date.now()+600000;
    while (Date.now()<routeDeadline && nativeChild.exitCode === null && nativeChild.signalCode === null && proxySSH.exitCode === null && proxySSH.signalCode === null) {
        const check = spawnSync('ssh', [...sshArgs, `test -f ${remote}/bridge/routes-ready`], {timeout:20000});
        if (check.status === 0) { routeReady = true; break; }
        await wait(1000);
    }
    assert.ok(routeReady, 'Native route coordination did not complete');
    if (withNotification && !withOffline) await startNotificationTunnel();
    if (withProductRules) {
        const observed = (await allRules()).filter(own);
        assert.equal(observed.length,3,'Product Cloudflare rule readback incomplete');
        for (const route of routes) assert.equal(observed.filter(v=>v.matchers[0].value===route.address).length,1);
        state.public_rule_sync = 'product-ledger-and-cloud-readback';
        await save();
    } else for (const route of routes) {
        const body = {name:prefix+hash(route.address), enabled:true, source:'api', priority:0,
            actions:[{type:'worker',value:[name]}], matchers:[{type:'literal',field:'to',value:route.address}]};
        const response = await fetch('https://api.cloudflare.com/client/v4'+ruleBase, {method:'POST',
            headers:{Authorization:'Bearer '+routing.token,'Content-Type':'application/json'},body:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(45000)});
        const result = await response.json();
        assert.ok(response.ok && result.success && own(result.result), 'Controlled public recipient creation failed');
        const id = result.result.id || result.result.tag;
        assert.ok(own(await api(ruleBase+'/'+id)), 'Controlled public recipient readback failed');
    }
    // API readback can precede SMTP propagation. Probe only the isolated
    // addresses, optionally via an explicit local SOCKS proxy; never send DATA.
    state.rcpt_checks = [];
    state.rcpt_probe_source = rcptSocksPort === null ? 'finance' : 'local-loopback-socks';
    let recipientsReady = false, acceptedMX = '';
    const rcptDeadline = Date.now()+120000;
    for (let attempt=0; attempt<8 && Date.now()<rcptDeadline; attempt++) {
        await wait(attempt === 0 ? 20000 : 10000);
        const mx = ['route1.mx.cloudflare.net','route2.mx.cloudflare.net','route3.mx.cloudflare.net'][attempt%3];
        const check = rcptSocksPort === null
            ? spawnSync('ssh', [...sshArgs, `python3 ${remote}/rcpt-check.py ${remote}/worker-native.json ${mx}`], {encoding:'utf8',timeout:30000})
            : spawnSync('python3', ['test-env/scripts/mail-cloudflare-rcpt-check.py', binding, mx, String(rcptSocksPort)], {encoding:'utf8',timeout:30000});
        let outcome = {phase:'unknown'};
        try { outcome = JSON.parse(check.stdout); } catch { }
        state.rcpt_checks.push({mx,phase:outcome.phase,codes:outcome.codes||[],error:outcome.error||null});
        await save();
        if (check.status === 0 && outcome.phase === 'complete' && JSON.stringify(outcome.codes) === '[250,250,250]') { recipientsReady = true; acceptedMX = mx; break; }
    }
    assert.ok(recipientsReady, 'SMTP recipient acceptance not confirmed; no message submitted');
    if(withNotification) {
        local('ssh',[...sshArgs,`touch ${remote}/bridge/consumer-start`]);
        let running=false;
        for(let i=0;i<90 && nativeChild.exitCode===null;i++){const check=spawnSync('ssh',[...sshArgs,`test -f ${remote}/bridge/consumer-ready`],{timeout:20000});if(check.status===0){running=true;break;}await wait(1000);}
        assert.ok(running,withOffline?'Offline receiver fixture not ready':'Initial scheduled poll did not complete');
    }
    // A lost SSH response may still mean the provider accepted DATA. Never
    // automatically repeat this transaction without reconciling the outcome.
    state.issued.smtp = true;
    await save();
    const sent = sendMode === 'direct-mx'
        ? spawnSync('python3', ['test-env/scripts/mail-cloudflare-direct-smtp-send.py', binding, acceptedMX, String(rcptSocksPort)], {encoding:'utf8',timeout:150000})
        : spawnSync('ssh', [...sshArgs, `python3 ${remote}/smtp-send.py ${root}/${smtp.provider}-sending-key.json ${remote}/worker-native.json`], {encoding:'utf8',timeout:150000});
    await writeFile(join(report,'smtp-submit.log'), (sent.stdout||'')+(sent.stderr||''), {mode:0o600});
    assert.equal(sent.status, 0, 'Controlled SMTP submission failed; private log preserved');
    state.smtp_submission = JSON.parse(sent.stdout);
    state.smtp_submit_host = sendMode === 'direct-mx' ? 'local-loopback-socks' : 'finance';
    await save();
    if (!withNotification) { console.log('Real SMTP submitted; leaving consumer stopped for 70 seconds'); await wait(70000); }
    assert.equal(nativeChild.exitCode, null, 'Native fixture stopped before offline interval completed');
    local('ssh', [...sshArgs, `touch ${remote}/bridge/smtp-submitted`]);
    if (withOffline) {
        let observed=false;
        for(let i=0;i<120 && nativeChild.exitCode===null;i++){const check=spawnSync('ssh',[...sshArgs,`test -f ${remote}/bridge/offline-observed`],{timeout:20000});if(check.status===0){observed=true;break;}await wait(1000);}
        assert.ok(observed,'Retained messages and failed notifications were not confirmed');
        const start=Date.now();
        await wait(70000);
        assert.ok(Date.now()-start>=70000,'Disconnected interval was shortened');
        state.offline_interval_ms=Date.now()-start;
        await startNotificationTunnel();
        if (withScheduledRetry) {
            const schedule = account + '/workers/scripts/' + name + '/schedules';
            const prior = await api(schedule);
            assert.deepEqual(prior.schedules, [], 'Unexpected existing schedule on isolated Worker');
            await api(schedule, 'PUT', [{cron:'* * * * *'}]);
            const observedSchedule = await api(schedule);
            assert.deepEqual(observedSchedule.schedules.map(v=>v.cron), ['* * * * *'], 'Isolated schedule readback failed');
            state.schedule_readback = true;
            await save();
            local('ssh',[...sshArgs,`touch ${remote}/bridge/retry-start`]);
            let retried=false;
            const retryDeadline=Date.now()+1250000;
            while(Date.now()<retryDeadline && nativeChild.exitCode===null){const check=spawnSync('ssh',[...sshArgs,`test -f ${remote}/bridge/retry-observed`],{timeout:20000});if(check.status===0){retried=true;break;}await wait(2000);}
            assert.ok(retried,'Scheduled authenticated notifications were not confirmed');
            state.scheduled_retry_observed = true;
        }
        local('ssh',[...sshArgs,`touch ${remote}/bridge/offline-resume`]);
        await save();
    }
    const status = await bounded(nativeResult, withScheduledRetry ? 1900000 : 540000, {error:'Native fixture timeout'});
    assert.equal(logFailed, false, 'Private native log write failed');
    assert.equal(status.code, 0, 'Real Worker/native fixture failed; private log preserved');
    state.go_test_passed = true;
}
catch (error) {
    state.failure = error.message;
    if (nativeChild && nativeChild.exitCode === null && nativeChild.signalCode === null && remoteCreated) {
        try { local('ssh', [...sshArgs, `touch ${remote}/bridge/abort`]); }
        catch { state.cleanup_errors.push('Could not signal native fixture to stop'); }
        const exited = nativeChild.exitCode !== null || nativeChild.signalCode !== null || await bounded(new Promise(resolve => nativeChild.once('close', ()=>resolve(true))),450000,false);
        if (!exited) state.cleanup_errors.push('Native fixture cleanup outcome unknown');
    }
}
finally {
    if (state.issued.worker && !state.created.worker) state.cleanup_errors.push('Worker creation outcome unknown; reconcile before cleanup');
    if (state.issued.bucket && !state.created.bucket) state.cleanup_errors.push('Bucket creation outcome unknown; reconcile before cleanup');
    for(const child of [notifySSH,cloudTunnel]) if(child && child.exitCode===null && child.signalCode===null) { child.kill('SIGTERM'); const stopped=await bounded(new Promise(resolve=>child.once('close',()=>resolve(true))),10000,false); if(!stopped) state.cleanup_errors.push('Notification tunnel cleanup unknown'); }
    if (proxySSH && proxySSH.exitCode === null) {
        proxySSH.kill('SIGTERM');
        const stopped = proxySSH.exitCode !== null || proxySSH.signalCode !== null || await bounded(new Promise(resolve => proxySSH.once('close', ()=>resolve(true))),10000,false);
        if (!stopped) state.cleanup_errors.push('Restricted SSH tunnel cleanup unknown');
    }
    if (proxyServer) { state.proxy_stats = proxyServer.stats; await new Promise(resolve => proxyServer.close(resolve)); }
    await unlink(binding).catch(e => { if (e.code !== 'ENOENT')
        state.cleanup_errors.push('Local binding removal failed'); });
    if (remoteCreated) {
        try {
            local('ssh', [...sshArgs, `rm -f -- ${remote}/worker-native.json`]);
            state.cleaned.remote_binding = true;
        }
        catch (e) {
            state.cleanup_errors.push(e.message);
        }
        if (!nativeChild || nativeChild.exitCode !== null || nativeChild.signalCode !== null) {
            try {
                local('ssh', [...sshArgs, `rm -f -- ${remote}/bridge/cloud.sock ${remote}/bridge/notify.sock ${remote}/bridge/routes-ready ${remote}/bridge/consumer-start ${remote}/bridge/consumer-ready ${remote}/bridge/smtp-submitted ${remote}/bridge/offline-observed ${remote}/bridge/retry-start ${remote}/bridge/retry-observed ${remote}/bridge/offline-resume ${remote}/bridge/abort`]);
                local('ssh', [...sshArgs, `rmdir -- ${remote}/bridge`]);
                state.cleaned.remote_bridge = true;
            } catch (e) { state.cleanup_errors.push('Remote Worker bridge cleanup failed'); }
        } else state.cleanup_errors.push('Remote Worker bridge retained while native fixture state is unknown');
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
    if (withScheduledRetry && state.created.worker) {
        try {
            const schedule = account + '/workers/scripts/' + name + '/schedules';
            await api(schedule, 'PUT', []);
            assert.deepEqual((await api(schedule)).schedules, [], 'Isolated schedule cleanup failed');
            state.cleaned.schedule = true;
        } catch (e) { state.cleanup_errors.push('Isolated schedule cleanup unknown'); }
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
    if (state.created.bucket && (!state.issued.worker || state.cleaned.worker)) {
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
            local('ssh', [...sshOptions, '-O', 'exit', 'whl@finance.hlong.wang']);
        }
        for(const name of ['notify-key.pem','notify-cert.pem','cloudflared.yml']) await unlink(join(controlDirectory,name)).catch(e=>{if(e.code!=='ENOENT') throw e;});
        await rmdir(controlDirectory);
        state.cleaned.ssh_master = true;
    } catch (error) { state.cleanup_errors.push(error.message); }
    state.finished = new Date().toISOString();
    await save();
    console.log(JSON.stringify({ passed: state.go_test_passed === true, failure: state.failure || null, cleaned: state.cleaned, unrelated_rules_unchanged: state.unrelated_rules_unchanged, cleanup_errors: state.cleanup_errors }));
}
if (state.failure || state.cleanup_errors.length || !state.unrelated_rules_unchanged)
    process.exitCode = 1;
