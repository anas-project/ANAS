// TEST_CASES: MAILMOD-T-079
// OSA's real relay SMTP/storage/notification -> finance handler + PostgreSQL.
// Private test-only addresses, loopback ports and temporary directories only.
import assert from 'node:assert/strict';
import {readFile,writeFile,mkdtemp,rm} from 'node:fs/promises';
import {appendFileSync} from 'node:fs';
import {spawn,spawnSync} from 'node:child_process';
import {randomBytes,randomUUID,createHash} from 'node:crypto';
import net from 'node:net';
import https from 'node:https';
import {join} from 'node:path';

const relayBinary=process.env.MAIL_VPS_TEST_BINARY, receiverBinary=process.env.MAIL_NOTIFICATION_TEST_BINARY, report=process.env.MAIL_VPS_TEST_REPORT;
assert.ok(relayBinary&&receiverBinary&&report,'explicit test binaries and private report required');
const temp=await mkdtemp('/private/tmp/anas-vps-live-');
const state={checks:[],cleaned:{},errors:[]};
const source=randomUUID(),generation=randomUUID(),owner=randomUUID(),mainBox=randomUUID(),extraBox=randomUUID();
const secret=()=>randomBytes(32).toString('hex'),reader=secret(),control=secret(),notify=secret(),testToken=secret();
const routes=['main','alias','extra'].map((kind,i)=>({address:kind+'-'+randomBytes(4).toString('hex')+'@example.test',mailbox_id:i===2?extraBox:mainBox,owner_anchor:owner,claim_version:randomUUID()}));
const hash=v=>createHash('sha256').update(v).digest('hex');
const save=()=>writeFile(report,JSON.stringify(state,null,2),{mode:0o600});
function local(command,args,options={}){const p=spawnSync(command,args,{encoding:'utf8',timeout:45000,...options});if(p.status!==0||p.error)throw Error(command+' failed');return p.stdout;}
function ssh(host,command,options={}){return local('ssh',['-o','BatchMode=yes','-o','ConnectTimeout=15',host,command],options);}
function nextLine(child,predicate,timeout=120000){return new Promise((resolve,reject)=>{let pending='';const timer=setTimeout(()=>done(Error('process readiness timeout')),timeout);const receive=chunk=>{pending+=chunk.toString();if(pending.length>65536)return done(Error('process output exceeded limit'));for(;;){const i=pending.indexOf('\n');if(i<0)break;const line=pending.slice(0,i);pending=pending.slice(i+1);if(predicate(line))return done(null,line);}};const ended=()=>done(Error('process ended before readiness'));function done(error,value){clearTimeout(timer);child.stdout?.off('data',receive);child.stderr?.off('data',receive);child.off('exit',ended);error?reject(error):resolve(value);}child.stdout?.on('data',receive);child.stderr?.on('data',receive);child.once('exit',ended);});}
function freePort(){return new Promise((resolve,reject)=>{const s=net.createServer();s.once('error',reject);s.listen(0,'127.0.0.1',()=>{const p=s.address().port;s.close(()=>resolve(p));});});}
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
async function waitFor(fn,label){for(let n=0;n<75;n++){try{if(await fn())return}catch{}await sleep(2000)}throw Error(label+' unavailable');}
function request(port,ca,path,{method='GET',token='',body}={}){return new Promise((resolve,reject)=>{const req=https.request({hostname:'127.0.0.1',port,path,method,ca,timeout:12000,headers:{...(token?{Authorization:'Bearer '+token}:{}),...(body?{'Content-Type':'application/json'}:{})}},res=>{const parts=[];let size=0;res.on('data',p=>{size+=p.length;if(size>32<<20)req.destroy(Error('response too large'));else parts.push(p)});res.once('end',()=>resolve({status:res.statusCode,body:Buffer.concat(parts)}));});req.once('error',reject);req.once('timeout',()=>req.destroy(Error('request timeout')));req.end(body);});}
function tunnel(host,localPort,remotePort){const p=spawn('ssh',['-N','-o','BatchMode=yes','-o','ExitOnForwardFailure=yes','-o','ServerAliveInterval=15','-L',`127.0.0.1:${localPort}:127.0.0.1:${remotePort}`,host],{stdio:['ignore','ignore','pipe']});p.stderr.resume();return p;}
let financeDir='',osaDir='',financeChild,osaChild,financeTunnel,relayTunnel,smtpTunnel,cloudTunnel;
const finance='whl@finance.hlong.wang',osa='ubuntu@osa.letuh.com';
try{
 await save();await writeFile(join(temp,'empty.yml'),'{}\n',{mode:0o600});
 local('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-keyout',join(temp,'key.pem'),'-out',join(temp,'cert.pem'),'-days','1','-subj','/CN=relay.example.test','-addext','subjectAltName=IP:127.0.0.1,DNS:relay.example.test']);
 const cert=await readFile(join(temp,'cert.pem'),'utf8'),key=await readFile(join(temp,'key.pem'),'utf8');
 financeDir=ssh(finance,'umask 077; mktemp -d /tmp/anas-mail-live-XXXXXXXX').trim();assert.match(financeDir,/^\/tmp\/anas-mail-live-[A-Za-z0-9]+$/);
 ssh(finance,'umask 077; cat > '+financeDir+'/receiver; chmod 700 '+financeDir+'/receiver',{input:await readFile(receiverBinary)});
 ssh(finance,'umask 077; cat > '+financeDir+'/server.py',{input:await readFile(new URL('../mail-notification-live/server.py',import.meta.url))});
 financeChild=spawn('ssh',['-o','BatchMode=yes',finance,'python3 '+financeDir+'/server.py '+financeDir],{stdio:['pipe','pipe','pipe']});
 const financeReady=nextLine(financeChild,line=>{try{return JSON.parse(line).state==='ready'}catch{return false}},150000);
 financeChild.stdin.write(JSON.stringify({source,generation,secret:notify,test_token:testToken,cert,key})+'\n');
 const receiverPort=JSON.parse(await financeReady).port,localReceiver=await freePort();financeTunnel=tunnel(finance,localReceiver,receiverPort);
 await waitFor(async()=>(await request(localReceiver,cert,'/__test/health')).status===204,'finance receiver');
 cloudTunnel=spawn('cloudflared',['--config',join(temp,'empty.yml'),'tunnel','--no-autoupdate','--metrics','127.0.0.1:0','--protocol','http2','--url',`https://127.0.0.1:${localReceiver}`,'--origin-ca-pool',join(temp,'cert.pem')],{stdio:['ignore','pipe','pipe']});
 cloudTunnel.stderr.on('data',chunk=>appendFileSync(report+'.tunnel.log',chunk,{mode:0o600}));
 const tunnelLine=await nextLine(cloudTunnel,line=>/https:\/\/[a-z0-9-]+\.trycloudflare\.com/.test(line),120000);
 const origin=tunnelLine.match(/https:\/\/[a-z0-9-]+\.trycloudflare\.com/)[0];state.tunnel_origin=origin;await save();cloudTunnel.stdout.resume();cloudTunnel.stderr.resume();
 await waitFor(async()=>ssh(osa,'curl --silent --show-error --connect-timeout 5 --max-time 8 -o /dev/null -w "%{http_code}" '+origin+'/__test/health').trim()==='204','OSA to finance notification tunnel');
 state.checks.push('authenticated-https-from-osa-to-finance');await save();
 osaDir=ssh(osa,'umask 077; mktemp -d /tmp/anas-mail-vps-live-XXXXXXXX').trim();assert.match(osaDir,/^\/tmp\/anas-mail-vps-live-[A-Za-z0-9]+$/);state.osa_directory=osaDir;await save();
 ssh(osa,'umask 077; cat > '+osaDir+'/relay; chmod 700 '+osaDir+'/relay',{input:await readFile(relayBinary)});
 ssh(osa,'umask 077; cat > '+osaDir+'/remote.py',{input:await readFile(new URL('remote.py',import.meta.url))});
 osaChild=spawn('ssh',['-o','BatchMode=yes',osa,'python3 '+osaDir+'/remote.py '+osaDir],{stdio:['pipe','pipe','pipe']});
 const relayReady=nextLine(osaChild,line=>{try{return JSON.parse(line).state==='ready'}catch{return false}},90000);
 osaChild.stdin.write(JSON.stringify({source,generation,notify,reader,control,origin,cert,key})+'\n');
 const remote=JSON.parse(await relayReady),localRelay=await freePort(),localSMTP=await freePort();
 relayTunnel=tunnel(osa,localRelay,remote.https_port);smtpTunnel=tunnel(osa,localSMTP,remote.smtp_port);
 await waitFor(async()=>(await request(localRelay,cert,'/v1/status',{token:reader})).status===200,'relay source');
 const fingerprint=hash(JSON.stringify([...routes].sort((a,b)=>a.address.localeCompare(b.address))));
 const routeReq=JSON.stringify({source_id:source,generation,expected_revision:0,operation_id:randomUUID(),fingerprint,routes});
 const routeReply=await request(localRelay,cert,'/v1/routes',{method:'POST',token:control,body:routeReq});assert.equal(routeReply.status,200);
 const raw='From: sender@example.net\r\nTo: forged@foreign.test\r\nSubject: isolated VPS test\r\n\r\nNo production content.\r\n';
 local('python3',[new URL('send.py',import.meta.url).pathname],{input:JSON.stringify({port:localSMTP,cert_path:join(temp,'cert.pem'),addresses:routes.map(r=>r.address),invalid_address:'unregistered-'+randomBytes(4).toString('hex')+'@example.test',message:raw})});
 state.checks.push('real-starttls-smtp-to-osa');await save();
 const status=async()=>{const r=await request(localReceiver,cert,'/__test/status',{token:testToken});assert.equal(r.status,200);return JSON.parse(r.body)};
 await waitFor(async()=>(await status()).notices.length===3,'finance durable notifications');
 const notices=await status();assert.equal(notices.notices.length,3);assert.ok(notices.wakes>=3);assert.ok(notices.notices.every(n=>n.state==='pending'));
 let page;await waitFor(async()=>{const listing=await request(localRelay,cert,'/v1/messages?limit=100',{token:reader});assert.equal(listing.status,200);page=JSON.parse(listing.body);return page.messages.length===3&&page.messages.every(m=>m.notification?.state==='notified'&&m.notification.http_status===202)},'persisted notification receipt');assert.equal((await request(localRelay,cert,'/v1/messages?limit=100',{token:secret()})).status,401);
 for(const route of routes){const found=page.messages.filter(m=>m.metadata.accepted_address===route.address);assert.equal(found.length,1);const m=found[0];assert.equal(m.metadata.mailbox_id,route.mailbox_id);assert.equal(m.metadata.owner_anchor,route.owner_anchor);assert.equal(m.metadata.claim_version,route.claim_version);assert.ok(!m.delivery);assert.ok(notices.notices.some(n=>n.message_id===m.metadata.message_id&&n.mailbox_id===m.metadata.mailbox_id));const original=await request(localRelay,cert,'/v1/messages/'+m.metadata.message_id+'/raw?target='+m.metadata.mailbox_id,{token:reader});assert.equal(original.status,200);assert.equal(hash(original.body),hash(raw));}
 state.checks.push('vps-original-and-three-correct-targets','smtp-rejects-unknown-and-reader-rejects-wrong-token','vps-notification-receipts','signed-webhooks-durable-on-finance');await save();
 // Remove only the dedicated SSH forwarding process. Cloudflare remains up,
 // but the receiver is unavailable until the identical forwarding is restored.
 const tunnelStopped=new Promise(resolve=>financeTunnel.once('exit',resolve));financeTunnel.kill('SIGTERM');await tunnelStopped;financeTunnel=null;
 local('python3',[new URL('send.py',import.meta.url).pathname],{input:JSON.stringify({port:localSMTP,cert_path:join(temp,'cert.pem'),addresses:[routes[0].address],invalid_address:'unregistered-'+randomBytes(4).toString('hex')+'@example.test',message:raw})});
 let failure;await waitFor(async()=>{const r=await request(localRelay,cert,'/v1/messages?limit=100',{token:reader});const list=JSON.parse(r.body).messages;failure=list.find(m=>m.notification?.state==='failed');return list.length===4&&failure?.notification.attempts===1},'persisted webhook failure');
 const retained=await request(localRelay,cert,'/v1/messages/'+failure.metadata.message_id+'/raw?target='+failure.metadata.mailbox_id,{token:reader});assert.equal(retained.status,200);assert.equal(hash(retained.body),hash(raw));
 financeTunnel=tunnel(finance,localReceiver,receiverPort);await waitFor(async()=>(await request(localReceiver,cert,'/__test/health')).status===204,'restored finance receiver');
 await waitFor(async()=>{const r=await request(localRelay,cert,'/v1/messages?limit=100',{token:reader});const m=JSON.parse(r.body).messages.find(x=>x.metadata.message_id===failure.metadata.message_id);return m?.notification?.state==='notified'&&m.notification.attempts>=2},'webhook retry after receiver recovery');
 const recovered=await status();assert.equal(recovered.notices.length,4);assert.ok(recovered.notices.some(n=>n.message_id===failure.metadata.message_id));state.checks.push('webhook-failure-retains-original-and-retries-after-recovery');await save();
}catch(e){state.errors.push(e.message);await save();}
finally{
 for(const p of [cloudTunnel,financeTunnel,relayTunnel,smtpTunnel])if(p)p.kill('SIGTERM');
 if(osaChild){try{const done=nextLine(osaChild,l=>{try{return JSON.parse(l).state==='cleaned'}catch{return false}},45000);osaChild.stdin.end('stop\n');await done;state.cleaned.osa=true}catch(e){state.errors.push('osa-cleanup: '+e.message);osaChild.kill('SIGTERM')}}
 if(financeChild){try{const done=nextLine(financeChild,l=>{try{return JSON.parse(l).state==='cleaned'}catch{return false}},45000);financeChild.stdin.end('stop\n');await done;state.cleaned.finance=true}catch(e){state.errors.push('finance-cleanup: '+e.message);financeChild.kill('SIGTERM')}}
 if(financeDir&&/^\/tmp\/anas-mail-live-[A-Za-z0-9]+$/.test(financeDir)&&state.cleaned.finance){try{ssh(finance,'rm -f '+financeDir+'/receiver '+financeDir+'/server.py && rmdir '+financeDir);state.cleaned.finance_directory=true}catch(e){state.errors.push('finance-directory-cleanup: '+e.message)}}
 await rm(temp,{recursive:true,force:true});await save();
}
if(state.errors.length||!state.checks.includes('webhook-failure-retains-original-and-retries-after-recovery')||!state.cleaned.osa||!state.cleaned.finance||!state.cleaned.finance_directory){console.error('isolated VPS notification test incomplete; see private report');process.exitCode=1}else console.log('PASS isolated SMTP -> OSA relay -> signed HTTPS -> finance PostgreSQL notification subset');
