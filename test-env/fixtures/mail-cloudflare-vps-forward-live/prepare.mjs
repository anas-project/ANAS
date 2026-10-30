// TEST_CASES: MAILMOD-T-009
// Validate the isolated IPv6 relay and its production route client before any
// DNS/provider changes. The only message is synthetic and stays on OSA.
import assert from 'node:assert/strict';
import {readFile,writeFile,mkdtemp,rm} from 'node:fs/promises';
import {appendFileSync} from 'node:fs';
import {spawn,spawnSync} from 'node:child_process';
import {createHash,randomBytes,randomUUID} from 'node:crypto';
import https from 'node:https';
import net from 'node:net';
import {join,resolve} from 'node:path';

const relay=process.env.MAIL_CF_VPS_RELAY, binary=process.env.MAIL_CF_VPS_TEST_BINARY, report=process.env.MAIL_CF_VPS_PREPARE_REPORT;
assert.ok(relay&&binary&&report,'explicit isolated binaries and report required');
const sha=b=>createHash('sha256').update(b).digest('hex');
const relayBytes=await readFile(relay), relayHash=sha(relayBytes), binaryHash=sha(await readFile(binary));
const temp=await mkdtemp('/private/tmp/anas-cf-vps-prepare-'),suffix=randomBytes(6).toString('hex'),remote='/tmp/anas-mail-cf-vps-'+suffix;
const source=randomUUID(),generation=randomUUID(),reader=randomBytes(32).toString('hex'),control=randomBytes(32).toString('hex');
const sshArgs=['-o','BatchMode=yes','-o','ConnectTimeout=15','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=3'];
const out={checks:[],cleanup:[],errors:[],remote_directory:remote,relay_sha256:relayHash,test_binary_sha256:binaryHash};
let created=false,remoteProcess,bridge;
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
 const config={Source:source,Generation:generation,Origin:`https://127.0.0.1:${local}`,Reader:reader,Control:control,CAFile:join(temp,'ca.pem'),Account:'aa7aa9753a4fffabb1d6536bcf62fb95',Zone:'0d84033826bc3cd0c283efdb57a90f7a',DestinationToken:'d'.repeat(40),RoutingToken:'e'.repeat(40),Address:`cf-vps-test-${suffix}@mail-poc.bengbu.cc.cd`,MailboxID:randomUUID(),OwnerAnchor:randomUUID(),ClaimVersion:randomUUID(),Operation:randomUUID()};
 const prepared=await phase(config,'prepare-routes');assert.match(prepared.target,/^anas-[a-f0-9]{32}@vps-target\.mail-poc\.bengbu\.cc\.cd$/);out.checks.push('osa-isolated-forward-route');await save();
 const smtp=`import smtplib\ns=smtplib.SMTP("2603:c023:8:a900::a",25,timeout=10)\ns.sendmail("sender@example.test",["${prepared.target}"],"From: sender@example.test\\r\\nTo: unrelated@example.test\\r\\nSubject: synthetic relay preparation\\r\\n\\r\\nsynthetic only\\r\\n")\ns.quit()\n`;
 ssh('whl@finance.hlong.wang','python3 -',{input:Buffer.from(smtp)});out.checks.push('finance-to-osa-smtp');await save();
 const received=await phase(config,'read-verification');assert.equal(received.status,'received');out.checks.push('source-bound-message-retrieved');await save();
}catch(e){out.errors.push(e.message);await save();}
finally{
 if(bridge&&bridge.exitCode===null&&bridge.signalCode===null){bridge.kill('SIGTERM');if(!await closed(bridge,10000))out.errors.push('bridge shutdown unknown');}
 if(remoteProcess&&remoteProcess.exitCode===null&&remoteProcess.signalCode===null){
  try{const clean=nextLine(remoteProcess,line=>{try{return JSON.parse(line).state==='cleaned'}catch{return false}},45000);remoteProcess.stdin.end('stop\n');await clean;out.cleanup.push('osa-fixture');}catch{remoteProcess.kill('SIGTERM');out.errors.push('OSA cleanup unknown');}
 }else if(created&&!remoteProcess){
  try{ssh('ubuntu@osa.letuh.com',`rm -f ${remote}/relay ${remote}/remote.py && rmdir ${remote}`);out.cleanup.push('osa-fixture');}catch{out.errors.push('OSA unused directory cleanup unknown');}
 }else if(created&&remoteProcess){
  try{ssh('ubuntu@osa.letuh.com',`test ! -e ${remote}`);out.cleanup.push('osa-fixture');}catch{out.errors.push('OSA exited fixture cleanup unknown');}
 }
 await rm(temp,{recursive:true,force:true});out.cleanup.push('local-private-inputs');await save();
}
if(out.errors.length||out.checks.length!==3||!out.cleanup.includes('osa-fixture')){console.error('isolated relay preparation failed; see private report');process.exitCode=1;}
else console.log('PASS isolated IPv6 forward route, SMTP and source retrieval');
