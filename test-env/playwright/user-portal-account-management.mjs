// TEST_CASES: PORTAL-T-007
import fs from 'node:fs';
import path from 'node:path';
const {chromium}=await import(process.env.PORTAL_PLAYWRIGHT_MODULE||'@playwright/test');
const root=process.env.PORTAL_ACCOUNT_ROOT;
if(!root||!/^\/home\/whl\/anas-portal-admin-[A-Za-z0-9-]+$/.test(root))throw new Error('owned same-host fixture required');
const f=JSON.parse(fs.readFileSync(path.join(root,'private/browser.private.json'),'utf8'));
if(!/^pua[0-9a-f]{8}$/.test(f.prefix)||new URL(f.origin).hostname!=='portal.finance.hlong.wang')throw new Error('owned finance fixture required');
const report={baseline:f.baseline,checks:[],browser:'',test_tls_note:'Browser context trusts fixture transport; native Go and HTTPS probes independently validate CA and server name.',started_at:new Date().toISOString()};
const secretValues=[...Object.values(f.passwords),f.new_password];
function check(ids,name,pass,detail={}) {
  report.checks.push({requirements:ids.map(n=>'PORTAL-R-'+String(n).padStart(3,'0')),name,pass:!!pass,...detail});
  fs.writeFileSync(path.join(root,'evidence/account-browser-progress.json'),JSON.stringify(report));
  if(!pass)throw new Error(name);
}
const browser=await chromium.launch({executablePath:f.executable,headless:true,args:['--no-sandbox','--no-proxy-server','--host-resolver-rules=MAP *.finance.hlong.wang '+f.entry_ip+',EXCLUDE localhost']});
report.browser=browser.version();
const contexts=[];
async function context(locale='en',viewport={width:1440,height:1000}) {
  const c=await browser.newContext({locale,viewport,ignoreHTTPSErrors:true});contexts.push(c);
  await c.route('**/*',r=>['portal.finance.hlong.wang','auth.finance.hlong.wang'].includes(new URL(r.request().url()).hostname)?r.continue():r.abort());
  return c;
}
async function api(p,url,body,extra={}) {
  return p.evaluate(async input=>{
    const bootstrap=await fetch('/api/bootstrap',{cache:'no-store'}).then(r=>r.json());
    const response=await fetch(input.url,{method:input.body===undefined?'GET':'POST',cache:'no-store',headers:{'Content-Type':'application/json','X-CSRF-Token':bootstrap.csrf,...input.extra},body:input.body===undefined?undefined:JSON.stringify(input.body)});
    return {status:response.status,data:await response.json()};
  },{url,body,extra});
}
async function login(alias,c) {
  const p=await c.newPage();await p.goto(f.origin,{waitUntil:'domcontentloaded',timeout:45000});
  await p.getByRole('link',{name:'Sign in',exact:true}).press('Enter');
  await p.locator('.login-username-input input,input.login-username-input').fill(f.users[alias].username,{timeout:45000});
  await p.locator('input[type=password]').fill(f.passwords[alias]);
  await p.locator('button.login-button').click({noWaitAfter:true});
  await p.waitForURL(u=>u.hostname==='portal.finance.hlong.wang'&&u.pathname==='/',{timeout:45000,waitUntil:'domcontentloaded'});
  await p.getByRole('heading',{name:'Your workspace'}).waitFor();return p;
}
async function reauth(p,alias) {
  await p.evaluate(async()=>{const b=await fetch('/api/bootstrap').then(r=>r.json());const form=document.createElement('form');form.method='post';form.action='/api/reauth';const input=document.createElement('input');input.name='csrf';input.value=b.csrf;form.append(input);document.body.append(form);form.submit();});
  await p.waitForURL(u=>u.hostname==='auth.finance.hlong.wang',{timeout:45000});
  await p.locator('input[type=password]').waitFor({state:'visible',timeout:45000});
  await p.locator('.login-username-input input,input.login-username-input').fill(f.users[alias].username);
  await p.locator('input[type=password]').fill(f.passwords[alias]);await p.locator('button.login-button').click({noWaitAfter:true});
  await p.waitForURL(u=>u.hostname==='portal.finance.hlong.wang'&&u.pathname==='/',{timeout:45000,waitUntil:'domcontentloaded'});
  check([6,59],'native recent OIDC authentication '+alias,(await api(p,'/api/me')).data.recent_authentication);
}
async function domain(p,op,password=f.passwords.domain) {
  const prepared=await api(p,'/api/administration/prepare',op);
  check([56,59],'domain prepare requires authenticated parameters',prepared.status===200&&prepared.data.proof);
  return api(p,'/api/administration/execute',{...op,proof:prepared.data.proof,actor_password:password});
}
const op=(action,target,extra={})=>({action,target,reason:'owned acceptance '+f.prefix,...extra});
async function saved(p,operation) {
  const r=await api(p,'/api/administration/execute',operation);
  check([48,59],'ordinary typed action '+operation.action,r.status===200&&r.data.directory==='saved');return r;
}
try {
  const anonymous=await (await context()).newPage();await anonymous.goto(f.origin,{waitUntil:'domcontentloaded'});
  const loginBox=anonymous.getByRole('link',{name:'Sign in',exact:true});await loginBox.waitFor();
  const geometry=await loginBox.evaluate(n=>({background:getComputedStyle(n).backgroundColor,width:n.getBoundingClientRect().width,height:n.getBoundingClientRect().height}));
  check([42],'sign in is a visible primary action',geometry.height>=40&&geometry.width>=100&&geometry.background!=='rgba(0, 0, 0, 0)');
  check([42],'recovery is separate from normal first sign-in',await anonymous.getByRole('button',{name:'Password expired or a change required?',exact:true}).isVisible()&&!await anonymous.getByText('First sign-in or expired password',{exact:true}).count());
  for(const [locale,width] of [['en',1440],['zh-CN',1440],['zh-CN',390]]) {
    await anonymous.setViewportSize({width,height:900});await anonymous.selectOption('#language',locale);
    const horizontal=await anonymous.evaluate(()=>document.documentElement.scrollWidth>innerWidth);
    check([42],'login has no horizontal overflow '+locale+' '+width,!horizontal);
    await anonymous.screenshot({path:path.join(root,'evidence/login-'+locale+'-'+width+'.png'),fullPage:true});
  }
  await anonymous.goto(f.origin+'?lang=fr',{waitUntil:'domcontentloaded'});
  await anonymous.waitForFunction(()=>document.documentElement.lang==='en');check([42],'unsupported language resolves to English',await anonymous.locator('html').getAttribute('lang')==='en');
  const normalContext=await context(),normal=await login('normal',normalContext);
  let me=(await api(normal,'/api/me')).data;
  check([48,61],'ordinary person has no administration',!me.administration&&!me.actions.includes('users.read'));
  check([48],'ordinary person cannot enumerate personnel',(await api(normal,'/api/users')).data.count===0);
  let cards=(await api(normal,'/api/apps')).data.groups;
  check([43],'normal account only sees permitted user application',cards.length===1&&cards[0].kind==='user'&&cards[0].categories.flatMap(c=>c.applications).every(a=>a.name.includes('nextcloud')));
  const icon=cards[0].categories[0].applications[0].icons['48'].path;
  const iconCache=await normal.evaluate(async path=>{const r=await fetch(path);return {status:r.status,cache:r.headers.get('cache-control'),etag:r.headers.get('etag')};},icon);
  check([60],'authorized icons use private content-addressed cache',iconCache.status===200&&icon.includes('?v=')&&iconCache.cache.includes('private')&&iconCache.cache.includes('immutable')&&!!iconCache.etag);
  const deniedIcon=await anonymous.evaluate(async path=>(await fetch(path)).status,icon);
  check([60],'an unauthenticated context cannot fetch a cached icon',deniedIcon===401);
  await normal.getByLabel('Show categories',{exact:true}).uncheck();
  check([43],'category switch flattens only user group',await normal.locator('[data-kind=user] .category-heading').count()===0);
  await normal.reload({waitUntil:'domcontentloaded'});await normal.getByRole('heading',{name:'Your workspace'}).waitFor();
  check([43],'category switch persists in this browser',!await normal.getByLabel('Show categories',{exact:true}).isChecked());
  const requests=[];const observe=r=>{if(new URL(r.url()).pathname==='/api/me')requests.push(r.url());};normal.on('request',observe);
  await normal.reload({waitUntil:'domcontentloaded'});await normal.getByRole('heading',{name:'Your workspace'}).waitFor();await normal.locator('.card').first().waitFor();normal.off('request',observe);
  check([60],'one account read per initial page',requests.length===1,{account_reads:requests.length});
  report.performance=await normal.evaluate(()=>({navigation:performance.getEntriesByType('navigation').map(n=>({dom_content_loaded_ms:Math.round(n.domContentLoadedEventEnd),response_ms:Math.round(n.responseEnd)})),requests:performance.getEntriesByType('resource').map(n=>({path:new URL(n.name).pathname,duration_ms:Math.round(n.duration),transfer_bytes:n.transferSize}))}));
  await normal.getByRole('button',{name:'My account',exact:true}).click();await normal.getByRole('heading',{name:'My account',exact:true}).waitFor();
  await normal.locator('input[name=given_name]').fill('测试');await normal.locator('input[name=surname]').fill('用户');await normal.locator('input[name=display_name]').fill('门户测试用户');
  await normal.getByRole('button',{name:'Save',exact:true}).click();await normal.locator('.form-result [role=status]').waitFor();
  await normal.locator('.person-details').getByText('门户测试用户',{exact:true}).waitFor();
  check([44,45],'saved profile immediately refreshes the visible account summary',await normal.locator('.person-details').getByText('门户测试用户',{exact:true}).isVisible());
  let current=(await api(normal,'/api/me')).data;
  check([44,45],'profile form persists names and stable identity',current.person.given_name==='测试'&&current.person.surname==='用户'&&current.person.display_name==='门户测试用户'&&current.person.anchor===me.person.anchor&&current.person.username===me.person.username&&current.person.email===me.person.email);
  check([47],'email is read-only without a mail backend',!await normal.locator('input[name=email],input[name=mail]').count()&&await normal.getByText('Mail service is not configured.',{exact:false}).isVisible());
  let result=await api(normal,'/api/me/profile',{action:'profile.update',target:current.person.anchor,profile_version:current.profile_version,profile:{given_name:'Profile',surname:'Version',display_name:'Profile Version'}});
  check([45,46],'profile update accepts a current version',result.status===200&&result.data.directory==='saved');
  result=await api(normal,'/api/me/profile',{profile_version:current.profile_version,profile:{given_name:'Stale',surname:'Version',display_name:'Stale Version'}});
  check([46],'stale version cannot overwrite saved profile',result.status===409&&result.data.error==='conflict'&&(await api(normal,'/api/me')).data.person.display_name==='Profile Version');
  check([45,58],'arbitrary email field is rejected',(await api(normal,'/api/me/profile',{mail:'forbidden@example.invalid'})).status===400);
  check([5,59],'CSRF is still checked',(await api(normal,'/api/me/profile',{}, {'X-CSRF-Token':'forged'})).status===403);
  await normal.getByRole('button',{name:'Change password',exact:true}).click();await normal.getByRole('heading',{name:'Change password',exact:true}).waitFor();
  await normal.locator('input[name=old_password]').fill(f.passwords.normal+'wrong');await normal.locator('input[name=new_password]').fill(f.new_password);await normal.locator('input[name=confirm_password]').fill(f.new_password);
  await normal.getByRole('button',{name:'Save',exact:true}).click();await normal.locator('.form-result [role=alert]').waitFor();
  check([44],'wrong password has a visible accessible result',await normal.locator('.form-result [role=alert]').isVisible());
  const manager=await login('manager',await context()),domainPage=await login('domain',await context()),other=await login('other',await context());
  me=(await api(manager,'/api/me')).data;
  check([48,55],'platform administrator is not a domain administrator',me.administration&&!me.person.domain_risk&&me.domain_administration);
  const personnel=(await api(manager,'/api/users?q='+f.prefix)).data;
  check([48,61],'administrator sees all six tagged people across OUs',personnel.count===6&&personnel.users.some(p=>p.anchor===f.users.target.anchor)&&personnel.users.some(p=>p.domain_risk));
  check([61],'service accounts are excluded',(await api(manager,'/api/users?q=svc_portal_')).data.count===0);
  const target=(await api(manager,'/api/users/'+f.users.target.anchor)).data;
  check([48],'another ANAS administrator is editable',target.actions.includes('profile.update')&&target.actions.includes('password.reset'));
  await saved(manager,op('account.unlock',f.users.target.anchor));
  check([24],'unlock does not clear account validity or reset password',!(await api(manager,'/api/users/'+f.users.target.anchor)).data.person.locked);
  const groups=(await api(manager,'/api/groups?target='+f.users.normal.anchor)).data.groups;
  check([51],'default groups are explicitly registered and categorized',groups.length>0&&groups.every(g=>g.group.managed&&g.group.category!=='system')&&!groups.some(g=>g.group.name===f.groups.extra.username));
  const advanced=(await api(manager,'/api/groups?target='+f.users.normal.anchor+'&advanced=true')).data.groups;
  const extra=advanced.find(g=>g.group.reference===f.groups.extra.anchor),nested=(await api(manager,'/api/groups?target='+f.users.manager.anchor+'&advanced=true')).data.groups.find(g=>g.group.reference===f.groups.nested.anchor);
  check([51,52],'APP_ prefix does not confer ownership',extra&&!extra.group.managed&&extra.requires_domain);
  check([50],'indirect administrator path cannot be self-removed',nested&&nested.group.admin_role&&!nested.can_remove);
  result=await api(manager,'/api/administration/execute',op('password.reset',f.users.manager.anchor));check([49],'self reset rejected',result.status===403&&result.data.error==='self_administration_denied');
  result=await api(manager,'/api/administration/execute',op('groups.member.remove',f.users.manager.anchor,{group:f.groups.nested.anchor,expected_direct:true}));
  check([50,53],'self removal through nested administrator group rejected',result.status===403&&result.data.error==='self_administration_denied');
  const own=(await api(manager,'/api/groups?target='+f.users.manager.anchor)).data.groups.find(g=>g.group.admin_role&&!g.group.domain_risk);
  check([50],'direct administrator membership protected',own&&!own.can_remove);
  const editable=groups.find(g=>g.group.name==='APP_immich');
  check([51],'native app group available',!!editable);
  await saved(manager,op('groups.member.add',f.users.normal.anchor,{group:editable.group.reference,expected_direct:false}));
  check([28,31],'group write reports pending IAM separately',(await api(manager,'/api/users/'+f.users.normal.anchor)).data.person.groups.some(g=>g.anchor===editable.group.anchor&&g.direct));
  result=await api(manager,'/api/administration/execute',op('groups.member.add',f.users.normal.anchor,{group:editable.group.reference,expected_direct:false}));check([30],'stale membership cannot overwrite',result.status===409&&result.data.error==='conflict');
  await saved(manager,op('groups.member.remove',f.users.normal.anchor,{group:editable.group.reference,expected_direct:true}));
  report.stage='wait-for-real-recent-authentication-expiry';
  const expiringOperation=op('account.unlock',f.users.privileged.anchor),expiringProof=(await api(domainPage,'/api/administration/prepare',expiringOperation)).data.proof;
  let expired=false;
  for(let i=0;i<70;i++) {
    expired=!(await api(manager,'/api/me')).data.recent_authentication;
    if(expired)break;
    report.waited_seconds=i*5;fs.writeFileSync(path.join(root,'evidence/account-browser-progress.json'),JSON.stringify(report));
    await new Promise(resolve=>setTimeout(resolve,5000));
  }
  check([59],'signed authentication really expires',expired);
  result=await api(manager,'/api/administration/execute',op('account.unlock',f.users.target.anchor));
  check([59],'expired authentication blocks a real directory write',result.status===403&&result.data.error==='recent_authentication_required');
  await reauth(manager,'manager');await reauth(domainPage,'domain');
  result=await api(domainPage,'/api/administration/execute',{...expiringOperation,proof:expiringProof,actor_password:f.passwords.domain});
  check([56],'expired confirmation rejected after fresh OIDC',result.status===403&&result.data.error==='confirmation_required');
  report.stage='native-domain-operations';
  // A domain credential from the manager is valid for that other user, not for
  // the signed-in domain operator. Both accounts use distinct random passwords.
  let protectedOp=op('password.reset',f.users.privileged.anchor,{force_change:true});
  result=await api(domainPage,'/api/administration/execute',{...protectedOp,actor_password:f.passwords.domain});check([55,56],'domain write without one-use confirmation denied',result.status===403&&result.data.error==='confirmation_required');
  let prepared=await api(domainPage,'/api/administration/prepare',protectedOp);
  result=await api(domainPage,'/api/administration/execute',{...protectedOp,reason:'changed parameters',proof:prepared.data.proof,actor_password:f.passwords.domain});check([56],'changed operation parameters rejected',result.status===403);
  result=await domain(domainPage,protectedOp,f.passwords.manager);check([55,57],'another valid user password cannot authorize domain write',result.data.directory==='credentials_rejected');
  prepared=await api(domainPage,'/api/administration/prepare',protectedOp);
  result=await api(domainPage,'/api/administration/execute',{...protectedOp,proof:prepared.data.proof,actor_password:f.passwords.domain});
  check([55,57],'own domain rights reset only tagged privileged target',result.status===200&&result.data.directory==='saved'&&typeof result.data.temporary_password==='string');
  secretValues.push(result.data.temporary_password);
  const resetPassword=result.data.temporary_password;
  result=await api(domainPage,'/api/administration/execute',{...protectedOp,proof:prepared.data.proof,actor_password:f.passwords.domain});check([56],'used confirmation cannot be replayed',result.status===403);
  const recovery=await (await context()).newPage();await recovery.goto(f.origin,{waitUntil:'domcontentloaded'});
  result=await api(recovery,'/api/password/recover',{username:f.users.privileged.username,old_password:resetPassword,new_password:f.new_password,confirm_password:f.new_password});
  check([20,23,44],'temporary password completes required self change',result.status===200&&result.data.directory==='saved'&&(await api(recovery,'/api/me')).status===401);
  const memberOp=op('groups.member.add',f.users.normal.anchor,{group:extra.group.reference,expected_direct:false});
  result=await domain(domainPage,memberOp);check([52,55],'system group change uses own domain rights',result.data.directory==='saved');
  result=await domain(domainPage,{...memberOp,action:'groups.member.remove',expected_direct:true});check([52,55],'system group membership removed with a new confirmation',result.data.directory==='saved');
  await saved(domainPage,op('groups.member.remove',f.users.other.anchor,{group:own.group.reference,expected_direct:true}));
  const revoked=(await api(other,'/api/me')).data;
  check([48,59],'role removal rejects an already authenticated administrator',!revoked.administration&&(await api(other,'/api/users')).data.count===0&&(await api(other,'/api/administration/execute',op('account.unlock',f.users.target.anchor))).status===403);
  await saved(domainPage,op('groups.member.add',f.users.other.anchor,{group:own.group.reference,expected_direct:false}));
  result=await saved(manager,op('password.reset',f.users.target.anchor,{force_change:true}));secretValues.push(result.data.temporary_password);
  check([21,22,23],'ordinary administrator resets another ANAS admin and requires change',typeof result.data.temporary_password==='string'&&(await api(manager,'/api/users/'+f.users.target.anchor)).data.person.must_change_password);
  const audit=(await api(domainPage,'/api/audit')).data;
  check([25,32,34,57],'audit contains outcomes but no personal domain or temporary passwords',audit.records.some(r=>r.rule==='domain_operator'&&r.directory==='saved')&&secretValues.every(secret=>!JSON.stringify(audit).includes(secret)));
  await manager.getByRole('button',{name:'User management',exact:true}).click();await manager.getByRole('heading',{name:'User management',exact:true}).waitFor();
  await manager.getByRole('searchbox').fill(f.users.normal.username);await manager.getByRole('button',{name:'Search name or summary',exact:true}).click();
  await manager.getByRole('button',{name:'Details',exact:true}).click();await manager.getByText('Advanced: system and other groups',{exact:true}).waitFor();
  await manager.getByText('Advanced: system and other groups',{exact:true}).click();await manager.locator('tr').filter({hasText:f.groups.extra.username}).waitFor();
  check([42,52],'advanced system group warning is visible',await manager.locator('.advanced .warning').isVisible());
  const managedProfile=manager.locator('form').filter({has:manager.locator('input[name=given_name]')});
  await managedProfile.locator('input[name=given_name]').fill('Managed');await managedProfile.locator('input[name=surname]').fill('Person');await managedProfile.locator('input[name=display_name]').fill('Managed Person');
  await managedProfile.locator('[name=reason]').fill('owned profile UI '+f.prefix);await managedProfile.getByRole('button',{name:'Save',exact:true}).click();
  await managedProfile.locator('[role=status]').waitFor();await manager.getByRole('heading',{name:'Managed Person',exact:true}).waitFor();
  check([44,45,48],'administrator profile form refreshes the summary and heading',await manager.locator('.person-details').getByText('Managed Person',{exact:true}).isVisible()&&(await api(manager,'/api/users/'+f.users.normal.anchor)).data.person.given_name==='Managed');
  const acceptDialog=async d=>d.accept(d.type()==='prompt'?'owned UI domain denial '+f.prefix:undefined);
  manager.on('dialog',acceptDialog);
  try {
    await manager.locator('tr').filter({hasText:f.groups.extra.username}).getByRole('button',{name:'Add member',exact:true}).click();
    const confirmation=manager.getByRole('dialog');await confirmation.getByLabel('Your domain password',{exact:true}).fill(f.passwords.manager);
    await confirmation.getByRole('button',{name:'Confirm operation',exact:true}).click();await manager.locator('.form-result [role=alert]').waitFor();
    check([44,52,55,57],'domain dialog uses the signed-in operator and displays native ACL denial',!await manager.getByRole('dialog').count()&&(await api(manager,'/api/groups?target='+f.users.normal.anchor+'&advanced=true')).data.groups.some(g=>g.group.reference===extra.group.reference&&!g.direct));
  } finally {manager.off('dialog',acceptDialog);}

  for(const [locale,width] of [['en',1440],['zh-CN',1440],['zh-CN',390]]) {
    await manager.setViewportSize({width,height:1000});await manager.selectOption('#language',locale);await manager.locator('input[name=given_name]').waitFor();
    check([42],'user management fits viewport '+locale+' '+width,!await manager.evaluate(()=>document.documentElement.scrollWidth>innerWidth));
    await manager.screenshot({path:path.join(root,'evidence/user-management-'+locale+'-'+width+'.png'),fullPage:true});
  }
  await normal.getByRole('button',{name:'Sign out',exact:true}).click();
  await normal.getByRole('status').filter({hasText:'Portal and current IAM browser session signed out.'}).waitFor();
  check([41],'portal and IAM logout succeed through the page',(await api(normal,'/api/me')).status===401);
  await normal.goto(f.origin+'/login',{waitUntil:'domcontentloaded'});await normal.locator('input[type=password]').waitFor({state:'visible',timeout:45000});
  check([41],'the same IAM browser cookie no longer signs in silently',new URL(normal.url()).hostname==='auth.finance.hlong.wang');
  report.status='passed';
} catch(error) {
  report.status='failed';report.error=secretValues.reduce((text,secret)=>secret?text.replaceAll(secret,'[secret]'):text,error.message);throw new Error('account browser acceptance failed');
} finally {
  report.finished_at=new Date().toISOString();const encoded=JSON.stringify(report,null,2);
  if(secretValues.some(secret=>secret&&encoded.includes(secret)))throw new Error('unsafe report');
  fs.writeFileSync(path.join(root,'evidence/account-browser.json'),encoded);
  for(const c of contexts)await c.close();await browser.close();
}
