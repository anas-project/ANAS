// TEST_CASES: MAILMOD-T-080
// Actual OSA relay -> finance isolated Go/PG/DMS/Roundcube. All SMTP, source
// and notification credentials are ephemeral; no production mailbox or port.
import assert from 'node:assert/strict';
import {readFile,writeFile,mkdtemp,rm} from 'node:fs/promises';
import {appendFileSync} from 'node:fs';
import {spawn,spawnSync} from 'node:child_process';
import {randomBytes,randomUUID,createHash} from 'node:crypto';
import net from 'node:net';
import https from 'node:https';
import {join,resolve} from 'node:path';
import {quickTunnelOrigin} from '../mail-cloudflare-vps-forward-live/quick-tunnel.mjs';

const bundle=process.env.MAIL_VPS_CROSSHOST_BUNDLE, expected=process.env.MAIL_VPS_CROSSHOST_BUNDLE_SHA256;
const relay=process.env.MAIL_VPS_CROSSHOST_RELAY, report=process.env.MAIL_VPS_CROSSHOST_REPORT;
assert.ok(bundle&&relay&&report&&/^[a-f0-9]{64}$/.test(expected||''),'explicit isolated artifacts and report required');
const sha=b=>createHash('sha256').update(b).digest('hex');
assert.equal(sha(await readFile(bundle)),expected,'fixture bundle checksum mismatch');
const temp=await mkdtemp('/private/tmp/anas-vps-crosshost-');
const finance='whl@finance.hlong.wang',osa='ubuntu@osa.letuh.com';
const runID=randomBytes(8).toString('hex');
const remote='/home/whl/anas-vps-native-crosshost-'+runID;
const reportState={checks:[],cleaned:{},errors:[],bundle_sha256:expected,finance_directory:remote};
const source=randomUUID(),generation='10000000-0000-4000-8000-000000000001';
const secret=()=>randomBytes(32).toString('hex'),reader=secret(),control=secret(),notification=secret();
const sshOptions=['-o','BatchMode=yes','-o','ConnectTimeout=15','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=3'];
let financeCreated=false,osaDir='',native,osaChild,notifySSH,cloudTunnel,relaySSH,smtpSSH,financeSSH,notifyCert,notifyPort,nativeCode=null;
let interrupted=false;
for(const name of ['SIGINT','SIGTERM','SIGHUP'])process.on(name,()=>{interrupted=true;});
const save=()=>writeFile(report,JSON.stringify(reportState,null,2)+'\n',{mode:0o600});
function command(name,args,options={}){const r=spawnSync(name,args,{timeout:60000,...options});if(r.status!==0||r.error)throw Error(name+' failed');return r.stdout;}
function ssh(host,cmd,options={}){return command('ssh',[...sshOptions,host,cmd],options);}
function fileWrite(host,path,bytes){ssh(host,`umask 077; cat > ${path}`,{input:bytes});}
function forward(host,args){const p=spawn('ssh',[...sshOptions,'-o','ExitOnForwardFailure=yes','-N',...args,host],{stdio:['ignore','ignore','pipe']});p.stderr.on('data',b=>appendFileSync(report+'.ssh.log',b,{mode:0o600}));return p;}
function nextLine(child,predicate,timeout){return new Promise((resolve,reject)=>{let pending='';const timer=setTimeout(()=>finish(Error('readiness timeout')),timeout);const data=b=>{pending+=b.toString();if(pending.length>65536)return finish(Error('readiness output too large'));for(;;){const i=pending.indexOf('\n');if(i<0)break;const line=pending.slice(0,i);pending=pending.slice(i+1);if(predicate(line))return finish(null,line);}};const end=()=>finish(Error('process ended before readiness'));function finish(e,v){clearTimeout(timer);child.stdout?.off('data',data);child.stderr?.off('data',data);child.off('exit',end);e?reject(e):resolve(v);}child.stdout.on('data',data);child.stderr.on('data',data);child.once('exit',end);});}
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function waitFor(fn,label,ms=180000){const end=Date.now()+ms;while(Date.now()<end){if(interrupted)throw Error('interrupted');if(native&&(native.exitCode!==null||native.signalCode!==null))throw Error('native fixture ended before readiness');try{if(await fn())return}catch{}await delay(1000)}throw Error(label+' unavailable');}
async function freePort(){return new Promise((resolve,reject)=>{const s=net.createServer();s.once('error',reject);s.listen(0,'127.0.0.1',()=>{const p=s.address().port;s.close(()=>resolve(p));});});}
function healthy(port,ca){return new Promise((resolve,reject)=>{const r=https.get({hostname:'127.0.0.1',port,path:'/__.health',ca,timeout:3000},v=>{v.resume();resolve(v.statusCode===204)});r.on('error',reject);r.on('timeout',()=>r.destroy(Error('timeout')));});}
function relayHealthy(port,ca,token){return new Promise((resolve,reject)=>{const r=https.get({hostname:'127.0.0.1',port,path:'/v1/status',ca,headers:{Authorization:'Bearer '+token},timeout:3000},v=>{v.resume();resolve(v.statusCode===200)});r.on('error',reject);r.on('timeout',()=>r.destroy(Error('timeout')));});}
function tcpReady(port){return new Promise(resolve=>{const c=net.createConnection({host:'127.0.0.1',port});c.setTimeout(3000);c.once('connect',()=>{c.destroy();resolve(true)});c.once('error',()=>resolve(false));c.once('timeout',()=>{c.destroy();resolve(false)});});}
function closed(p,ms){return new Promise(resolve=>{if(!p||p.exitCode!==null||p.signalCode!==null)return resolve(true);const timer=setTimeout(()=>resolve(false),ms);p.once('close',()=>{clearTimeout(timer);resolve(true)});});}

try{
 await save();
 command('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-keyout',join(temp,'relay.key'),'-out',join(temp,'relay.crt'),'-days','1','-subj','/CN=relay.example.test','-addext','subjectAltName=IP:127.0.0.1,DNS:relay.example.test'],{stdio:'ignore'});
 const cert=await readFile(join(temp,'relay.crt'),'utf8'),key=await readFile(join(temp,'relay.key'),'utf8');
 await writeFile(join(temp,'empty.yml'),'{}\n',{mode:0o600});
 ssh(finance,`umask 077; mkdir -m 700 ${remote}`);financeCreated=true;
 ssh(finance,`mkdir -m 700 ${remote}/bridge`);
 fileWrite(finance,remote+'/bundle.tar',await readFile(bundle));
 fileWrite(finance,remote+'/run.py',await readFile(new URL('../mail-vps-native/run.py',import.meta.url)));
 fileWrite(finance,remote+'/vps-crosshost.json',Buffer.from(JSON.stringify({Source:source,Generation:generation,Reader:reader,Control:control,Notification:notification,RelayCA:cert})));
 native=spawn('ssh',[...sshOptions,finance,`env MAIL_NATIVE_VPS_CROSSHOST=1 MAIL_NATIVE_CROSSHOST_COMBINED_ONLY=1 python3 ${remote}/run.py ${remote} ${expected}`],{stdio:['ignore','pipe','pipe']});
 for(const stream of [native.stdout,native.stderr])stream.on('data',b=>appendFileSync(report+'.native.log',b,{mode:0o600}));
 native.once('close',code=>{nativeCode=code;});
 await waitFor(()=>spawnSync('ssh',[...sshOptions,finance,`test -S ${remote}/bridge/notify.sock && test -s ${remote}/bridge/notify-cert.pem`],{timeout:20000}).status===0,'finance notification receiver',600000);
 notifyCert=ssh(finance,`cat ${remote}/bridge/notify-cert.pem`);await writeFile(join(temp,'notify.crt'),notifyCert,{mode:0o600});
 notifyPort=await freePort();notifySSH=forward(finance,['-L',`127.0.0.1:${notifyPort}:${remote}/bridge/notify.sock`]);
 await waitFor(()=>healthy(notifyPort,notifyCert),'finance TLS notification');
 cloudTunnel=spawn('cloudflared',['--config',join(temp,'empty.yml'),'tunnel','--no-autoupdate','--metrics','127.0.0.1:0','--protocol','http2','--url',`https://127.0.0.1:${notifyPort}`,'--origin-ca-pool',join(temp,'notify.crt')],{stdio:['ignore','pipe','pipe']});
 cloudTunnel.stderr.on('data',b=>appendFileSync(report+'.tunnel.log',b,{mode:0o600}));
 const line=await nextLine(cloudTunnel,v=>quickTunnelOrigin(v)!==null,120000);
 const origin=quickTunnelOrigin(line);cloudTunnel.stdout.resume();
 await waitFor(()=>ssh(osa,`curl --silent --show-error --connect-timeout 5 --max-time 8 -o /dev/null -w '%{http_code}' ${origin}/__.health`).toString().trim()==='204','OSA HTTPS notification');
 const newOsaDir='/tmp/anas-mail-vps-live-'+runID;ssh(osa,`umask 077; mkdir -m 700 ${newOsaDir}`);osaDir=newOsaDir;assert.match(osaDir,/^\/tmp\/anas-mail-vps-live-[A-Za-z0-9]+$/);
 fileWrite(osa,osaDir+'/relay',await readFile(relay));ssh(osa,`chmod 700 ${osaDir}/relay`);
 fileWrite(osa,osaDir+'/remote.py',await readFile(new URL('../mail-vps-notification-live/remote.py',import.meta.url)));
 osaChild=spawn('ssh',[...sshOptions,osa,`python3 ${osaDir}/remote.py ${osaDir}`],{stdio:['pipe','pipe','pipe']});
 const ready=nextLine(osaChild,v=>{try{return JSON.parse(v).state==='ready'}catch{return false}},90000);
 osaChild.stdin.write(JSON.stringify({source,generation,notify:notification,reader,control,origin,cert,key})+'\n');
 const ports=JSON.parse(await ready),localRelay=await freePort(),localSMTP=await freePort();
 assert.ok(Number.isInteger(ports.https_port)&&Number.isInteger(ports.smtp_port));
 relaySSH=forward(osa,['-L',`127.0.0.1:${localRelay}:127.0.0.1:${ports.https_port}`]);
 smtpSSH=forward(osa,['-L',`127.0.0.1:${localSMTP}:127.0.0.1:${ports.smtp_port}`]);
 await waitFor(()=>relayHealthy(localRelay,cert,reader),'verified OSA source');
 await waitFor(()=>tcpReady(localSMTP),'OSA isolated SMTP tunnel');
 financeSSH=forward(finance,['-R',`${remote}/bridge/relay.sock:127.0.0.1:${localRelay}`,'-R',`${remote}/bridge/smtp.sock:127.0.0.1:${localSMTP}`]);
 await waitFor(()=>spawnSync('ssh',[...sshOptions,finance,`test -S ${remote}/bridge/relay.sock && test -S ${remote}/bridge/smtp.sock`],{timeout:20000}).status===0,'restricted source tunnels');
 ssh(finance,`umask 077; touch ${remote}/bridge/relay-ready`);reportState.checks.push('cross-host-bridges-ready');await save();
 assert.ok(await closed(native,900000),'native fixture did not complete');
 assert.equal(nativeCode,0,'native fixture failed');
 const result=JSON.parse(ssh(finance,`cat ${remote}/final.json`).toString());
 assert.equal(result.failure,null);assert.deepEqual(result.cleanup_errors,[]);assert.equal(result.business_unchanged,true);
 assert.match(result.parent,/^\/home\/whl\/anas-mail-runtime-20261026\/anas-mail-runtime-test-anas-vps-native-crosshost-[a-f0-9]+$/);
 const log=ssh(finance,`cat ${result.parent}/dms-fixture/roundcube-fixture/evidence/native.log`).toString();
 assert.match(log,/--- PASS: TestRoundcubeNativeMailProtocol\/vps-smtp-webhook-and-real-native-inbox/);
 assert.match(log,/--- PASS: TestRoundcubeNativeMailProtocol /);
 reportState.checks.push('osa-smtp-webhook-finance-dms-roundcube');reportState.finance_evidence=result.parent;await save();
}catch(e){reportState.errors.push(e.message);await save();}
finally{
 if(native&&native.exitCode===null&&native.signalCode===null){
  try{ssh(finance,`umask 077; touch ${remote}/bridge/abort`);}catch{reportState.errors.push('finance abort signal unavailable');}
  if(!await closed(native,120000)){native.kill('SIGTERM');reportState.errors.push('finance fixture cleanup unknown');}
 }
 for(const p of [financeSSH,relaySSH,smtpSSH,notifySSH,cloudTunnel])if(p&&p.exitCode===null&&p.signalCode===null){
  p.kill('SIGTERM');if(!await closed(p,10000))reportState.errors.push('temporary tunnel shutdown unknown');
 }
 if(osaChild&&osaChild.exitCode===null&&osaChild.signalCode===null){
  try{const finished=nextLine(osaChild,v=>{try{return JSON.parse(v).state==='cleaned'}catch{return false}},45000);osaChild.stdin.end('stop\n');await finished;reportState.cleaned.osa=true;}catch{osaChild.kill('SIGTERM');reportState.errors.push('OSA cleanup unknown');}
 }
 if(osaDir&&!osaChild){
  try{ssh(osa,`rm -f ${osaDir}/relay ${osaDir}/remote.py && rmdir ${osaDir}`);reportState.cleaned.osa=true;}catch{reportState.errors.push('OSA unused directory cleanup unknown');}
 }
 if(financeCreated&&(!native||nativeCode!==null)){
  try{ssh(finance,`rm -f ${remote}/vps-crosshost.json ${remote}/bridge/notify-cert.pem ${remote}/bridge/relay-ready ${remote}/bridge/abort ${remote}/bridge/relay.sock ${remote}/bridge/smtp.sock ${remote}/bridge/notify.sock`);reportState.cleaned.finance_inputs=true;}catch{reportState.errors.push('finance private input cleanup unknown');}
 }
 await rm(temp,{recursive:true,force:true});await save();
}
if(reportState.errors.length||!reportState.checks.includes('osa-smtp-webhook-finance-dms-roundcube')||!reportState.cleaned.osa||!reportState.cleaned.finance_inputs){console.error('cross-host native fixture incomplete; inspect private report');process.exitCode=1;}
else console.log('PASS cross-host normal inbox subset; standalone DMS intake and Sieve acceptance were not run');
