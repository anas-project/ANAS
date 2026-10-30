// Requires separately approved transfer of the restricted pull credential.
// Does not transfer credentials or alter DNS. Cloud lifecycle is explicit.
import fs from 'node:fs/promises';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
const execute=promisify(execFile);
const dir=process.env.MAIL_POC_REPORT_DIR;
assert.equal((await fs.stat(dir)).mode & 0o077,0);
const root='/home/whl/anas-mail-ingress-test-20261025';
const host='whl@finance.hlong.wang';
const proxy=process.env.MAIL_POC_HTTPS_PROXY||'';
assert.ok(!proxy||proxy==='socks5h://127.0.0.1:10829','Only the explicitly configured local test proxy is allowed');
const report={started:new Date().toISOString(),checks:[],transport:proxy?'temporary-local-socks5h':'direct'};
const hash=x=>createHash('sha256').update(x).digest('hex');
const state=async()=>JSON.parse(await fs.readFile(dir+'/cloud-private.json','utf8'));
const normalized=x=>x.replaceAll('\r\n','\n');
async function ssh(args){
 return execute('ssh',['-o','BatchMode=yes',host,...args],{timeout:360000,maxBuffer:4*1024*1024});
}
async function server(mode){return JSON.parse((await ssh(['python3',root+'/consumer_server.py',mode])).stdout)}
async function consumer(fault='',code=0){
 let out;
 const environment=[...(proxy?['HTTPS_PROXY='+proxy]:[]),...(fault?['MAIL_POC_FAULT='+fault]:[])];
 try{out=await ssh([...(environment.length?['env',...environment]:[]),root+'/consumer',root+'/consumer.json']);assert.equal(code,0,'expected injected process exit')}
 catch(e){assert.equal(e.code,code,`consumer exit: ${e.stdout||e.message}`);out=e}
 return out.stdout.trim()?JSON.parse(out.stdout):{fault,exit:code};
}
async function cloud(mode,...args){
 await execute(process.execPath,['test-env/mail-ingress-poc/consumer-cloud.mjs',mode,...args],{env:process.env,timeout:60000});
 return mode==='snapshot'?JSON.parse(await fs.readFile(dir+'/cloud-snapshot.json','utf8')):undefined;
}
async function inspect(heldLabel){
 const s=await state(); const local=await server('snapshot'); const remote=await cloud('snapshot');
 assert.equal(local.ledger.length,s.messages.length);
 assert.equal(remote.messages.length,s.messages.length);
 assert.equal(local.mail.a.length+local.mail.b.length,s.messages.length-(heldLabel?1:0));
 for(const m of s.messages){
  const target=m.target||(m.to==='a@example.org'?'box-a':'box-b');
  assert.ok(['box-a','box-b'].includes(target));
  const guid=hash(s.source+'\0'+m.id+'\0'+target).slice(0,32);
  const ledger=local.ledger.find(x=>x.id===m.id);
  const r=remote.messages.find(x=>x.id===m.id);
  assert.equal(ledger.source,s.source);assert.equal(ledger.target,target);assert.equal(ledger.guid,guid);
  assert.equal(ledger.sha256,hash(m.raw));assert.equal(r.target,target);
  const all=[...local.mail.a,...local.mail.b].filter(x=>x.guid===guid);
  if(m.label===heldLabel){
   assert.equal(ledger.state,'uncertain');assert.equal(ledger.cloud_acked,false);
   assert.equal(r.delivered,false);assert.equal(all.length,0);
  }else{
   assert.equal(ledger.state,'complete');assert.equal(ledger.cloud_acked,true);assert.equal(r.delivered,true);
   assert.equal(all.length,1);
   const box=target==='box-a'?local.mail.a:local.mail.b;
   assert.equal(box.filter(x=>x.guid===guid).length,1);
   assert.equal(normalized(all[0].text),normalized(m.raw));
  }
 }
 await fs.writeFile(dir+'/verified-snapshot.json',JSON.stringify({local,remote},null,2),{mode:0o600});
 return {messages:s.messages.length,inbox_a:local.mail.a.length,inbox_b:local.mail.b.length,held:heldLabel||null};
}
async function check(name,fn){
 const start=Date.now();
 try{const details=await fn();report.checks.push({name,passed:true,ms:Date.now()-start,details});console.log(JSON.stringify({name,passed:true}))}
 catch(e){report.checks.push({name,passed:false,ms:Date.now()-start,error:e.message});throw e}
 finally{await fs.writeFile(dir+'/consumer-results.json',JSON.stringify(report,null,2),{mode:0o600})}
}
try{
 if(process.env.MAIL_POC_EXISTING_COMPLETE==='1'){
  await check('verified completed SMTP baseline for remaining fault tests',async()=>{
   const before=await cloud('snapshot');assert.equal(before.messages.length,2);assert.ok(before.messages.every(x=>x.delivered));
   return inspect();
  });
 }else{
 await check('offline backlog and concurrent cold start',async()=>{
  const before=await cloud('snapshot');assert.equal(before.messages.length,2);assert.ok(before.messages.every(x=>!x.delivered));
  const r=await Promise.all([consumer(),consumer()]);assert.equal(r.reduce((n,x)=>n+x.saved,0),2);
  assert.ok(r.every(x=>x.ok));return {runs:r,...await inspect()};
 });
 }
 await check('repeat pull imports nothing',async()=>{
  const r=await consumer();assert.equal(r.saved,0);assert.equal(r.skipped,2);return {run:r,...await inspect()};
 });
 for(const [fault,code,label] of [['after-save',72,'crash-after-save'],['after-complete',73,'crash-after-complete'],['after-ack',74,'crash-after-ack']]){
  await check(fault+' recovery without duplicate',async()=>{
   await cloud('seed',label);await consumer(fault,code);
   const pending=await server('snapshot');const m=(await state()).messages.find(x=>x.label===label);
   const row=pending.ledger.find(x=>x.id===m.id);assert.equal(row.state,fault==='after-save'?'uncertain':'complete');assert.equal(row.cloud_acked,false);
   assert.equal([...pending.mail.a,...pending.mail.b].filter(x=>x.guid===row.guid).length,1);
   const before=await cloud('snapshot');assert.equal(before.messages.find(x=>x.id===m.id).delivered,fault==='after-ack');
   const r=await consumer();assert.equal(r.saved,0);assert.equal(r.reconciled,fault==='after-save'?1:0);assert.equal(r.acked,1);
   return {recovery:r,...await inspect()};
  });
 }
 await check('retained completion list reconciles lost ledger',async()=>{
  await server('forget-ledger');const r=await consumer();const n=(await state()).messages.length;
  assert.equal(r.saved,0);assert.equal(r.reconciled,n);assert.equal(r.acked,n);return {run:r,...await inspect()};
 });
 await check('unknown absent result stays held and does not block later mail',async()=>{
  await cloud('seed','hold-before-save');await consumer('before-save',71);
  let r=await consumer('',1);assert.equal(r.saved,0);assert.equal(r.ok,false);assert.match(r.error,/uncertain DMS result/);
  await cloud('seed','after-held','b@example.org');r=await consumer('',1);
  assert.equal(r.saved,1);assert.equal(r.acked,1);assert.equal(r.ok,false);
  return {run:r,...await inspect('hold-before-save')};
 });
 report.passed=true;
}catch(e){report.passed=false;report.error=e.message;process.exitCode=1}
finally{
 report.finished=new Date().toISOString();
 report.sources={};
 for(const file of ['consumer/main.go','consumer/main_test.go','consumer_server.py','consumer-test.mjs','consumer-cloud.mjs','worker.mjs','cloud-fixture.mjs'])
  report.sources[file]=hash(await fs.readFile(new URL(file,import.meta.url)));
 await fs.writeFile(dir+'/consumer-results.json',JSON.stringify(report,null,2),{mode:0o600});
 console.log(JSON.stringify({passed:report.passed,checks:report.checks.length,error:report.error}));
}
