// TEST_CASES: PORTAL-T-007, MAIL-T-004, GROUPCAT-T-002, PORTALUX-T-004
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
  report.checks.push({requirements:ids.map(n=>String(n).startsWith('UX-')?'PORTALUX-R-'+String(n).slice(3):'PORTAL-R-'+String(n).padStart(3,'0')),name,pass:!!pass,...detail});
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
  await p.locator('body[data-page=home]').waitFor({state:'attached'});return p;
}
async function reauth(p,alias) {
  await p.evaluate(async()=>{const b=await fetch('/api/bootstrap').then(r=>r.json());const form=document.createElement('form');form.method='post';form.action='/api/reauth';const input=document.createElement('input');input.name='csrf';input.value=b.csrf;form.append(input);document.body.append(form);form.submit();});
  await p.waitForURL(u=>u.hostname==='auth.finance.hlong.wang',{timeout:45000});
  await p.locator('input[type=password]').waitFor({state:'visible',timeout:45000});
  await p.locator('.login-username-input input,input.login-username-input').fill(f.users[alias].username);
  await p.locator('input[type=password]').fill(f.passwords[alias]);await p.locator('button.login-button').click({noWaitAfter:true});
  await p.waitForURL(u=>u.hostname==='portal.finance.hlong.wang'&&u.pathname==='/',{timeout:45000,waitUntil:'domcontentloaded'});
  check(['UX-026',59],'OIDC re-sign-in still establishes recent authentication '+alias,(await api(p,'/api/me')).data.recent_authentication);
}
async function domain(p,op,password=f.passwords.domain) {
  const prepared=await api(p,'/api/administration/prepare',op);
  check([56,59],'domain prepare requires authenticated parameters',prepared.status===200&&prepared.data.proof);
  return api(p,'/api/administration/execute',{...op,proof:prepared.data.proof,actor_password:password});
}
const op=(action,target,extra={})=>({action,target,...extra});
// ANAS administrator role changes reuse recent authentication and require a
// bound confirmation (PORTAL-R-075); the scoped group writer performs the change.
async function confirmedRole(p,operation,password) {
  const prepared=await api(p,'/api/administration/prepare',operation);
  check([75],'administrator role change prepares a bound confirmation',prepared.status===200&&prepared.data.proof);
  return api(p,'/api/administration/execute',{...operation,proof:prepared.data.proof,...(password?{actor_password:password}:{})});
}
async function saved(p,operation) {
  const r=await api(p,'/api/administration/execute',operation);
  check([48,59],'ordinary typed action '+operation.action,r.status===200&&r.data.directory==='saved');return r;
}
try {
  const anonymous=await (await context()).newPage();await anonymous.goto(f.origin,{waitUntil:'domcontentloaded'});
  const loginBox=anonymous.getByRole('link',{name:'Sign in',exact:true});await loginBox.waitFor();
  const geometry=await loginBox.evaluate(n=>({background:getComputedStyle(n).backgroundColor,width:n.getBoundingClientRect().width,height:n.getBoundingClientRect().height}));
  check([42],'sign in is a visible primary action',geometry.height>=40&&geometry.width>=100&&geometry.background!=='rgba(0, 0, 0, 0)');
  const dropdownArrow=await anonymous.evaluate(async()=>{const r=await fetch('/static/chevron-down.svg');return {status:r.status,type:r.headers.get('content-type'),body:await r.text()};});
  check([42],'same-origin dropdown arrow is available under the portal CSP',dropdownArrow.status===200&&dropdownArrow.type==='image/svg+xml'&&dropdownArrow.body.includes('<svg'));
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
  check([48,62],'ordinary person has no administration',!me.administration&&!me.actions.includes('users.read'));
  check([48],'ordinary person cannot enumerate personnel',(await api(normal,'/api/users')).data.count===0);
  let cards=(await api(normal,'/api/apps')).data.groups;
  check([77],'normal account only sees permitted user application',cards.length===1&&cards[0].kind==='user'&&cards[0].categories.flatMap(c=>c.applications).every(a=>a.name.includes('nextcloud')));
  const icon=cards[0].categories[0].applications[0].icons['48'].path;
  const iconCache=await normal.evaluate(async path=>{const r=await fetch(path);return {status:r.status,cache:r.headers.get('cache-control'),etag:r.headers.get('etag')};},icon);
  check([60],'authorized icons use private content-addressed cache',iconCache.status===200&&icon.includes('?v=')&&iconCache.cache.includes('private')&&iconCache.cache.includes('immutable')&&!!iconCache.etag);
  const deniedIcon=await anonymous.evaluate(async path=>(await fetch(path)).status,icon);
  check([60],'an unauthenticated context cannot fetch a cached icon',deniedIcon===401);
  await normal.getByLabel('Show categories',{exact:true}).uncheck();
  check([77],'category switch flattens visible application groups',await normal.locator('[data-kind=user] .category-heading').count()===0);
  await normal.reload({waitUntil:'domcontentloaded'});await normal.locator('body[data-page=home]').waitFor({state:'attached'});
  check([77],'category switch persists in this browser',!await normal.getByLabel('Show categories',{exact:true}).isChecked());
  const requests=[];const observe=r=>{if(new URL(r.url()).pathname==='/api/me')requests.push(r.url());};normal.on('request',observe);
  await normal.reload({waitUntil:'domcontentloaded'});await normal.locator('body[data-page=home]').waitFor({state:'attached'});await normal.locator('.card').first().waitFor();normal.off('request',observe);
  check([60],'one account read per initial page',requests.length===1,{account_reads:requests.length});
  report.performance=await normal.evaluate(()=>({navigation:performance.getEntriesByType('navigation').map(n=>({dom_content_loaded_ms:Math.round(n.domContentLoadedEventEnd),response_ms:Math.round(n.responseEnd)})),requests:performance.getEntriesByType('resource').map(n=>({path:new URL(n.name).pathname,duration_ms:Math.round(n.duration),transfer_bytes:n.transferSize}))}));
  await normal.getByRole('link',{name:'My account',exact:true}).click();await normal.getByRole('heading',{name:'My account',exact:true}).waitFor();
  await normal.locator('input[name=given_name]').fill('测试');await normal.locator('input[name=surname]').fill('用户');await normal.locator('input[name=display_name]').fill('门户测试用户');
  await normal.getByRole('button',{name:'Save',exact:true}).click();await normal.locator('.form-result [role=status]').waitFor();
  await normal.locator('.person-details').getByText('门户测试用户',{exact:true}).waitFor();
  check([44,45],'saved profile immediately refreshes the visible account summary',await normal.locator('.person-details').getByText('门户测试用户',{exact:true}).isVisible());
  let current=(await api(normal,'/api/me')).data;
  check([44,45],'profile form persists names and stable identity',current.person.given_name==='测试'&&current.person.surname==='用户'&&current.person.display_name==='门户测试用户'&&current.person.anchor===me.person.anchor&&current.person.username===me.person.username&&current.person.email===me.person.email);
  // The mailbox notice lives in the Email section of My account.
  await normal.getByRole('link',{name:'Email',exact:true}).click();await normal.locator('body[data-page=account-mail]').waitFor({state:'attached'});
  check([47],'email is read-only without a mail backend',!await normal.locator('input[name=email],input[name=mail]').count()&&await normal.getByText('Your primary address is generated by the system.',{exact:false}).isVisible());
  let result=await api(normal,'/api/me/profile',{action:'profile.update',target:current.person.anchor,profile_version:current.profile_version,profile:{given_name:'Profile',surname:'Version',display_name:'Profile Version'}});
  check([45,46],'profile update accepts a current version',result.status===200&&result.data.directory==='saved');
  result=await api(normal,'/api/me/profile',{profile_version:current.profile_version,profile:{given_name:'Stale',surname:'Version',display_name:'Stale Version'}});
  check([46],'stale version cannot overwrite saved profile',result.status===409&&result.data.error==='conflict'&&(await api(normal,'/api/me')).data.person.display_name==='Profile Version');
  check([45,58],'arbitrary email field is rejected',(await api(normal,'/api/me/profile',{mail:'forbidden@example.invalid'})).status===400);
  check([5,59],'CSRF is still checked',(await api(normal,'/api/me/profile',{}, {'X-CSRF-Token':'forged'})).status===403);
  await normal.getByRole('link',{name:'Password and security',exact:true}).click();await normal.getByRole('heading',{name:'Change password',exact:true}).waitFor();
  await normal.locator('input[name=old_password]').fill(f.passwords.normal+'wrong');await normal.locator('input[name=new_password]').fill(f.new_password);await normal.locator('input[name=confirm_password]').fill(f.new_password);
  await normal.getByRole('button',{name:'Save',exact:true}).click();await normal.locator('.form-result [role=alert]').waitFor();
  check([44],'wrong password has a visible accessible result',await normal.locator('.form-result [role=alert]').isVisible());
  const manager=await login('manager',await context()),domainPage=await login('domain',await context()),other=await login('other',await context());
  me=(await api(manager,'/api/me')).data;
  check([48,55],'platform administrator is not a domain administrator',me.administration&&!me.person.domain_risk&&me.domain_administration);
  const personnel=(await api(manager,'/api/users?q='+f.prefix)).data;
  check([48,62],'administrator sees six tagged people in configured personnel OU',personnel.count===6&&personnel.users.some(p=>p.anchor===f.users.target.anchor)&&personnel.users.every(p=>!p.domain_risk)&&!personnel.users.some(p=>p.anchor===f.users.outside.anchor));
  check([62],'outside personnel stable references are rejected',(await api(manager,'/api/users/'+f.users.outside.anchor)).status===403&&(await api(manager,'/api/groups?target='+f.users.outside.anchor)).status===403);
  check([62],'service accounts are excluded',(await api(manager,'/api/users?q=svc_portal_')).data.count===0);
  check([63],'ordinary user cannot create a person',(await api(normal,'/api/users',{username:f.prefix+'denied',profile:{given_name:'No',surname:'Role',display_name:'No Role'},force_change:true})).status===403);
  check([63],'only configured administrators receive create action',me.actions.includes('user.create'));
  for(const field of ['mail','dn','groups','userAccountControl'])check([64,65],'creation rejects client-controlled '+field,(await api(manager,'/api/users',{username:f.prefix+'invalid',profile:{display_name:'Invalid'},force_change:true,[field]:field==='groups'?[]:'forbidden'})).status===400);
  check([73],'creation rejects removed reason field',(await api(manager,'/api/users',{username:f.prefix+'removed',profile:{display_name:'Removed'},reason:'removed field'})).status===400);
  const reserved=await api(manager,'/api/users',{username:f.prefix+'reserved',profile:{display_name:'Reserved'},force_change:true});
  check([67],'registered mail on another principal blocks creation',reserved.status===409&&reserved.data.directory==='conflict'&&!reserved.data.temporary_password);
  const newName=f.prefix+'created';
  // A private same-host cleanup name is registered before the request so even
  // a browser failure after Add can be reconciled without retrying creation.
  fs.writeFileSync(path.join(root,'private/created-user.private.json'),JSON.stringify({username:newName,prefix:f.prefix}));
  await manager.getByRole('link',{name:'Manage',exact:true}).click();
  await manager.getByRole('button',{name:'New user',exact:true}).click();
  const createForm=manager.locator('form').filter({has:manager.locator('input[name=username]')});
  await createForm.locator('input[name=username]').fill(newName);
  check([64],'generated primary address is visible without editable mail',(await createForm.locator('[data-generated-email]').textContent())===newName+'@'+me.mail_domain&&!await createForm.locator('input[name=mail],input[name=email]').count());
  await createForm.locator('input[name=given_name]').fill('Created');await createForm.locator('input[name=surname]').fill('Person');await createForm.locator('input[name=display_name]').fill('Created Person');
  await createForm.getByRole('button',{name:'New user',exact:true}).click();
  const creationDialog=manager.getByRole('dialog');await creationDialog.waitFor();
  check([69,73],'creation summary contains account without a reason field',(await creationDialog.innerText()).includes(newName)&&await creationDialog.locator('[name=reason],textarea').count()===0);
  const createdResponse=manager.waitForResponse(r=>new URL(r.url()).pathname==='/api/users'&&r.request().method()==='POST',{timeout:120000});
  await creationDialog.getByRole('button',{name:'Confirm operation',exact:true}).click();
  const created=await (await createdResponse).json();
  fs.writeFileSync(path.join(root,'private/created-user.private.json'),JSON.stringify({username:newName,prefix:f.prefix,target:created.target||null}));
  if(created.temporary_password)secretValues.push(created.temporary_password);
  check([64,66],'native creation shows a one-time password and stable target',created.directory==='saved'&&created.target&&created.temporary_password&&await createForm.locator('input[readonly]').isEnabled());
  const createdPerson=(await api(manager,'/api/users/'+created.target)).data.person;
  check([64,65],'created user has default mail, required password change and no admin role',createdPerson.username===newName&&createdPerson.email===newName+'@'+me.mail_domain&&createdPerson.must_change_password&&!createdPerson.administrator&&!createdPerson.domain_risk);
  const conflict=await api(manager,'/api/users',{username:newName,profile:{display_name:'Attempted replacement'},force_change:false});
  check([67],'existing username is not reset by a repeated creation',conflict.status===409&&conflict.data.directory==='conflict'&&!conflict.data.temporary_password&&(await api(manager,'/api/users/'+created.target)).data.person.display_name==='Created Person');
  const createdRecovery=await (await context()).newPage();await createdRecovery.goto(f.origin,{waitUntil:'domcontentloaded'});
  // Let the page finish its own anonymous bootstrap first; a concurrent
  // helper bootstrap could otherwise pair a CSRF token with another cookie.
  await createdRecovery.getByRole('link',{name:'Sign in',exact:true}).waitFor();
  const recoveryResult=await api(createdRecovery,'/api/password/recover',{username:newName,old_password:created.temporary_password,new_password:f.new_password,confirm_password:f.new_password});
  check([64,66],'new account temporary password completes native required change',recoveryResult.status===200&&recoveryResult.data.directory==='saved',{status:recoveryResult.status,error:recoveryResult.data.error||null,directory:recoveryResult.data.directory||null});
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
  const englishGroup=(await api(manager,'/api/groups?target='+f.users.normal.anchor+'&lang=fr')).data.groups.find(g=>g.group.name==='APP_immich');
  const chineseGroup=(await api(manager,'/api/groups?target='+f.users.normal.anchor+'&lang=zh-CN')).data.groups.find(g=>g.group.name==='APP_immich');
  check([72],'unsupported group language falls back to English and Chinese is available',englishGroup.group.description.startsWith('Allows entry')&&chineseGroup.group.description.includes('允许进入'));

  await manager.getByRole('link',{name:'Manage',exact:true}).click();
  await manager.getByRole('row').filter({has:manager.getByText(f.users.normal.username,{exact:true})}).getByRole('button',{name:'Details',exact:true}).click();
  const advancedPanel=manager.locator('details.advanced');
  check([72],'advanced groups are collapsed at the page bottom',await advancedPanel.evaluate(node=>!node.open)&&await advancedPanel.evaluate(node=>node===node.parentElement.lastElementChild));
  const ordinaryRow=manager.getByRole('row').filter({has:manager.getByText('APP_immich',{exact:true})});
  check([72],'registered application group has a description',!!(await ordinaryRow.locator('.group-description').innerText()));
  const directAdd=manager.waitForResponse(r=>r.url().includes('/api/administration/execute')&&r.request().method()==='POST');
  await ordinaryRow.getByRole('button',{name:'Add member',exact:true}).click();const directResult=await directAdd;
  check([73,71],'ordinary group addition directly saves with no confirmation and no note',directResult.ok()&&!await manager.getByRole('dialog').count()&&!Object.hasOwn(directResult.request().postDataJSON(),'reason'));

  // A recent session and one-use confirmation suffice for this scoped role write.
  const role=(await api(manager,'/api/groups?target='+f.users.normal.anchor)).data.groups.find(g=>g.group.admin_role&&!g.group.domain_risk);
  check([75],'administrator role reuses recent authentication without domain administration',role&&!role.requires_password&&!role.requires_domain&&role.can_add);
  const roleRow=manager.getByRole('row').filter({has:manager.getByText(role.group.display_name||role.group.name,{exact:true})});
  const roleWrites=[];const roleWatch=r=>{if(r.url().includes('/api/administration/execute'))roleWrites.push(r);};manager.on('request',roleWatch);
  await roleRow.getByRole('button',{name:'Add member',exact:true}).click();
  const roleDialog=manager.getByRole('dialog');await roleDialog.waitFor();
  check([75,69],'administrator role add opens an in-page confirmation without password',(await roleDialog.getByRole('heading').innerText())==='Confirm administrator role change'&&await roleDialog.locator('input[type=password]').count()===0);
  await roleDialog.getByRole('button',{name:'Cancel',exact:true}).click();manager.off('request',roleWatch);
  check([75],'cancelled administrator role add writes nothing',roleWrites.length===0);
  const roleAdd=op('groups.member.add',f.users.normal.anchor,{group:role.group.reference,expected_direct:false});
  result=await api(manager,'/api/administration/execute',roleAdd);
  check([75],'administrator role add without confirmation is rejected',result.status===403&&result.data.error==='confirmation_required');
  await reauth(other,'other');
  result=await confirmedRole(other,roleAdd,f.passwords.manager);
  check([75],'role write rejects an unnecessary password',result.status===400&&result.data.error==='invalid_request'&&!(await api(manager,'/api/users/'+f.users.normal.anchor)).data.person.administrator);
  result=await confirmedRole(other,roleAdd);
  check([75],'confirmed administrator role add saves through the scoped writer',result.status===200&&result.data.directory==='saved'&&(await api(manager,'/api/users/'+f.users.normal.anchor)).data.person.administrator);
  result=await confirmedRole(other,op('groups.member.remove',f.users.normal.anchor,{group:role.group.reference,expected_direct:true}));
  check([75],'confirmed administrator role removal restores the fixture',result.status===200&&result.data.directory==='saved'&&!(await api(manager,'/api/users/'+f.users.normal.anchor)).data.person.administrator);

  check([28,31],'group write reports pending IAM separately',(await api(manager,'/api/users/'+f.users.normal.anchor)).data.person.groups.some(g=>g.anchor===editable.group.anchor&&g.direct));
  result=await api(manager,'/api/administration/execute',op('groups.member.add',f.users.normal.anchor,{group:editable.group.reference,expected_direct:false}));check([30],'stale membership cannot overwrite',result.status===409&&result.data.error==='conflict');
  await saved(manager,op('groups.member.remove',f.users.normal.anchor,{group:editable.group.reference,expected_direct:true}));
  report.stage='wait-for-real-recent-authentication-expiry';
  const expiringOperation=op('groups.member.add',f.users.normal.anchor,{group:extra.group.reference,expected_direct:false}),expiringProof=(await api(domainPage,'/api/administration/prepare',expiringOperation)).data.proof;
  let expired=false;
  for(let i=0;i<70;i++) {
    const authentication=await api(manager,'/api/me');
    expired=authentication.status===200&&authentication.data.recent_authentication===false;
    if(expired)break;
    report.waited_seconds=i*5;fs.writeFileSync(path.join(root,'evidence/account-browser-progress.json'),JSON.stringify(report));
    await new Promise(resolve=>setTimeout(resolve,5000));
  }
  check([59],'signed authentication really expires',expired);
  result=await api(manager,'/api/administration/execute',op('account.unlock',f.users.target.anchor));
  check([59],'expired authentication blocks a real directory write',result.status===403&&result.data.error==='recent_authentication_required');
  // PORTALUX-R-026..028: the page asks for the operator's own domain password
  // and the portal verifies it against Samba; IAM is not involved.
  report.stage='in-page-recent-authentication';
  result=await api(manager,'/api/reauth/password',{password:f.passwords.manager},{'X-CSRF-Token':'forged'});
  check(['UX-027'],'password re-authentication checks CSRF',result.status===403&&!(await api(manager,'/api/me')).data.recent_authentication);
  result=await api(manager,'/api/reauth/password',{password:f.passwords.manager,username:f.users.domain.username});
  check(['UX-027'],'password re-authentication accepts no other fields',result.status===400);
  await manager.goto(f.origin+'/#/admin/users/'+encodeURIComponent(f.users.target.anchor),{waitUntil:'domcontentloaded'});await manager.locator('body[data-page=admin-user]').waitFor({state:'attached'});
  const zone=manager.locator('section.danger-zone'),unlockWrites=[];
  const countUnlock=request=>{if(request.method()==='POST'&&new URL(request.url()).pathname==='/api/administration/execute')unlockWrites.push(request.postDataJSON()?.action);};
  manager.on('request',countUnlock);
  await zone.getByRole('button',{name:'Unlock account',exact:true}).click();await manager.getByRole('dialog').getByRole('button',{name:'Confirm operation',exact:true}).click();
  const reauthDialog=manager.getByRole('dialog',{name:'Confirm your identity'});await reauthDialog.waitFor();
  check(['UX-028'],'expired recent authentication opens the in-page password dialog for the signed-in person',(await reauthDialog.innerText()).includes(f.users.manager.username)&&!manager.url().includes('auth.finance'));
  await reauthDialog.getByRole('button',{name:'Cancel',exact:true}).click();
  check(['UX-028'],'cancelling the password dialog does not retry the operation',!await manager.getByRole('dialog').count()&&unlockWrites.length===1);
  await zone.getByRole('button',{name:'Unlock account',exact:true}).click();await manager.getByRole('dialog').getByRole('button',{name:'Confirm operation',exact:true}).click();await reauthDialog.waitFor();
  await reauthDialog.getByLabel('Current password',{exact:true}).fill(f.passwords.domain);await reauthDialog.getByRole('button',{name:'Confirm operation',exact:true}).click();
  await reauthDialog.getByRole('alert').waitFor();
  check(['UX-026','UX-027','UX-028'],'another valid person password is rejected and the dialog stays open',await reauthDialog.isVisible()&&!(await api(manager,'/api/me')).data.recent_authentication&&unlockWrites.length===2);
  await reauthDialog.getByLabel('Current password',{exact:true}).fill(f.passwords.manager);await reauthDialog.getByRole('button',{name:'Confirm operation',exact:true}).click();
  await zone.locator('.form-result .result[role=status]').waitFor({timeout:45000});
  check(['UX-026','UX-028'],'own domain password restores recent authentication and the operation runs once',!await manager.getByRole('dialog').count()&&(await api(manager,'/api/me')).data.recent_authentication&&unlockWrites.length===3);
  manager.off('request',countUnlock);
  const reauthAudit=(await api(manager,'/api/audit')).data.records.filter(r=>r.action==='session.reauth');
  check(['UX-027'],'password re-authentication audits success and failure without passwords',reauthAudit.some(r=>r.directory==='saved')&&reauthAudit.some(r=>r.directory==='denied')&&secretValues.every(secret=>!JSON.stringify(reauthAudit).includes(secret)));
  await reauth(domainPage,'domain');
  result=await api(domainPage,'/api/administration/execute',{...expiringOperation,proof:expiringProof,actor_password:f.passwords.domain});
  check([56],'expired confirmation rejected after fresh OIDC',result.status===403&&result.data.error==='confirmation_required');
  report.stage='native-domain-operations';
  // A domain credential from the manager is valid for that other user, not for
  // the signed-in domain operator. Both accounts use distinct random passwords.
  let protectedOp=op('groups.member.add',f.users.normal.anchor,{group:extra.group.reference,expected_direct:false});
  result=await api(domainPage,'/api/administration/execute',{...protectedOp,actor_password:f.passwords.domain});check([55,56],'domain write without one-use confirmation denied',result.status===403&&result.data.error==='confirmation_required',{status:result.status,error:result.data.error});
  let prepared=await api(domainPage,'/api/administration/prepare',protectedOp);
  result=await api(domainPage,'/api/administration/execute',{...protectedOp,target:f.users.target.anchor,proof:prepared.data.proof,actor_password:f.passwords.domain});check([56],'changed operation parameters rejected',result.status===403&&result.data.error==='confirmation_required',{status:result.status,error:result.data.error});
  result=await domain(domainPage,protectedOp,f.passwords.manager);check([55,57],'another valid user password cannot authorize domain write',result.data.directory==='credentials_rejected');
  prepared=await api(domainPage,'/api/administration/prepare',protectedOp);
  result=await api(domainPage,'/api/administration/execute',{...protectedOp,proof:prepared.data.proof,actor_password:f.passwords.domain});
  check([55,57],'valid platform password does not supply domain ACLs',result.data.directory==='denied');
  result=await api(domainPage,'/api/administration/execute',{...protectedOp,proof:prepared.data.proof,actor_password:f.passwords.domain});check([56],'used confirmation cannot be replayed',result.status===403);
  const resetResult=await saved(manager,op('password.reset',f.users.target.anchor,{force_change:true}));
  secretValues.push(resetResult.data.temporary_password);
  const recovery=await (await context()).newPage();await recovery.goto(f.origin,{waitUntil:'domcontentloaded'});
  // Let the page finish its own anonymous bootstrap first; a concurrent
  // helper bootstrap could otherwise pair a CSRF token with another cookie.
  await recovery.getByRole('link',{name:'Sign in',exact:true}).waitFor();
  result=await api(recovery,'/api/password/recover',{username:f.users.target.username,old_password:resetResult.data.temporary_password,new_password:f.new_password,confirm_password:f.new_password});
  check([20,23,44],'temporary password completes required self change',result.status===200&&result.data.directory==='saved'&&(await api(recovery,'/api/me')).status===401);
  // Successful domain writes and creator isolation run only in PORTAL-T-008's
  // disconnected forest. No finance fixture receives any domain privilege.
  report.domain_success_evidence='PORTAL-T-008 isolated owner.test; not executed in the finance domain';
  check([52,55],'unauthorized system membership remains absent',!(await api(domainPage,'/api/groups?target='+f.users.normal.anchor+'&advanced=true')).data.groups.find(g=>g.group.reference===extra.group.reference).direct);
  result=await confirmedRole(domainPage,op('groups.member.remove',f.users.other.anchor,{group:own.group.reference,expected_direct:true}));
  check([48,75],'confirmed removal of another administrator saves',result.status===200&&result.data.directory==='saved');
  const revoked=(await api(other,'/api/me')).data;
  check([48,59],'role removal rejects an already authenticated administrator',!revoked.administration&&(await api(other,'/api/users')).data.count===0&&(await api(other,'/api/administration/execute',op('account.unlock',f.users.target.anchor))).status===403);
  result=await confirmedRole(domainPage,op('groups.member.add',f.users.other.anchor,{group:own.group.reference,expected_direct:false}));
  check([48,75],'confirmed restore of another administrator saves',result.status===200&&result.data.directory==='saved');
  result=await saved(manager,op('password.reset',f.users.target.anchor,{force_change:true}));secretValues.push(result.data.temporary_password);
  check([21,22,23],'ordinary administrator resets another ANAS admin and requires change',typeof result.data.temporary_password==='string'&&(await api(manager,'/api/users/'+f.users.target.anchor)).data.person.must_change_password);
  const audit=(await api(domainPage,'/api/audit')).data;
  check([25,32,34,57],'audit contains outcomes but no personal domain or temporary passwords',audit.records.some(r=>r.rule==='domain_operator'&&r.directory==='denied')&&secretValues.every(secret=>!JSON.stringify(audit).includes(secret)));
  await manager.getByRole('link',{name:'Manage',exact:true}).click();await manager.getByRole('heading',{name:'User management',exact:true}).waitFor();
  await manager.getByRole('searchbox').fill(f.users.normal.username);await manager.getByRole('button',{name:'Search',exact:true}).click();
  await manager.getByRole('button',{name:'Details',exact:true}).click();await manager.getByText('Advanced: system and other groups',{exact:true}).waitFor();
  await manager.getByText('Advanced: system and other groups',{exact:true}).click();await manager.locator('tr').filter({hasText:f.groups.extra.username}).waitFor();
  check([42,52],'advanced system group warning is visible',await manager.locator('.advanced .warning').isVisible());
  const managedProfile=manager.locator('form').filter({has:manager.locator('input[name=given_name]')});
  await managedProfile.locator('input[name=given_name]').fill('Managed');await managedProfile.locator('input[name=surname]').fill('Person');await managedProfile.locator('input[name=display_name]').fill('Managed Person');
  await managedProfile.getByRole('button',{name:'Save',exact:true}).click();
  const profileDialog=manager.getByRole('dialog');check([73],'administrator summary has no reason field',await profileDialog.locator('[name=reason],textarea').count()===0);await profileDialog.getByRole('button',{name:'Confirm operation',exact:true}).click();
  await managedProfile.locator('[role=status]').waitFor();await manager.getByRole('heading',{name:'Managed Person',exact:true}).waitFor();
  check([44,45,48],'administrator profile form refreshes the summary and heading',await manager.locator('.person-details').getByText('Managed Person',{exact:true}).isVisible()&&(await api(manager,'/api/users/'+f.users.normal.anchor)).data.person.given_name==='Managed');
  check([32,73],'operation audit preserves outcome without a reason',(await api(manager,'/api/audit')).data.records.some(record=>record.action==='profile.update'&&record.target===f.users.normal.anchor&&!Object.hasOwn(record,'reason')));
  const rejectNativeDialog=()=>{throw new Error('native browser dialog opened');};
  manager.on('dialog',rejectNativeDialog);
  try {
    const systemAdd=manager.locator('tr').filter({hasText:f.groups.extra.username}).getByRole('button',{name:'Add member',exact:true});
    await systemAdd.click();
    const cancelled=manager.getByRole('dialog');
    check([69,71],'system group uses an in-page summary without a reason',(await cancelled.innerText()).includes(f.groups.extra.username)&&await cancelled.locator('[name=reason],textarea').count()===0);
    await cancelled.getByRole('button',{name:'Cancel',exact:true}).click();
    check([69],'cancelling the system confirmation does not add membership',!await manager.getByRole('dialog').count()&&!(await api(manager,'/api/groups?target='+f.users.normal.anchor+'&advanced=true')).data.groups.find(g=>g.group.reference===extra.group.reference).direct);
    await systemAdd.click();await manager.getByRole('dialog').press('Escape');
    check([69],'closing the system confirmation does not add membership',!await manager.getByRole('dialog').count()&&!(await api(manager,'/api/groups?target='+f.users.normal.anchor+'&advanced=true')).data.groups.find(g=>g.group.reference===extra.group.reference).direct);
    await systemAdd.click();
    const confirmation=manager.getByRole('dialog');await confirmation.getByLabel('Your domain password',{exact:true}).fill(f.passwords.manager);
    await confirmation.getByRole('button',{name:'Confirm operation',exact:true}).click();await manager.locator('.form-result [role=alert]').waitFor();
    check([44,52,55,57],'domain dialog uses the signed-in operator and displays native ACL denial',!await manager.getByRole('dialog').count()&&(await api(manager,'/api/groups?target='+f.users.normal.anchor+'&advanced=true')).data.groups.some(g=>g.group.reference===extra.group.reference&&!g.direct));
  } finally {manager.off('dialog',rejectNativeDialog);}

  for(const [locale,width] of [['en',1440],['zh-CN',1440],['zh-CN',390]]) {
    await manager.setViewportSize({width,height:1000});await manager.selectOption('#language',locale);await manager.locator('input[name=given_name]').waitFor();
    await manager.locator('.group-description').first().waitFor();
    check([42],'user management fits viewport '+locale+' '+width,!await manager.evaluate(()=>document.documentElement.scrollWidth>innerWidth));
    if(width<=600)check([42,72],'mobile group status and action labels fit on one line',await manager.locator('.group-description').evaluateAll(descriptions=>descriptions.length>0&&descriptions.every(description=>{
      const cells=description.closest('tr').cells;
      return [cells[1],...cells[2].querySelectorAll('button')].every(node=>{
        const range=document.createRange();range.selectNodeContents(node);
        return [...range.getClientRects()].filter(rect=>rect.width>0&&rect.height>0).length===1;
      });
    })));
    await manager.screenshot({path:path.join(root,'evidence/user-management-'+locale+'-'+width+'.png'),fullPage:true});
  }
  // PORTALUX-R-006: account preferences follow the person to another browser.
  const visibleApp=(await api(normal,'/api/apps')).data.groups[0].categories[0].applications[0].name;
  const prefsSaved=await api(normal,'/api/me/preferences',{language:'ja',theme:'dark',show_categories:true,pinned:[visibleApp]});
  check(['UX-006'],'account preferences save through the portal',prefsSaved.status===200&&prefsSaved.data.preferences.theme==='dark');
  const second=await login('normal',await context());
  check(['UX-006'],'another browser applies the account language, theme and pinned application',await second.evaluate(()=>document.documentElement.dataset.theme)==='dark'&&await second.locator('html').getAttribute('lang')==='ja'&&await second.locator('.pin-tile').count()===1);
  await api(second,'/api/me/preferences',{language:'',theme:'system',show_categories:true,pinned:[]});
  // PORTALUX-R-021: every page fits a 390px screen with bottom navigation.
  await manager.setViewportSize({width:390,height:844});await manager.selectOption('#language','en');
  for(const [hash,name] of [['#/apps','home'],['#/account/profile','account-profile'],['#/account/security','account-security'],['#/account/groups','account-groups'],['#/admin/users','admin-users'],['#/admin/users/'+encodeURIComponent(f.users.normal.anchor),'admin-user']]){
    await manager.goto(f.origin+'/'+hash,{waitUntil:'domcontentloaded'});await manager.locator('body[data-page='+name+']').waitFor({state:'attached'});await manager.waitForTimeout(300);
    const box=await manager.locator('.bottom-nav').boundingBox();
    check(['UX-021'],'390px page fits without horizontal scrolling '+hash,!await manager.evaluate(()=>document.documentElement.scrollWidth>innerWidth)&&!!box&&Math.abs(box.y+box.height-844)<=1);
  }
  await normal.getByRole('button',{name:'Sign out',exact:true}).click();
  // This account suite deliberately allows only portal/IAM traffic. Other
  // application frames may be unconfirmed; their complete logout and isolation
  // have their own PORTAL-T-010 suite. Require the actual confirmed IAM result.
  const iamHost=new URL(f.origin.replace('://portal.','://auth.')).host;
  await normal.getByText(iamHost+': Signed out',{exact:true}).waitFor({timeout:120000});
  report.logout_results=await normal.locator('.logout-results li').allTextContents();
  check([76],'sign-out provides a homepage link',await normal.getByRole('link',{name:'Return to homepage',exact:true}).getAttribute('href')==='/');
  await normal.getByRole('link',{name:'Return to homepage',exact:true}).click();await normal.getByRole('link',{name:'Sign in',exact:true}).waitFor();
  check([76],'homepage button returns to the anonymous homepage',new URL(normal.url()).pathname==='/'&&(await api(normal,'/api/me')).status===401);
  check([41],'portal and IAM logout succeed through the page',(await api(normal,'/api/me')).status===401);
  await normal.goto(f.origin+'/login',{waitUntil:'domcontentloaded',timeout:120000});await normal.locator('input[type=password]').waitFor({state:'visible',timeout:45000});
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
