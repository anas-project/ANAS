// Manual, explicitly authorized cloud fixture lifecycle for the real Go/DMS experiment.
import fs from 'node:fs/promises';
import { randomBytes } from 'node:crypto';
import assert from 'node:assert/strict';
const dir=process.env.MAIL_POC_REPORT_DIR;
const credential=JSON.parse(await fs.readFile(process.env.MAIL_POC_CREDENTIAL_FILE,'utf8'));
assert.equal((await fs.stat(process.env.MAIL_POC_CREDENTIAL_FILE)).mode & 0o077,0);
assert.equal((await fs.stat(dir)).mode & 0o077,0);
const statePath=dir+'/cloud-private.json';
const mode=process.argv[2];
let s;
const save=()=>fs.writeFile(statePath,JSON.stringify(s,null,2),{mode:0o600});
async function api(path,method='GET',body){
 const r=await fetch('https://api.cloudflare.com/client/v4'+path,{method,
  headers:{Authorization:`Bearer ${credential.token}`,...(body && !(body instanceof FormData)?{'Content-Type':'application/json'}:{})},
  body:body instanceof FormData?body:body?JSON.stringify(body):undefined,redirect:'error',signal:AbortSignal.timeout(45000)});
 if(r.status===204)return null;
 const j=await r.json();assert.ok(r.ok && j.success,`API ${method} HTTP ${r.status}`);return j.result;
}
const account=`/accounts/${credential.account_id}`;
if(mode==='create'){
 try{await fs.stat(statePath);throw Error('Fixture state already exists')}catch(e){if(e.code!=='ENOENT')throw e}
 const name='anas-mail-consumer-20261025-'+randomBytes(4).toString('hex');
 s={name,source:name,test:randomBytes(32).toString('hex'),token:randomBytes(32).toString('hex'),messages:[],created:{}};await save();
 const workers=await api(account+'/workers/scripts');assert.ok(!workers.some(x=>x.id===name));
 const buckets=await api(account+'/r2/buckets');assert.ok(!buckets.buckets.some(x=>x.name===name));
 await api(account+'/r2/buckets','POST',{name,storageClass:'Standard'});s.created.bucket=true;await save();
 const sub=await api(account+'/workers/subdomain');s.url=`https://${name}.${sub.subdomain}.workers.dev`;
 const binding=(name,text,secret=false)=>({name,text,type:secret?'secret_text':'plain_text'});
 const notifyBindings=[];
 if(process.env.MAIL_POC_NOTIFY_CONFIG){
  const file=process.env.MAIL_POC_NOTIFY_CONFIG;assert.equal((await fs.stat(file)).mode&0o077,0);
  const n=JSON.parse(await fs.readFile(file,'utf8'));const u=new URL(n.url);
  assert.equal(u.protocol,'https:');assert.ok(u.hostname.endsWith('.trycloudflare.com'));
  assert.equal(u.pathname,'/notify');assert.ok(!u.username&&!u.password&&!u.search&&!u.hash);
  assert.ok(typeof n.secret==='string'&&n.secret.length>=32);
  notifyBindings.push(binding('NOTIFY_URL',n.url),binding('NOTIFY_SECRET',n.secret,true));
 }
 const form=new FormData();form.set('metadata',new Blob([JSON.stringify({main_module:'cloud-fixture.mjs',compatibility_date:'2026-07-01',bindings:[
  {name:'MAIL',type:'r2_bucket',bucket_name:name},binding('SOURCE',s.source),binding('PULL_TOKEN',s.token,true),binding('TEST_TOKEN',s.test,true),
  ...notifyBindings,binding('EXPIRES',String(Date.now()+3*3600000)),binding('REVISION','consumer-1'),binding('ROUTES',JSON.stringify({'a@example.org':'box-a','b@example.org':'box-b'}))],compatibility_flags:['global_fetch_strictly_public']})],{type:'application/json'}));
 for(const file of ['cloud-fixture.mjs','worker.mjs'])form.set(file,new Blob([await fs.readFile(new URL(file,import.meta.url))],{type:'application/javascript+module'}),file);
 const result=await api(account+'/workers/scripts/'+name,'PUT',form);s.created.worker=true;s.version=result.deployment_id||result.etag;await save();
 await api(account+'/workers/scripts/'+name+'/subdomain','POST',{enabled:true,previews_enabled:false});
 let ready=false;
 for(let i=0;i<60;i++){
  try{const r=await fetch(s.url+'/__health',{headers:{Authorization:`Bearer ${s.test}`},redirect:'error'});if(r.ok && (await r.json()).revision==='consumer-1'){ready=true;break}}catch{}
  await new Promise(resolve=>setTimeout(resolve,2000));
 }
 assert.ok(ready,'Cloud fixture not ready');
 const managed=await api(account+'/r2/buckets/'+name+'/domains/managed');assert.equal(managed.enabled,false);
 await fs.writeFile(dir+'/cloud-runtime.json',JSON.stringify({url:s.url,source:s.source,token:s.token}),{mode:0o600});
 console.log(JSON.stringify({created:name,private_bucket:true,ready}));
}else{
 s=JSON.parse(await fs.readFile(statePath,'utf8'));
 if(mode==='custom-domain'){
  const hostname='pull.mail-poc.bengbu.cc.cd';
  const zone='0d84033826bc3cd0c283efdb57a90f7a';
  if(!s.domain){
   const info=await api('/zones/'+zone);assert.equal(info.name,'bengbu.cc.cd');
   const existing=await api('/zones/'+zone+'/dns_records?name='+hostname);assert.equal(existing.length,0);
   const domains=await api(account+'/workers/domains');assert.ok(!domains.some(x=>x.hostname===hostname));
   s.domain=await api(account+'/workers/domains','PUT',{hostname,service:s.name,zone_id:zone,enabled:true,previews_enabled:false});
   assert.equal(s.domain.hostname,hostname);await save();
  }
  s.workersDevURL ||= s.url;s.url=s.workersDevURL;s.runtimeURL='https://'+hostname;await save();
  // Availability must be verified from the NAS, not inferred from the control machine.
  await fs.writeFile(dir+'/cloud-runtime.json',JSON.stringify({url:s.runtimeURL,source:s.source,token:s.token}),{mode:0o600});
  console.log(JSON.stringify({custom_domain:hostname,nas_readiness:'requires-client-check'}));
 }else if(mode==='smtp-enable'||mode==='smtp-redeploy'){
  const zone='0d84033826bc3cd0c283efdb57a90f7a';
  const sender='sender.mail-poc.bengbu.cc.cd';
  if(mode==='smtp-enable'){
  assert.ok(!s.smtp,'SMTP experiment already configured');
  s.smtp={rules:[],dns:[]};await save();
  const existing=await api('/zones/'+zone+'/dns_records?name='+sender);assert.equal(existing.length,0);
  for(const record of [{type:'A',name:sender,content:'221.212.214.94',proxied:false,ttl:60},
    {type:'TXT',name:sender,content:'v=spf1 ip4:221.212.214.94 -all',ttl:60}]){
   const created=await api('/zones/'+zone+'/dns_records','POST',{...record,comment:'Temporary ANAS mail SMTP experiment 20261025'});
   s.smtp.dns.push(created);await save();
  }
  }else assert.ok(s.smtp,'SMTP experiment must already exist');
  const settings=await api(account+'/workers/scripts/'+s.name+'/settings');
  const expires=settings.bindings.find(x=>x.name==='EXPIRES').text;
  assert.ok(Number(expires)>Date.now());
  const binding=(name,text,secret=false)=>({name,text,type:secret?'secret_text':'plain_text'});
  const form=new FormData();form.set('metadata',new Blob([JSON.stringify({main_module:'smtp-fixture.mjs',compatibility_date:'2026-07-01',bindings:[
   {name:'MAIL',type:'r2_bucket',bucket_name:s.name},binding('SOURCE',s.source),binding('PULL_TOKEN',s.token,true),binding('TEST_TOKEN',s.test,true),
   binding('EXPIRES',expires),binding('REVISION','consumer-smtp-2'),binding('ROUTES',JSON.stringify({
    'a@example.org':'box-a','b@example.org':'box-b','a@mail-poc.bengbu.cc.cd':'box-a','b@mail-poc.bengbu.cc.cd':'box-b'}))]})],{type:'application/json'}));
  for(const file of ['smtp-fixture.mjs','cloud-fixture.mjs','worker.mjs'])form.set(file,new Blob([await fs.readFile(new URL(file,import.meta.url))],{type:'application/javascript+module'}),file);
  const deployed=await api(account+'/workers/scripts/'+s.name,'PUT',form);s.smtp.version=deployed.deployment_id||deployed.etag;await save();
  if(mode==='smtp-enable'){
  const rules=await api('/zones/'+zone+'/email/routing/rules');
  for(const name of ['a','b','fail']){
   const to=name+'@mail-poc.bengbu.cc.cd';assert.ok(!rules.some(x=>x.matchers?.some(m=>m.value===to)));
   const rule=await api('/zones/'+zone+'/email/routing/rules','POST',{name:'ANAS isolated SMTP '+name,enabled:true,
    matchers:[{type:'literal',field:'to',value:to}],actions:[{type:'worker',value:[s.name]}]});
   s.smtp.rules.push(rule);await save();
  }
  }
  console.log(JSON.stringify({smtp:true,rules:s.smtp.rules.length,sender,expires}));
 }else if(mode==='seed'){
  const label=process.argv[3];assert.match(label,/^[a-z0-9-]{1,32}$/);
  const to=process.argv[4]||'a@example.org';assert.ok(['a@example.org','b@example.org'].includes(to));
  const raw=`From: sender@example.net\r\nTo: forged@example.org\r\nMessage-ID: <same@sender.example>\r\nSubject: ${label}\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=poc\r\n\r\n--poc\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n测试 ${label}\r\n--poc\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n\r\nAAECA/8=\r\n--poc--\r\n`;
  const r=await fetch(s.url+'/__receive?to='+encodeURIComponent(to),{method:'POST',body:raw,headers:{Authorization:`Bearer ${s.test}`},redirect:'error'});
  assert.equal(r.status,200);const msg=await r.json();assert.ok(msg.id);
  s.messages.push({id:msg.id,label,to,raw});await save();console.log(JSON.stringify({id:msg.id,label}));
 }else if(mode==='snapshot'){
  const r=await fetch(s.url+'/messages?limit=100',{headers:{Authorization:`Bearer ${s.token}`},redirect:'error'});assert.equal(r.status,200);
  const data=await r.json();assert.equal(data.cursor,null);
  await fs.writeFile(dir+'/cloud-snapshot.json',JSON.stringify(data,null,2),{mode:0o600});
  console.log(JSON.stringify({messages:data.messages.length,delivered:data.messages.filter(x=>x.delivered).length}));
 }else if(mode==='reset-synthetic'){
  assert.ok(!s.smtp,'Do not reset a live SMTP fixture');
  await fs.writeFile(dir+'/cloud-before-reset-'+Date.now()+'.json',JSON.stringify(s,null,2),{mode:0o600});
  for(let i=0;i<20;i++){
   const r=await fetch(s.url+'/__cleanup',{method:'POST',headers:{Authorization:`Bearer ${s.test}`},redirect:'error'});assert.equal(r.status,200);
   const page=await r.json();if(!page.deleted)break;assert.ok(i<19);
  }
  s.messages=[];await save();console.log(JSON.stringify({synthetic_reset:true}));
 }else if(mode==='cleanup'){
  let deleted=0;
  if(s.smtp){
   for(const rule of s.smtp.rules.filter(x=>!x.removed)){
    await api('/zones/0d84033826bc3cd0c283efdb57a90f7a/email/routing/rules/'+rule.id,'DELETE');rule.removed=true;await save();
   }
   for(const record of s.smtp.dns.filter(x=>!x.removed)){
    const path='/zones/0d84033826bc3cd0c283efdb57a90f7a/dns_records/'+record.id;
    const current=await api(path);assert.equal(current.name,record.name);assert.equal(current.content,record.content);
    await api(path,'DELETE');record.removed=true;await save();
   }
  }
  if(s.created.worker){
   for(let i=0;i<20;i++){
    const r=await fetch(s.url+'/__cleanup',{method:'POST',headers:{Authorization:`Bearer ${s.test}`},redirect:'error'});assert.equal(r.status,200);
    const page=await r.json();deleted+=page.deleted;if(!page.deleted)break;assert.ok(i<19);
   }
   if(s.domain&&!s.domainRemoved){
    const current=(await api(account+'/workers/domains')).find(x=>x.id===s.domain.id);
    if(current){assert.equal(current.hostname,s.domain.hostname);assert.equal(current.service,s.name);await api(account+'/workers/domains/'+s.domain.id,'DELETE')}
    s.domainRemoved=true;await save();
   }
   await api(account+'/workers/scripts/'+s.name,'DELETE');
   s.created.worker=false;await save();
  }
  if(s.created.bucket){await api(account+'/r2/buckets/'+s.name,'DELETE');s.created.bucket=false;await save()}
  s.cleaned=true;await save();console.log(JSON.stringify({cleaned:true,deleted}));
 }else throw Error('Unknown mode');
}
