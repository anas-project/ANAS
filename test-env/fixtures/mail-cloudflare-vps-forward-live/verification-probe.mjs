// TEST_CASES: MAILMOD-T-009
// Validate the isolated IPv6 relay and its production route client before any
// DNS/provider changes. The only message is synthetic and stays on OSA.
import assert from 'node:assert/strict';
import {readFile,writeFile,mkdtemp,rm} from 'node:fs/promises';
import {appendFileSync} from 'node:fs';
import {cloudflareDNS,createTestDNS,cleanupTestDNS,newTestTag} from './dns.mjs';
import {cloudflareDestination,cleanupDestination} from './destination-cleanup.mjs';
import {spawn,spawnSync} from 'node:child_process';
import {createHash,randomBytes,randomUUID} from 'node:crypto';
import https from 'node:https';
import net from 'node:net';
import {join,resolve} from 'node:path';

const relay=process.env.MAIL_CF_VPS_RELAY, binary=process.env.MAIL_CF_VPS_TEST_BINARY, report=process.env.MAIL_CF_VPS_LIVE_REPORT;
assert.ok(relay&&binary&&report,'explicit isolated binaries and report required');
const sha=b=>createHash('sha256').update(b).digest('hex');
const relayBytes=await readFile(relay), relayHash=sha(relayBytes), binaryHash=sha(await readFile(binary));
const temp=await mkdtemp('/private/tmp/anas-cf-vps-prepare-'),suffix=randomBytes(6).toString('hex'),remote='/tmp/anas-mail-cf-vps-'+suffix;
const source=randomUUID(),generation=randomUUID(),reader=randomBytes(32).toString('hex'),control=randomBytes(32).toString('hex');
const sshArgs=['-o','BatchMode=yes','-o','ConnectTimeout=15','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=3'];
const tag=newTestTag();
const out={checks:[],cleanup:[],errors:[],remote_directory:remote,journal_directory:temp,relay_sha256:relayHash,test_binary_sha256:binaryHash};
let created=false,remoteProcess,bridge,config,dnsAPI,destAPI,target,destinationID,dnsIssued=false,destinationIssued=false;
const save=()=>writeFile(report,JSON.stringify(out,null,2)+'\n',{mode:0o600});
function command(name,args,options={}){const r=spawnSync(name,args,{timeout:60000,...options});if(r.status!==0||r.error)throw Error(name+' command failed');return r.stdout;}
function ssh(host,cmd,options={}){return command('ssh',[...sshArgs,host,cmd],options);}
function nextLine(child,predicate,timeout){return new Promise((resolve,reject)=>{let pending='';const timer=setTimeout(()=>finish(Error('remote readiness timeout')),timeout);const data=b=>{pending+=b.toString();if(pending.length>65536)return finish(Error('remote output too large'));for(;;){const i=pending.indexOf('\n');if(i<0)break;const line=pending.slice(0,i);pending=pending.slice(i+1);if(predicate(line))return finish(null,line);}};const end=()=>finish(Error('remote process ended'));function finish(e,v){clearTimeout(timer);child.stdout.off('data',data);child.off('exit',end);e?reject(e):resolve(v);}child.stdout.on('data',data);child.once('exit',end);});}
function closed(p,ms){return new Promise(resolve=>{if(!p||p.exitCode!==null||p.signalCode!==null)return resolve(true);const timer=setTimeout(()=>resolve(false),ms);p.once('close',()=>{clearTimeout(timer);resolve(true);});});}
async function freePort(){return new Promise((resolve,reject)=>{const s=net.createServer();s.once('error',reject);s.listen(0,'127.0.0.1',()=>{const p=s.address().port;s.close(()=>resolve(p));});});}
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
function healthy(port,ca){return new Promise(resolve=>{const req=https.get({hostname:'127.0.0.1',port,path:'/v1/status',ca,headers:{Authorization:'Bearer '+reader},timeout:2500},r=>{r.resume();resolve(r.statusCode===200)});req.on('error',()=>resolve(false));req.on('timeout',()=>req.destroy());});}
async function phase(config,name){config.Phase=name;config.Output=join(temp,name+'.json');await writeFile(join(temp,'input.json'),JSON.stringify(config),{mode:0o600});const r=spawnSync(resolve(binary),['-test.v','-test.run','^TestCloudflareVPSForwardLivePhase$','-test.timeout','120s'],{env:{...process.env,MAIL_CF_VPS_FORWARD_TEST_FILE:join(temp,'input.json')},timeout:130000});if(r.status!==0||r.error)throw Error('isolated route client '+name+' failed');return JSON.parse(await readFile(config.Output,'utf8'));}
try{
 await save();
 command('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-keyout',join(temp,'key.pem'),'-out',join(temp,'ca.pem'),'-days','1','-subj','/CN=127.0.0.1','-addext','subjectAltName=IP:127.0.0.1,DNS:vps-relay.mail-poc.bengbu.cc.cd'],{stdio:'ignore'});
 const cert=await readFile(join(temp,'ca.pem'),'utf8'),key=await readFile(join(temp,'key.pem'),'utf8');
 ssh('ubuntu@osa.letuh.com',`umask 077; mkdir -m 700 ${remote}`);created=true;
 ssh('ubuntu@osa.letuh.com',`umask 077; cat > ${remote}/relay`,{input:relayBytes});
 ssh('ubuntu@osa.letuh.com',`chmod 700 ${remote}/relay`);
 ssh('ubuntu@osa.letuh.com',`umask 077; cat > ${remote}/remote.py`,{input:await readFile(new URL('./remote.py',import.meta.url))});
 remoteProcess=spawn('ssh',[...sshArgs,'ubuntu@osa.letuh.com',`python3 ${remote}/remote.py ${remote} ${relayHash}`],{stdio:['pipe','pipe','pipe']});
 remoteProcess.stderr.on('data',b=>appendFileSync(report+'.remote.log',b,{mode:0o600}));const ready=nextLine(remoteProcess,line=>{try{return JSON.parse(line).state==='ready'}catch{return false}},90000);
 remoteProcess.stdin.write(JSON.stringify({source,generation,reader,control,cert,key})+'\n');
 const response=JSON.parse(await ready);assert.ok(Number.isInteger(response.https_port));
 const local=await freePort();bridge=spawn('ssh',[...sshArgs,'-o','ExitOnForwardFailure=yes','-N','-L',`127.0.0.1:${local}:127.0.0.1:${response.https_port}`,'ubuntu@osa.letuh.com'],{stdio:['ignore','ignore','pipe']});bridge.stderr.resume();
 let ok=false;for(let i=0;i<30;i++){if(await healthy(local,cert)){ok=true;break}await delay(1000)}assert.ok(ok,'source tunnel unavailable');
 const manager=JSON.parse(await readFile('test-env/reports/mail-ingress-20261024/cloudflare-token.json','utf8'));
 const destination=JSON.parse(await readFile('/private/tmp/anas-forward-destination-20261028/token.json','utf8'));
 const routing=JSON.parse(await readFile('test-env/reports/mail-runtime-20261026/cloudflare-routing-key.json','utf8'));
 dnsAPI=cloudflareDNS(manager.token);destAPI=cloudflareDestination(destination.token);
 config={Source:source,Generation:generation,Origin:`https://127.0.0.1:${local}`,Reader:reader,Control:control,CAFile:join(temp,'ca.pem'),Account:'aa7aa9753a4fffabb1d6536bcf62fb95',Zone:'0d84033826bc3cd0c283efdb57a90f7a',DestinationToken:destination.token,RoutingToken:routing.token,Address:`cf-vps-test-${suffix}@mail-poc.bengbu.cc.cd`,MailboxID:randomUUID(),OwnerAnchor:randomUUID(),ClaimVersion:randomUUID(),Operation:randomUUID()};
 const prepared=await phase(config,'prepare-routes');target=prepared.target;assert.match(target,/^anas-[a-f0-9]{32}@vps-target\.mail-poc\.bengbu\.cc\.cd$/);out.checks.push('osa-isolated-forward-route');await save();
 await phase(config,'destination-preflight');await phase(config,'inspect-rule');out.checks.push('provider-preflight');await save();
 dnsIssued=true;await createTestDNS(dnsAPI,temp,tag);out.checks.push('temporary-dns-created');await save();
 // The provider's verification message is sent as part of creation. Persisted
 // intent and ID are retained in the private journal on an unknown response.
 destinationIssued=true;const result=await phase(config,'create-destination');destinationID=result.destination_id;
 assert.match(destinationID,/^[a-f0-9]{32}$/);out.checks.push('destination-created');await save();
 const received=await phase(config,'read-verification');assert.equal(received.status,'received');
 const metadata=JSON.parse(command('python3',[resolve('test-env/fixtures/mail-cloudflare-vps-forward-live/verification-meta.py'),join(temp,'verification.eml')],{encoding:'utf8'}));
 await writeFile(join(temp,'verification-metadata.json'),JSON.stringify(metadata)+'\n',{mode:0o600});
 out.checks.push('message-received-sender-unverified');await save();

}catch(e){out.errors.push(e.message);await save();}
finally{
 let destinationSafe=!destinationIssued;
 if(destinationIssued&&destinationID){
  try{
   // No public rule was issued in this probe. Recheck its absence before
   // deleting the exact recorded destination and then the test-only DNS.
   config.Phase='inspect-rule';config.Output=join(temp,'inspect-rule-cleanup.json');await writeFile(join(temp,'input.json'),JSON.stringify(config),{mode:0o600});const r=spawnSync(resolve(binary),['-test.run','^TestCloudflareVPSForwardLivePhase$','-test.timeout','120s'],{env:{...process.env,MAIL_CF_VPS_FORWARD_TEST_FILE:join(temp,'input.json')},timeout:130000});if(r.status!==0)throw Error();
  }catch{out.errors.push('rule cleanup preflight unconfirmed');}
  if(!out.errors.includes('rule cleanup preflight unconfirmed')){
   try{await cleanupDestination(destAPI,temp,{id:destinationID,address:config.Address,target});destinationSafe=true;out.cleanup.push('cloudflare-destination');}catch{out.errors.push('destination cleanup unconfirmed');}
  }
 }else if(destinationIssued)out.errors.push('destination creation outcome unknown; manual reconciliation required');
 if(dnsIssued&&destinationSafe){try{await cleanupTestDNS(dnsAPI,temp,tag);out.cleanup.push('temporary-dns');}catch{out.errors.push('DNS cleanup unconfirmed');}}
 else if(dnsIssued)out.errors.push('DNS retained pending destination reconciliation');
 await save();
 if(bridge&&bridge.exitCode===null&&bridge.signalCode===null){bridge.kill('SIGTERM');if(!await closed(bridge,10000))out.errors.push('bridge shutdown unknown');}
 if(remoteProcess&&remoteProcess.exitCode===null&&remoteProcess.signalCode===null){
  try{const clean=nextLine(remoteProcess,line=>{try{return JSON.parse(line).state==='cleaned'}catch{return false}},45000);remoteProcess.stdin.end('stop\n');await clean;out.cleanup.push('osa-fixture');}catch{remoteProcess.kill('SIGTERM');out.errors.push('OSA cleanup unknown');}
 }else if(created&&!remoteProcess){
  try{ssh('ubuntu@osa.letuh.com',`rm -f ${remote}/relay ${remote}/remote.py && rmdir ${remote}`);out.cleanup.push('osa-fixture');}catch{out.errors.push('OSA unused directory cleanup unknown');}
 }else if(created&&remoteProcess){
  try{ssh('ubuntu@osa.letuh.com',`test ! -e ${remote}`);out.cleanup.push('osa-fixture');}catch{out.errors.push('OSA exited fixture cleanup unknown');}
 }
 for(const name of ['input.json','ca.pem','key.pem','verification.eml'])await rm(join(temp,name),{force:true});out.cleanup.push('local-private-inputs');await save();
}
if(out.errors.length||!out.checks.includes('message-received-sender-unverified')||!out.cleanup.includes('osa-fixture')||!out.cleanup.includes('cloudflare-destination')||!out.cleanup.includes('temporary-dns')){console.error('isolated Cloudflare verification delivery incomplete; see private report');process.exitCode=1;}
else console.log('PASS isolated message retrieval and cleanup; sender and public rule not verified');
