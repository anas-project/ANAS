// Continuation from the explicitly verified four-message relay baseline.
import fs from 'node:fs/promises';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {execFile} from 'node:child_process';
const dir=process.env.MAIL_POC_REPORT_DIR;
const cfg=JSON.parse(await fs.readFile(dir+'/cloud-runtime.json','utf8'));
const report={started:new Date().toISOString(),checks:[]};
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
function execute(args){return new Promise((resolve,reject)=>execFile('ssh',['-o','BatchMode=yes',...args],{timeout:60000,maxBuffer:4*1024*1024},(e,out)=>e?reject(e):resolve(out)))}
const cloud=async mode=>JSON.parse(await execute(['ubuntu@osa.letuh.com','python3','/home/ubuntu/anas-mail-relay-test-20261025/relay-fixture.py',mode]));
async function messages(){const r=await fetch(cfg.url+'/messages?limit=100',{headers:{Authorization:'Bearer '+cfg.token},signal:AbortSignal.timeout(15000)});assert.equal(r.status,200);return (await r.json()).messages}
async function wait(fn){const end=Date.now()+90000;while(Date.now()<end){const x=await fn();if(x)return x;await sleep(2000)}throw Error('retry verification timed out')}
const smtp=async(label,to)=>JSON.parse(await execute(['whl@finance.hlong.wang','python3','/home/whl/anas-mail-ingress-test-20261025/relay-smtp.py',label,to]));
async function check(name,fn){const t=Date.now();try{const details=await fn();report.checks.push({name,passed:true,ms:Date.now()-t,details})}catch(e){report.checks.push({name,passed:false,error:e.message});throw e}}
try{
 const before=await messages();assert.equal(before.length,4);assert.ok(before.every(x=>x.delivered));
 await check('Postfix retries a published message without creating another delivery',async()=>{
  await cloud('fault-after-publish');const s=await smtp('relay-publish-retry','a');assert.equal(s.data_code,250);
  const pending=await wait(async()=>{const x=await cloud('snapshot');return x.queue.includes('deferred')&&x.messages.length===5&&x});
  const published=await messages();assert.equal(published.length,5);
  await cloud('flush');await wait(async()=>!(await cloud('snapshot')).queue);
  const delivered=await wait(async()=>{const m=await messages();return m.length===5&&m.every(x=>x.delivered)&&m});
  assert.deepEqual(delivered.map(x=>x.id).sort(),published.map(x=>x.id).sort());
  return {smtp_code:250,published_before_Postfix_success:true,source_messages_after_retry:5,queue_empty:true};
 });
 await check('a new SMTP transaction with the same Message-ID remains separate',async()=>{
  const s=await smtp('relay-new-queue','b');assert.equal(s.data_code,250);
  const m=await wait(async()=>{const m=await messages();return m.length===6&&m.every(x=>x.delivered)&&m});
  const local=JSON.parse(await execute(['whl@finance.hlong.wang','python3','/home/whl/anas-mail-ingress-test-20261025/consumer_server.py','snapshot']));
  const rows=local.ledger.filter(x=>x.source===cfg.source);assert.equal(rows.length,6);assert.ok(rows.every(x=>x.state==='complete'&&x.cloud_acked));
  for(const x of m){const guid=createHash('sha256').update(cfg.source+'\0'+x.id+'\0'+x.target).digest('hex').slice(0,32);assert.equal([...local.mail.a,...local.mail.b].filter(y=>y.guid===guid).length,1)}
  return {source_messages:6,DMS_copies:6,queue_empty:!(await cloud('snapshot')).queue};
 });
 report.passed=true;
}catch(e){report.passed=false;report.error=e.message;process.exitCode=1}
finally{
 report.finished=new Date().toISOString();report.sources={};for(const file of ['relay/main.go','relay/main_test.go','relay-fixture.py','relay-retry-test.mjs'])report.sources[file]=createHash('sha256').update(await fs.readFile(new URL(file,import.meta.url))).digest('hex');
 await fs.writeFile(dir+'/relay-retry-results.json',JSON.stringify(report,null,2),{mode:0o600});console.log(JSON.stringify(report));
}
