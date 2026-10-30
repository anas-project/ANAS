// TEST_CASES: MAILMOD-T-001 MAILMOD-T-002 MAILMOD-T-003 MAILMOD-T-004 MAILMOD-T-005
import {chromium} from '@playwright/test';
import fs from 'node:fs/promises';
import path from 'node:path';
import https from 'node:https';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
// Run against the formally applied isolated stack, never a production directory.
const file=process.env.MAIL_FORMAL_BROWSER_INPUT,root=process.env.MAIL_FORMAL_BROWSER_OUTPUT;
assert.ok(file&&root,'explicit private fixture and output paths required');
assert.ok(path.isAbsolute(file)&&path.isAbsolute(root));
assert.equal((await fs.lstat(path.dirname(file))).mode&0o777,0o700);
await fs.mkdir(root,{recursive:true,mode:0o700});
assert.equal((await fs.lstat(root)).mode&0o777,0o700);
const stat=await fs.lstat(file);assert.ok(stat.isFile()&&!stat.isSymbolicLink());assert.ok(stat.size<=1024*1024);assert.equal(stat.mode&0o777,0o600);
const input=JSON.parse(await fs.readFile(file,'utf8'));
assert.ok(['casdoor','llng'].includes(input.provider));
assert.ok(typeof input.password==='string'&&input.password.length>=8);
assert.ok(typeof input.username==='string'&&input.username.length>0);
for(const key of ['mail_origin','portal_origin','webmail_origin','iam_issuer']){
 const u=new URL(input[key]);assert.equal(u.protocol,'https:');assert.equal(u.port,'10000');assert.ok(u.hostname.endsWith('.mail-lab.test'));
}
assert.ok(process.argv.length<=3&&process.argv.slice(2).every(value=>['--central-logout','--company-directory','--revocation-fault','--setup'].includes(value)));
const scenario=process.argv[2]?.slice(2)||(process.env.MAIL_FORMAL_BROWSER_SCENARIO||'sso');
assert.ok(['sso','central-logout','company-directory','revocation-fault','setup'].includes(scenario));
const result={success:false,provider:input.provider,test_case:{sso:'MAILMOD-T-001','central-logout':'MAILMOD-T-002','company-directory':'MAILMOD-T-003','revocation-fault':'MAILMOD-T-004',setup:'MAILMOD-T-005'}[scenario],checks:[],stage:'TLS',password_posts:0};
const record=name=>{result.checks.push(name);console.log(JSON.stringify({check:name}));};
const redact=value=>String(value).replaceAll(input.password,'[redacted]').replaceAll(encodeURIComponent(input.password),'[redacted]').replace(/https?:\/\/[^\s"')]+/g,'[url]');
function verifiedGet(url){return new Promise((resolve,reject)=>{
 const request=https.get(url,{ca:input.ca_pem,lookup:(name,options,callback)=>options.all?callback(null,[{address:'127.0.0.1',family:4}]):callback(null,'127.0.0.1',4)},response=>{response.resume();resolve(response.statusCode);});
 request.on('error',reject);request.setTimeout(10000,()=>request.destroy(new Error('TLS probe timeout')));
});}
let browser,page;
try{
 assert.equal(await verifiedGet(input.mail_origin+'/healthz'),200);record('actual_certificate_chain_and_hostname_verified');
 const pin=crypto.createHash('sha256').update(new crypto.X509Certificate(input.leaf_pem).publicKey.export({type:'spki',format:'der'})).digest('base64');
 browser=await chromium.launch({...(process.env.MAIL_FORMAL_CHROME_EXECUTABLE?{executablePath:process.env.MAIL_FORMAL_CHROME_EXECUTABLE}:{}),headless:true,args:['--no-proxy-server','--host-resolver-rules=MAP *.mail-lab.test 127.0.0.1','--ignore-certificate-errors-spki-list='+pin]});
 const context=await browser.newContext({serviceWorkers:'block'});page=await context.newPage();
 page.setDefaultTimeout(30000);page.setDefaultNavigationTimeout(45000);
 const iamHost=new URL(input.iam_issuer).hostname;let leak=false,codeInWebmailURL=false,badHandoffOrigin=false;let handoffPosts=0;
 context.on('request',request=>{
  const u=new URL(request.url()),body=request.postData()||'';if(request.method()==='POST'&&((u.origin===input.webmail_origin&&u.searchParams.get('_action')?.startsWith('plugin.anas_'))||(u.origin===input.mail_origin&&u.pathname==='/webmail/continue'))){const h=request.headers(),expected=u.origin===input.mail_origin?input.webmail_origin:input.mail_origin;handoffPosts++;if(h.origin!==expected||h.referer!==expected+'/')badHandoffOrigin=true;}
  if(body.includes(input.password)||[...new URLSearchParams(body).values()].includes(input.password)){result.password_posts++;if(u.hostname!==iamHost)leak=true;}
  if(u.origin===input.webmail_origin&&['code','access_token','refresh_token','signature','session_id','mailbox_id','browser_state','start_nonce','nonce'].some(k=>u.searchParams.has(k)))codeInWebmailURL=true;
 });
 result.stage='portal IAM login';
 await page.goto(input.portal_origin+'/login',{waitUntil:'domcontentloaded'});
 const selectors=input.provider==='llng'?['input[name="user"]']:['.login-username-input input','input.login-username-input','form#normal_login input#input','input[name="username"]','input[type="text"]'];
 let user;
 for(let attempt=0;attempt<100&&!user;attempt++){
  for(const selector of selectors){const candidate=page.locator(selector).first();if(await candidate.isVisible().catch(()=>false)){user=candidate;break;}}
  if(!user)await page.waitForTimeout(300);
 }
 assert.ok(user,'Actual IAM login form did not appear');assert.equal(new URL(page.url()).hostname,iamHost);
 await user.fill(input.username);await page.locator('input[type="password"]').first().fill(input.password);
 const loginForm=user.locator('xpath=ancestor::form[1]');
 const submit=loginForm.locator('button[type="submit"],input[type="submit"]').first();await submit.click();
 await page.waitForURL(url=>url.origin===input.portal_origin,{timeout:60000,waitUntil:'domcontentloaded'});
 await page.waitForFunction(async()=>{const r=await fetch('/api/me',{cache:'no-store'});return r.status===200;},undefined,{timeout:30000});
 record('actual_portal_and_directory_IAM_login');const before=result.password_posts;
 result.stage='Go mail SSO';
 await page.waitForFunction(()=>document.body.dataset.page==='home',undefined,{timeout:30000});
 const entry=page.locator('a[href="'+input.mail_origin+'"],a[href="'+input.mail_origin+'/"]').first();
 await entry.waitFor({state:'visible'});
 if(await entry.getAttribute('target')==='_blank') {
  const opened=context.waitForEvent('page');await entry.click();page=await opened;
  page.setDefaultTimeout(30000);page.setDefaultNavigationTimeout(45000);
 } else await entry.click();
 record('actual_portal_application_entry_opens_mail');

 await page.locator('.mailbox').first().waitFor({state:'visible',timeout:60000});
 const view=await page.evaluate(async()=>{const r=await fetch('/api/mailboxes',{cache:'no-store'});return {status:r.status,data:await r.json()};});
 assert.equal(view.status,200);assert.ok(view.data.mailboxes.length>=1);assert.ok(view.data.mailboxes.some(b=>b.status==='ready'));
 assert.equal(result.password_posts,before,'Go required another personnel-password submission');record('existing_IAM_session_opens_Go_without_password');
 if(scenario==='setup') {
  result.stage='actual administrator Tutorial draft';assert.equal(view.data.can_configure,true);
  const initial=await page.evaluate(async()=>{const r=await fetch('/api/setup',{cache:'no-store'});return {status:r.status,data:await r.json()};});assert.equal(initial.status,200);assert.equal(initial.data.enabled,false);
  await page.locator('#setup').click();await page.locator('#setup-form').waitFor({state:'visible'});await page.locator('#language').selectOption('zh-CN');
  await page.locator('#inbound').selectOption('worker-r2');await page.locator('#domain').fill(view.data.mailboxes[0].address.split('@')[1]);await page.locator('#source_url').fill('https://source.mail-lab.test');await page.locator('#ingress_secret_ref').fill('secrets.MAIL_INGRESS');
  for(const [id,value]of Object.entries({additional_mailboxes:2,aliases:10,mailbox_bytes:2147483648,messages_per_minute:30,cloud_bytes:2147483648,delivered_days:7}))await page.locator('#'+id).fill(String(value));await page.locator('#limits_confirmed').check();
  await page.locator('#save').click();await page.waitForFunction(()=>document.getElementById('status').textContent.includes('草稿已保存'));
  const saved=await page.evaluate(async()=>{const r=await fetch('/api/setup',{cache:'no-store'});return {status:r.status,data:await r.json()};});assert.equal(saved.status,200);assert.equal(saved.data.state.revision,initial.data.state.revision+1);assert.equal(saved.data.enabled,false);assert.equal(saved.data.checks.local_delivery,'unknown');assert.equal(saved.data.checks.account_quota,'unknown');
  assert.equal(await page.locator('#enable').isEnabled(),false);record('actual_PG_draft_saved_without_claiming_cloud_enablement');
  await page.reload({waitUntil:'domcontentloaded'});await page.locator('#setup-form').waitFor({state:'visible'});assert.equal(await page.locator('#ingress_secret_ref').inputValue(),'secrets.MAIL_INGRESS');assert.equal(await page.locator('#aliases').inputValue(),'10');
  const checks=await page.evaluate(async({state})=>{const send=async body=>{const r=await fetch('/api/setup',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});return {status:r.status,text:await r.text()};};return {stale:await send({revision:state.revision-1,draft:state.draft}),secret:await send({revision:state.revision,draft:{...state.draft,password:'not-a-real-secret'}}),storage:JSON.stringify({...localStorage,...sessionStorage})};},{state:saved.data.state});
  assert.equal(checks.stale.status,409);assert.equal(checks.secret.status,400);assert.ok(!checks.secret.text.includes('not-a-real-secret'));assert.equal(checks.storage,'{}');record('reentered_draft_stale_version_and_secret_fields_rejected');
  await page.screenshot({path:path.join(root,'setup-draft.png'),fullPage:true});
  await page.goto(input.mail_origin+'/',{waitUntil:'domcontentloaded'});await page.locator('.mailbox').first().waitFor({state:'visible'});
 }
 const ready=page.locator('.mailbox').filter({has:page.locator('button:not(:disabled)')}).first();await ready.getByRole('button').click();
 result.stage='Roundcube handoff';await page.locator('#messagelist, #messagelist-content').first().waitFor({state:'visible',timeout:45000});
 assert.equal(new URL(page.url()).origin,input.webmail_origin);assert.equal(result.password_posts,before);assert.ok(!leak);assert.ok(!codeInWebmailURL);assert.equal(handoffPosts,3);assert.ok(!badHandoffOrigin,'cross-origin handoff lost its trusted Origin or disclosed a referrer path');assert.equal(result.password_posts,1);
 record('actual_Roundcube_handoff_without_personnel_password');record('handoff_codes_not_in_webmail_URL');record('three_navigation_POSTs_preserve_trusted_origin_without_referrer_path');
 await page.screenshot({path:path.join(root,'sso-mailbox.png'),fullPage:true});
 if(scenario==='company-directory') {
  result.stage='Roundcube company directory';
  await page.goto(input.webmail_origin+'/?_task=addressbook',{waitUntil:'domcontentloaded'});
  await page.waitForFunction(()=>!!window.rcmail?.env?.address_sources&&!window.rcmail.busy);
  const sources=await page.evaluate(()=>Object.fromEntries(Object.entries(window.rcmail.env.address_sources).map(([id,s])=>[id,{readonly:s.readonly,autocomplete:s.autocomplete}])));
  assert.ok(sources.anas_company?.readonly);assert.ok(sources.anas_company.autocomplete);
  assert.ok(Object.entries(sources).some(([id,s])=>id!=='anas_company'&&!s.readonly));
  record('company_source_is_readonly_and_private_source_remains_separate');
  const companyReply=page.waitForResponse(response=>{const u=new URL(response.url());return u.origin===input.webmail_origin&&u.searchParams.get('_action')==='list'&&u.searchParams.get('_source')==='anas_company';});
  const [companyResponse]=await Promise.all([companyReply,page.locator('#directorylist a[rel="anas_company"]').click()]);assert.equal(companyResponse.status(),200);
  await page.waitForFunction(()=>window.rcmail?.env?.source==='anas_company'&&!window.rcmail.busy);
  await page.waitForFunction(()=>document.querySelectorAll('#contacts-table tbody tr').length>0);
  result.company_contacts=await page.locator('#contacts-table tbody tr').count();
  record('actual_AD_company_contacts_visible_in_Roundcube');
  const mailGroup=page.locator('#directorylist a[rel^="anas_company:"]').filter({hasText:'APP_mail'}).first();
  const groupID=(await mailGroup.getAttribute('rel')).slice('anas_company:'.length);
  assert.match(groupID,/^[a-f0-9-]{36}$/);
  const groupReply=page.waitForResponse(response=>{
   const request=response.request(),u=new URL(response.url());
   return u.origin===input.webmail_origin&&['xhr','fetch'].includes(request.resourceType())&&[...u.searchParams.values(),...new URLSearchParams(request.postData()||'').values()].includes(groupID);
  });
  const [groupResponse]=await Promise.all([groupReply,mailGroup.click()]);assert.equal(groupResponse.status(),200);
  await page.waitForFunction(()=>window.rcmail&&!window.rcmail.busy);
  await page.waitForFunction(()=>document.querySelectorAll('#contacts-table tbody tr').length>0);
  record('actual_AD_mail_group_contacts_visible_in_Roundcube');
  await page.screenshot({path:path.join(root,'company-directory.png'),fullPage:true});
 }
 if(scenario==='central-logout') {
  result.stage='Roundcube and IAM logout';
  const priorCookies=await context.cookies([input.mail_origin,input.webmail_origin]);
  await page.waitForFunction(()=>window.rcmail?.env?.request_token&&!window.rcmail.busy,undefined,{timeout:30000});
  const callback=page.waitForResponse(response=>{const u=new URL(response.url());return u.origin===input.mail_origin&&u.pathname==='/oidc/logout-callback';},{timeout:60000});
  const complete=async()=>{
   await page.locator('a.logout').click();
   await page.waitForURL(u=>u.origin===input.mail_origin&&u.pathname==='/oidc/logout-callback'||u.origin===new URL(input.iam_issuer).origin,{timeout:45000,waitUntil:'domcontentloaded'});
   if(new URL(page.url()).origin===new URL(input.iam_issuer).origin) {
    // A native IdP confirmation is an intentional part of RP logout.
    assert.equal(input.provider,'llng');assert.equal(new URL(page.url()).pathname,'/oauth2/logout');
    await page.locator('form.confirm button.positive[type="submit"]').click();
    record('native_IAM_logout_confirmation_accepted');
   }
  };
  const [callbackResponse]=await Promise.all([callback,complete()]);
  assert.equal(callbackResponse.status(),200);
  await page.waitForURL(u=>u.origin===input.mail_origin&&u.pathname==='/oidc/logout-callback',{timeout:45000,waitUntil:'domcontentloaded'});
  assert.equal(await page.locator('main h1').count(),1);
  assert.equal(await page.locator('main a[href="/login"]').count(),1);
  record('actual_Roundcube_logout_reaches_bound_IAM_return');
  const replay=await browser.newContext({serviceWorkers:'block'});
  try {
   await replay.addCookies(priorCookies);const old=await replay.newPage();
   result.stage='old Go Cookie replay';
   assert.equal((await old.goto(input.mail_origin+'/api/me',{waitUntil:'domcontentloaded'})).status(),401,'old Go session remained usable');
   result.stage='old Roundcube Cookie replay';
   const oldResponse=await old.goto(input.webmail_origin+'/?_task=mail',{waitUntil:'domcontentloaded'});
   result.replayed_webmail={status:oldResponse.status(),...await old.evaluate(()=>({task:window.rcmail?.env?.task||null,authenticated_user:!!window.rcmail?.env?.user_id,message_lists:document.querySelectorAll('#messagelist, #messagelist-content').length,login_forms:document.querySelectorAll('#login-form').length}))};
   if(oldResponse.status()!==401) {
    // Core deletes the RC session during logout. A later anonymous request
    // follows the plugin's fixed Go redirect to a fresh IAM sign-in page.
    assert.equal(oldResponse.status(),200);
    assert.equal(new URL(old.url()).origin,new URL(input.iam_issuer).origin,'old Roundcube session reached a protected application');
    await old.locator('input[type="password"]').first().waitFor({state:'visible'});
    assert.equal(result.replayed_webmail.authenticated_user,false);
    assert.equal(result.replayed_webmail.message_lists,0);
    result.replayed_webmail.fresh_IAM_form=true;
   }
  } finally { await replay.close(); }
  record('old_Go_and_Roundcube_sessions_stay_denied_after_central_logout');
  await page.goto(input.mail_origin+'/login',{waitUntil:'domcontentloaded'});
  await page.locator('input[type="password"]').first().waitFor({state:'visible',timeout:45000});
  assert.equal(new URL(page.url()).hostname,iamHost);
  assert.equal(result.password_posts,1);
  record('new_mail_login_requires_fresh_IAM_authentication_after_central_logout');
  await page.screenshot({path:path.join(root,'fresh-IAM-required.png'),mask:[page.locator('input')]});
 }
 if(scenario==='revocation-fault') {
 result.stage='persistent withdrawal fault coordination';
 const priorCookies=await context.cookies([input.mail_origin,input.webmail_origin]);
 const cookie=priorCookies.find(c=>c.name==='__Host-anas-mail-session');assert.ok(cookie);
 await fs.writeFile(path.join(root,'r034-browser.json'),JSON.stringify({token_hash:crypto.createHash('sha256').update(cookie.value).digest('hex')}),{mode:0o600,flag:'wx'});
 record('actual_browser_session_ready_for_isolated_fault');
 async function waitFile(name,seconds){for(let attempt=0;attempt<seconds;attempt++){try{return await fs.readFile(path.join(root,name),'utf8');}catch(error){if(error.code!=='ENOENT')throw error;}await page.waitForTimeout(1000);}throw new Error('fault coordinator timeout: '+name);}
 await waitFile('dms-paused',180);
 await page.goto(input.mail_origin+'/healthz',{waitUntil:'domcontentloaded'});
 const response=await page.evaluate(async()=>{const r=await fetch('/api/session/revoke',{method:'POST'});return {status:r.status,data:await r.json()};});
 assert.equal(response.status,200);assert.equal(response.data.status,'local-session-revoked');
 await fs.writeFile(path.join(root,'browser-revoked'),JSON.stringify({status:200}),{mode:0o600,flag:'wx'});
 record('actual_browser_revocation_committed_while_DMS_paused');
 const fault=JSON.parse(await waitFile('fault-result.json',600));assert.equal(fault.failure,null);assert.deepEqual(fault.cleanup_errors,[]);assert.ok(fault.business_unchanged);assert.ok(fault.target_unavailable_task_retained);assert.ok(fault.Go_restart_with_AD_stopped);assert.ok(fault.durable_withdrawal_completed_while_AD_stopped);
 record('server_confirms_pending_task_survived_Go_restart_and_completed_without_AD');
 for(let attempt=0;attempt<60;attempt++){if(await verifiedGet(input.mail_origin+'/healthz')===200)break;if(attempt===59)throw new Error('public Go route did not recover');await page.waitForTimeout(1000);}
 const replay=await browser.newContext({serviceWorkers:'block'});
 try{await replay.addCookies(priorCookies);const old=await replay.newPage();assert.equal((await old.goto(input.mail_origin+'/api/me',{waitUntil:'domcontentloaded'})).status(),401);assert.equal((await old.goto(input.webmail_origin+'/?_task=mail',{waitUntil:'domcontentloaded'})).status(),401);}finally{await replay.close();}
 record('old_Go_and_RC_cookies_remain_denied_after_fault_recovery');
 }
 result.mailboxes=view.data.mailboxes.length;result.success=true;result.stage='passed';
} catch(error) {
 result.failure=redact(error.message).slice(0,1800);
 if(page) {
  // Failure evidence must not replace the original error or print raw URLs.
  try {
   result.current_path=new URL(page.url()).pathname;
   result.current_surface=Object.keys(input).find(key=>['mail_origin','portal_origin','webmail_origin','iam_issuer'].includes(key)&&new URL(input[key]).origin===new URL(page.url()).origin)||'other';
   const body=await page.locator('body').innerText({timeout:3000});
   await fs.writeFile(path.join(root,'failure.txt'),redact(body).slice(0,12000),{mode:0o600});
   await page.screenshot({path:path.join(root,'failure.png'),mask:[page.locator('input')],timeout:5000});
  } catch { result.failure_evidence_incomplete=true; }
 }
}
finally{
 await browser?.close();await fs.writeFile(path.join(root,'result.json'),JSON.stringify(result,null,2),{mode:0o600});console.log(JSON.stringify(result));
}
if(!result.success)process.exitCode=1;
