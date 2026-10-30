import fs from 'node:fs/promises';
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { execFile } from 'node:child_process';
const dir=process.env.MAIL_POC_REPORT_DIR;
assert.equal((await fs.stat(dir)).mode&0o077,0);
const root='/home/whl/anas-mail-ingress-test-20261025';
const report={started:new Date().toISOString(),checks:[],transport:'temporary-SSH-SOCKS-through-osa',notification:'temporary-Quick-Tunnel-through-osa-and-SSH-to-finance'};
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
const state=async()=>JSON.parse(await fs.readFile(dir+'/cloud-private.json','utf8'));
function execute(command,args,body){return new Promise((resolve,reject)=>{
 const p=execFile(command,args,{timeout:120000,maxBuffer:4*1024*1024},(error,stdout,stderr)=>error?reject(Object.assign(error,{stdout,stderr})):resolve(stdout));
 if(body)p.stdin.end(body);else p.stdin.end();
})}
async function server(mode,body){return JSON.parse(await execute('ssh',['-o','BatchMode=yes','whl@finance.hlong.wang','python3',root+'/scheduler-fixture.py',mode],body?JSON.stringify(body):undefined))}
async function cloud(mode,...args){await execute(process.execPath,['test-env/mail-ingress-poc/consumer-cloud.mjs',mode,...args])}
async function api(path,method='GET',test=true){
 const s=await state();const r=await fetch(s.url+path,{method,headers:{Authorization:'Bearer '+(test?s.test:s.token)},redirect:'error',signal:AbortSignal.timeout(15000)});
 assert.equal(r.status,200);return r.json();
}
async function seed(label){await cloud('seed',label);return (await state()).messages.find(m=>m.label===label)}
async function snapshot(){const s=await state(),r=await server('snapshot');return {events:r.events.filter(x=>x.source===s.source),polls:r.polls.filter(x=>x.source===s.source),ledger:r.ledger.filter(x=>x.source===s.source)}}
async function waitFor(fn,ms=90000){
 const end=Date.now()+ms;let value;
 while(Date.now()<end){value=await fn();if(value)return value;await sleep(2000)}
 throw Error('condition timed out after '+ms+' ms');
}
async function delivered(m,ms=90000){
 const r=await waitFor(async()=>{const r=await snapshot();return r.ledger.some(x=>x.id===m.id&&x.state==='complete'&&x.cloud_acked)&&r},ms);
 const cloud=await api('/messages?limit=100','GET',false);assert.ok(cloud.messages.some(x=>x.id===m.id&&x.delivered));return r;
}
async function check(name,fn){
 const start=Date.now();try{const details=await fn();report.checks.push({name,passed:true,ms:Date.now()-start,details});console.log(JSON.stringify({name,passed:true}))}
 catch(e){report.checks.push({name,passed:false,ms:Date.now()-start,error:e.message});throw e}
 finally{await fs.writeFile(dir+'/scheduler-results.json',JSON.stringify(report,null,2),{mode:0o600})}
}
try{
 await server('start');
 await waitFor(async()=>{try{return (await snapshot()).polls.some(x=>x.finished_at)}catch{return false}},30000);
 let first;
 await check('real Worker notification reaches NAS and immediately imports',async()=>{
  first=await seed('notify-immediate');const start=Date.now();
  const notified=await api('/__notify?mode=nas&id='+first.id,'POST');assert.equal(notified.state,'notified');
  const r=await delivered(first,45000);assert.equal(r.events.filter(x=>x.id===first.id).length,1);
  const inspect=await api('/__inspect?id='+first.id);assert.ok(inspect['notified/'+first.id]);assert.ok(inspect['ack/'+first.id]);
  return {notification:notified.state,delivery_ms:Date.now()-start,persisted_events:1};
 });
 await check('concurrent duplicate HTTPS notifications register one task',async()=>{
  const results=await Promise.all(Array.from({length:8},()=>server('notify',{id:first.id})));
  assert.ok(results.every(x=>x.status===202));const r=await snapshot();assert.equal(r.events.filter(x=>x.id===first.id).length,1);
  assert.equal(r.polls.reduce((n,x)=>n+(x.result?.saved||0),0),1);return {requests:8,persisted_events:1};
 });
 await check('invalid signature old signature and foreign target are rejected',async()=>{
  const id=randomUUID();const rows=[];
  for(const [data,expected] of [[{id,invalid:true},401],[{id,time_offset_ms:-301000},401],[{id,target:'foreign'},400]]){
   const r=await server('notify',data);assert.equal(r.status,expected);rows.push(r.status);
  }
  assert.ok(!(await snapshot()).events.some(x=>x.id===id));return {statuses:rows};
 });
 await check('database failure cannot acknowledge a notification',async()=>{
  const id=randomUUID();await server('postgres-stop');
  try{const r=await server('notify',{id});assert.equal(r.status,503)}finally{await server('postgres-start')}
  assert.ok(!(await snapshot()).events.some(x=>x.id===id));return {status:503,persisted_events:0};
 });
 await check('durable notification survives consumer restart and offline DMS',async()=>{
  await server('mail-stop');const m=await seed('notify-restart');
  try{
   const n=await api('/__notify?mode=nas&id='+m.id,'POST');assert.equal(n.state,'notified');
   const before=await snapshot();assert.equal(before.events.find(x=>x.id===m.id).processed_at,null);
   assert.ok(!(await api('/messages?limit=100','GET',false)).messages.find(x=>x.id===m.id).delivered);
   await server('stop');await server('start');
   await waitFor(async()=>{const r=await snapshot();return r.polls.filter(x=>x.reason==='startup'&&x.finished_at).length>=2},30000);
   assert.equal((await snapshot()).events.find(x=>x.id===m.id).processed_at,null);
  }finally{await server('mail-start')}
  const r=await delivered(m);assert.ok(r.events.find(x=>x.id===m.id).processed_at);
  const startup=r.polls.filter(x=>x.reason==='startup').at(-1),timer=r.polls.find(x=>x.reason==='timer'&&new Date(x.started_at)>new Date(startup.started_at));
  assert.ok(timer);const interval=new Date(timer.started_at)-new Date(startup.started_at);assert.ok(interval>=55000&&interval<=70000,`actual interval ${interval}`);
  return {notification_acked_while_DMS_offline:true,delivered_after_restart:true,actual_first_timer_ms:interval};
 });
 await check('real 60-second polling imports a message without any notification',async()=>{
  const m=await seed('poll-only');const r=await delivered(m);assert.ok(!r.events.some(x=>x.id===m.id));
  const timers=r.polls.filter(x=>x.reason==='timer');assert.ok(timers.length>=2);
  const interval=new Date(timers.at(-1).started_at)-new Date(timers.at(-2).started_at);assert.ok(interval>=55000&&interval<=70000,`actual interval ${interval}`);
  return {notifications:0,actual_timer_interval_ms:interval};
 });
 await check('real NAS notification timeout preserves cloud mail for polling',async()=>{
  const m=await seed('notify-timeout');await server('pause');let notification;
  try{notification=await api('/__notify?mode=nas&id='+m.id,'POST');assert.equal(notification.state,'failed')}
  finally{await server('resume')}
  const inspect=await api('/__inspect?id='+m.id);assert.equal(inspect['attempt/'+m.id+'/0'].failure,'timeout');assert.ok(inspect.rawPresent);
  await delivered(m);return {notification:notification.state,cloud_raw_retained:true,recovered_by_consumer:true};
 });
 report.passed=true;
}catch(e){report.passed=false;report.error=e.message;process.exitCode=1}
finally{
 report.finished=new Date().toISOString();report.sources={};
 for(const file of ['consumer/main.go','consumer/notify.go','consumer/notify_test.go','scheduler-fixture.py','scheduler-test.mjs','consumer-cloud.mjs','cloud-fixture.mjs','worker.mjs'])
  report.sources[file]=createHash('sha256').update(await fs.readFile(new URL(file,import.meta.url))).digest('hex');
 try{report.final=await snapshot()}catch(e){report.snapshot_error=e.message}
 await fs.writeFile(dir+'/scheduler-results.json',JSON.stringify(report,null,2),{mode:0o600});
 console.log(JSON.stringify({passed:report.passed,checks:report.checks.length,error:report.error}));
}
