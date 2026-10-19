// TEST_CASES: PORTAL-T-010
// Real pages and native OIDC callbacks; no mocked authentication or cookie injection.
import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
import net from 'node:net';
import {createHash} from 'node:crypto';
import {chromium,expect} from '@playwright/test';

const root=process.env.PORTAL_LOGOUT_ROOT;
if(!root||!/^\/(?:tmp|home\/whl)\/anas-portal-admin-[A-Za-z0-9-]+$/.test(root))throw Error('Owned private account fixture required');
const f=JSON.parse(fs.readFileSync(path.join(root,'private/browser.private.json')));
if(!/^pua[0-9a-f]{8}$/.test(f.prefix)||!f.users.manager.username.startsWith(f.prefix))throw Error('Owned fixture identities required');
const portal=new URL(f.origin);
if(portal.protocol!=='https:'||portal.hostname!=='portal.finance.hlong.wang')throw Error('Confirmed finance fixture required');
const origins={portal:portal.origin,iam:portal.origin.replace('://portal.', '://auth.'),nextcloud:portal.origin.replace('://portal.', '://nc.'),immich:portal.origin.replace('://portal.', '://photos.')};
const entryMode=f.entry_mode||'forward';
if(!['forward','direct'].includes(entryMode))throw Error('Unknown browser entry mode');
const http1Only=process.env.PORTAL_LOGOUT_HTTP1_ONLY==='1';
if(process.argv.slice(2).some(arg=>arg!=='--native-menu'))throw Error('Unknown logout test argument');
const nativeMenu=process.argv.includes('--native-menu')||process.env.PORTAL_LOGOUT_NATIVE_MENU==='1';
const runId=new Date().toISOString().replace(/[:.]/g,'-')+'-'+process.pid;
const evidence=path.join(root,'evidence',runId);fs.mkdirSync(evidence,{mode:0o700});
const output=path.join(evidence,'cross-application-logout.json');
const selected=(process.env.PORTAL_LOGOUT_SCENARIOS||(nativeMenu?'nextcloud,immich':'categories,portal,nextcloud,immich,portal-after-central-cookie-loss,portal-partial,nextcloud-after-central-cookie-loss,immich-after-central-cookie-loss,nextcloud-without-portal,immich-without-portal')).split(',');
const allowed=['categories','portal','nextcloud','immich','portal-after-central-cookie-loss','portal-partial','nextcloud-after-central-cookie-loss','immich-after-central-cookie-loss','nextcloud-without-portal','immich-without-portal'];
if(!selected.length||selected.some(s=>!allowed.includes(s)))throw Error('Unknown logout scenario');
const report={baseline:f.baseline,driver_sha256:createHash('sha256').update(fs.readFileSync(new URL(import.meta.url))).digest('hex'),entry_mode:entryMode,http1_only:http1Only,native_menu:nativeMenu,started_at:new Date().toISOString(),scenarios:[],selected,scope:'Real browser login; current-browser cross-app logout, refresh, old bearer, and other-browser isolation'};
const save=()=>fs.writeFileSync(output,JSON.stringify(report,null,2),{mode:0o600});
const sockets=new Set();
// Both modes restrict CONNECT to the four confirmed origins. Direct mode uses
// normal domain routing; forward mode uses the fixture's owned SSH entry.
const proxy=http.createServer((_,r)=>r.writeHead(405).end());
proxy.on('connect',(request,client,head)=>{
 if(!Object.values(origins).some(v=>new URL(v).host===request.url)){client.destroy();return;}
 const target=new URL('https://'+request.url);
 const upstream=net.connect(entryMode==='direct'?Number(target.port||443):Number(f.entry_port||portal.port||443),entryMode==='direct'?target.hostname:f.entry_ip||'127.0.0.1',()=>{
  client.write('HTTP/1.1 200 Connection Established\r\n\r\n');if(head.length)upstream.write(head);client.pipe(upstream);upstream.pipe(client);
 });
 for(const socket of [client,upstream]){sockets.add(socket);socket.on('close',()=>sockets.delete(socket));socket.on('error',error=>{(report.transport_errors||=[]).push({at:new Date().toISOString(),code:error.code||'socket_error',host:request.url});save();client.destroy();upstream.destroy();});}
});
await new Promise(resolve=>proxy.listen(0,'127.0.0.1',resolve));
let browser,independentContext;
const probes=new WeakMap();
function stage(name){const row=report.scenarios.at(-1);if(row){row.stage=name;save();}}
const safeText=value=>String(value).replace(/https?:\S+/g,'[URL]').replaceAll(f.passwords.manager,'[secret]').replace(/\beyJ[A-Za-z0-9_.-]+/g,'[token]');
async function probe(context,app){
 let pages=probes.get(context);if(!pages){pages={};probes.set(context,pages);}
 if(!pages[app]){
  const endpoint={portal:'/api/bootstrap',iam:'/api/get-account',nextcloud:'/status.php',immich:'/api/server/config'}[app];
  pages[app]=await context.newPage();await pages[app].goto(origins[app]+endpoint,{waitUntil:'domcontentloaded',timeout:120000});
 }
 return pages[app];
}
async function browserGet(context,app,endpoint,bearer){
 const page=await probe(context,app);
 return page.evaluate(async input=>{
  const response=await fetch(input.endpoint,{cache:'no-store',signal:AbortSignal.timeout(15000),credentials:input.bearer?'omit':'include',headers:input.bearer?{Authorization:'Bearer '+input.bearer}:input.app==='nextcloud'?{'OCS-APIRequest':'true'}:input.app==='iam'?{'Accept-Language':'en'}:{}});
  return {status:response.status,body:await response.json().catch(()=>({}))};
 },{app,endpoint,bearer});
}
async function state(context){
 const result={};
 for(const [app,endpoint]of Object.entries({portal:'/api/me',iam:'/api/get-account',nextcloud:'/ocs/v2.php/cloud/user?format=json',immich:'/api/users/me'})){
  const started=Date.now();
  try{
  const r=await browserGet(context,app,endpoint),j=r.body;
  const identity=app==='portal'?j.person?.anchor:app==='iam'?j.data?.name:app==='nextcloud'?j.ocs?.data?.id:j.email;
  const expected=app==='iam'?f.users.manager.username:app==='immich'?f.users.manager.username+'@'+portal.hostname.slice(7):f.users.manager.anchor;
  const authenticated=app==='iam'?j.status==='ok'&&!!identity:r.status===200&&!!identity;
  // A 5xx or invalid response is unavailable, never proof of logout.
  const signedOut=app==='iam'?r.status===200&&j.status==='error'&&j.msg==='Please login first':r.status===401;
  result[app]={status:r.status,duration_ms:Date.now()-started,authenticated,signed_out:signedOut,...(app==='iam'?{native_status:j.status}:{}),...(authenticated?{identity_matches:identity===expected}:{})};
  }catch{
   result[app]={status:null,duration_ms:Date.now()-started,authenticated:false,signed_out:false,unavailable:true};
  }
 }
 return result;
}
async function readyState(context,predicate,label){
 const end=Date.now()+120000;let result;
 do{
  result=await state(context);
  const row=report.scenarios.at(-1);(row.preparation_probes||=[]).push({at:new Date().toISOString(),label,state:result});save();
  if(predicate(result))return result;
  if(Date.now()<end)await new Promise(resolve=>setTimeout(resolve,1500));
 }while(Date.now()<end);
 return result;
}
async function completeAuthorization(page,allowPassword=true){
 const end=Date.now()+120000;let submitted=false,selectedAccount=false;
 while(Date.now()<end){
  const host=new URL(page.url()).hostname;
  if(host!==new URL(origins.iam).hostname){await page.waitForTimeout(200);if(new URL(page.url()).hostname!==new URL(origins.iam).hostname)return;}
  const account=page.getByText(new RegExp('^.*\\('+f.users.manager.username+'\\)$'));
  if(!selectedAccount&&await account.isVisible().catch(()=>false)){await account.click({noWaitAfter:true});selectedAccount=true;continue;}
  const user=page.locator('.login-username-input input,input.login-username-input,form#normal_login input#input').first();
  if(allowPassword&&!submitted&&await user.isVisible().catch(()=>false)){
   await user.fill(f.users.manager.username);await page.locator('input[type=password]').fill(f.passwords.manager);
   await page.locator('button.login-button,form#normal_login button[type=submit]').first().click({noWaitAfter:true});submitted=true;
  }
  if(submitted&&new URL(page.url()).hostname!==new URL(origins.iam).hostname)return;
  await page.waitForTimeout(300);
 }
 throw Error('Native authorization did not finish');
}
async function pageSession(context,app,allowPassword=false){
 stage('native browser login: '+app);
 const page=await context.newPage();page.setDefaultTimeout(60000);page.setDefaultNavigationTimeout(120000);
 const pending=new Map();
 page.on('request',request=>{const u=new URL(request.url());pending.set(request,{host:u.hostname,path:u.pathname,type:request.resourceType(),started:Date.now()});});
 page.on('requestfinished',request=>pending.delete(request));
 page.on('requestfailed',request=>{const row=report.scenarios.at(-1),requestData=pending.get(request);pending.delete(request);if(row){(row.request_failures||=[]).push({...requestData,error:safeText(request.failure()?.errorText)});save();}});
 page.on('pageerror',error=>{const row=report.scenarios.at(-1);if(row){(row.page_errors||=[]).push(safeText(error.message).slice(0,500));save();}});
 page.logoutPendingRequests=pending;
 page.on('response',response=>{
  const url=new URL(response.url()),row=report.scenarios.at(-1);
  if(row&&response.status()>=400&&url.hostname===new URL(origins.immich).hostname&&url.pathname.startsWith('/api/oauth/')){(row.oauth_errors||=[]).push({app,path:url.pathname,status:response.status()});save();}
  if(response.request().resourceType()!=='document')return;
  if(row){(row.documents||=[]).push({app,host:url.hostname,path:url.pathname,status:response.status()});save();}
 });
 if(app==='portal'){
  await page.goto(origins.portal+'/login',{waitUntil:'commit'});await page.waitForURL(u=>u.hostname===new URL(origins.iam).hostname,{waitUntil:'commit'});await completeAuthorization(page);
  await page.waitForLoadState('domcontentloaded');
  const rejected=report.scenarios.at(-1).documents?.find(d=>d.app==='portal'&&d.path==='/oidc/callback'&&d.status>=400);
  if(rejected)throw Error('Portal native callback rejected: HTTP '+rejected.status);
  await expect(page.getByRole('heading',{name:'Your workspace',exact:true})).toBeVisible({timeout:120000});
 }else if(app==='nextcloud'){
  await page.goto(origins.nextcloud+'/apps/user_oidc/login/1',{waitUntil:'commit'});
  // The provider already has this user's session. Only select that account;
  // submitting a password in parallel can consume the same callback state twice.
  if(new URL(page.url()).hostname===new URL(origins.iam).hostname)await completeAuthorization(page,allowPassword);
  await page.waitForURL(u=>u.hostname===new URL(origins.nextcloud).hostname,{waitUntil:'commit',timeout:120000});
  const rejected=report.scenarios.at(-1).documents?.find(d=>d.app==='nextcloud'&&d.path==='/apps/user_oidc/code'&&d.status>=400);
  if(rejected){
   const text=await page.locator('body').innerText();report.scenarios.at(-1).nextcloud_callback_error=text.replace(/https?:\S+/g,'[URL]').replace(/\b[A-Za-z0-9_-]{24,}\b/g,'[value]').slice(0,1600);save();
   throw Error('Nextcloud native callback rejected: HTTP '+rejected.status);
  }
  await expect.poll(async()=>{try{const r=await browserGet(context,'nextcloud','/ocs/v2.php/cloud/user?format=json');report.scenarios.at(-1).last_nextcloud_probe={status:r.status,native_status:r.body.ocs?.meta?.statuscode,has_id:!!r.body.ocs?.data?.id,id_matches:r.body.ocs?.data?.id===f.users.manager.anchor};save();return r.status===200&&r.body.ocs?.data?.id===f.users.manager.anchor;}catch{return false;}},{timeout:120000}).toBe(true);
 }else{
  stage('Immich login page hydration');
  await page.goto(origins.immich+'/auth/login?autoLaunch=0',{waitUntil:'domcontentloaded'});
  await page.getByRole('button',{name:'ANAS',exact:true}).waitFor({timeout:120000});
  stage('Immich native authorization button');
  await page.getByRole('button',{name:'ANAS',exact:true}).click({noWaitAfter:true});
  stage('Immich native authorization callback');
  let authorizationHandled=false;
  await expect.poll(async()=>{
   if(new URL(page.url()).hostname===new URL(origins.iam).hostname&&!authorizationHandled){authorizationHandled=true;await completeAuthorization(page,allowPassword);}
   const current=new URL(page.url());
   return current.hostname===new URL(origins.immich).hostname&&['/photos','/auth/onboarding'].includes(current.pathname);
  },{timeout:120000}).toBe(true);
  stage('Immich post-login notice');
  const notice=page.getByRole('dialog').filter({hasText:'NEW VERSION AVAILABLE'});
  if(await notice.waitFor({state:'visible',timeout:5000}).then(()=>true,()=>false))await notice.getByRole('button',{name:'Acknowledge',exact:true}).click();
  for(let i=0;i<12&&new URL(page.url()).pathname==='/auth/onboarding';i++){
   stage('Immich onboarding: '+(new URL(page.url()).searchParams.get('step')||'hello'));
   const previous=page.url(),next=page.locator('#onboarding-page').getByRole('button').last(),label=await next.innerText();
   await next.click();await page.waitForURL(u=>u.href!==previous,{waitUntil:'commit'});
   if(new URL(page.url()).pathname==='/auth/onboarding')await expect(next).not.toHaveText(label);
  }
  await expect(page.getByRole('button',{name:new RegExp(f.users.manager.username+'@')})).toBeVisible({timeout:120000});
 }
 return page;
}
async function sessions(context){const pages={};for(const app of ['portal','nextcloud','immich'])pages[app]=await pageSession(context,app);return pages;}
try{
 browser=await chromium.launch({headless:true,...(http1Only?{args:['--disable-http2']}:{}),...(f.executable?{executablePath:f.executable}:{})});
 report.browser_version=browser.version();
 for(const source of selected){
  const row={source,checks:[],started_at:new Date().toISOString()};report.scenarios.push(row);save();let context,other;
  const check=(name,pass,detail={},fatal=true)=>{row.checks.push({name,pass:!!pass,...detail});save();if(!pass&&fatal)throw Error(name);};
  try{
   const options={ignoreHTTPSErrors:true,locale:'en',proxy:{server:'http://127.0.0.1:'+proxy.address().port}};
   context=await browser.newContext(options);
   if(source==='categories'){
    const page=await pageSession(context,'portal');
    for(const language of ['en','zh-CN']){
     await page.locator('#language').selectOption(language);
     const toggle=page.getByLabel(language==='en'?'Show categories':'显示分类',{exact:true});
     await toggle.waitFor();await page.locator('.card').first().waitFor();
     check(language+': administrator sees both main application groups',await page.locator('[data-kind=user]').count()===1&&await page.locator('[data-kind=admin]').count()===1);
     const order=await page.locator('.card h3').allTextContents();
     await toggle.uncheck();
     check(language+': category switch flattens both groups without changing cards',await page.locator('.category-heading').count()===0&&JSON.stringify(await page.locator('.card h3').allTextContents())===JSON.stringify(order));
     await page.reload({waitUntil:'domcontentloaded'});await toggle.waitFor();await page.locator('.card').first().waitFor();
     check(language+': category preference survives a real page reload',!await toggle.isChecked()&&await page.locator('.category-heading').count()===0);
     await toggle.check();check(language+': category headings can be restored',await page.locator('[data-kind=admin] .category-heading').count()>0);
     await page.screenshot({path:path.join(evidence,'categories-'+language+'.png')});
    }
    row.status='passed';continue;
   }

   const withoutPortal=source.endsWith('-without-portal');
   let pages;
   if(withoutPortal){pages={portal:await context.newPage(),nextcloud:await pageSession(context,'nextcloud',true),immich:await pageSession(context,'immich')};}
   else pages=await sessions(context);
   const expectedSessions=values=>Object.entries(values).every(([app,s])=>withoutPortal&&app==='portal'?s.signed_out:s.authenticated&&s.identity_matches);
   stage('verify original browser sessions');row.before=await readyState(context,expectedSessions,'original browser');check(withoutPortal?'native apps match the test user without a portal login':'all four native sessions match the test user',expectedSessions(row.before));
   const oldBearer=(await context.cookies(origins.immich)).find(c=>c.name==='immich_access_token')?.value;check('real Immich web callback supplied its session cookie',!!oldBearer);
   const bearerBefore=await browserGet(context,'immich','/api/users/me',oldBearer);
   check('original Immich bearer is valid before logout',bearerBefore.status===200&&bearerBefore.body.email===f.users.manager.username+'@'+portal.hostname.slice(7),{status:bearerBefore.status});
   // Keep this independent browser alive across sources. Every source rechecks
   // its identity before and after logout; never reauthenticate it to hide a
   // session that an earlier source incorrectly revoked.
   if(!independentContext){
    const candidate=await browser.newContext(options);
    try{await sessions(candidate);independentContext=candidate;}catch(error){
     row.independent_pages=[];
     for(const page of candidate.pages()){
      const u=new URL(page.url());row.independent_pages.push({host:u.hostname,path:u.pathname,text:safeText(await page.locator('body').innerText().catch(()=>'')).slice(0,1600),pending:[...(page.logoutPendingRequests?.values()||[])].map(({started,...data})=>({...data,duration_ms:Date.now()-started}))});
      if(u.hostname===new URL(origins.immich).hostname)await page.screenshot({path:path.join(evidence,'independent-precondition-'+source+'.png')}).catch(()=>{});
     }
     save();await candidate.close();throw error;
    }
   }
   other=independentContext;row.other_before=await readyState(other,values=>Object.values(values).every(s=>s.authenticated&&s.identity_matches),'independent browser');check('another browser has independent native sessions',Object.values(row.other_before).every(s=>s.authenticated&&s.identity_matches));
   stage('verify both browsers before logout');
   row.before_logout=await state(context);check('opening another browser did not invalidate the first',expectedSessions(row.before_logout));
   if(withoutPortal){await context.clearCookies({domain:portal.hostname});check('native source has no portal cookie before logout',!(await context.cookies(origins.portal)).some(c=>c.name==='__Host-anas_portal'));}
   if(source.includes('after-central-cookie-loss')||source==='portal-partial'){
    // Also drop the provider's cached sign-in, as with expired IAM site data;
    // retaining its cached token could silently restore the old parent session.
    const iam=await probe(context,'iam');await iam.evaluate(()=>localStorage.clear());
    await context.clearCookies({domain:new URL(origins.iam).hostname});
    row.after_iam_site_data_loss=await state(context);
    check('only IAM site data was lost while old application sessions remain valid',row.after_iam_site_data_loss.iam.signed_out&&['portal','nextcloud','immich'].every(app=>row.after_iam_site_data_loss[app].authenticated&&row.after_iam_site_data_loss[app].identity_matches));
    await pages.portal.close();pages.portal=await pageSession(context,'portal');
    row.before_logout=await state(context);check('old app cookies remain valid after native IAM reauthentication',Object.values(row.before_logout).every(s=>s.authenticated&&s.identity_matches));
   }
   stage('native logout: '+source);
   let logoutPage=pages.portal;
   if(source.startsWith('portal')){
    if(source==='portal-partial')await context.route(origins.immich+'/.well-known/anas/browser-logout?*',route=>route.abort());
    await logoutPage.getByRole('button',{name:'Sign out',exact:true}).click();
   }else{
    const native=source.startsWith('nextcloud')?'nextcloud':'immich';logoutPage=pages[native];
    // Native route navigation from the signed-in app document preserves real
    // Fetch Metadata. It does not inject cookies or call a mock logout API.
    if(nativeMenu){
     if(native==='nextcloud'){
      const introClose=logoutPage.getByRole('button',{name:'Close',exact:true}).filter({visible:true});
      if(await introClose.count())await introClose.first().click();
      await logoutPage.getByRole('button',{name:/^(Settings menu|Open user menu)/}).click();
      const action=logoutPage.getByText(/Log\s*out/i).filter({visible:true}).first();await action.waitFor();
      const href=await action.evaluate(n=>n.closest('a')?.getAttribute('href')||n.querySelector('a')?.getAttribute('href')||'');
      if(href)row.native_menu_path=new URL(href,origins.nextcloud).pathname;
      await logoutPage.screenshot({path:path.join(evidence,'native-menu-nextcloud.png')});
      await action.click({noWaitAfter:true});
     }else{
      await logoutPage.getByRole('button',{name:new RegExp(f.users.manager.username+'@')}).click();
      const action=logoutPage.getByRole('link',{name:/Sign\s*out|Log\s*out/i});await action.waitFor();
      await logoutPage.screenshot({path:path.join(evidence,'native-menu-immich.png')});
      await action.click({noWaitAfter:true});
     }
     check('logout is triggered through the native visible user menu',true);
    }else await logoutPage.evaluate(path=>location.assign(path),native==='nextcloud'?'/apps/user_oidc/sls':'/auth/logout');
    await logoutPage.waitForURL(u=>u.origin===origins.portal&&u.pathname==='/signed-out',{waitUntil:'domcontentloaded',timeout:120000});
    check('native app returns automatically to the anonymous-capable portal callback',await logoutPage.locator('meta[name=anas-native-logout]').getAttribute('content')==='automatic');
   }
   await expect(logoutPage.locator('#notice')).toContainText(/This browser has signed out|Some sign-outs could not be confirmed/,{timeout:120000});
   if(source==='portal-partial'){
    check('unavailable app is partial rather than complete',await logoutPage.getByRole('alert').isVisible()&&await logoutPage.getByRole('button',{name:'Retry sign-out',exact:true}).isVisible());
    row.partial=await state(context);check('local portal session ends despite an unavailable app',row.partial.portal.signed_out);
    check('unavailable old Immich session is not mistaken for signed out',row.partial.immich.authenticated&&row.partial.immich.identity_matches);
    await logoutPage.reload({waitUntil:'domcontentloaded'});await logoutPage.getByRole('button',{name:'Retry sign-out',exact:true}).waitFor();
    check('partial result and retry survive page reload',await logoutPage.getByRole('alert').isVisible());
    await context.unroute(origins.immich+'/.well-known/anas/browser-logout?*');
    await logoutPage.getByRole('button',{name:'Retry sign-out',exact:true}).click();
    await expect(logoutPage.getByText('This browser has signed out of IAM and the connected applications.',{exact:true})).toBeVisible({timeout:120000});
    check('fresh logout-only retry completes after the app recovers',true);
   }
   row.logout_notice=await logoutPage.locator('#notice').innerText();
   row.logout_results=await logoutPage.locator('.logout-results li').allTextContents();save();
   check('logout offers a homepage button',await logoutPage.getByRole('link',{name:'Return to homepage',exact:true}).getAttribute('href')==='/');
   await logoutPage.getByRole('link',{name:'Return to homepage',exact:true}).click();await logoutPage.getByRole('link',{name:'Sign in',exact:true}).waitFor();
   check('homepage button returns to the anonymous portal',new URL(logoutPage.url()).pathname==='/'&&(await browserGet(context,'portal','/api/me')).status===401);

   row.observations=[];
   stage('observe current-browser sessions after logout');
   const end=Date.now()+90000;
   do{row.after=await state(context);row.observations.push({at:new Date().toISOString(),state:row.after});save();if(Object.values(row.after).every(s=>s.signed_out))break;await new Promise(r=>setTimeout(r,1500));}while(Date.now()<end);
   check('one application logout ends every current-browser native session',Object.values(row.after).every(s=>s.signed_out),{},false);
   check('anonymous completion cannot read the business application catalogue',(await browserGet(context,'portal','/api/apps')).status===401,{},false);
   const old=await browserGet(context,'immich','/api/users/me',oldBearer);check('original Immich bearer is rejected',old.status===401,{status:old.status},false);
   stage('verify other browser after logout');row.other_after=await state(other);check('the other browser remains signed in',Object.values(row.other_after).every(s=>s.authenticated&&s.identity_matches),{},false);
   stage('refresh applications after logout');
   for(const app of ['nextcloud','immich']){
    if(new URL(pages[app].url()).origin!==origins[app])await pages[app].goto(origins[app],{waitUntil:'commit'});else await pages[app].reload({waitUntil:'commit'});
   }
   await pages.portal.goto(origins.portal+'/login',{waitUntil:'commit'});
   row.after_refresh=await state(context);check('refresh cannot silently restore any application',Object.values(row.after_refresh).every(s=>s.signed_out),{},false);
   row.visible_immich_identity=await pages.immich.getByRole('button',{name:new RegExp(f.users.manager.username+'@')}).isVisible();
   check('refreshed Immich is unauthenticated and shows no account',row.after_refresh.immich.signed_out&&!row.visible_immich_identity,{},false);
   await pages.immich.screenshot({path:path.join(evidence,'logout-'+source+'.png')});
   if(source==='portal'){
    stage('explicit new login invalidates cached exit results');
    // Cancel this tab's pending login document before starting a fresh flow.
    await logoutPage.goto(origins.portal+'/signed-out',{waitUntil:'domcontentloaded'});
    // Preserve the measured result, including an honest partial result after
    // a network timeout. A new authenticated bootstrap must override either.
    await expect(logoutPage.locator('#notice')).toHaveText(row.logout_notice);
    await pageSession(context,'portal');
    const fresh=await browserGet(context,'portal','/api/me');row.explicit_new_login={status:fresh.status,identity_matches:fresh.body.person?.anchor===f.users.manager.anchor};
    check('explicit native login creates a new portal session',fresh.status===200&&row.explicit_new_login.identity_matches);
    await logoutPage.reload({waitUntil:'domcontentloaded'});
    await expect(logoutPage.getByRole('heading',{name:'Your workspace',exact:true})).toBeVisible();
    check('old exit tab cannot report success for a new authenticated session',new URL(logoutPage.url()).pathname==='/'&&await logoutPage.getByRole('button',{name:'Sign out',exact:true}).isVisible());
   }
   row.status=row.checks.every(c=>c.pass)?'passed':'failed';
  }catch(e){
   row.status='failed';row.error=safeText(e.message).split('\n')[0];row.error_detail=safeText(e.message).slice(0,2200);row.pages=[];
   for(const page of context?.pages()||[]){const url=new URL(page.url());const headings=await page.locator('h1,h2,.warning,.error,[role=alert]').allTextContents({timeout:3000}).catch(()=>[]);row.pages.push({host:url.hostname,path:url.pathname,title:safeText(await page.title().catch(()=>'')),headings:headings.map(s=>safeText(s).slice(0,300)),pending:[...(page.logoutPendingRequests?.values()||[])].map(({started,...data})=>({...data,duration_ms:Date.now()-started}))});}
   if(nativeMenu){
    const native=source.startsWith('nextcloud')?'nextcloud':'immich';
    const nativePage=context?.pages().find(p=>new URL(p.url()).origin===origins[native]&&!new URL(p.url()).pathname.startsWith('/api/'));
    if(nativePage){
     row.native_menu_controls=(await nativePage.locator('button,a,[role=menuitem]').evaluateAll(nodes=>nodes.filter(n=>n.getClientRects().length).map(n=>({tag:n.tagName,label:n.getAttribute('aria-label'),title:n.getAttribute('title'),text:n.innerText?.slice(0,160)})).slice(0,100)).catch(()=>[])).map(v=>Object.fromEntries(Object.entries(v).map(([k,x])=>[k,x?safeText(x):x])));
     await nativePage.screenshot({path:path.join(evidence,'native-menu-failure-'+source+'.png')}).catch(()=>{});
    }
   }
   const failedPhoto=context?.pages().find(p=>new URL(p.url()).hostname===new URL(origins.immich).hostname&&!new URL(p.url()).pathname.startsWith('/api/'));
   if(failedPhoto)await failedPhoto.screenshot({path:path.join(evidence,'precondition-'+source+'.png')}).catch(()=>{});
  }
  finally{row.finished_at=new Date().toISOString();save();await context?.close();}
 }
 report.status=report.scenarios.every(s=>s.status==='passed')?'passed':'failed';
}finally{
 report.finished_at=new Date().toISOString();save();await independentContext?.close();await browser?.close();for(const socket of sockets)socket.destroy();await new Promise(resolve=>proxy.close(resolve));
}
console.log(JSON.stringify({status:report.status,evidence,scenarios:report.scenarios.map(s=>({source:s.source,status:s.status,error:s.error}))}));
if(report.status!=='passed')process.exitCode=1;
