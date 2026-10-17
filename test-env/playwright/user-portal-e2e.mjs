// TEST_CASES: PORTAL-T-002, PORTAL-T-003, PORTAL-T-004
import fs from 'node:fs';
import crypto from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {chromium} from '@playwright/test';

const root=process.env.PORTAL_PROBE_ROOT;
const remote=process.env.PORTAL_TEST_REMOTE_ROOT;
const host=process.env.PORTAL_TEST_SSH_HOST||'whl@finance.hlong.wang';
const sshOptions=process.env.PORTAL_TEST_SSH_CONTROL?['-o','ControlPath='+process.env.PORTAL_TEST_SSH_CONTROL]:[];
const portal=process.env.PORTAL_TEST_ORIGIN||'https://portal.portal.test:29546';
const localFixture=process.env.PORTAL_TEST_LOCAL_FIXTURE==='1';
const run=localFixture?JSON.parse(fs.readFileSync(remote+'/run.json')):undefined;
if(localFixture&&process.env.DOCKER_HOST!=='unix://'+run.socket)throw new Error('owned Docker endpoint required');
const resolvedIP=process.env.PORTAL_TEST_RESOLVED_IP||'127.0.0.1';
if(!/^127\.0\.0\.1$/.test(resolvedIP)&&!(localFixture&&resolvedIP===run.ip))throw new Error('owned fixture address required');
const recentWindowSeconds=Number(process.env.PORTAL_TEST_RECENT_WINDOW_SECONDS||'60');
if(!Number.isInteger(recentWindowSeconds)||recentWindowSeconds<5||recentWindowSeconds>900)throw new Error('invalid fixture authentication window');
if(!root||!remote||!/^\/home\/whl\/anas-user-portal-e2e-test-[A-Za-z0-9-]+$/.test(remote)||new URL(portal).hostname!=='portal.portal.test')throw new Error('isolated portal fixture required');
const fixture=JSON.parse(fs.readFileSync(root+'/private/ldap.json'));
const a=fixture.anchors;
const secrets=[fixture.old,fixture.new,fixture.wrong];
const report={stage:'start',checks:[],browser:'',started_at:new Date().toISOString()};
const browser=await chromium.launch({executablePath:process.env.PORTAL_TEST_BROWSER||(process.platform==='darwin'?'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome':undefined),headless:true,args:['--no-proxy-server','--host-resolver-rules=MAP *.portal.test '+resolvedIP+',EXCLUDE localhost']});
report.browser=browser.version();
const contexts=[];
function check(ids,name,pass,detail={}){report.checks.push({requirements:ids.map(n=>'PORTAL-R-'+String(n).padStart(3,'0')),name,pass:!!pass,...detail});fs.writeFileSync(root+'/evidence/browser-progress.json',JSON.stringify({stage:report.stage,checks:report.checks.length,last:name,pass:!!pass}));if(!pass)throw new Error(name);}
function fixtureCommand(script,action){if(!/^[a-z-]+$/.test(action))throw new Error('invalid fixture action');const args=[remote+'/source/test-env/scripts/'+script,remote,action];return execFileSync(localFixture?'python3':'ssh',localFixture?args:[...sshOptions,host,'sudo','-n','python3',...args],{encoding:'utf8',timeout:45000});}
function remoteAction(action){return JSON.parse(fixtureCommand('user-portal-fixture-control.py',action));}
async function context(locale='en'){const c=await browser.newContext({ignoreHTTPSErrors:true,locale});contexts.push(c);await c.route('**/*',r=>new URL(r.request().url()).hostname.endsWith('.portal.test')?r.continue():r.abort());return c;}
async function api(page,path,body,extra={}){report.last_request=path;return page.evaluate(async x=>{const boot=await fetch('/api/bootstrap',{cache:'no-store'}).then(r=>r.json());const r=await fetch(x.path,{method:x.body===undefined?'GET':'POST',headers:x.body===undefined?{}:{'Content-Type':'application/json','X-CSRF-Token':boot.csrf,...x.extra},body:x.body===undefined?undefined:JSON.stringify(x.body)});let data;try{data=await r.json();}catch{data={}}return {status:r.status,data};},{path,body,extra});}
async function login(c,name,password=fixture.old){const p=await c.newPage();await p.goto(portal,{waitUntil:'domcontentloaded',timeout:45000});await p.getByRole('link',{name:'Sign in',exact:true}).click();await p.locator('.login-username-input input, input.login-username-input').fill(name,{timeout:45000});await p.locator('input[type=password]').fill(password);await p.locator('button.login-button').click({noWaitAfter:true});await p.waitForURL(u=>u.hostname==='portal.portal.test'&&u.pathname==='/',{waitUntil:'domcontentloaded',timeout:45000});const v=await api(p,'/api/me');if(v.status!==200)throw new Error('native portal login failed');return p;}
async function reauth(p,name='poperator',password=fixture.old){await p.evaluate(async()=>{const b=await fetch('/api/bootstrap').then(r=>r.json());const f=document.createElement('form');f.method='post';f.action='/api/reauth';const i=document.createElement('input');i.name='csrf';i.value=b.csrf;f.append(i);document.body.append(f);f.submit();});await p.waitForURL(u=>u.hostname==='auth.portal.test',{timeout:45000});await p.locator('input[type=password]').waitFor({state:'visible',timeout:45000});await p.locator('.login-username-input input, input.login-username-input').fill(name);await p.locator('input[type=password]').fill(password);await p.locator('button.login-button').click({noWaitAfter:true});await p.waitForURL(u=>u.hostname==='portal.portal.test'&&u.pathname==='/',{waitUntil:'domcontentloaded',timeout:45000});}
function cards(v){return v.groups.flatMap(g=>g.categories.flatMap(c=>c.applications));}
async function admin(p,path,body){await reauth(p);return api(p,path,body);}

function fault(action){return JSON.parse(fixtureCommand('user-portal-runtime-fault.py',action));}
async function ready(){for(let i=0;i<20;i++){try{const c=await context();const p=await c.newPage();const r=await p.goto(portal+'/health',{timeout:5000});await c.close();if(r.status()===200)return;}catch{}await new Promise(resolve=>setTimeout(resolve,500));}throw new Error('portal not ready');}
async function boundary(){
 report.stage='boundary';const p=await login(await context(),'poperator');
 let v=await p.evaluate(async()=>{const r=await fetch('/oidc/callback?state=forged&code=forged');return r.status;});check([2],'unbound OIDC callback refused',v===401);
 for(const target of [a.pprotected,a.pscopeb])check([14],'protected or outside member target denied '+target,(await admin(p,'/api/groups/'+a.PortalAllowed+'/member',{target,add:true,expected_direct:false,reason:'fixture forbidden target'})).status===403);
 for(const group of [a.PortalPrivileged,a.PortalScope])check([15],'privileged ancestry and boundary group denied '+group,(await admin(p,'/api/groups/'+group+'/member',{target:a.pscopea,add:true,expected_direct:false,reason:'fixture forbidden group'})).status===403);
 remoteAction('recreate-editable-group');const reused=await admin(p,'/api/groups/'+a.PortalAllowed+'/member',{target:a.pscopea,add:true,expected_direct:false,reason:'fixture reused group'});
 check([30],'same name and DN group reuse cannot accept old group reference',reused.status===403&&remoteAction('editable-group-empty').empty);
 const observer=await login(await context(),'pcasuser');remoteAction('recreate-observer');check([3,30],'same name and DN reuse does not replace original session',(await api(observer,'/api/me')).status===401);
}

async function faults(){
 report.stage='expired-and-current-state';const op=await login(await context(),'poperator');remoteAction('expire-target');
 check([20],'directory confirms expired target',(await api(op,'/api/users/'+a.pexpired)).data.person.password_expired);
 const recover=await (await context()).newPage();await recover.goto(portal,{waitUntil:'domcontentloaded'});let v=await api(recover,'/api/password/recover',{username:'pexpired',old_password:fixture.old,new_password:fixture.new,confirm_password:fixture.new});
 check([20],'expired original password change before SSO succeeds',v.data.directory==='saved'&&(await api(recover,'/api/me')).status===401);remoteAction('restore-expiry');
 remoteAction('lock-operator');check([7],'locked current actor loses old portal session',(await api(op,'/api/me')).status===401);remoteAction('restore-operator-lock');
 report.stage='rate-limit';const rate=await (await context()).newPage();await rate.goto(portal,{waitUntil:'domcontentloaded'});
 for(let i=0;i<5;i++)check([38,39],'uniform public rejected attempt '+i,(await api(rate,'/api/password/recover',{username:'rate-nonexistent',old_password:fixture.wrong,new_password:fixture.new,confirm_password:fixture.new})).status===409);
 check([38],'sixth account attempt refused',(await api(rate,'/api/password/recover',{username:'rate-nonexistent',old_password:fixture.wrong,new_password:fixture.new,confirm_password:fixture.new})).status===429);
 report.stage='unknown-write';fault('proxy-start');fault('proxy-enable');await ready();const self=await login(await context(),'pnormal',fixture.new);fault('proxy-arm');const next='Aa1!'+crypto.randomBytes(24).toString('hex');secrets.push(next);
 v=await api(self,'/api/password/change',{old_password:fixture.new,new_password:next,confirm_password:next});const wire=fault('proxy-result');check([26],'saved LDAP write with lost reply is unknown exactly once',v.data.directory==='unknown'&&wire.ldap_code===0&&wire.modify_requests===1&&wire.saved_dropped===1);
 await self.waitForTimeout(1200);check([26],'no automatic LDAP retry after unknown result',fault('proxy-result').modify_requests===1);fault('proxy-disable');await ready();const verified=await login(await context(),'pnormal',next);check([26],'manual verification observes applied password',!!verified);
 report.stage='intent-audit-failure';const operator=await login(await context(),'poperator');fault('audit-intent-failure');v=await admin(operator,'/api/groups/'+a.PortalAllowed+'/member',{target:a.pscopea,add:true,expected_direct:false,reason:'fixture intent must persist'});
 check([35],'intent persistence failure prevents directory request',v.status===503&&v.data.directory==='not_attempted');fault('audit-unlink-failure');await ready();const op2=await login(await context(),'poperator');check([35],'failed intent leaves direct membership unchanged',!(await api(op2,'/api/groups?target='+a.pscopea)).data.groups.find(g=>g.group.anchor===a.PortalAllowed).direct);
 report.stage='result-audit-failure';fault('audit-arm');v=await admin(op2,'/api/groups/'+a.PortalAllowed+'/member',{target:a.pscopea,add:true,expected_direct:false,reason:'fixture result audit failure'});
 check([37],'directory success and audit failure remain distinct',v.data.directory==='saved'&&v.data.audit==='unavailable');v=await api(op2,'/api/groups/'+a.PortalAllowed+'/member',{target:a.pscopea,add:false,expected_direct:true,reason:'fixture write must be paused'});check([37],'result persistence failure closes subsequent writes',v.status===503&&v.data.directory==='not_attempted');
 fault('audit-restore');await ready();check([36],'restart invalidates old session',(await api(op2,'/api/me')).status===401);const op3=await login(await context(),'poperator');const records=(await api(op3,'/api/audit')).data.records;
 check([36],'durable pending intent becomes unknown without replay',records.some(r=>r.reason==='fixture result audit failure'&&r.directory==='unknown')&&(await api(op3,'/api/groups?target='+a.pscopea)).data.groups.find(g=>g.group.anchor===a.PortalAllowed).direct);
 await op3.waitForTimeout(1200);check([36],'service restart does not replay pending member write',(await api(op3,'/api/groups?target='+a.pscopea)).data.groups.find(g=>g.group.anchor===a.PortalAllowed).direct);
 report.stage='review-regressions';const alias=remoteAction('recovery-alias');
 check([38,39],'recovery aliases refer to the same actual directory identity',alias.anchor===a.pscopeb&&alias.username==='pscopeb'&&alias.upn==='pscopeb@portal.test');
 const publicPage=await (await context()).newPage();await publicPage.goto(portal,{waitUntil:'domcontentloaded'});
 const recoverBody=username=>({username,old_password:fixture.wrong,new_password:fixture.new,confirm_password:fixture.new});
 for(let i=0;i<5;i++)check([38,39],'stable identity rejected attempt '+i,(await api(publicPage,'/api/password/recover',recoverBody(alias.username))).status===409);
 const limited=await api(publicPage,'/api/password/recover',recoverBody(alias.upn));const absent=await api(publicPage,'/api/password/recover',recoverBody('review-nonexistent'));
 check([39],'identity-limited known alias and absent account have identical public response',limited.status===409&&absent.status===409&&JSON.stringify(limited.data)===JSON.stringify(absent.data));
 check([38],'submitted label still refuses its sixth attempt',(await api(publicPage,'/api/password/recover',recoverBody(alias.username))).status===429);
 const invalid=await api(op3,'/api/groups/'+a.PortalAllowed+'/member',{unexpected:true});const audit=(await api(op3,'/api/audit')).data.records;
 check([32,34],'invalid member request records the bound actor',invalid.status===400&&audit.some(r=>r.action==='groups.member'&&r.actor===a.poperator&&r.directory==='denied'));
}

try {
 if(process.env.PORTAL_TEST_MODE==='boundary'){await boundary();report.stage='complete';} else if(process.env.PORTAL_TEST_MODE==='faults') {await faults();report.stage='complete';} else {
 report.stage='anonymous-and-login';const anonymous=await context();const anon=await anonymous.newPage();await anon.goto(portal,{waitUntil:'domcontentloaded'});
 check([4,40],'anonymous endpoints expose no account or catalog',(await api(anon,'/api/me')).status===401&&(await api(anon,'/api/apps')).status===401&&(await api(anon,'/api/assets/user-tool/48')).status===401);
 const normalContext=await context();const normal=await login(normalContext,'pnormal');let me=(await api(normal,'/api/me')).data;
 check([2,3,8,13],'native PKCE binds actual AD identity and default closes delegation',me.person.anchor===a.pnormal&&me.person.username==='pnormal'&&me.person.email===''&&me.actions.length===0);
 const cookie=(await normalContext.cookies(portal)).find(c=>c.name==='__Host-anas_portal');check([4],'session uses host protected HttpOnly Secure Lax cookie',cookie?.secure&&cookie?.httpOnly&&cookie?.sameSite==='Lax'&&cookie?.path==='/');
 let apps=(await api(normal,'/api/apps?lang=en')).data;let list=cards(apps);check([1,7],'ordinary user receives only allowed application',list.length===1&&list[0].name==='user-tool'&&apps.groups[0].kind==='user');
 check([1],'name and summary search preserve permission set',cards((await api(normal,'/api/apps?q=Useful')).data).length===1&&cards((await api(normal,'/api/apps?q=Administration')).data).length===0);
 check([1],'unsupported language falls back to English',(await api(normal,'/api/apps?lang=fr')).data.language==='en');
 check([1],'Chinese catalog and raw URL are retained',cards((await api(normal,'/api/apps?lang=zh-CN')).data)[0].display_name==='用户工具'&&list[0].url==='https://tools.example.net/user?view=one#details');
 const sizes=Object.keys(list[0].icons).sort();check([4],'seven authorized icon sizes served',sizes.length===7);for(const size of sizes){const code=await normal.evaluate(async path=>(await fetch(path)).status,list[0].icons[size].path);check([4],'icon '+size+' readable only after authorization',code===200);}
 check([4,25],'public DTO excludes IAM and LDAP secrets',!JSON.stringify(me).match(/accessToken|access_token|id_token|bind_password|ldap_bind|objectGUID|objectSid|"dn"/));
 check([5,9,40],'write CSRF and arbitrary attributes are refused',(await api(normal,'/api/password/change',{old_password:fixture.old,new_password:fixture.new,confirm_password:fixture.new},{'X-CSRF-Token':'forged'})).status===403&&(await api(normal,'/api/password/change',{mail:'evil@example.net'})).status===400&&(await api(normal,'/api/me',{displayName:'overwrite'})).status===405);
 await normal.getByRole('button',{name:'My account',exact:true}).click();await normal.getByRole('heading',{name:'My account'}).waitFor();await normal.selectOption('#language','zh-CN');await normal.getByRole('heading',{name:'我的账号'}).waitFor();check([8,10],'account language switches without replacing identity',(await api(normal,'/api/me')).data.person.anchor===a.pnormal);
 await normal.selectOption('#language','en');await normal.getByRole('button',{name:'Applications',exact:true}).click();await normal.getByRole('heading',{name:'Your workspace'}).waitFor();await normal.screenshot({path:root+'/evidence/portal-apps-en.png',fullPage:true});

 report.stage='delegated-scope';const opContext=await context();const op=await login(opContext,'poperator');me=(await api(op,'/api/me')).data;check([7,11],'delegated operator receives declared actions',me.actions.includes('password.reset')&&me.actions.includes('users.read'));
 const users=(await api(op,'/api/users?limit=2')).data;check([11],'filtering precedes pagination and count',users.count===6&&users.users.length===2&&!users.users.some(u=>u.username==='pscopeb'));
 check([11],'outside scope detail and search remain closed',(await api(op,'/api/users/'+a.pscopeb)).status===403&&(await api(op,'/api/users?q=pscopeb')).data.count===0);
 const target=(await api(op,'/api/users/'+a.pscopea)).data.person;const inherited=target.groups.find(g=>g.anchor===a.PortalAllowed);check([10,29],'nested membership provides a verifiable source',inherited&&!inherited.direct&&inherited.sources.includes(a.PortalNested));
 let groups=(await api(op,'/api/groups?target='+a.pscopea)).data.groups;const allowed=groups.find(g=>g.group.anchor===a.PortalAllowed);check([15,29],'inherited and protected groups cannot be removed',allowed.inherited&&!allowed.can_remove&&!groups.some(g=>[a.PortalPrivileged,a.PortalScope].includes(g.group.anchor)));
 check([14],'protected and outside scope reset refused',(await api(op,'/api/users/'+a.pprotected+'/reset',{reason:'fixture'})).status===403&&(await api(op,'/api/users/'+a.pscopeb+'/reset',{reason:'fixture'})).status===403);
 await op.waitForTimeout(recentWindowSeconds*1000+1500);let outcome=await api(op,'/api/users/'+a.pscopea+'/unlock',{reason:'fixture'});check([6],'expired signed authentication prevents administration',outcome.status===403&&outcome.data.error==='recent_authentication_required');
 outcome=await admin(op,'/api/groups/'+a.PortalAllowed+'/member',{target:a.pscopea,add:true,expected_direct:false,reason:'fixture direct membership'});check([6,28,31],'one direct member added with separate IAM pending result',outcome.data.directory==='saved'&&outcome.data.iam==='pending'&&outcome.data.external_sessions==='unverified',{status:outcome.status,directory:outcome.data.directory,error:outcome.data.error});
 outcome=await admin(op,'/api/groups/'+a.PortalAllowed+'/member',{target:a.pscopea,add:true,expected_direct:false,reason:'fixture stale direct expectation'});check([30],'stale membership expectation conflicts without overwrite',outcome.status===409&&outcome.data.directory==='conflict');
 outcome=await admin(op,'/api/groups/'+a.PortalAllowed+'/member',{target:a.pscopea,add:false,expected_direct:true,reason:'fixture remove direct only'});check([28,29],'removing direct membership retains nested grant',outcome.data.directory==='saved'&&(await api(op,'/api/users/'+a.pscopea)).data.person.groups.some(g=>g.anchor===a.PortalAllowed&&!g.direct),{status:outcome.status,directory:outcome.data.directory,error:outcome.data.error});
 outcome=await admin(op,'/api/groups/'+a.PortalOther+'/member',{target:a.pscopea,add:true,expected_direct:false,reason:'fixture ACL counterexample'});check([17],'directory ACL rejects group outside dedicated binding',outcome.status===409&&outcome.data.directory==='denied');
 check([21],'reset reason mandatory',(await admin(op,'/api/users/'+a.pscopea+'/reset',{reason:''})).status===400);
 outcome=await admin(op,'/api/users/'+a.pscopea+'/reset',{reason:'fixture reset and require change',force_change:true});const temporary=outcome.data.temporary_password;if(temporary)secrets.push(temporary);
 check([21,22,23],'native atomic reset returns password once and forces change',outcome.data.directory==='saved'&&typeof temporary==='string'&&(await api(op,'/api/users/'+a.pscopea)).data.person.must_change_password);
 check([22,25,32,33,34],'scoped audit contains actual outcomes and no credentials',!JSON.stringify((await api(op,'/api/audit')).data).includes(temporary)&&(await api(op,'/api/audit')).data.records.some(r=>r.action==='password.reset'&&r.rule==='fixture-helpdesk'&&r.target===a.pscopea&&r.phase==='result'&&r.directory==='saved'));
 const recoverContext=await context();const recover=await recoverContext.newPage();await recover.goto(portal,{waitUntil:'domcontentloaded'});outcome=await api(recover,'/api/password/recover',{username:'pscopea',old_password:temporary,new_password:fixture.new,confirm_password:fixture.new});check([20,23],'forced change completes with original temporary password before SSO',outcome.data.directory==='saved'&&(await api(recover,'/api/me')).status===401);
 remoteAction('lock-target');const beforeLock=remoteAction('snapshot');check([24],'fixture target actually locked',beforeLock.pscopea.lockoutTime!=='0');outcome=await admin(op,'/api/users/'+a.pscopea+'/unlock',{reason:'fixture unlock only'});const afterLock=remoteAction('snapshot');check([24],'unlock changes only lock state',outcome.data.directory==='saved'&&afterLock.pscopea.lockoutTime==='0'&&beforeLock.pscopea.pwdLastSet===afterLock.pscopea.pwdLastSet&&beforeLock.pscopea.userAccountControl===afterLock.pscopea.userAccountControl&&beforeLock.pscopea.accountExpires===afterLock.pscopea.accountExpires);
 check([18],'incorrect old password never becomes reset',(await api(normal,'/api/password/change',{old_password:fixture.wrong,new_password:fixture.new,confirm_password:fixture.new})).data.directory==='credentials_rejected');
 check([19],'actual directory rejects weak password',(await api(normal,'/api/password/change',{old_password:fixture.old,new_password:'short',confirm_password:'short'})).data.directory==='password_policy_rejected');
 outcome=await api(normal,'/api/password/change',{old_password:fixture.old,new_password:fixture.new,confirm_password:fixture.new});check([18,27],'self change persists and invalidates portal session',outcome.data.directory==='saved'&&outcome.data.reauthenticate&&(await api(normal,'/api/me')).status===401);
 const normal2=await login(await context(),'pnormal',fixture.new);check([18],'new password actually authenticates',!!normal2);
 const firstContext=await context();const first=await firstContext.newPage();await first.goto(portal,{waitUntil:'domcontentloaded'});outcome=await api(first,'/api/password/recover',{username:'pfirst',old_password:fixture.old,new_password:fixture.new,confirm_password:fixture.new});check([20],'first password change creates no normal session',outcome.data.directory==='saved'&&(await api(first,'/api/me')).status===401);
 const fail1=await api(first,'/api/password/recover',{username:'does-not-exist',old_password:fixture.wrong,new_password:fixture.new,confirm_password:fixture.new});const fail2=await api(first,'/api/password/recover',{username:'pprotected',old_password:fixture.wrong,new_password:fixture.new,confirm_password:fixture.new});check([39],'public failure shape hides account existence',fail1.status===fail2.status&&JSON.stringify(fail1.data)===JSON.stringify(fail2.data));
 report.stage='live-revocation';const observerContext=await context();const observer=await login(observerContext,'pcasuser');remoteAction('remove-app');check([7],'current AD removal denies old IAM claims',(await api(observer,'/api/apps')).data.groups.length===0&&(await api(observer,'/api/assets/user-tool/48')).status===404);remoteAction('restore-app');
 remoteAction('rename-observer');me=(await api(observer,'/api/me')).data;check([3,8],'rename preserves anchor and returns real directory fields',me.person.anchor===a.pcasuser&&me.person.username==='pcas-renamed'&&me.person.display_name==='Renamed fixture');
 remoteAction('disable-observer');check([7],'disabled current person loses existing portal session',(await api(observer,'/api/me')).status===401);remoteAction('restore-observer');
 remoteAction('remove-operator');check([7],'old page cannot retain removed delegation',(await api(op,'/api/me')).data.actions.length===0&&(await api(op,'/api/users/'+a.pscopea+'/reset',{reason:'stale page'})).status===403);remoteAction('restore-operator');
 check([33],'ordinary account audit is limited to itself',(await api(normal2,'/api/audit')).data.records.every(r=>r.target===a.pnormal));
 report.stage='joint-logout';await normal2.getByRole('button',{name:'Sign out',exact:true}).click();await normal2.getByRole('status').filter({hasText:'Portal and current IAM browser session signed out.'}).waitFor({timeout:30000});check([41],'logout clears local portal session',(await api(normal2,'/api/me')).status===401);await normal2.goto(portal+'/login');await normal2.locator('input[type=password]').waitFor({state:'visible',timeout:45000});check([41],'native IAM browser session also ended',new URL(normal2.url()).hostname==='auth.portal.test');
 await op.route('**/api/logout',r=>new URL(r.request().url()).hostname==='auth.portal.test'?r.abort():r.continue());await op.getByRole('button',{name:'Sign out',exact:true}).click();await op.getByRole('status').filter({hasText:'IAM sign-out has not been confirmed.'}).waitFor({timeout:30000});check([41],'IAM failure is unconfirmed after local signout',(await api(op,'/api/me')).status===401);await op.unroute('**/api/logout');await op.getByRole('button',{name:'Retry IAM sign-out'}).click();await op.getByRole('status').filter({hasText:'Portal and current IAM browser session signed out.'}).waitFor();check([41],'retry confirms native IAM signout',true);
 report.stage='complete';
 }
} catch(error){report.error=error.message.split('\n')[0];}
finally {
 for(const c of contexts)await c.close();await browser.close();
 let json=JSON.stringify(report,null,2);for(const secret of secrets)if(secret&&json.includes(secret))throw new Error('secret in portal report');
 fs.writeFileSync(root+'/evidence/'+(process.env.PORTAL_TEST_MODE==='boundary'?'portal-boundary.json':process.env.PORTAL_TEST_MODE==='faults'?'portal-faults.json':'portal-browser.json'),json+'\n');
 console.log(JSON.stringify({stage:report.stage,passed:report.checks.filter(c=>c.pass).length,failed:report.checks.filter(c=>!c.pass).map(c=>c.name),error:report.error}));
 if(report.stage!=='complete')process.exitCode=1;
}
