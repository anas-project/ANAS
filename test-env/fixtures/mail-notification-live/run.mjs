// TEST_CASES: MAILMOD-T-078
// Real Cloudflare Worker/R2 -> temporary HTTPS tunnel -> formal finance
// notification handler and isolated PostgreSQL. EmailEvent is synthetic.
import assert from 'node:assert/strict';
import {readFile,writeFile,stat,mkdtemp,rm} from 'node:fs/promises';
import {appendFileSync} from 'node:fs';
import {spawn,spawnSync} from 'node:child_process';
import {randomBytes,randomUUID,createHash,createHmac} from 'node:crypto';
import net from 'node:net';
import https from 'node:https';
import {join} from 'node:path';

const credentialPath=process.env.MAIL_CLOUDFLARE_TEST_CREDENTIAL_FILE;
const binary=process.env.MAIL_NOTIFICATION_TEST_BINARY;
const report=process.env.MAIL_NOTIFICATION_TEST_REPORT;
assert.ok(credentialPath&&binary&&report,'explicit private credential, binary and report required');
assert.equal((await stat(credentialPath)).mode&0o077,0);
const credential=JSON.parse(await readFile(credentialPath,'utf8'));
assert.match(credential.account_id,/^[a-f0-9]{32}$/);
const temp=await mkdtemp('/private/tmp/anas-notify-live-');
const hash=x=>createHash('sha256').update(x).digest('hex');
const secret=()=>randomBytes(32).toString('hex');
const state={checks:[],created:{},cleaned:{},errors:[]};
let remote='',remoteChild,sshTunnel,proxyTunnel,cloudTunnel,proxyPort,base,workerID,createdWorker=false,createdBucket=false,objectsClean=false;
const save=()=>writeFile(report,JSON.stringify(state,null,2),{mode:0o600});
function local(command,args,options={}){const p=spawnSync(command,args,{encoding:'utf8',timeout:45000,...options});if(p.status!==0||p.error)throw Error(command+' failed');return p.stdout;}
const sshArgs=['-o','BatchMode=yes','-o','ConnectTimeout=15','whl@finance.hlong.wang'];
function ssh(command,options={}){return local('ssh',[...sshArgs,command],options);}
function nextLine(child,predicate,timeout=120000){return new Promise((resolve,reject)=>{let pending='';const timer=setTimeout(()=>done(Error('process readiness timeout')),timeout);const receive=chunk=>{pending+=chunk.toString();if(pending.length>65536)return done(Error('process output exceeded limit'));for(;;){const i=pending.indexOf('\n');if(i<0)break;const line=pending.slice(0,i);pending=pending.slice(i+1);if(predicate(line))return done(null,line);}};const ended=()=>done(Error('process ended before readiness'));function done(error,value){clearTimeout(timer);child.stdout?.off('data',receive);child.stderr?.off('data',receive);child.off('exit',ended);error?reject(error):resolve(value);}child.stdout?.on('data',receive);child.stderr?.on('data',receive);child.once('exit',ended);});}
function freePort(){return new Promise((resolve,reject)=>{const server=net.createServer();server.once('error',reject);server.listen(0,'127.0.0.1',()=>{const p=server.address().port;server.close(()=>resolve(p));});});}
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
const timeoutSignal=()=>AbortSignal.timeout(30000);
async function api(path,method='GET',body){const r=await fetch('https://api.cloudflare.com/client/v4'+path,{method,headers:{Authorization:'Bearer '+credential.token,...(body?{'Content-Type':'application/json'}:{})},body:body?JSON.stringify(body):undefined,redirect:'error',signal:timeoutSignal()});let j;try{j=await r.json()}catch{throw Error('Cloudflare response invalid: '+r.status)};if(!r.ok||!j.success)throw Error('Cloudflare '+method+' returned '+r.status+' codes '+(j.errors||[]).map(e=>e.code).join(','));return j.result;}
async function apiStatus(path,method='GET'){const r=await fetch('https://api.cloudflare.com/client/v4'+path,{method,headers:{Authorization:'Bearer '+credential.token},redirect:'error',signal:timeoutSignal()});return r.status;}
function trustedHealth(port,cert){return new Promise((resolve,reject)=>{const req=https.get({hostname:'127.0.0.1',port,path:'/__test/health',ca:cert,timeout:3000},r=>{r.resume();resolve(r.statusCode)});req.on('error',reject);req.on('timeout',()=>req.destroy(Error('timeout')));});}
async function waitFor(fn,label){for(let n=0;n<75;n++){try{if(await fn())return}catch{}await sleep(2000)}throw Error(label+' unavailable');}
// Curl reads secrets from a 0600 private config, never process arguments.
// SOCKS resolves the public hostname on OSA and retains end-to-end TLS checks.
async function proxiedFetch(url,{method='GET',headers={},body}={}){
 const parsed=new URL(url);if(parsed.protocol!=='https:'||parsed.username||parsed.password)throw Error('invalid proxy target');
 const dir=await mkdtemp(join(temp,'request-'));const config=join(dir,'curl.conf'),output=join(dir,'response'),bodyFile=join(dir,'body');
 const quoted=x=>{x=String(x);if(/[\r\n\0]/.test(x))throw Error('invalid request option');return '"'+x.replaceAll('\\','\\\\').replaceAll('"','\\"')+'"'};
 try{
  const lines=['socks5-hostname = '+quoted('127.0.0.1:'+proxyPort),'url = '+quoted(url),'request = '+quoted(method),'max-time = 30','connect-timeout = 10'];
  for(const [k,v] of Object.entries(headers))lines.push('header = '+quoted(k+': '+v));
  if(body!==undefined){await writeFile(bodyFile,body,{mode:0o600});lines.push('data-binary = '+quoted('@'+bodyFile));}
  await writeFile(config,lines.join('\n')+'\n',{mode:0o600});
  const code=await new Promise((resolve,reject)=>{const child=spawn('curl',['--silent','--show-error','--config',config,'--output',output,'--write-out','%{http_code}'],{stdio:['ignore','pipe','ignore']});let value='';child.stdout.on('data',d=>{value+=d.toString()});child.once('error',reject);child.once('exit',status=>status===0?resolve(value):reject(Error('HTTPS proxy request failed')))});
  const status=Number(code);if(!Number.isInteger(status)||status<200||status>599)throw Error('invalid HTTPS proxy response');
  const content=await readFile(output);if(content.length>32<<20)throw Error('HTTPS response too large');
  return new Response([204,205,304].includes(status)?null:content,{status});
 }finally{await rm(dir,{recursive:true,force:true})}
}
const account='/accounts/'+credential.account_id,name='anas-notify-live-'+randomBytes(10).toString('hex');
const source=randomUUID(),generation=randomUUID(),owner=randomUUID(),box=randomUUID(),extraBox=randomUUID(),reader=secret(),control=secret(),notify=secret(),testToken=secret(),fixtureToken=secret();
const domain='mail-poc.bengbu.cc.cd';
const routes=['main','alias','extra'].map((kind,index)=>({address:`${kind}-${randomBytes(5).toString('hex')}@${domain}`,mailbox_id:index===2?extraBox:box,owner_anchor:owner,claim_version:randomUUID()}));
state.name=name;state.source=source;state.generation=generation;
try{
 await save();assert.equal((await api('/user/tokens/verify')).status,'active');
 await writeFile(join(temp,'empty-cloudflared.yml'),'{}\n',{mode:0o600});
 local('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-keyout',join(temp,'key.pem'),'-out',join(temp,'cert.pem'),'-days','1','-subj','/CN=127.0.0.1','-addext','subjectAltName=IP:127.0.0.1']);
 const cert=await readFile(join(temp,'cert.pem'),'utf8'),key=await readFile(join(temp,'key.pem'),'utf8');
 remote=ssh('umask 077; mktemp -d /tmp/anas-mail-live-XXXXXXXX').trim();assert.match(remote,/^\/tmp\/anas-mail-live-[A-Za-z0-9]+$/);
 ssh('umask 077; cat > '+remote+'/receiver; chmod 700 '+remote+'/receiver',{input:await readFile(binary)});
 ssh('umask 077; cat > '+remote+'/server.py',{input:await readFile(new URL('server.py',import.meta.url))});
 remoteChild=spawn('ssh',[...sshArgs,'python3 '+remote+'/server.py '+remote],{stdio:['pipe','pipe','pipe']});
 const readyPromise=nextLine(remoteChild,line=>{try{return JSON.parse(line).state==='ready'}catch{return false}},150000);
 remoteChild.stdin.write(JSON.stringify({source,generation,secret:notify,test_token:testToken,cert,key})+'\n');
 const remotePort=JSON.parse(await readyPromise).port;state.checks.push('isolated-postgresql-and-formal-receiver-ready');await save();
 const localPort=await freePort();sshTunnel=spawn('ssh',['-N','-o','BatchMode=yes','-o','ExitOnForwardFailure=yes','-o','ServerAliveInterval=15','-L',`127.0.0.1:${localPort}:127.0.0.1:${remotePort}`,'whl@finance.hlong.wang'],{stdio:['ignore','ignore','pipe']});sshTunnel.stderr.resume();
 await waitFor(async()=>await trustedHealth(localPort,cert)===204,'authenticated local HTTPS origin');
 proxyPort=await freePort();proxyTunnel=spawn('ssh',['-N','-o','BatchMode=yes','-o','ExitOnForwardFailure=yes','-o','ServerAliveInterval=15','-D',`127.0.0.1:${proxyPort}`,'ubuntu@osa.letuh.com'],{stdio:['ignore','ignore','pipe']});proxyTunnel.stderr.resume();
 await waitFor(async()=>new Promise(resolve=>{const s=net.connect(proxyPort,'127.0.0.1');s.once('connect',()=>{s.destroy();resolve(true)});s.once('error',()=>resolve(false));}), 'isolated OSA SOCKS proxy');
 cloudTunnel=spawn('cloudflared',['--config',join(temp,'empty-cloudflared.yml'),'tunnel','--no-autoupdate','--metrics','127.0.0.1:0','--protocol','http2','--url',`https://127.0.0.1:${localPort}`,'--origin-ca-pool',join(temp,'cert.pem')],{stdio:['ignore','pipe','pipe']});
 cloudTunnel.stderr.on('data',chunk=>appendFileSync(report+'.tunnel.log',chunk,{mode:0o600}));
 const tunnelLine=await nextLine(cloudTunnel,line=>/https:\/\/[a-z0-9-]+\.trycloudflare\.com/.test(line),120000);
 const origin=tunnelLine.match(/https:\/\/[a-z0-9-]+\.trycloudflare\.com/)[0];state.tunnel_origin=origin;await save();
 cloudTunnel.stdout.resume();cloudTunnel.stderr.resume();
 await waitFor(async()=>{try{const r=await proxiedFetch(origin+'/__test/health');state.tunnel_probe='http-'+r.status;return r.status===204}catch(e){state.tunnel_probe=e.cause?.code||e.name;return false}},'temporary HTTPS notification tunnel');state.checks.push('verified-origin-tls-and-public-https-tunnel');await save();
 const existing=(await api(account+'/r2/buckets')).buckets;assert.ok(!existing.some(v=>v.name===name));
 await api(account+'/r2/buckets','POST',{name,storageClass:'Standard'});createdBucket=true;state.created.bucket=true;await save();
 const created=await api(account+'/workers/workers','POST',{name});assert.equal(created.name,name);assert.match(created.id,/^[a-f0-9]{32}$/);workerID=created.id;createdWorker=true;state.created.worker_id=workerID;await save();
 const own=await api(account+'/workers/workers/'+workerID);assert.equal(own.id,workerID);assert.equal(own.name,name);
 const sourceFile=await readFile('modules/mail/cloudflare/worker.mjs','utf8'),notification=await readFile('modules/mail/cloudflare/notification.mjs','utf8'),fixture=await readFile(new URL('fixture.mjs',import.meta.url),'utf8');
 const module=(name,text)=>({name,content_type:'application/javascript+module',content_base64:Buffer.from(text).toString('base64')});
 const binding=(name,text,privateValue=false)=>({name,type:privateValue?'secret_text':'plain_text',text});
 const revision=hash(sourceFile);
 await api(account+'/workers/workers/'+workerID+'/versions?deploy=true','POST',{main_module:'fixture.mjs',compatibility_date:'2026-07-01',compatibility_flags:['global_fetch_strictly_public'],modules:[module('fixture.mjs',fixture),module('worker.mjs',sourceFile),module('notification.mjs',notification)],bindings:[{name:'MAIL',type:'r2_bucket',bucket_name:name},binding('SOURCE_ID',source),binding('GENERATION',generation),binding('DOMAINS',JSON.stringify([domain])),binding('CAPACITY_BYTES',String(8<<20)),binding('DELIVERED_DAYS','7'),binding('READER_TOKEN',reader,true),binding('CONTROL_TOKEN',control,true),binding('NOTIFY_ORIGIN',origin),binding('NOTIFY_TOKEN',notify,true),binding('TEST_TOKEN',fixtureToken,true),binding('EXPIRES',String(Date.now()+1800000)),binding('REVISION',revision)]});
 const sub=await api(account+'/workers/subdomain');base=`https://${name}.${sub.subdomain}.workers.dev`;
 const before=await api(account+'/workers/workers/'+workerID);assert.equal(before.name,name);assert.equal(before.id,workerID);
 await api(account+'/workers/scripts/'+name+'/subdomain','POST',{enabled:true,previews_enabled:false});
 const request=(path,{method='GET',body,token=path.startsWith('/__')?fixtureToken:reader}={})=>proxiedFetch(base+path,{method,body,headers:{Authorization:'Bearer '+token}});
 await waitFor(async()=>{const r=await request('/__health');return r.status===200&&(await r.json()).revision===revision},'isolated Worker');
 const fingerprint=hash(JSON.stringify([...routes].sort((a,b)=>a.address.localeCompare(b.address))));
 const routeResp=await request('/v1/routes',{method:'POST',token:control,body:JSON.stringify({source_id:source,generation,expected_revision:0,operation_id:randomUUID(),fingerprint,routes})});assert.equal(routeResp.status,200);
 const raw=Buffer.from('From: probe@example.net\r\nTo: unrelated@example.net\r\nSubject: ANAS isolated notification\r\n\r\nSynthetic test.\r\n');
 for(const route of routes){const response=await request('/__event?to='+encodeURIComponent(route.address),{method:'POST',body:raw});assert.equal(response.status,200);assert.equal((await response.json()).rejected,'');}
 const status=async()=>{const r=await proxiedFetch(origin+'/__test/status',{headers:{Authorization:'Bearer '+testToken}});assert.equal(r.status,200);return r.json();};
 await waitFor(async()=>(await status()).notices.length===3,'durable PostgreSQL notices');const first=await status();assert.equal(first.notices.length,3);assert.ok(first.wakes>=3);assert.ok(first.notices.every(n=>n.state==='pending')); 
 const messages=await request('/v1/messages?limit=100');assert.equal(messages.status,200);const page=await messages.json();assert.equal(page.messages.length,3);assert.ok(page.messages.every(m=>m.notification?.state==='notified'&&!m.delivery));
 for(const route of routes){const found=page.messages.filter(m=>m.metadata.accepted_address===route.address);assert.equal(found.length,1);const meta=found[0].metadata;assert.equal(meta.mailbox_id,route.mailbox_id);assert.equal(meta.owner_anchor,route.owner_anchor);assert.equal(meta.claim_version,route.claim_version);assert.ok(first.notices.some(n=>n.message_id===meta.message_id&&n.mailbox_id===meta.mailbox_id));}
 for(const message of page.messages){const r=await request('/v1/messages/'+message.metadata.message_id+'/raw?target='+message.metadata.mailbox_id);assert.equal(r.status,200);assert.equal(hash(Buffer.from(await r.arrayBuffer())),hash(raw));}
 state.checks.push('worker-r2-then-signed-webhook-and-durable-notices');await save();
 const n=first.notices[0],body=JSON.stringify({schema:'anas.mail-notification/v1',source_id:source,generation,notification_id:n.id,message_id:n.message_id,mailbox_id:n.mailbox_id}),stamp=String(Math.floor(Date.now()/1000)),signature=createHmac('sha256',Buffer.from(notify,'hex')).update(stamp+'.'+body).digest('hex');
 const post=(payload,sig)=>proxiedFetch(origin+'/api/ingress/notify',{method:'POST',headers:{'Content-Type':'application/json','X-ANAS-Mail-Time':stamp,'X-ANAS-Mail-Signature':sig},body:payload});
 const repeats=await Promise.all(Array.from({length:8},()=>post(body,signature)));assert.ok(repeats.every(r=>r.status===202));
 assert.equal((await post(body,'f'.repeat(64))).status,401);
 const ambiguous=body.replace('"source_id":','"SOURCE_ID":"'+generation+'","source_id":'),ambiguousSig=createHmac('sha256',Buffer.from(notify,'hex')).update(stamp+'.'+ambiguous).digest('hex');assert.equal((await post(ambiguous,ambiguousSig)).status,400);
 const after=await status();assert.equal(after.notices.length,3);assert.ok(after.wakes>=first.wakes+8);state.checks.push('duplicate-and-forged-notices-no-extra-record');await save();
 for(let i=0;i<30;i++){const r=await request('/__cleanup',{method:'POST'});assert.equal(r.status,200);const data=await r.json();if(data.deleted===0){objectsClean=true;break;}}assert.ok(objectsClean);state.cleaned.objects=true;await save();
}catch(e){state.errors.push(e.message);await save();}
finally{
 if(createdWorker&&workerID&&base&&!objectsClean){
  try{
   const own=await api(account+'/workers/workers/'+workerID);assert.equal(own.id,workerID);assert.equal(own.name,name);
   for(let i=0;i<30;i++){const r=await proxiedFetch(base+'/__cleanup',{method:'POST',headers:{Authorization:'Bearer '+fixtureToken}});if(r.status!==200)throw Error('cleanup endpoint unavailable');const result=await r.json();if(result.deleted===0){objectsClean=true;state.cleaned.objects=true;break;}}
  }catch(e){state.errors.push('objects-cleanup: '+e.message)}
 }
 if(createdWorker&&workerID){try{const own=await api(account+'/workers/workers/'+workerID);assert.equal(own.id,workerID);assert.equal(own.name,name);await api(account+'/workers/workers/'+workerID,'DELETE');assert.equal(await apiStatus(account+'/workers/workers/'+workerID),404);state.cleaned.worker=true}catch(e){state.errors.push('worker-cleanup: '+e.message)}}
 if(createdBucket&&(objectsClean||!createdWorker)){try{await api(account+'/r2/buckets/'+name,'DELETE');assert.equal(await apiStatus(account+'/r2/buckets/'+name),404);state.cleaned.bucket=true}catch(e){state.errors.push('bucket-cleanup: '+e.message)}}
 if(cloudTunnel)cloudTunnel.kill('SIGTERM');if(sshTunnel)sshTunnel.kill('SIGTERM');if(proxyTunnel)proxyTunnel.kill('SIGTERM');
 if(remoteChild){try{const cleanup=nextLine(remoteChild,line=>{try{return JSON.parse(line).state==='cleaned'}catch{return false}},45000);remoteChild.stdin.end('stop\n');await cleanup;state.cleaned.remote=true}catch(e){state.errors.push('remote-cleanup: '+e.message);remoteChild.kill('SIGTERM')}}
 if(remote&&/^\/tmp\/anas-mail-live-[A-Za-z0-9]+$/.test(remote)&&state.cleaned.remote){try{ssh('rm -f '+remote+'/receiver '+remote+'/server.py && rmdir '+remote);state.cleaned.directory=true}catch(e){state.errors.push('directory-cleanup: '+e.message)}}
 await rm(temp,{recursive:true,force:true});await save();
}
if(state.errors.length||!state.checks.includes('duplicate-and-forged-notices-no-extra-record')||!state.cleaned.worker||!state.cleaned.bucket||!state.cleaned.remote||!state.cleaned.directory){console.error('isolated notification test incomplete; see private report');process.exitCode=1}else console.log('PASS real Worker/R2 -> signed HTTPS -> finance PostgreSQL notification subset');
