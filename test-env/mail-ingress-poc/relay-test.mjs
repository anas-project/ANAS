import fs from 'node:fs/promises';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
const dir=process.env.MAIL_POC_REPORT_DIR;
assert.equal((await fs.stat(dir)).mode&0o077,0);
const cfg=JSON.parse(await fs.readFile(dir+'/relay-runtime.json','utf8'));
const runtime=JSON.parse(await fs.readFile(dir+'/cloud-runtime.json','utf8'));
const report={started:new Date().toISOString(),checks:[],source:cfg.source};
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
const hash=x=>createHash('sha256').update(x).digest('hex');
const root='/home/whl/anas-mail-ingress-test-20261025';
function execute(cmd,args){return new Promise((resolve,reject)=>execFile(cmd,args,{timeout:180000,maxBuffer:4*1024*1024},(e,stdout,stderr)=>e?reject(Object.assign(e,{stdout,stderr})):resolve(stdout)))}
async function nas(mode){return JSON.parse(await execute('ssh',['-o','BatchMode=yes','whl@finance.hlong.wang','python3',root+'/scheduler-fixture.py',mode]))}
async function cloud(mode){return JSON.parse(await execute('ssh',['-o','BatchMode=yes','ubuntu@osa.letuh.com','python3','/home/ubuntu/anas-mail-relay-test-20261025/relay-fixture.py',mode]))}
async function smtp(label,to){return JSON.parse(await execute('ssh',['-o','BatchMode=yes','whl@finance.hlong.wang','python3',root+'/relay-smtp.py',label,to]))}
async function request(path,method='GET',body,token=runtime.token){
 const r=await fetch(runtime.url+path,{method,body:body?JSON.stringify(body):undefined,headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},redirect:'error',signal:AbortSignal.timeout(15000)});
 return {status:r.status,data:r.headers.get('Content-Type')?.startsWith('application/json')?await r.json():await r.text()};
}
async function messages(){const r=await request('/messages?limit=100');assert.equal(r.status,200);assert.equal(r.data.cursor,null);assert.equal(r.data.source,cfg.source);return r.data.messages}
async function waitFor(fn,ms=90000){const end=Date.now()+ms;while(Date.now()<end){const r=await fn();if(r)return r;await sleep(2000)}throw Error('condition timed out')}
async function local(){const r=await nas('snapshot');return {events:r.events.filter(x=>x.source===cfg.source),ledger:r.ledger.filter(x=>x.source===cfg.source),polls:r.polls.filter(x=>x.source===cfg.source)}}
async function allDelivered(count){return waitFor(async()=>{const m=await messages();return m.length===count&&m.every(x=>x.delivered)&&m})}
async function inspect(count){
 const m=await allDelivered(count),l=await local();assert.equal(l.ledger.length,count);assert.ok(l.ledger.every(x=>x.state==='complete'&&x.cloud_acked));
 const dms=JSON.parse(await execute('ssh',['-o','BatchMode=yes','whl@finance.hlong.wang','python3',root+'/consumer_server.py','snapshot']));
 for(const x of m){
  const guid=hash(cfg.source+'\0'+x.id+'\0'+x.target).slice(0,32);const box=x.target==='box-a'?dms.mail.a:dms.mail.b;
  const copies=box.filter(y=>y.guid===guid);assert.equal(copies.length,1);
  const raw=await request('/messages/'+x.id+'/raw');assert.equal(raw.status,200);assert.equal(hash(raw.data),x.sha256);
  assert.equal(copies[0].text.replaceAll('\r\n','\n'),raw.data.replaceAll('\r\n','\n'));
 }
 await fs.writeFile(dir+'/verified-snapshot.json',JSON.stringify({cloud:m,local:l,dms},null,2),{mode:0o600});return {messages:m.length,inbox_copies:count};
}
async function check(name,fn){const start=Date.now();try{const details=await fn();report.checks.push({name,passed:true,ms:Date.now()-start,details});console.log(JSON.stringify({name,passed:true}))}catch(e){report.checks.push({name,passed:false,ms:Date.now()-start,error:e.message});throw e}finally{await fs.writeFile(dir+'/relay-results.json',JSON.stringify(report,null,2),{mode:0o600})}}
try{
 assert.equal((await messages()).length,0);
 await check('Postfix accepts and cloud publishes while NAS services are stopped',async()=>{
  await nas('mail-stop');await nas('postgres-stop');
  const a=await smtp('relay-offline-a','a'),b=await smtp('relay-offline-b','b');assert.equal(a.data_code,250);assert.equal(b.data_code,250);
  const m=await waitFor(async()=>{const m=await messages();return m.length===2&&m});assert.ok(m.every(x=>!x.delivered));assert.deepEqual(m.map(x=>x.target).sort(),['box-a','box-b']);
  return {smtp_codes:[a.data_code,b.data_code],published_messages:2,NAS_database_and_DMS_stopped:true};
 });
 await check('unknown recipients and external relay are rejected before DATA',async()=>{
  const a=await smtp('relay-unknown','unknown'),b=await smtp('relay-external','relay');assert.ok(a.rcpt_code>=500&&b.rcpt_code>=500);assert.ok(!a.data_started&&!b.data_started);
  return {unknown:a.rcpt_code,external_relay:b.rcpt_code};
 });
 await check('cloud restart retains raw bytes and trusted envelope metadata',async()=>{
  const before=await messages();await cloud('restart');
  const after=await waitFor(async()=>{try{return await messages()}catch{return false}},30000);assert.deepEqual(after,before);
  for(const m of after){const raw=await request('/messages/'+m.id+'/raw');assert.equal(raw.status,200);assert.equal(hash(raw.data),m.sha256)}
  return {retained:after.length,raw_hashes_equal:true};
 });
 await check('NAS recovery pulls cloud mail and repeats without duplicate copies',async()=>{
  await nas('postgres-start');await nas('mail-start');await nas('start');await allDelivered(2);
  const r=JSON.parse(await execute('ssh',['-o','BatchMode=yes','whl@finance.hlong.wang','env','HTTPS_PROXY=socks5h://127.0.0.1:10829',root+'/consumer-scheduler',root+'/consumer-scheduler.json']));assert.equal(r.saved,0);assert.equal(r.skipped,2);
  return {repeat:r,...await inspect(2)};
 });
 await check('source authentication and cross-target acknowledgement are enforced',async()=>{
  assert.equal((await request('/messages','GET',undefined,'wrong')).status,401);
  const m=(await messages())[0];assert.equal((await request('/messages/'+m.id+'/ack','POST',{source:cfg.source,target:m.target==='box-a'?'box-b':'box-a',receipt:'invalid'})).status,400);
  return {wrong_token:401,cross_target:400};
 });
 await check('real cloud Go notification causes immediate NAS import',async()=>{
  const before=new Set((await messages()).map(x=>x.id));
  const start=Date.now();const s=await smtp('relay-notify','a');assert.equal(s.data_code,250);
  const m=await waitFor(async()=>{const m=await messages();return m.find(x=>x.delivered&&!before.has(x.id))&&m.length===3&&m},45000);
  const added=m.find(x=>!before.has(x.id));
  const l=await local();assert.ok(l.events.some(x=>x.id===added.id));return {delivery_ms:Date.now()-start,registered_events:l.events.length,...await inspect(3)};
 });
 await check('failed durable handoff stays in Postfix queue and recovers',async()=>{
  await cloud('block-spool');let code;
  try{
   const s=await smtp('relay-spool-failure','b');code=s.data_code;assert.equal(code,250);
   const snap=await waitFor(async()=>{const s=await cloud('snapshot');return s.queue.includes('deferred')&&s},30000);
   assert.equal((await messages()).length,3);assert.ok(snap.queue.includes('relay-spool-failure')===false);
  }finally{await cloud('unblock-spool');await cloud('flush')}
  await allDelivered(4);return {smtp_code:code,queue_retained_until_handoff:true,...await inspect(4)};
 });
 report.passed=true;
}catch(e){report.passed=false;report.error=e.message;process.exitCode=1}
finally{
 report.finished=new Date().toISOString();report.sources={};for(const file of ['relay/main.go','relay/main_test.go','relay-fixture.py','relay-smtp.py','relay-test.mjs','consumer/main.go','consumer/notify.go'])report.sources[file]=hash(await fs.readFile(new URL(file,import.meta.url)));
 try{report.final=await cloud('snapshot')}catch(e){report.snapshot_error=e.message}
 await fs.writeFile(dir+'/relay-results.json',JSON.stringify(report,null,2),{mode:0o600});console.log(JSON.stringify({passed:report.passed,checks:report.checks.length,error:report.error}));
}
