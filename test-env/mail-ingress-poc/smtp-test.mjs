// Run after the consumer test evidence is archived and synthetic data is reset.
import fs from 'node:fs/promises';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
const execute=promisify(execFile);
const dir=process.env.MAIL_POC_REPORT_DIR;
const s=JSON.parse(await fs.readFile(dir+'/cloud-private.json','utf8'));
assert.ok(s.smtp && s.messages.length===0);
const root='/home/whl/anas-mail-ingress-test-20261025';
const resume=process.argv.includes('--resume-import');
const report=resume?JSON.parse(await fs.readFile(dir+'/smtp-results.json','utf8')):
 {started:new Date().toISOString(),messages:[],checks:[]};
if(resume){
 assert.ok(report.finished && !report.mail_services_stopped);
 assert.ok(['a','b'].every(kind=>report.messages.some(m=>m.kind===kind&&m.accepted)));
 report.resumed=new Date().toISOString();report.checks=[];delete report.error;
}
const hash=x=>createHash('sha256').update(x).digest('hex');
const save=()=>fs.writeFile(dir+'/smtp-results.json',JSON.stringify(report,null,2),{mode:0o600});
async function ssh(args){return execute('ssh',['-o','BatchMode=yes','whl@finance.hlong.wang',...args],{timeout:360000,maxBuffer:4*1024*1024})}
async function server(mode){return JSON.parse((await ssh(['python3',root+'/consumer_server.py',mode])).stdout)}
async function get(path,test=false){
 const r=await fetch(s.url+path,{headers:{Authorization:'Bearer '+(test?s.test:s.token)},redirect:'error',signal:AbortSignal.timeout(30000)});
 assert.equal(r.status,200);return r;
}
try{
 let ready=false;
 for(let i=0;i<30;i++){
  try{if((await (await get('/__health',true)).json()).revision==='consumer-smtp-2'){ready=true;break}}catch{}
  await new Promise(resolve=>setTimeout(resolve,2000));
 }
 assert.ok(ready,'SMTP deployment not ready');
 if(!resume){
 assert.equal((await (await get('/messages?limit=100')).json()).messages.length,0,'SMTP test must start without a previous delivery');
 await server('stop');report.mail_services_stopped=true;await save();
 for(const kind of ['a','b','fail','unknown']){
  let out;
  const retry=process.env.MAIL_POC_RETRY_REJECTED==='1'?['--retry-rejected']:[];
  try{out=await ssh(['python3',root+'/smtp_sender.py',kind,...retry])}catch(e){out=e}
  assert.ok(out.stdout?.trim(),'Sender has no result');
  const result=JSON.parse(out.stdout);report.messages.push({kind,...result});await save();
  console.log(JSON.stringify({smtp:kind,accepted:result.accepted,smtp_code:result.smtp_code,error:result.error}));
  // An external spam/authentication failure prevents this path, not a passing storage test.
  if(['a','b'].includes(kind) && !result.accepted)throw Error('SMTP '+kind+' was not accepted: '+(result.error||'unknown'));
 }
 }
 const cloud=await (await get('/messages?limit=100')).json();assert.equal(cloud.cursor,null);
 assert.equal(cloud.messages.length,2);assert.ok(cloud.messages.every(x=>!x.delivered));
 const raws={};
 for(const m of cloud.messages){
  const raw=Buffer.from(await (await get('/messages/'+m.id+'/raw')).arrayBuffer());raws[m.id]=raw;
  await fs.writeFile(dir+'/smtp-cloud-'+m.id+'.eml',raw,{mode:0o600});
 }
 const fault=await (await get('/__smtp-faults',true)).json();assert.equal(fault.observed,1);
 const fail=report.messages.find(x=>x.kind==='fail');assert.ok(fail.smtp_code>=400&&fail.smtp_code<500,'Injected storage failure must produce a temporary SMTP rejection');
 const unknown=report.messages.find(x=>x.kind==='unknown');
 report.checks.push({name:'cloud accepted and retained while DMS/PostgreSQL stopped',passed:true});
 report.checks.push({name:'storage exception tempfail',passed:true});
 report.checks.push({name:'unknown recipient reject',passed:unknown.smtp_code>=500&&unknown.smtp_code<600,
   ...(unknown.error?{detail:unknown.error}:{})});
 await server('start');report.mail_services_stopped=false;
 const first=JSON.parse((await ssh([root+'/consumer',root+'/consumer.json'])).stdout);assert.ok(first.ok);assert.equal(first.saved,2);assert.equal(first.acked,2);
 const local=await server('snapshot');assert.equal(local.mail.a.length,1);assert.equal(local.mail.b.length,1);
 for(const m of cloud.messages){
  const target=m.target==='box-a'?'a':m.target==='box-b'?'b':null;assert.ok(target);
  assert.equal(m.recipient,target+'@mail-poc.bengbu.cc.cd');
  const raw=raws[m.id];const guid=hash(s.source+'\0'+m.id+'\0'+m.target).slice(0,32);
  const actual=local.mail[target][0];assert.equal(actual.guid,guid);
  assert.equal(actual.text.replaceAll('\r\n','\n'),raw.toString('utf8').replaceAll('\r\n','\n'));
  const row=local.ledger.find(x=>x.id===m.id);assert.equal(row.sha256,hash(raw));assert.equal(row.state,'complete');assert.equal(row.cloud_acked,true);
 }
 const second=JSON.parse((await ssh([root+'/consumer',root+'/consumer.json'])).stdout);assert.ok(second.ok);assert.equal(second.saved,0);assert.equal(second.skipped,2);
 const completed=await (await get('/messages?limit=100')).json();assert.ok(completed.messages.every(x=>x.delivered));
 report.checks.push({name:'DMS content and envelope target, final receipts, repeated pull',passed:true});
 report.cloud=completed;report.consumer={first,second};report.passed=report.checks.every(x=>x.passed);
 if(!report.passed){report.error='Failed checks: '+report.checks.filter(x=>!x.passed).map(x=>x.name).join(', ');process.exitCode=1}
 await fs.writeFile(dir+'/smtp-local-snapshot.json',JSON.stringify(local,null,2),{mode:0o600});
}catch(e){report.passed=false;report.error=e.message;process.exitCode=1}
finally{
 if(report.mail_services_stopped){
  try{await server('start');report.mail_services_stopped=false}catch(e){report.restart_error=e.message;report.passed=false;process.exitCode=1}
 }
 report.finished=new Date().toISOString();report.sources={};
 for(const file of ['worker.mjs','smtp-fixture.mjs','smtp_sender.py','smtp-test.mjs','consumer/main.go','consumer_server.py'])report.sources[file]=hash(await fs.readFile(new URL(file,import.meta.url)));
 await save();console.log(JSON.stringify({passed:report.passed,error:report.error}));
}
