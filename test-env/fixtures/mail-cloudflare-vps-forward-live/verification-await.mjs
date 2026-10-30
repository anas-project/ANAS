// TEST_CASES: MAILMOD-T-009 MAILMOD-T-080
// Isolated IPv6 relay and provider routing. The optional native mode imports
// only the verified synthetic test message into a disposable finance fixture.
import assert from 'node:assert/strict';
import {readFile,writeFile,mkdtemp,rm} from 'node:fs/promises';
import {appendFileSync} from 'node:fs';
import {open} from 'node:fs/promises';
import {cloudflareDNS,createTestDNS,cleanupTestDNS,newTestTag} from './dns.mjs';
import {cloudflareDestination,cleanupDestination} from './destination-cleanup.mjs';
import {confirmationWindow,waitForConfirmation} from './wait-user.mjs';
import {phaseFailure,retryDestinationPreflight,recipientProbeFailure} from './phase-diagnostic.mjs';
import {dnsReady,dnsObservation,queryCloudflareDNS,queryCloudflareDNSViaSocks} from './dns-readiness.mjs';
import {nativeReadyPayload,nativeReadyWriter} from './native-signal.mjs';
import {quickTunnelOrigin} from './quick-tunnel.mjs';
import {spawn,spawnSync} from 'node:child_process';
import {createHash,randomBytes,randomUUID} from 'node:crypto';
import https from 'node:https';
import net from 'node:net';
import {join,resolve} from 'node:path';

const relay=process.env.MAIL_CF_VPS_RELAY, binary=process.env.MAIL_CF_VPS_TEST_BINARY, report=process.env.MAIL_CF_VPS_AWAIT_REPORT, dkimPath=process.env.MAIL_CF_VPS_DKIM_PATH;
assert.ok(relay&&binary&&report&&dkimPath&&dkimPath.startsWith('/'),'explicit isolated binaries, DKIM verifier and report required');
const nativeAssets=process.env.MAIL_CF_VPS_NATIVE_REMOTE_ASSETS, nativeBundleHash=process.env.MAIL_CF_VPS_NATIVE_BUNDLE_SHA256;
const nativeMode=nativeAssets!==undefined||nativeBundleHash!==undefined;
const nativePrepareOnly=process.env.MAIL_CF_VPS_NATIVE_PREPARE_ONLY==='1';
const nativeWebhook=process.env.MAIL_CF_VPS_NATIVE_WEBHOOK==='1';
const notifyOnOSA=process.env.MAIL_CF_VPS_NOTIFY_ON_OSA==='1';
assert.ok(!nativeMode||/^\/home\/whl\/anas-vps-native-public-assets-[a-f0-9]{12}$/.test(nativeAssets||'')&&/^[a-f0-9]{64}$/.test(nativeBundleHash||''),'explicit isolated finance artifacts required');
assert.ok(!nativeMode||process.env.MAIL_CF_VPS_RESEND_SMTP==='1'&&!process.env.MAIL_CF_VPS_DIRECT_SMTP_PROXY,'native fixture requires the authorized Resend synthetic sender');
assert.ok(!nativePrepareOnly||nativeMode,'preparation-only mode requires the native fixture');
assert.ok(!nativeWebhook||nativeMode,'notification mode requires the native fixture');
assert.ok(!notifyOnOSA||nativeWebhook,'OSA tunnel requires notification mode');
assert.ok(!nativeMode||nativePrepareOnly||/^[1-9][0-9]{0,4}$/.test(process.env.MAIL_CF_VPS_RECIPIENT_PROBE_PROXY||'')&&Number(process.env.MAIL_CF_VPS_RECIPIENT_PROBE_PROXY)<=65535,'native public test requires a bounded SMTP recipient probe');
const dnsSocks=process.env.MAIL_CF_VPS_DNS_SOCKS_PORT;
assert.ok(dnsSocks===undefined || /^[1-9][0-9]{0,4}$/.test(dnsSocks) && Number(dnsSocks)<=65535,'invalid isolated DNS proxy');
const sha=b=>createHash('sha256').update(b).digest('hex');
const relayBytes=await readFile(relay), relayHash=sha(relayBytes), binaryHash=sha(await readFile(binary));
const temp=await mkdtemp('/private/tmp/anas-cf-vps-prepare-'),suffix=randomBytes(6).toString('hex'),remote='/tmp/anas-mail-cf-vps-'+suffix;
const source=randomUUID(),generation=randomUUID(),reader=randomBytes(32).toString('hex'),control=randomBytes(32).toString('hex');
const notification=nativeWebhook?randomBytes(32).toString('hex'):'';
const mailboxID=randomUUID(),ownerAnchor=randomUUID(),claimVersion=randomUUID(),nonce=randomBytes(16).toString('hex');
const address=`cf-vps-test-${suffix}@mail-poc.bengbu.cc.cd`,financeRemote=`/home/whl/anas-vps-native-public-${suffix}`;
const sshArgs=['-o','BatchMode=yes','-o','ConnectTimeout=15','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=3'];
const tag=newTestTag();
const out={checks:[],cleanup:[],errors:[],remote_directory:remote,journal_directory:temp,relay_sha256:relayHash,test_binary_sha256:binaryHash};
let created=false,fixtureStopped=false,remoteProcess,bridge,config,dnsAPI,destAPI,target,destinationID,dnsIssued=false,destinationIssued=false,ruleIssued=false,ruleCreated=false,destinationCreatedAt=0;
let financeCreated=false,native,nativeCode=null,financeBridge,notifySSH,cloudTunnel,notifyOrigin;
let osaNotifySSH,osaTunnel,osaTunnelCreated=false;
const osaTunnelDir='/tmp/anas-mail-notify-'+suffix;
const save=()=>writeFile(report,JSON.stringify(out,null,2)+'\n',{mode:0o600});
function command(name,args,options={}){const r=spawnSync(name,args,{timeout:60000,...options});if(r.status!==0||r.error)throw Error(name+' command failed');return r.stdout;}
function ssh(host,cmd,options={}){return command('ssh',[...sshArgs,host,cmd],options);}
function nextLine(child,predicate,timeout){return new Promise((resolve,reject)=>{let pending='';const timer=setTimeout(()=>finish(Error('remote readiness timeout')),timeout);const data=b=>{pending+=b.toString();if(pending.length>65536)return finish(Error('remote output too large'));for(;;){const i=pending.indexOf('\n');if(i<0)break;const line=pending.slice(0,i);pending=pending.slice(i+1);if(predicate(line))return finish(null,line);}};const end=()=>finish(Error('remote process ended'));function finish(e,v){clearTimeout(timer);child.stdout?.off('data',data);child.stderr?.off('data',data);child.off('exit',end);e?reject(e):resolve(v);}child.stdout.on('data',data);child.stderr.on('data',data);child.once('exit',end);});}
function closed(p,ms){return new Promise(resolve=>{if(!p||p.exitCode!==null||p.signalCode!==null)return resolve(true);const timer=setTimeout(()=>resolve(false),ms);p.once('close',()=>{clearTimeout(timer);resolve(true);});});}
async function freePort(){return new Promise((resolve,reject)=>{const s=net.createServer();s.once('error',reject);s.listen(0,'127.0.0.1',()=>{const p=s.address().port;s.close(()=>resolve(p));});});}
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function waitNativeFile(path,label,timeout){const deadline=Date.now()+timeout;while(Date.now()<deadline){if(native&&(native.exitCode!==null||native.signalCode!==null))throw Error('finance fixture ended before '+label);const r=spawnSync('ssh',[...sshArgs,'whl@finance.hlong.wang',`test -f ${path}`],{timeout:20000});if(r.status===0)return;await delay(2000)}throw Error('finance '+label+' unavailable');}

async function issued(name,value){const f=await open(join(temp,name),'wx',0o600);try{await f.writeFile(JSON.stringify(value));await f.sync()}finally{await f.close()}const d=await open(temp,'r');try{await d.sync()}finally{await d.close()}}

function healthy(port,ca){return new Promise(resolve=>{const req=https.get({hostname:'127.0.0.1',port,path:'/v1/status',ca,headers:{Authorization:'Bearer '+reader},timeout:2500},r=>{r.resume();resolve(r.statusCode===200)});req.on('error',()=>resolve(false));req.on('timeout',()=>req.destroy());});}
function notifyHealthy(port,ca){return new Promise(resolve=>{const req=https.get({hostname:'127.0.0.1',port,path:'/__.health',ca,timeout:3000},r=>{r.resume();resolve(r.statusCode===204)});req.on('error',()=>resolve(false));req.on('timeout',()=>req.destroy());});}
async function waitFor(check,label,timeout){const deadline=Date.now()+timeout;while(Date.now()<deadline){if(native&&(native.exitCode!==null||native.signalCode!==null))throw Error('finance fixture ended before '+label);try{if(await check())return}catch{}await delay(1000)}throw Error(label+' unavailable');}
async function phase(config,name){config.Phase=name;config.Output=join(temp,name+'.json');await writeFile(join(temp,'input.json'),JSON.stringify(config),{mode:0o600});const r=spawnSync(resolve(binary),['-test.v','-test.run','^TestCloudflareVPSForwardLivePhase$','-test.timeout','120s'],{env:{...process.env,MAIL_CF_VPS_FORWARD_TEST_FILE:join(temp,'input.json')},timeout:130000});if(r.status!==0||r.error)throw Error(phaseFailure(name,r.stdout,r.stderr));return JSON.parse(await readFile(config.Output,'utf8'));}
async function stopFixture(){
 if(fixtureStopped)return;fixtureStopped=true;
 if(bridge&&bridge.exitCode===null&&bridge.signalCode===null){bridge.kill('SIGTERM');if(!await closed(bridge,10000))out.errors.push('bridge shutdown unknown');}
 if(remoteProcess&&remoteProcess.exitCode===null&&remoteProcess.signalCode===null){
  try{const clean=nextLine(remoteProcess,line=>{try{return JSON.parse(line).state==='cleaned'}catch{return false}},45000);remoteProcess.stdin.end('stop\n');await clean;out.cleanup.push('osa-fixture');}catch{remoteProcess.kill('SIGTERM');out.errors.push('OSA cleanup unknown');}
 }else if(created&&!remoteProcess){
  try{ssh('ubuntu@osa.letuh.com',`rm -f ${remote}/relay ${remote}/remote.py && rmdir ${remote}`);out.cleanup.push('osa-fixture');}catch{out.errors.push('OSA unused directory cleanup unknown');}
 }else if(created&&remoteProcess){
  try{ssh('ubuntu@osa.letuh.com',`test ! -e ${remote}`);out.cleanup.push('osa-fixture');}catch{out.errors.push('OSA exited fixture cleanup unknown');}
 }
}
try{
 await save();
 command('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-keyout',join(temp,'key.pem'),'-out',join(temp,'ca.pem'),'-days','1','-subj','/CN=127.0.0.1','-addext','subjectAltName=IP:127.0.0.1,DNS:vps-relay.mail-poc.bengbu.cc.cd'],{stdio:'ignore'});
 const cert=await readFile(join(temp,'ca.pem'),'utf8'),key=await readFile(join(temp,'key.pem'),'utf8');
 if(nativeMode){
  const runBytes=await readFile(new URL('../mail-vps-native/run.py',import.meta.url)),runHash=sha(runBytes);
  ssh('whl@finance.hlong.wang',`umask 077; mkdir -m 700 ${financeRemote}`);financeCreated=true;out.finance_directory=financeRemote;await save();
  ssh('whl@finance.hlong.wang',`mkdir -m 700 ${financeRemote}/bridge`);
  ssh('whl@finance.hlong.wang',`test "$(sha256sum ${nativeAssets}/bundle.tar | cut -d' ' -f1)" = ${nativeBundleHash} && cp ${nativeAssets}/bundle.tar ${financeRemote}/bundle.tar`);
  ssh('whl@finance.hlong.wang',`umask 077; cat > ${financeRemote}/run.py`,{input:runBytes});
  ssh('whl@finance.hlong.wang',`test "$(sha256sum ${financeRemote}/run.py | cut -d' ' -f1)" = ${runHash}`);
  const binding={Source:source,Generation:generation,Reader:reader,Control:control,RelayCA:cert,Address:address,MailboxID:mailboxID,OwnerAnchor:ownerAnchor,ClaimVersion:claimVersion,Nonce:nonce,...(nativeWebhook?{Notification:notification}:{})};
  ssh('whl@finance.hlong.wang',`umask 077; cat > ${financeRemote}/vps-public.json`,{input:Buffer.from(JSON.stringify(binding))});
  native=spawn('ssh',[...sshArgs,'whl@finance.hlong.wang',`env MAIL_NATIVE_VPS_PUBLIC=1 python3 ${financeRemote}/run.py ${financeRemote} ${nativeBundleHash}`],{stdio:['ignore','pipe','pipe']});
  for(const stream of [native.stdout,native.stderr])stream.on('data',b=>appendFileSync(report+'.native.log',b,{mode:0o600}));
  native.once('exit',code=>{nativeCode=code;});
  await waitNativeFile(`${financeRemote}/bridge/fixture-mounted`,'fixture mount',600000);
  out.checks.push('finance-isolated-fixture-mounted');out.finance_directory=financeRemote;out.native_bundle_sha256=nativeBundleHash;await save();
  if(nativeWebhook){
   await waitFor(()=>spawnSync('ssh',[...sshArgs,'whl@finance.hlong.wang',`test -S ${financeRemote}/bridge/notify.sock && test -s ${financeRemote}/bridge/notify-cert.pem`],{timeout:20000}).status===0,'finance notification receiver',600000);
   const notifyCert=ssh('whl@finance.hlong.wang',`cat ${financeRemote}/bridge/notify-cert.pem`);
   await writeFile(join(temp,'notify.crt'),notifyCert,{mode:0o600});await writeFile(join(temp,'empty.yml'),'{}\n',{mode:0o600});
   const notifyPort=await freePort();
   notifySSH=spawn('ssh',[...sshArgs,'-o','ExitOnForwardFailure=yes','-N','-L',`127.0.0.1:${notifyPort}:${financeRemote}/bridge/notify.sock`,'whl@finance.hlong.wang'],{stdio:['ignore','ignore','pipe']});notifySSH.stderr.on('data',b=>appendFileSync(report+'.notify-ssh.log',b,{mode:0o600}));
   await waitFor(()=>notifyHealthy(notifyPort,notifyCert),'finance TLS notification',90000);
   let tunnel;
   if(notifyOnOSA){
    const portText=ssh('ubuntu@osa.letuh.com',`python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'`).toString().trim();
    assert.match(portText,/^[1-9][0-9]{0,4}$/);assert.ok(Number(portText)<=65535);
    osaNotifySSH=spawn('ssh',[...sshArgs,'-o','ExitOnForwardFailure=yes','-N','-R',`127.0.0.1:${portText}:127.0.0.1:${notifyPort}`,'ubuntu@osa.letuh.com'],{stdio:['ignore','ignore','pipe']});osaNotifySSH.stderr.on('data',b=>appendFileSync(report+'.osa-notify-ssh.log',b,{mode:0o600}));
    ssh('ubuntu@osa.letuh.com',`umask 077; mkdir -m 700 ${osaTunnelDir}`);osaTunnelCreated=true;
    ssh('ubuntu@osa.letuh.com',`umask 077; cat > ${osaTunnelDir}/notify.crt`,{input:notifyCert});
    ssh('ubuntu@osa.letuh.com',`umask 077; cat > ${osaTunnelDir}/empty.yml`,{input:Buffer.from('{}\n')});
    ssh('ubuntu@osa.letuh.com',`umask 077; cat > ${osaTunnelDir}/remote-tunnel.py`,{input:await readFile(new URL('./remote-tunnel.py',import.meta.url))});
    await waitFor(()=>{if(osaNotifySSH.exitCode!==null||osaNotifySSH.signalCode!==null)throw Error('OSA forward stopped');return ssh('ubuntu@osa.letuh.com',`curl --silent --show-error --connect-timeout 3 --max-time 5 --cacert ${osaTunnelDir}/notify.crt -o /dev/null -w '%{http_code}' https://127.0.0.1:${portText}/__.health`).toString().trim()==='204'},'OSA verified TLS forward',60000);
    osaTunnel=spawn('ssh',[...sshArgs,'ubuntu@osa.letuh.com',`python3 ${osaTunnelDir}/remote-tunnel.py ${osaTunnelDir} ${portText} 1d356dac769d0dbf62ebe588c4034824bbff98d5790c2f844cc99e0e2f57462d`],{stdio:['pipe','pipe','pipe']});
    for(const stream of [osaTunnel.stdout,osaTunnel.stderr])stream.on('data',b=>appendFileSync(report+'.tunnel.log',b,{mode:0o600}));
    tunnel=osaTunnel;
   }else{
    cloudTunnel=spawn('cloudflared',['--config',join(temp,'empty.yml'),'tunnel','--no-autoupdate','--metrics','127.0.0.1:0','--protocol','http2','--url',`https://127.0.0.1:${notifyPort}`,'--origin-ca-pool',join(temp,'notify.crt')],{stdio:['ignore','pipe','pipe']});
    cloudTunnel.stderr.on('data',b=>appendFileSync(report+'.tunnel.log',b,{mode:0o600}));tunnel=cloudTunnel;
   }
   const line=await nextLine(tunnel,v=>quickTunnelOrigin(v)!==null,120000);
   notifyOrigin=quickTunnelOrigin(line);tunnel.stdout.resume();
   await waitFor(()=>ssh('ubuntu@osa.letuh.com',`curl --silent --show-error --connect-timeout 5 --max-time 8 -o /dev/null -w '%{http_code}' ${notifyOrigin}/__.health`).toString().trim()==='204','OSA HTTPS notification',120000);
   out.checks.push('finance-notification-health-from-osa');await save();
  }
 }
 ssh('ubuntu@osa.letuh.com',`umask 077; mkdir -m 700 ${remote}`);created=true;
 ssh('ubuntu@osa.letuh.com',`umask 077; cat > ${remote}/relay`,{input:relayBytes});
 ssh('ubuntu@osa.letuh.com',`chmod 700 ${remote}/relay`);
 ssh('ubuntu@osa.letuh.com',`umask 077; cat > ${remote}/remote.py`,{input:await readFile(new URL('./remote.py',import.meta.url))});
 remoteProcess=spawn('ssh',[...sshArgs,'ubuntu@osa.letuh.com',`python3 ${remote}/remote.py ${remote} ${relayHash}`],{stdio:['pipe','pipe','pipe']});
 remoteProcess.stderr.on('data',b=>appendFileSync(report+'.remote.log',b,{mode:0o600}));const ready=nextLine(remoteProcess,line=>{try{return JSON.parse(line).state==='ready'}catch{return false}},90000);
 remoteProcess.stdin.write(JSON.stringify({source,generation,reader,control,cert,key,...(nativeMode?{lease_seconds:1500}:{}),...(nativeWebhook?{notify:notification,origin:notifyOrigin}:{})})+'\n');
 const response=JSON.parse(await ready);assert.ok(Number.isInteger(response.https_port));
 const local=await freePort();bridge=spawn('ssh',[...sshArgs,'-o','ExitOnForwardFailure=yes','-N','-L',`127.0.0.1:${local}:127.0.0.1:${response.https_port}`,'ubuntu@osa.letuh.com'],{stdio:['ignore','ignore','pipe']});bridge.stderr.resume();
 let ok=false;for(let i=0;i<30;i++){if(await healthy(local,cert)){ok=true;break}await delay(1000)}assert.ok(ok,'source tunnel unavailable');
 const manager=JSON.parse(await readFile('test-env/reports/mail-ingress-20261024/cloudflare-token.json','utf8'));
 const destination=JSON.parse(await readFile('/private/tmp/anas-forward-destination-20261028/token.json','utf8'));
 const routing=JSON.parse(await readFile('test-env/reports/mail-runtime-20261026/cloudflare-routing-key.json','utf8'));
 dnsAPI=cloudflareDNS(manager.token);destAPI=cloudflareDestination(destination.token);
 config={Source:source,Generation:generation,Origin:`https://127.0.0.1:${local}`,Reader:reader,Control:control,CAFile:join(temp,'ca.pem'),Account:'aa7aa9753a4fffabb1d6536bcf62fb95',Zone:'0d84033826bc3cd0c283efdb57a90f7a',DestinationToken:destination.token,RoutingToken:routing.token,Address:address,MailboxID:mailboxID,OwnerAnchor:ownerAnchor,ClaimVersion:claimVersion,Operation:randomUUID()};
 if(nativeMode){
  financeBridge=spawn('ssh',[...sshArgs,'-o','ExitOnForwardFailure=yes','-N','-R',`${financeRemote}/bridge/relay.sock:127.0.0.1:${local}`,'whl@finance.hlong.wang'],{stdio:['ignore','ignore','pipe']});financeBridge.stderr.on('data',b=>appendFileSync(report+'.finance-bridge.log',b,{mode:0o600}));
  const deadline=Date.now()+60000;let socket=false;while(Date.now()<deadline){if(financeBridge.exitCode!==null||financeBridge.signalCode!==null)throw Error('finance source tunnel stopped');const check=spawnSync('ssh',[...sshArgs,'whl@finance.hlong.wang',`test -S ${financeRemote}/bridge/relay.sock`],{timeout:20000});if(check.status===0){socket=true;break}await delay(1000)}assert.ok(socket,'finance source tunnel unavailable');
  ssh('whl@finance.hlong.wang',`umask 077; touch ${financeRemote}/bridge/relay-ready`);
  await waitNativeFile(`${financeRemote}/bridge/routes-ready`,'route intent',300000);out.checks.push('finance-route-operation-confirmed');await save();
 }
 const prepared=await phase(config,nativeMode?'inspect-prepared-routes':'prepare-routes');target=prepared.target;assert.match(target,/^anas-[a-f0-9]{32}@vps-target\.mail-poc\.bengbu\.cc\.cd$/);out.checks.push('osa-isolated-forward-route');await save();
 if(nativePrepareOnly){
  ssh('whl@finance.hlong.wang',`umask 077; touch ${financeRemote}/bridge/abort`);
  if(!await closed(native,200000)||nativeCode===null||nativeCode===0)throw Error('finance preparation abort unconfirmed');
  const result=JSON.parse(ssh('whl@finance.hlong.wang',`cat ${financeRemote}/final.json`).toString());
  assert.equal(result.failure,'RuntimeError: isolated native combined test failed');assert.deepEqual(result.cleanup_errors,[]);assert.equal(result.business_unchanged,true);assert.equal(result.native_intake_executed,false);
  assert.match(result.parent,/^\/home\/whl\/anas-mail-runtime-20261026\/anas-mail-runtime-test-anas-vps-native-public-[a-f0-9]{12}$/);
  const log=ssh('whl@finance.hlong.wang',`cat ${result.parent}/dms-fixture/roundcube-fixture/evidence/native.log`).toString();
  assert.match(log,/public message signal unavailable public fixture aborted/);
  out.finance_evidence=result.parent;out.checks.push('finance-preparation-aborted-and-cleaned');await save();
 }else{
 await retryDestinationPreflight(()=>phase(config,'destination-preflight'),delay);await phase(config,'inspect-rule');out.checks.push('provider-preflight');await save();
 dnsIssued=true;await createTestDNS(dnsAPI,temp,tag);out.checks.push('temporary-dns-created');await save();
 // Provider configuration readback does not establish public resolver visibility.
 // Do not generate a time-limited verification message until this check passes.
 let visible=false;const dnsDeadline=Date.now()+240000;const lastDNS={};
 out.dns_query_path=dnsSocks===undefined?'direct':'loopback-socks';
 const rememberDNS=observation=>{
  out.dns_observation=observation;
  if(JSON.stringify(lastDNS[observation.type])!==JSON.stringify(observation)){
   lastDNS[observation.type]=observation;
   out.dns_observations ||= [];
   if(out.dns_observations.length<16)out.dns_observations.push(observation);
  }
 };
 const dnsQuery=async(name,type)=>{
  try{
   const result=dnsSocks===undefined?await queryCloudflareDNS(name,type):queryCloudflareDNSViaSocks(name,type,Number(dnsSocks));
   rememberDNS(dnsObservation(name,type,result));
   return result;
  }catch(error){rememberDNS({type,error:'resolver unavailable'});throw error;}
 };
 while(Date.now()<dnsDeadline){try{visible=await dnsReady(dnsQuery)}catch{}await save();if(visible)break;await delay(2000);}
 if(!visible)throw Error('public DNS readiness unconfirmed');
 out.checks.push('public-dns-readback');await save();
 // The provider's verification message is sent as part of creation. Persisted
 // intent and ID are retained in the private journal on an unknown response.
 destinationIssued=true;destinationCreatedAt=Date.now();const result=await phase(config,'create-destination');destinationID=result.destination_id;
 assert.match(destinationID,/^[a-f0-9]{32}$/);out.checks.push('destination-created');await save();
 const received=await phase(config,'read-verification');assert.equal(received.status,'received');config.PreviousMessageID=received.message_id;
 const metadata=JSON.parse(command('python3',[resolve('test-env/fixtures/mail-cloudflare-vps-forward-live/verification-meta.py'),join(temp,'verification.eml')],{encoding:'utf8'}));
 await writeFile(join(temp,'verification-metadata.json'),JSON.stringify(metadata)+'\n',{mode:0o600});
 const authenticated=JSON.parse(command('python3',[resolve('test-env/fixtures/mail-cloudflare-vps-forward-live/verification-auth.py'),join(temp,'verification.eml'),join(temp,nativeMode?'inspect-prepared-routes.json':'prepare-routes.json')],{encoding:'utf8',env:{...process.env,PYTHONPATH:dkimPath}}));
 await writeFile(join(temp,'verification-auth.json'),JSON.stringify(authenticated)+'\n',{mode:0o600});
 out.checks.push('message-received-authenticated');out.verification_metadata=metadata;out.verification_authentication=authenticated;out.awaiting_user=true;await save();
 // Bound the wait by creation time as well as the nine-minute user window;
 // the provider readback remains authoritative afterwards.
 const confirmationMs=confirmationWindow(destinationCreatedAt,Date.now());
 if(confirmationMs<=0)throw Error('verification window unavailable');
 console.log('AWAIT_USER_VERIFICATION');await waitForConfirmation(process.stdin,confirmationMs);out.awaiting_user=false;await save();
 config.DestinationID=destinationID;const verified=await phase(config,'verify-destination');assert.equal(verified.status,'verified','provider did not confirm verification');
 out.checks.push('destination-provider-verified');await rm(join(temp,'verification.eml'),{force:true});await save();
 ruleIssued=true;await phase(config,'create-rule');ruleCreated=true;out.checks.push('public-rule-readback');await save();
 const directPort=process.env.MAIL_CF_VPS_DIRECT_SMTP_PROXY;
 const resend=process.env.MAIL_CF_VPS_RESEND_SMTP==='1';
 const probePort=process.env.MAIL_CF_VPS_RECIPIENT_PROBE_PROXY;
 if(probePort){
  if(!resend||directPort||!/^(?:[1-9][0-9]{0,4})$/.test(probePort)||Number(probePort)>65535)throw Error('invalid recipient preflight configuration');
  const probe=spawnSync('python3',[resolve('test-env/fixtures/mail-cloudflare-vps-forward-live/send-direct.py'),'all',probePort,'probe'],{input:Buffer.from(JSON.stringify({recipient:config.Address,nonce})),timeout:120000});
  if(probe.status!==0||probe.error)throw Error(recipientProbeFailure(probe.stderr));
  out.checks.push('recipient-preflight-accepted-no-data');await save();
 }
 await issued('smtp-send-issued.json',{address:config.Address,nonce});
 if(directPort&&resend)throw Error('only one isolated SMTP source may be selected');
 if(directPort&&!/^(?:[1-9][0-9]{0,4})$/.test(directPort))throw Error('invalid explicit loopback SMTP proxy');
 const submitCommand=directPort?null:'python3 -c '+"'import base64;exec(base64.b64decode(\""+Buffer.from(await readFile(new URL(resend?'./send-resend.py':'./send-public.py',import.meta.url))).toString('base64')+"\"))'";
 const submitted=directPort
  ?spawnSync('python3',[resolve('test-env/fixtures/mail-cloudflare-vps-forward-live/send-direct.py'),'all',directPort],{input:Buffer.from(JSON.stringify({recipient:config.Address,nonce})),timeout:120000})
  :spawnSync('ssh',[...sshArgs,'whl@finance.hlong.wang',submitCommand],{input:Buffer.from(JSON.stringify({recipient:config.Address,nonce})),timeout:60000});
 if(submitted.status!==0||submitted.error){
  // Never relay arbitrary SSH/provider output into the report. Only these
  // fixed markers establish an explicit negative SMTP response.
  const lines=(submitted.stderr?.toString()||'').split(/\r?\n/);
  const marker=lines.find(line=>/^smtp-data-rejected:[45][0-9]{2}$/.test(line)||/^smtp-recipient-rejected:[45][0-9]{2}(?:\/[45]\.[0-9]{1,3}\.[0-9]{1,3})?(?:,[45][0-9]{2}(?:\/[45]\.[0-9]{1,3}\.[0-9]{1,3})?){0,5}$/.test(line)||['smtp-all-recipients-rejected-before-data','smtp-pre-data-failed','smtp-data-outcome-unknown'].includes(line));
  throw Error(marker||'SMTP submission unconfirmed; do not retry blindly');
 }
 out.smtp_source=directPort?'local-direct-mx-via-authorized-proxy':resend?'finance-resend':'finance-eusend';out.checks.push('smtp-submission-accepted');await save();
 const publicMessage=await phase(config,'read-public-mail');assert.equal(publicMessage.status,'received');
 out.checks.push('public-message-downloaded-unverified');await save();
 const verifyArgs=resend?['resend',config.Address]:directPort?['whlsxl@126.com']:[];
 const verifiedMessage=spawnSync('python3',[resolve('test-env/fixtures/mail-cloudflare-vps-forward-live/verify-public.py'),join(temp,'public-test.eml'),nonce,...verifyArgs],{encoding:'utf8',timeout:60000});
 if(verifiedMessage.status!==0||verifiedMessage.error){
  const marker=(verifiedMessage.stderr||'').trim();
  const safe=['synthetic-message-id-invalid','synthetic-message-id-mismatch','synthetic-subject-mismatch','synthetic-from-mismatch','synthetic-visible-to-mismatch','synthetic-body-mismatch','synthetic-attachment-mismatch'];
  throw Error(safe.includes(marker)?marker:'public-message-verification-unconfirmed');
 }
 if(resend){
  const verifiedLine=(verifiedMessage.stdout||'').trim();
  if(!['synthetic headers, body and attachment matched; visible To preserved','synthetic headers, body and attachment matched; visible To rewritten'].includes(verifiedLine))throw Error('public-message-verification-unconfirmed');
  out.visible_to=verifiedLine.endsWith('preserved')?'preserved':'rewritten';await save();
 }
 out.checks.push('public-synthetic-message-retrieved');await save();
 if(nativeMode){
  const signal=nativeReadyPayload(config.PreviousMessageID,publicMessage.message_id);
  // Atomic publication prevents finance from reading a partial identifier.
  ssh('whl@finance.hlong.wang',nativeReadyWriter(`${financeRemote}/bridge/public-ready.json`),{input:signal});
  out.checks.push('finance-public-message-signalled');await save();
  if(!await closed(native,420000))throw Error('finance native result timed out');
  if(nativeCode!==0)throw Error('finance native test failed');
  const result=JSON.parse(ssh('whl@finance.hlong.wang',`cat ${financeRemote}/final.json`).toString());
  assert.equal(result.failure,null);assert.deepEqual(result.cleanup_errors,[]);assert.equal(result.business_unchanged,true);assert.equal(result.native_intake_executed,false);
  assert.match(result.parent,/^\/home\/whl\/anas-mail-runtime-20261026\/anas-mail-runtime-test-anas-vps-native-public-[a-f0-9]{12}$/);
  const log=ssh('whl@finance.hlong.wang',`cat ${result.parent}/dms-fixture/roundcube-fixture/evidence/native.log`).toString();
  assert.match(log,/--- PASS: TestRoundcubeNativeMailProtocol\/cloudflare-vps-public-and-real-native-inbox /);
  assert.match(log,/--- PASS: TestRoundcubeNativeMailProtocol /);
  if(nativeWebhook){assert.match(log,/public VPS notifications recorded before native import/);out.checks.push('cloudflare-vps-webhook-finance-notice');}
  out.finance_evidence=result.parent;out.checks.push('cloudflare-vps-finance-dms-roundcube-single-target');await save();
 }
 }

}catch(e){out.errors.push(e.message);await save();}
finally{
 if(native&&native.exitCode===null&&native.signalCode===null){
  try{ssh('whl@finance.hlong.wang',`umask 077; touch ${financeRemote}/bridge/abort`);}catch{out.errors.push('finance abort signal unavailable');}
  if(!await closed(native,200000)){native.kill('SIGTERM');out.errors.push('finance fixture cleanup unknown');}
 }
 if(financeBridge&&financeBridge.exitCode===null&&financeBridge.signalCode===null){financeBridge.kill('SIGTERM');if(!await closed(financeBridge,10000))out.errors.push('finance bridge shutdown unknown');}
 for(const p of [notifySSH,cloudTunnel])if(p&&p.exitCode===null&&p.signalCode===null){p.kill('SIGTERM');if(!await closed(p,10000))out.errors.push('notification tunnel shutdown unknown');}
 if(osaTunnel&&osaTunnel.exitCode===null&&osaTunnel.signalCode===null){
  try{const finished=nextLine(osaTunnel,v=>{try{return JSON.parse(v).state==='cleaned'}catch{return false}},45000);osaTunnel.stdin.end('stop\n');await finished;out.cleanup.push('osa-notification-tunnel');}catch{osaTunnel.kill('SIGTERM');out.errors.push('OSA notification tunnel cleanup unknown');}
 }else if(osaTunnelCreated&&!osaTunnel){
  try{ssh('ubuntu@osa.letuh.com',`rm -f ${osaTunnelDir}/notify.crt ${osaTunnelDir}/empty.yml ${osaTunnelDir}/remote-tunnel.py && rmdir ${osaTunnelDir}`);out.cleanup.push('osa-notification-tunnel');}catch{out.errors.push('OSA notification tunnel cleanup unknown');}
 }else if(osaTunnelCreated){
  try{ssh('ubuntu@osa.letuh.com',`test ! -e ${osaTunnelDir}`);out.cleanup.push('osa-notification-tunnel');}catch{out.errors.push('OSA notification tunnel cleanup unknown');}
 }
 if(osaNotifySSH&&osaNotifySSH.exitCode===null&&osaNotifySSH.signalCode===null){osaNotifySSH.kill('SIGTERM');if(!await closed(osaNotifySSH,10000))out.errors.push('OSA notification forward shutdown unknown');}
 if(financeCreated&&(!native||nativeCode!==null)){
  try{ssh('whl@finance.hlong.wang',`rm -f ${financeRemote}/vps-public.json ${financeRemote}/bridge/fixture-mounted ${financeRemote}/bridge/relay-ready ${financeRemote}/bridge/routes-ready ${financeRemote}/bridge/public-ready.json ${financeRemote}/bridge/public-ready.json.new ${financeRemote}/bridge/abort ${financeRemote}/bridge/relay.sock ${financeRemote}/bridge/notify.sock ${financeRemote}/bridge/notify-cert.pem`);out.cleanup.push('finance-private-inputs');}catch{out.errors.push('finance private input cleanup unknown');}
 }
 let destinationSafe=!destinationIssued;
 out.awaiting_user=false;
 await rm(join(temp,'verification.eml'),{force:true});await rm(join(temp,'public-test.eml'),{force:true});
 if(ruleIssued&&ruleCreated){try{await phase(config,'delete-rule');out.cleanup.push('public-rule');}catch{out.errors.push('rule deletion unconfirmed');}}
 else if(ruleIssued)out.errors.push('rule creation outcome unknown; manual reconciliation required');
 if(destinationIssued&&destinationID&&!out.errors.includes('rule deletion unconfirmed')&&!out.errors.includes('rule creation outcome unknown; manual reconciliation required')){
  try{
   // Recheck that the public rule is absent before
   // deleting the exact recorded destination and then the test-only DNS.
   config.Phase='inspect-rule';config.Output=join(temp,'inspect-rule-cleanup.json');await writeFile(join(temp,'input.json'),JSON.stringify(config),{mode:0o600});const r=spawnSync(resolve(binary),['-test.run','^TestCloudflareVPSForwardLivePhase$','-test.timeout','120s'],{env:{...process.env,MAIL_CF_VPS_FORWARD_TEST_FILE:join(temp,'input.json')},timeout:130000});if(r.status!==0)throw Error();
  }catch{out.errors.push('rule cleanup preflight unconfirmed');}
  if(!out.errors.includes('rule cleanup preflight unconfirmed')){
   try{await stopFixture();const remaining=605000-(Date.now()-destinationCreatedAt);if(remaining>0){out.waiting_for_cleanup=true;await save();await delay(remaining);out.waiting_for_cleanup=false;}await cleanupDestination(destAPI,temp,{id:destinationID,address:config.Address,target});destinationSafe=true;out.cleanup.push('cloudflare-destination');}catch{out.errors.push('destination cleanup unconfirmed');}
  }
 }else if(destinationIssued&&!destinationID)out.errors.push('destination creation outcome unknown; manual reconciliation required');
 await stopFixture();
 if(dnsIssued&&destinationSafe){try{await cleanupTestDNS(dnsAPI,temp,tag);out.cleanup.push('temporary-dns');}catch{out.errors.push('DNS cleanup unconfirmed');}}
 else if(dnsIssued)out.errors.push('DNS retained pending destination reconciliation');
 await save();
 for(const name of ['input.json','ca.pem','key.pem','verification.eml'])await rm(join(temp,name),{force:true});out.cleanup.push('local-private-inputs');await save();
}
const passed=nativePrepareOnly
 ?out.checks.includes('finance-route-operation-confirmed')&&out.checks.includes('finance-preparation-aborted-and-cleaned')&&out.cleanup.includes('finance-private-inputs')&&out.cleanup.includes('osa-fixture')
 :out.checks.includes('public-synthetic-message-retrieved')&&(!nativeMode||out.checks.includes('cloudflare-vps-finance-dms-roundcube-single-target')&&out.cleanup.includes('finance-private-inputs'))&&out.cleanup.includes('osa-fixture')&&out.cleanup.includes('cloudflare-destination')&&out.cleanup.includes('temporary-dns');
if(out.errors.length||!passed){console.error('isolated Cloudflare verification delivery incomplete; see private report');process.exitCode=1;}
else console.log(nativePrepareOnly?'PASS isolated finance-to-OSA route preparation and abort; no public mail claim':nativeMode?'PASS isolated single-target Cloudflare to finance native inbox; standalone intake and Sieve not tested':'PASS isolated public forward to VPS source and cleanup; no DMS claim');
