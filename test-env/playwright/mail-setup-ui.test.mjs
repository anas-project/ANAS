// TEST_CASES: MAILMOD-T-005 MAILMOD-T-017 MAILMOD-T-019 MAILMOD-T-020 MAILMOD-T-037 MAILMOD-T-038 MAILMOD-T-042
// Controlled-browser Tutorial checks; actual provider/DMS acceptance is separate.
import {after,before,test} from 'node:test';
import {readFile} from 'node:fs/promises';
import {chromium,expect} from '@playwright/test';
let browser;
before(async()=>{browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}:{})});});
after(async()=>{await browser?.close();});
const origin='https://mail.example.test',root=new URL('../../modules/mail/service/ui/',import.meta.url);
const initial=()=>({revision:0,draft:{}});
const result=(state,language)=>({state,language,enabled:false,outbound_status:'not-configured',checks:{source:'pending',limits:'pending',cloud_storage:state.draft.inbound==='direct'?'not-applicable':'unknown',routing:'unknown',local_delivery:'unknown',outbound:'pending',notification:state.draft.inbound==='direct'?'not-applicable':'not-configured',account_quota:state.draft.inbound==='direct'?'not-applicable':'unknown'}});
async function fixture(language='en',installerEnabled=false){
 const context=await browser.newContext(),page=await context.newPage();let saved=initial(),conflict=false,runtimeSources=[],runtimeDown=false;const requests=[],installs=[];
 await context.route('**/*',async route=>{
  const req=route.request(),url=new URL(req.url());
  if(url.pathname==='/setup')return route.fulfill({contentType:'text/html',body:await readFile(new URL('setup.html',root),'utf8')});
  if(url.pathname.startsWith('/assets/'))return route.fulfill({contentType:url.pathname.endsWith('.js')?'text/javascript':'text/css',body:await readFile(new URL(url.pathname.slice(8),root),'utf8')});
  if(url.pathname==='/api/ingress')return route.fulfill({status:runtimeDown?503:200,contentType:'application/json',body:JSON.stringify({sources:runtimeSources,public_ready:false})});
  if(url.pathname==='/api/setup/worker-install'){
   const body=req.postDataJSON();installs.push(body);return route.fulfill({contentType:'application/json',body:JSON.stringify({state:body.action==='prepare'?'prepared':'configured',source_id:'11111111-1111-4111-8111-111111111111',enabled:false})});
  }
  if(url.pathname==='/api/setup'){
   if(req.method()==='PUT'){const body=req.postDataJSON();requests.push(body);if(conflict)return route.fulfill({status:409,contentType:'application/json',body:'{"error":"draft-changed"}'});saved={revision:saved.revision+1,draft:body.draft};}
   return route.fulfill({contentType:'application/json',body:JSON.stringify({...result(saved,language),worker_install_available:installerEnabled,outbound_configure_available:installerEnabled})});
  }
  throw Error('Unexpected Tutorial request: '+url.pathname);
 });await page.goto(origin+'/setup');await expect(page.locator('#setup-form')).toBeVisible();
 return {context,page,requests,installs,setConflict:value=>{conflict=value;},setSaved:value=>{saved=value;},setRuntime:(sources,down=false)=>{runtimeSources=sources;runtimeDown=down;}};
}
test('source status timeout clears stale cards and allows a fresh observation',async()=>{
 const {context,page,setRuntime}=await fixture('zh-CN');let release;
 const stalled=async route=>{await new Promise(resolve=>{release=resolve;});try{await route.fulfill({contentType:'application/json',body:JSON.stringify({sources:[{provider:'worker-r2',mode:'active',domains:['stale.example'],cloud_check:'observed'}]})});}catch{}};
 try{
  setRuntime([{provider:'worker-r2',mode:'active',domains:['old.example'],cloud_check:'observed'}]);
  await page.locator('#runtime-refresh').click();await expect(page.locator('#runtime-sources')).toContainText('old.example');
  await context.route(origin+'/api/ingress',stalled);
  await page.evaluate(()=>{const timeout=AbortSignal.timeout;AbortSignal.timeout=ms=>timeout.call(AbortSignal,ms===10000?50:ms);});
  await page.locator('#runtime-refresh').click();await expect(page.locator('#runtime-status')).toContainText('状态读取失败');
  await expect(page.locator('#runtime-sources article')).toHaveCount(0);await expect(page.locator('#runtime-refresh')).toBeEnabled();
  await context.unroute(origin+'/api/ingress',stalled);release?.();
  setRuntime([{provider:'worker-r2',mode:'active',domains:['fresh.example'],cloud_check:'observed'}]);
  await page.locator('#runtime-refresh').click();await expect(page.locator('#runtime-sources')).toContainText('fresh.example');
  await expect(page.locator('#runtime-sources')).not.toContainText('stale.example');
 }finally{release?.();await context.close();}
});
test('lost draft save response requires reload before further changes or installation',async()=>{
 const {context,page,setSaved}=await fixture('zh-CN',true);let release,puts=0;
 const lost=async route=>{if(route.request().method()!=='PUT')return route.fallback();puts++;const body=route.request().postDataJSON();setSaved({revision:body.revision+1,draft:body.draft});await new Promise(resolve=>{release=resolve;});try{await route.abort();}catch{}};
 try{
  await page.locator('#inbound').selectOption('worker-r2');await page.locator('#domain').fill('example.org');
  await page.locator('#source_url').fill('https://worker.example.org');await page.locator('#ingress_secret_ref').fill('secrets.MAIL_READER');
  for(const [id,value]of Object.entries({additional_mailboxes:2,aliases:10,mailbox_bytes:2147483648,messages_per_minute:30,cloud_bytes:2147483648,delivered_days:7}))await page.locator('#'+id).fill(String(value));
  await page.locator('#limits_confirmed').check();
  await context.route(origin+'/api/setup',lost);
  await page.evaluate(()=>{const timeout=AbortSignal.timeout;AbortSignal.timeout=ms=>timeout.call(AbortSignal,ms===15000?50:ms);});
  await page.locator('#save').click();await expect(page.locator('#status')).toContainText('保存结果未确认');
  await expect(page.locator('#save')).toBeDisabled();await expect(page.locator('#worker-prepare')).toBeDisabled();await expect(page.locator('#reload')).toBeVisible();
  await page.locator('#setup-form').evaluate(form=>form.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true})));
  if(puts!==1)throw Error('unknown draft save was retried');
  await context.unroute(origin+'/api/setup',lost);release?.();
  const failedReload=async route=>route.fulfill({status:503,contentType:'application/json',body:'{}'});
  await context.route(origin+'/api/setup',failedReload);await page.locator('#reload').click();
  await expect(page.locator('#setup-form')).toBeHidden();await expect(page.locator('#save')).toBeDisabled();
  await context.unroute(origin+'/api/setup',failedReload);await page.locator('#reload').click();
  await expect(page.locator('#revision')).toContainText('1');await expect(page.locator('#domain')).toHaveValue('example.org');
  await expect(page.locator('#save')).toBeEnabled();await expect(page.locator('#worker-prepare')).toBeEnabled();
 }finally{release?.();await context.close();}
});
test('edits made while saving remain visible and cannot start an installation',async()=>{
 const {context,page,setSaved}=await fixture('zh-CN',true);let release,started;
 const began=new Promise(resolve=>{started=resolve;});
 const delayed=async route=>{if(route.request().method()!=='PUT')return route.fallback();const body=route.request().postDataJSON();const state={revision:body.revision+1,draft:body.draft};setSaved(state);started();await new Promise(resolve=>{release=resolve;});await route.fulfill({contentType:'application/json',body:JSON.stringify({...result(state,'zh-CN'),worker_install_available:true})});};
 try{
  await page.locator('#inbound').selectOption('worker-r2');await page.locator('#domain').fill('original.example');
  for(const [id,value]of Object.entries({additional_mailboxes:2,aliases:10,mailbox_bytes:2147483648,messages_per_minute:30,cloud_bytes:2147483648,delivered_days:7}))await page.locator('#'+id).fill(String(value));
  await page.locator('#limits_confirmed').check();await context.route(origin+'/api/setup',delayed);
  await page.locator('#save').click();await began;await page.locator('#domain').fill('edited.example');release();
  await expect(page.locator('#status')).toContainText('保存期间字段发生变化');await expect(page.locator('#domain')).toHaveValue('edited.example');
  await expect(page.locator('#save')).toBeDisabled();await expect(page.locator('#worker-prepare')).toBeDisabled();
  await context.unroute(origin+'/api/setup',delayed);await page.locator('#reload').click();
  await expect(page.locator('#domain')).toHaveValue('original.example');await expect(page.locator('#worker-prepare')).toBeEnabled();
 }finally{release?.();await context.close();}
});
test('reloading a changed inbound scheme discards its pending SMTP observation',async()=>{
 const {context,page,setConflict,setSaved}=await fixture('zh-CN');let release,finished=0;
 try{
  await context.route(origin+'/api/setup/vps-smtp-probe',async route=>{const input=route.request().postDataJSON();await new Promise(resolve=>{release=resolve;});await route.fulfill({contentType:'application/json',body:JSON.stringify({host:input.host,ipv4:{status:'observed',addresses:[{address:'8.8.8.8',status:'smtp-greeting',tls:'verified'}]},ipv6:{status:'not-found',addresses:[]}})});finished++;});
  await page.locator('#inbound').selectOption('direct');await page.locator('#vps-probe-host').fill('mx.example.test');await page.locator('#vps-probe-check').click();await expect.poll(()=>typeof release).toBe('function');
  setConflict(true);await page.locator('#save').click();await expect(page.locator('#reload')).toBeVisible();setSaved({revision:1,draft:{inbound:'vps-direct'}});setConflict(false);await page.locator('#reload').click();await expect(page.locator('#inbound')).toHaveValue('vps-direct');
  release();await expect.poll(()=>finished).toBe(1);await expect(page.locator('#vps-probe-result')).toBeEmpty();await expect(page.locator('#enable')).toBeDisabled();
 }finally{await context.close();}
});
test('VPS SMTP check shows per-address observations and discards stale results',async()=>{
 const {context,page}=await fixture('zh-CN');let resolveProbe;let submitted,finished=0;
 try{
  await context.route(origin+'/api/setup/vps-smtp-probe',async route=>{const input=route.request().postDataJSON();submitted=input;await new Promise(resolve=>{resolveProbe=resolve;});await route.fulfill({contentType:'application/json',body:JSON.stringify({host:input.host,ipv4:{status:'observed',addresses:[{address:'8.8.8.8',status:'smtp-greeting'},{address:'1.1.1.1',status:'refused'}]},ipv6:{status:'unknown',addresses:[]},checked_at:'2026-10-28T12:00:00Z'})});finished++;});
  await page.locator('#inbound').selectOption('vps-direct');await expect(page.locator('#vps-probe')).toBeVisible();await expect(page.locator('#vps-probe-note')).toContainText('不发送邮件');
  await page.locator('#vps-probe-host').fill('relay.example.test');await page.locator('#vps-probe-check').click();await expect(page.locator('#vps-probe-result')).toContainText('正在检查');
  await page.locator('#vps-probe-host').fill('other.example.test');await expect.poll(()=>typeof resolveProbe).toBe('function');resolveProbe();await expect.poll(()=>finished).toBe(1);await expect(page.locator('#vps-probe-result')).toBeEmpty();resolveProbe=null;
  await page.locator('#vps-probe-host').fill('relay.example.test');await page.locator('#vps-probe-check').click();await expect(page.locator('#vps-probe-result')).toContainText('正在检查');await expect.poll(()=>typeof resolveProbe).toBe('function');resolveProbe();
  await expect(page.locator('#vps-probe-result')).toContainText('8.8.8.8: 已观察到 SMTP 问候');await expect(page.locator('#vps-probe-result')).toContainText('1.1.1.1: 连接被拒绝');await expect(page.locator('#vps-probe-result')).toContainText('IPv6: 未知或暂不可用');
  if(submitted.host!=='relay.example.test')throw Error('incorrect probe target');await expect(page.locator('#enable')).toBeDisabled();
  await page.locator('#inbound').selectOption('worker-r2');await expect(page.locator('#vps-probe')).toBeHidden();
 }finally{await context.close();}
});
test('NAS direct SMTP check reports TLS separately and never enables receipt',async()=>{
 const {context,page}=await fixture('zh-CN');let submitted;
 try{
  await context.route(origin+'/api/setup/vps-smtp-probe',async route=>{submitted=route.request().postDataJSON();await route.fulfill({contentType:'application/json',body:JSON.stringify({host:submitted.host,ipv4:{status:'observed',addresses:[{address:'8.8.8.8',status:'smtp-greeting',tls:'verified'},{address:'1.1.1.1',status:'smtp-greeting',tls:'not-advertised'},{address:'9.9.9.9',status:'smtp-greeting',tls:'invalid-certificate'},{address:'8.8.4.4',status:'smtp-greeting',tls:'unknown'}]},ipv6:{status:'not-found',addresses:[]},checked_at:'2026-10-28T12:00:00Z'})});});
  await page.locator('#inbound').selectOption('direct');await expect(page.locator('#vps-probe')).toBeVisible();await expect(page.locator('#vps-probe-note')).toContainText('不能证明公网可达或实际投递');
  await page.locator('#vps-probe-host').fill('mx.example.test');await page.locator('#vps-probe-check').click();
  for(const text of ['TLS 证书验证通过','未提供 STARTTLS','TLS 证书无效','TLS 未知'])await expect(page.locator('#vps-probe-result')).toContainText(text);
  if(submitted.host!=='mx.example.test')throw Error('Wrong SMTP target');await expect(page.locator('#enable')).toBeDisabled();
  await page.locator('#inbound').selectOption('worker-r2');await expect(page.locator('#vps-probe')).toBeHidden();await page.locator('#inbound').selectOption('direct');await expect(page.locator('#vps-probe-result')).toBeEmpty();
 }finally{await context.close();}
});
test('sending configuration uses the saved draft and clears the transient password',async()=>{
 const {context,page,requests}=await fixture('zh-CN',true);let submitted=[];try{
  await context.route(origin+'/api/setup/outbound-apply',async route=>{submitted.push(route.request().postDataJSON());await route.fulfill({contentType:'application/json',body:JSON.stringify({state:'configured',enabled:false})});});
  await page.locator('#provider').selectOption('resend');await expect(page.locator('#secret_ref')).toHaveValue('secrets.MAIL_OUTBOUND_PASSWORDS');await expect(page.locator('#outbound-apply')).toBeDisabled();
  await page.locator('#save').click();await expect(page.locator('#outbound-apply')).toBeEnabled();const operation=await page.locator('#outbound-operation').inputValue();
  await page.locator('#outbound-password').fill('synthetic-smtp-password');await page.locator('#outbound-apply').click();
  await expect(page.locator('#outbound-apply-result')).toContainText('部署和实际送达仍未验证');await expect(page.locator('#outbound-password')).toBeEmpty();await expect(page.locator('#outbound-apply')).toBeDisabled();
  if(submitted.length!==1||submitted[0].revision!==1||submitted[0].operation_id!==operation||submitted[0].password!=='synthetic-smtp-password'||JSON.stringify(requests).includes('synthetic-smtp-password'))throw Error('incorrect private submission');
  if(await page.evaluate(()=>JSON.stringify({...localStorage,...sessionStorage}))!=='{}')throw Error('private value persisted');await expect(page.locator('#enable')).toBeDisabled();
  await page.locator('#save').click();await expect(page.locator('#outbound-apply')).toBeEnabled();if(await page.locator('#outbound-operation').inputValue()===operation)throw Error('operation ID reused after completed action');
 }finally{await context.close();}
});
test('direct sending submits no SMTP password',async()=>{
 const {context,page}=await fixture('zh-CN',true);let submitted;try{
  await context.route(origin+'/api/setup/outbound-apply',async route=>{submitted=route.request().postDataJSON();await route.fulfill({contentType:'application/json',body:'{"state":"configured","enabled":false}'});});
  await page.locator('#provider').selectOption('direct');await page.locator('#save').click();await expect(page.locator('#outbound-password-field')).toBeHidden();await page.locator('#outbound-apply').click();
  await expect(page.locator('#outbound-apply-result')).toContainText('配置已保存');if(submitted.password!=='')throw Error('direct route sent a password');
 }finally{await context.close();}
});
test('unknown sending configuration result blocks blind retry and clears the password',async()=>{
 const {context,page}=await fixture('zh-CN',true);let calls=0;try{
  await context.route(origin+'/api/setup/outbound-apply',async route=>{calls++;await route.fulfill({status:503,contentType:'application/json',body:'{"error":"outbound-unknown"}'});});
  await page.locator('#provider').selectOption('resend');await page.locator('#save').click();await page.locator('#outbound-password').fill('synthetic-smtp-password');await page.locator('#outbound-apply').click();
  await expect(page.locator('#outbound-apply-result')).toContainText('请勿盲目重试');await expect(page.locator('#outbound-password')).toBeEmpty();await expect(page.locator('#outbound-apply')).toBeDisabled();
  await page.locator('#save').click();await expect(page.locator('#outbound-apply')).toBeDisabled();if(calls!==1)throw Error('unknown action automatically retried');
 }finally{await context.close();}
});
for(const [code,text,disabled]of [['outbound-service-forbidden','联系宿主管理员检查',true],['administrator-required','请勿盲目重试',true]])test(`sending configuration handles ${code} conservatively`,async()=>{
 const {context,page}=await fixture('zh-CN',true);try{
  await context.route(origin+'/api/setup/outbound-apply',async route=>route.fulfill({status:403,contentType:'application/json',body:JSON.stringify({error:code})}));
  await page.locator('#provider').selectOption('resend');await page.locator('#save').click();await page.locator('#outbound-password').fill('synthetic-smtp-password');await page.locator('#outbound-apply').click();
  await expect(page.locator('#outbound-apply-result')).toContainText(text);await expect(page.locator('#outbound-password')).toBeEmpty();if(disabled)await expect(page.locator('#outbound-apply')).toBeDisabled();else await expect(page.locator('#outbound-apply')).toBeEnabled();
 }finally{await context.close();}
});
for(const language of ['en','zh-CN','zh-TW','de','es','fr','ja','ko'])test(`sending configuration guidance is available in ${language}`,async()=>{
 const {context,page}=await fixture(language,true);try{
  await page.locator('#provider').selectOption('direct');await expect(page.locator('#outbound-apply-box')).toBeVisible();await expect(page.locator('#outbound-apply-title')).not.toBeEmpty();await expect(page.locator('#outbound-apply-note')).not.toBeEmpty();await expect(page.locator('#outbound-apply')).toBeDisabled();
 }finally{await context.close();}
});
for(const [language,title]of Object.entries({en:'Mail service setup','zh-CN':'邮件服务配置','zh-TW':'郵件服務設定',de:'Maildienst einrichten',es:'Configurar correo',fr:'Configurer la messagerie',ja:'メールサービス設定',ko:'메일 서비스 설정'}))test(`Tutorial ${language}: supported choices, materials and honest status`,async()=>{
 const {context,page}=await fixture(language);try{
  await expect(page.locator('h1')).toHaveText(title);await expect(page.locator('#enable')).toBeDisabled();
  await expect(page.locator('#outbound-runtime')).not.toBeEmpty();
  const choices=await page.locator('#inbound option').evaluateAll(options=>options.map(o=>o.value));if(JSON.stringify(choices)!=='["","direct","worker-r2","cloud-relay","vps-direct"]')throw Error('Unexpected ingress option exposed');
  await page.locator('#inbound').selectOption('direct');await expect(page.locator('#cloud-fields')).toBeHidden();await expect(page.locator('#notification-section')).toBeHidden();await expect(page.locator('#inbound-guide')).toContainText('25');await expect(page.locator('#vps-probe')).toBeVisible();await expect(page.locator('#vps-probe-note')).not.toBeEmpty();
  if(language==='zh-CN')await expect(page.locator('#inbound-guide')).toContainText('不能保证邮件保留或最终送达');
  await page.locator('#inbound').selectOption('worker-r2');await expect(page.locator('#inbound-guide')).toContainText('R2');await expect(page.locator('#inbound-guide a')).toHaveCount(4);
  await expect(page.locator('#notification-path')).toContainText('/api/ingress/notify');await expect(page.locator('#notification-origin-label')).not.toBeEmpty();
  if(language==='zh-CN'&&process.env.MAIL_SETUP_UI_OUTPUT)await page.screenshot({path:process.env.MAIL_SETUP_UI_OUTPUT,fullPage:true});
  await page.locator('#inbound').selectOption('cloud-relay');await expect(page.locator('#inbound-guide')).toContainText('25');
  await page.locator('#inbound').selectOption('vps-direct');await expect(page.locator('#inbound-guide')).toContainText('25');await expect(page.locator('#inbound-guide a')).toHaveCount(0);await expect(page.locator('#enable')).toBeDisabled();
  await page.locator('#provider').selectOption('resend');await expect(page.locator('#host')).toHaveValue('smtp.resend.com');await expect(page.locator('#outbound-guide')).toContainText('3');
  await page.locator('#provider').selectOption('smtp');await expect(page.locator('#outbound-guide')).toContainText('eusend');await expect(page.locator('#host')).toBeEmpty();
  await page.locator('#provider').selectOption('direct');await expect(page.locator('#smtp-fields')).toBeHidden();await expect(page.locator('#outbound-guide')).toContainText('25');await expect(page.locator('#enable')).toBeDisabled();
  if(language==='zh-CN'){await expect(page.locator('#outbound-guide')).toContainText('信誉');await expect(page.locator('#outbound-guide')).toContainText('第三方服务也不能保证进入收件箱');}
  await expect(page.locator('#quota-guide')).toContainText('2026-10-26');
  if((await page.locator('#provider option').allTextContents()).join(' ').includes('SMTP2GO'))throw Error('Canceled service exposed');
  await expect(page.locator('input[type="password"]:visible')).toHaveCount(0);
  if(await page.evaluate(()=>JSON.stringify({...localStorage,...sessionStorage}))!=='{}')throw Error('Draft or secrets persisted in browser');
 }finally{await context.close();}
});
test('Worker setup sends transient tokens only to the installer and requires a saved draft',async()=>{
 const {context,page,requests,installs}=await fixture('zh-CN',true);try{
  await page.locator('#inbound').selectOption('worker-r2');await expect(page.locator('#worker-installer')).toBeVisible();await expect(page.locator('#worker-prepare')).toBeDisabled();
  await page.locator('#domain').fill('example.test');
  for(const [id,value]of Object.entries({additional_mailboxes:2,aliases:10,mailbox_bytes:2147483648,messages_per_minute:30,cloud_bytes:2147483648,delivered_days:7}))await page.locator('#'+id).fill(String(value));
  await page.locator('#limits_confirmed').check();await page.locator('#save').click();await expect(page.locator('#worker-prepare')).toBeEnabled();
  const operation=await page.locator('#worker-operation').inputValue();if(!/^cfg-[0-9a-f]{32}$/.test(operation))throw Error('operation ID missing');
  for(const [id,value]of Object.entries({'worker-account':'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','worker-zone':'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','worker-name':'anas-worker','worker-bucket':'anas-bucket','worker-deploy-token':'synthetic-deploy-token','worker-routing-token':'synthetic-routing-token'}))await page.locator('#'+id).fill(value);
  await page.locator('#worker-prepare').click();await expect(page.locator('#worker-result')).toContainText('准备完成');await expect(page.locator('#worker-deploy-token')).toBeEmpty();await expect(page.locator('#worker-routing-token')).toBeEmpty();
  await page.locator('#worker-routing-token').fill('synthetic-routing-token');await page.locator('#worker-commit').click();await expect(page.locator('#worker-result')).toContainText('仍需部署');
  if(installs.length!==2||installs[0].operation_id!==operation||installs[1].operation_id!==operation||installs[1].deployment_token||installs[1].routing_token!=='synthetic-routing-token')throw Error('incorrect installer request');
  if(JSON.stringify(requests).includes('synthetic-')||await page.evaluate(()=>JSON.stringify({...localStorage,...sessionStorage}))!=='{}')throw Error('credentials persisted');
  await expect(page.locator('#enable')).toBeDisabled();await page.locator('#domain').fill('edited.test');await expect(page.locator('#worker-prepare')).toBeDisabled();
 }finally{await context.close();}
});
test('Worker installation does not show success after an in-flight draft edit',async()=>{
 const {context,page}=await fixture('en',true);let release,finished=0;try{
  await context.route(origin+'/api/setup/worker-install',async route=>{await new Promise(resolve=>{release=resolve;});await route.fulfill({contentType:'application/json',body:JSON.stringify({state:'prepared',source_id:'11111111-1111-4111-8111-111111111111',enabled:false})});finished++;});
  await page.locator('#inbound').selectOption('worker-r2');await page.locator('#domain').fill('example.test');
  for(const [id,value]of Object.entries({additional_mailboxes:2,aliases:10,mailbox_bytes:2147483648,messages_per_minute:30,cloud_bytes:2147483648,delivered_days:7}))await page.locator('#'+id).fill(String(value));
  await page.locator('#limits_confirmed').check();await page.locator('#save').click();await expect(page.locator('#worker-prepare')).toBeEnabled();
  for(const [id,value]of Object.entries({'worker-account':'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','worker-zone':'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','worker-name':'anas-worker','worker-bucket':'anas-bucket','worker-deploy-token':'synthetic-deploy-token','worker-routing-token':'synthetic-routing-token'}))await page.locator('#'+id).fill(value);
  await page.locator('#worker-prepare').click();await expect.poll(()=>typeof release).toBe('function');await page.locator('#domain').fill('changed.test');release();await expect.poll(()=>finished).toBe(1);
  await expect(page.locator('#worker-result')).toContainText('Outcome unknown');await expect(page.locator('#worker-prepare')).toBeDisabled();await expect(page.locator('#worker-deploy-token')).toBeEmpty();await expect(page.locator('#worker-routing-token')).toBeEmpty();
 }finally{await context.close();}
});
test('direct receiving saves only a draft without cloud credentials or cloud limits',async()=>{
 const {context,page,requests}=await fixture('zh-CN');try{
  await page.locator('#inbound').selectOption('worker-r2');await page.locator('#source_url').fill('https://source.example.org');await page.locator('#ingress_secret_ref').fill('secrets.MAIL_INGRESS');
  await page.locator('#inbound').selectOption('direct');await page.locator('#domain').fill('example.org');
  for(const [id,value]of Object.entries({additional_mailboxes:2,aliases:10,mailbox_bytes:2147483648,messages_per_minute:30}))await page.locator('#'+id).fill(String(value));
  await page.locator('#limits_confirmed').check();await page.locator('#save').click();await expect(page.locator('#status')).toContainText('草稿已保存');
  if(requests.length!==1||requests[0].draft.source_url!==''||requests[0].draft.ingress_secret_ref!==''||requests[0].draft.limits.cloud_bytes!==0||requests[0].draft.limits.delivered_days!==0)throw Error('direct receiving carried cloud settings');
  await expect(page.locator('#checks')).toContainText('云端存取: 不适用');await expect(page.locator('#checks')).toContainText('收件方案: 待验证');
  await expect(page.locator('#enable')).toBeDisabled();
 }finally{await context.close();}
});
test('VPS direct reception is a resumable draft, not an enabled service',async()=>{
 const {context,page,requests}=await fixture('zh-CN');try{
  await page.locator('#inbound').selectOption('vps-direct');await page.locator('#domain').fill('example.org');await page.locator('#source_url').fill('https://relay.example.org');await page.locator('#ingress_secret_ref').fill('secrets.MAIL_INGRESS');
  await page.locator('#save').click();await expect(page.locator('#status')).toContainText('草稿已保存');
  if(requests[0]?.draft?.inbound!=='vps-direct')throw Error('VPS draft not saved');
  await page.reload();await expect(page.locator('#inbound')).toHaveValue('vps-direct');await expect(page.locator('#enable')).toBeDisabled();
 }finally{await context.close();}
});
for(const inbound of ['worker-r2','vps-direct'])test(`${inbound} keeps only notification origin and Secret reference in its draft`,async()=>{
 const {context,page,requests}=await fixture('zh-CN');try{
  await page.locator('#inbound').selectOption(inbound);await page.locator('#notification_url').fill('https://mail.example.org:10000');await page.locator('#notification_secret_ref').fill('secrets.MAIL_NOTIFICATION');
  await page.locator('#save').click();await expect(page.locator('#status')).toContainText('草稿已保存');
  if(requests[0]?.draft?.notification_url!=='https://mail.example.org:10000'||requests[0]?.draft?.notification_secret_ref!=='secrets.MAIL_NOTIFICATION')throw Error('notification draft missing');
  await page.reload();await expect(page.locator('#notification_url')).toHaveValue('https://mail.example.org:10000');await expect(page.locator('#enable')).toBeDisabled();
 }finally{await context.close();}
});
test('live missing route remains distinct from the saved draft',async()=>{
 const {context,page}=await fixture('zh-CN');try{
  await expect(page.locator('#outbound-runtime')).toHaveText('当前发信路由：未配置。');
  await page.locator('#provider').selectOption('direct');await page.locator('#save').click();
  await expect(page.locator('#outbound-runtime')).toHaveText('当前发信路由：未配置。');
  await expect(page.locator('#enable')).toBeDisabled();
 }finally{await context.close();}
});
test('refreshing the live sending status preserves unsaved edits',async()=>{
 const {context,page,requests}=await fixture('zh-CN');try{
  await page.locator('#domain').fill('unsaved.example.org');await page.locator('#outbound-refresh').click();
  await expect(page.locator('#outbound-runtime')).toHaveText('当前发信路由：未配置。');
  await expect(page.locator('#domain')).toHaveValue('unsaved.example.org');
  if(requests.length)throw Error('refresh saved an unsaved draft');
 }finally{await context.close();}
});
for(const [state,text] of [['queued-observed','观察到队列邮件'],['none-observed','未观察到队列邮件'],['unknown','队列状态未知'],['empty','队列状态未知']])test(`queue observation ${state} does not authorize route changes`,async()=>{
 const {context,page,requests}=await fixture('zh-CN');try{
  let method='';await context.route(origin+'/api/setup/outbound-queue',async route=>{method=route.request().method();await route.fulfill({contentType:'application/json',body:JSON.stringify({status:state,checked_at:'2026-10-28T00:00:00Z'})});});
  await page.locator('#domain').fill('unsaved.example.org');await page.locator('#queue-check').click();
  await expect(page.locator('#queue-result')).toContainText(text);await expect(page.locator('#queue-note')).toContainText('也包含入站邮件');await expect(page.locator('#queue-note')).toContainText('不能保证队列为空');await expect(page.locator('#enable')).toBeDisabled();
  await expect(page.locator('#domain')).toHaveValue('unsaved.example.org');if(method!=='POST'||requests.length)throw Error('queue observation changed the draft');
 }finally{await context.close();}
});
test('DNS observation shows records without claiming route or delivery readiness',async()=>{
 const {context,page,requests}=await fixture('zh-CN');try{
  let submitted;
  await context.route(origin+'/api/setup/dns',async route=>{submitted=route.request().postDataJSON();await route.fulfill({contentType:'application/json',body:JSON.stringify({domain:'example.org',expected_mx:'mx.example.org',expected_status:'observed',status:'observed',records:[{preference:10,host:'mx.example.org',a_status:'observed',a:['192.0.2.1'],aaaa_status:'observed',aaaa:['2001:db8::1']}],checked_at:'2026-10-28T00:00:00Z'})});});
  await page.locator('#domain').fill('example.org');await page.locator('#dns-expected').fill('mx.example.org');await page.locator('#dns-check').click();await expect(page.locator('#dns-result')).toContainText('10 mx.example.org');await expect(page.locator('#dns-result')).toContainText('已观察到预期 MX');
  await expect(page.locator('#dns-result')).toContainText('A: 192.0.2.1');await expect(page.locator('#dns-result')).toContainText('AAAA: 2001:db8::1');await expect(page.locator('#dns-result')).toContainText('不代表指向所选方案');await expect(page.locator('#enable')).toBeDisabled();
  if(submitted.domain!=='example.org'||submitted.expected_mx!=='mx.example.org'||requests.length)throw Error('DNS check changed the draft');
  await page.locator('#dns-expected').fill('other.example.org');await expect(page.locator('#dns-result')).toBeEmpty();
  await page.locator('#domain').fill('other.example.org');await expect(page.locator('#dns-result')).toBeEmpty();
 }finally{await context.close();}
});
test('sending DNS uses a separate domain and never marks mail enabled',async()=>{
 const {context,page,requests}=await fixture('zh-CN');try{
  let submitted;
  await context.route(origin+'/api/setup/outbound-dns',async route=>{submitted=route.request().postDataJSON();await route.fulfill({contentType:'application/json',body:JSON.stringify({domain:'send.example.org',selector:'eusend',spf:'observed',dmarc:'not-found',dkim:'unknown',checked_at:'2026-10-28T00:00:00Z'})});});
  await page.locator('#domain').fill('receive.example.org');await page.locator('#sending-domain').fill('send.example.org');await page.locator('#sending-selector').fill('eusend');await page.locator('#sending-dns-check').click();
  await expect(page.locator('#sending-dns-result')).toContainText('SPF: 已观察到记录');await expect(page.locator('#sending-dns-result')).toContainText('DMARC: 未找到匹配记录');await expect(page.locator('#sending-dns-result')).toContainText('DKIM: DNS 不可用');
  await expect(page.locator('#sending-dns-note')).toContainText('DMARC 可能继承上级域');await expect(page.locator('#enable')).toBeDisabled();
  if(submitted.domain!=='send.example.org'||submitted.selector!=='eusend'||requests.length)throw Error('sending DNS check modified the draft');
  await page.locator('#sending-selector').fill('other');await expect(page.locator('#sending-dns-result')).toBeEmpty();
 }finally{await context.close();}
});
test('DNS replies for different inputs show unknown without claiming readiness',async()=>{
 const {context,page}=await fixture('zh-CN');try{
  await context.route(origin+'/api/setup/dns',async route=>await route.fulfill({contentType:'application/json',body:JSON.stringify({domain:'other.example.org',expected_mx:'',status:'observed',records:[]})}));
  await context.route(origin+'/api/setup/outbound-dns',async route=>await route.fulfill({contentType:'application/json',body:JSON.stringify({domain:'send.example.org',selector:'other',spf:'observed',dmarc:'observed',dkim:'observed'})}));
  await page.locator('#domain').fill('example.org');await page.locator('#dns-check').click();await expect(page.locator('#dns-result')).toContainText('DNS 不可用');
  await page.locator('#sending-domain').fill('send.example.org');await page.locator('#sending-selector').fill('selector');await page.locator('#sending-dns-check').click();
  await expect(page.locator('#sending-dns-result')).toContainText('SPF: DNS 不可用');await expect(page.locator('#sending-dns-result')).toContainText('DKIM: DNS 不可用');await expect(page.locator('#enable')).toBeDisabled();
 }finally{await context.close();}
});
test('sending DNS rejects stale responses after changing the domain',async()=>{
 const {context,page}=await fixture('zh-CN');try{
  let release;const barrier=new Promise(resolve=>{release=resolve;});
  await context.route(origin+'/api/setup/outbound-dns',async route=>{await barrier;await route.fulfill({contentType:'application/json',body:JSON.stringify({domain:'old.example.org',selector:'',spf:'observed',dmarc:'observed',dkim:'not-configured'})});});
  await page.locator('#sending-domain').fill('old.example.org');await page.locator('#sending-dns-check').click();await page.locator('#sending-domain').fill('new.example.org');release();
  await expect(page.locator('#sending-dns-result')).toBeEmpty();await expect(page.locator('#enable')).toBeDisabled();
 }finally{await context.close();}
});
test('DNS address lookup failure remains separate from an observed MX',async()=>{
 const {context,page}=await fixture('zh-CN');try{
  await context.route(origin+'/api/setup/dns',async route=>await route.fulfill({contentType:'application/json',body:JSON.stringify({domain:'example.org',expected_mx:'',expected_status:'not-configured',status:'observed',records:[{preference:10,host:'mx.example.org',a_status:'observed',a:['192.0.2.1'],aaaa_status:'unknown'}],checked_at:'2026-10-28T00:00:00Z'})}));
  await page.locator('#domain').fill('example.org');await page.locator('#dns-check').click();
  await expect(page.locator('#dns-result')).toContainText('已观察到 MX');await expect(page.locator('#dns-result')).toContainText('A: 192.0.2.1');await expect(page.locator('#dns-result')).toContainText('AAAA: A/AAAA 查询不可用或结果不完整');await expect(page.locator('#enable')).toBeDisabled();
 }finally{await context.close();}
});
test('DNS comparison reports a missing expected host without enabling mail',async()=>{
 const {context,page}=await fixture('zh-CN');try{
  await context.route(origin+'/api/setup/dns',async route=>await route.fulfill({contentType:'application/json',body:JSON.stringify({domain:'example.org',expected_mx:'other.example.org',expected_status:'not-observed',status:'observed',records:[{preference:10,host:'mx.example.org'}],checked_at:'2026-10-28T00:00:00Z'})}));
  await page.locator('#domain').fill('example.org');await page.locator('#dns-expected').fill('other.example.org');await page.locator('#dns-check').click();
  await expect(page.locator('#dns-result')).toContainText('未观察到预期 MX');await expect(page.locator('#enable')).toBeDisabled();
 }finally{await context.close();}
});
for(const [status,expected,description] of [['null-mx','not-observed','域名明确声明不接收邮件'],['invalid-mx','unknown','MX 配置无效']])test(`DNS ${status} cannot appear as a valid receiving route`,async()=>{
 const {context,page}=await fixture('zh-CN');try{
  await context.route(origin+'/api/setup/dns',async route=>await route.fulfill({contentType:'application/json',body:JSON.stringify({domain:'example.org',expected_mx:'mx.example.org',expected_status:expected,status,records:[{preference:0,host:'.'}],checked_at:'2026-10-28T00:00:00Z'})}));
  await page.locator('#domain').fill('example.org');await page.locator('#dns-expected').fill('mx.example.org');await page.locator('#dns-check').click();
  await expect(page.locator('#dns-result')).toContainText(description);await expect(page.locator('#dns-result')).not.toContainText('已观察到预期 MX');await expect(page.locator('#enable')).toBeDisabled();
 }finally{await context.close();}
});
test('direct sending draft requires no SMTP credentials',async()=>{
 const {context,page,requests}=await fixture('zh-CN');try{
  await page.locator('#provider').selectOption('direct');await page.locator('#save').click();await expect(page.locator('#status')).toContainText('草稿已保存');
  const outbound=requests[0]?.draft?.outbound;if(!outbound||outbound.provider!=='direct'||outbound.host||outbound.port||outbound.tls||outbound.username||outbound.secret_ref)throw Error('Direct draft contains SMTP credentials or relay settings');
  await page.reload();await expect(page.locator('#provider')).toHaveValue('direct');await expect(page.locator('#smtp-fields')).toBeHidden();await expect(page.locator('#enable')).toBeDisabled();
 }finally{await context.close();}
});
test('nonsecret draft survives reentry, retains explicit zero limits and never enables mail',async()=>{
 const {context,page,requests}=await fixture('zh-CN');try{
  await page.locator('#inbound').selectOption('worker-r2');await page.locator('#domain').fill('example.org');await page.locator('#source_url').fill('https://source.example.org');await page.locator('#ingress_secret_ref').fill('secrets.MAIL_INGRESS');
  for(const [id,value]of Object.entries({additional_mailboxes:0,aliases:10,mailbox_bytes:2147483648,messages_per_minute:30,cloud_bytes:2147483648,delivered_days:7}))await page.locator('#'+id).fill(String(value));
  await page.locator('#limits_confirmed').check();await page.locator('#save').click();await expect(page.locator('#status')).toContainText('草稿已保存');
  if(requests.length!==1||requests[0].draft.limits.additional_mailboxes!==0||!requests[0].draft.limits_confirmed||Object.hasOwn(requests[0],'enabled'))throw Error('Draft authority or explicit limits changed');
  await page.reload();await expect(page.locator('#additional_mailboxes')).toHaveValue('0');await expect(page.locator('#domain')).toHaveValue('example.org');await expect(page.locator('#ingress_secret_ref')).toHaveValue('secrets.MAIL_INGRESS');await expect(page.locator('#enable')).toBeDisabled();await expect(page.locator('#checks')).toContainText('未知');
 }finally{await context.close();}
});
test('draft conflict preserves edits and requires an explicit reload',async()=>{
 const {context,page,setConflict}=await fixture();try{
  await page.locator('#domain').fill('mine.example.org');setConflict(true);await page.locator('#save').click();await expect(page.locator('#status')).toContainText('Another administrator');await expect(page.locator('#domain')).toHaveValue('mine.example.org');await expect(page.locator('#reload')).toBeVisible();
  setConflict(false);await page.locator('#reload').click();await expect(page.locator('#domain')).toBeEmpty();await expect(page.locator('#enable')).toBeDisabled();
 }finally{await context.close();}
});
test('confirming explicit limits does not silently fill missing quantities',async()=>{
 const {context,page,requests}=await fixture();try{await page.locator('#limits_confirmed').check();await page.locator('#save').click();if(requests.length)throw Error('Blank limits were submitted as explicit zero');}finally{await context.close();}
});
test('Tutorial remains readable on a narrow screen in dark mode',async()=>{
 const {context,page}=await fixture('zh-CN');try{await page.setViewportSize({width:390,height:844});await page.emulateMedia({colorScheme:'dark'});await page.locator('#inbound').selectOption('worker-r2');await page.locator('#provider').selectOption('smtp');if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('Tutorial overflows the viewport');await expect(page.locator('#enable')).toBeDisabled();}finally{await context.close();}
});

test('runtime status distinguishes observed limits, failures and unknown quota without enabling mail',async()=>{
 const {context,page,setRuntime}=await fixture('zh-CN');try{
  await expect(page.locator('#runtime-status')).toContainText('尚未绑定');
  setRuntime([{provider:'worker-r2',mode:'active',domains:['example.org'],cloud_check:'configuration-changed',rule_read:'observed',local:{last_poll:'2026-10-26T17:00:00Z',backlog:3,task_errors:1,poll_error:'source-unavailable',route_revision:4,route_observed_at:'2026-10-26T17:00:00Z',route_error:'source-write-unconfirmed',expired_notices:2},cloud:{used_bytes:1024,capacity_bytes:2147483648,notifications:'unconfigured',account_quota:'unknown',alert:{code:'capacity-or-admission-limit'}}}]);
  await page.locator('#runtime-refresh').click();await expect(page.locator('#runtime-sources')).toContainText('配置已改变');await expect(page.locator('#runtime-sources')).toContainText('3 / 1');await expect(page.locator('#runtime-sources')).toContainText('容量或接收上限');await expect(page.locator('#runtime-sources')).toContainText('规则读取（写权限未验证）');await expect(page.locator('#runtime-sources')).toContainText('未知');await expect(page.locator('#enable')).toBeDisabled();
  setRuntime([{provider:'worker-r2',mode:'active',domains:['example.org'],cloud_check:'observed',rule_read:'authentication-failed'}]);await page.locator('#runtime-refresh').click();await expect(page.locator('#runtime-sources')).toContainText('认证失败');await expect(page.locator('#enable')).toBeDisabled();
  setRuntime([],true);await page.locator('#runtime-refresh').click();await expect(page.locator('#runtime-status')).toContainText('状态读取失败');await expect(page.locator('#runtime-sources article')).toHaveCount(0);
 }finally{await context.close();}
});
for(const [field,value]of [['worker-operation','cfg-99999999999999999999999999999999'],['worker-name','changed-worker'],['worker-capacity','12345'],['worker-drain','33333333-3333-4333-8333-333333333333']])test(`Worker in-flight ${field} edit cannot display the old result as current`,async()=>{
 const {context,page}=await fixture('en',true);let release,finished=0;try{
  await context.route(origin+'/api/setup/worker-install',async route=>{await new Promise(resolve=>{release=resolve;});await route.fulfill({contentType:'application/json',body:JSON.stringify({state:'prepared',source_id:'11111111-1111-4111-8111-111111111111',enabled:false})});finished++;});
  await page.locator('#inbound').selectOption('worker-r2');await page.locator('#domain').fill('example.test');
  for(const [id,v]of Object.entries({additional_mailboxes:2,aliases:10,mailbox_bytes:2147483648,messages_per_minute:30,cloud_bytes:2147483648,delivered_days:7}))await page.locator('#'+id).fill(String(v));
  await page.locator('#limits_confirmed').check();await page.locator('#save').click();
  for(const [id,v]of Object.entries({'worker-account':'a'.repeat(32),'worker-zone':'b'.repeat(32),'worker-name':'anas-worker','worker-bucket':'anas-bucket','worker-deploy-token':'private-deploy','worker-routing-token':'private-routing'}))await page.locator('#'+id).fill(v);
  const originalOperation=await page.locator('#worker-operation').inputValue();await page.locator('#worker-prepare').click();await expect.poll(()=>typeof release).toBe('function');await page.locator('#'+field).fill(value);release();await expect.poll(()=>finished).toBe(1);
  await expect(page.locator('#worker-result')).toContainText('Outcome unknown');await expect(page.locator('#worker-result')).toContainText(originalOperation);await expect(page.locator('#worker-result')).not.toContainText('Prepared;');await expect(page.locator('#worker-routing-token')).toBeEmpty();await page.locator('#worker-bucket').fill('other-bucket');await expect(page.locator('#worker-result')).toContainText(originalOperation);
 }finally{await context.close();}
});
test('outbound SMTP check reports the entered connection without credentials or delivery claims',async()=>{
 const {context,page}=await fixture('zh-CN',true);let submitted;try{
  await context.route(origin+'/api/setup/outbound-smtp-probe',async route=>{submitted=route.request().postDataJSON();await route.fulfill({contentType:'application/json',body:JSON.stringify({...submitted,ipv4:{status:'observed',addresses:[{address:'8.8.8.8',status:'smtp-greeting',tls:'verified'}]},ipv6:{status:'not-found',addresses:[]},checked_at:'2026-10-29T00:00:00Z'})});});
  await page.locator('#provider').selectOption('resend');await page.locator('#outbound-probe-check').click();
  await expect(page.locator('#outbound-probe-result')).toContainText('TLS 证书验证通过');await expect(page.locator('#outbound-probe-note')).toContainText('不能证明认证');
  expect(submitted).toEqual({host:'smtp.resend.com',port:587,tls:'starttls'});await expect(page.locator('#enable')).toBeDisabled();
  await page.locator('#port').fill('465');await expect(page.locator('#outbound-probe-result')).toBeEmpty();
 }finally{await context.close();}
});
test('outbound SMTP check drops a result after the endpoint changes',async()=>{
 const {context,page}=await fixture('en',true);let release,finished=0;try{
  await context.route(origin+'/api/setup/outbound-smtp-probe',async route=>{const submitted=route.request().postDataJSON();await new Promise(resolve=>{release=resolve;});await route.fulfill({contentType:'application/json',body:JSON.stringify({...submitted,ipv4:{status:'observed',addresses:[{address:'8.8.8.8',status:'smtp-greeting',tls:'verified'}]},ipv6:{status:'not-found',addresses:[]},checked_at:'2026-10-29T00:00:00Z'})});finished++;});
  await page.locator('#provider').selectOption('resend');await page.locator('#outbound-probe-check').click();await expect.poll(()=>typeof release).toBe('function');await page.locator('#host').fill('changed.example.test');release();await expect.poll(()=>finished).toBe(1);
  await expect(page.locator('#outbound-probe-result')).toBeEmpty();await expect(page.locator('#enable')).toBeDisabled();
 }finally{await context.close();}
});
