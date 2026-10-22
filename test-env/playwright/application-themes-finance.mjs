// TEST_CASES: APPTHEME-T-006
// Finance-only, same-host native OIDC fixture. Never transfers directory secrets.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
const {chromium,expect}=await import(process.env.PORTAL_PLAYWRIGHT_MODULE||'@playwright/test');
const root=process.env.PORTAL_THEME_ROOT;
if(!root||!/^\/home\/whl\/anas-portal-themes-[0-9]{8}$/.test(root))throw Error('owned same-host theme fixture required');
const fixture=JSON.parse(fs.readFileSync(path.join(root,'private/browser.private.json'),'utf8'));
if(!/^pua[0-9a-f]{8}$/.test(fixture.prefix)||new URL(fixture.origin).hostname!=='portal.finance.hlong.wang')throw Error('owned finance fixture required');
const phase=process.env.PORTAL_THEME_PHASE||'before-restart';if(!['before-restart','after-restart'].includes(phase))throw Error('invalid phase');
const report={baseline:fixture.baseline,phase,checks:[],started_at:new Date().toISOString()};
const reportFile=path.join(root,'evidence/themes-'+phase+'.json');
function check(ids,name,pass){report.checks.push({requirements:ids.map(n=>'APPTHEME-R-'+String(n).padStart(3,'0')),name,pass:!!pass});fs.writeFileSync(reportFile,JSON.stringify(report,null,2));if(!pass)throw Error(name);}
const hash=body=>crypto.createHash('sha256').update(body).digest('hex');
const browser=await chromium.launch({headless:true,executablePath:fixture.executable,args:['--no-sandbox','--no-proxy-server','--host-resolver-rules=MAP *.finance.hlong.wang '+fixture.entry_ip+',EXCLUDE localhost']});const contexts=[];
async function login(alias){const context=await browser.newContext({ignoreHTTPSErrors:true,locale:'en',viewport:{width:1280,height:1000}});contexts.push(context);const p=await context.newPage();await p.goto(fixture.origin,{waitUntil:'domcontentloaded'});await p.getByRole('link',{name:'Sign in',exact:true}).click();await p.locator('.login-username-input input,input.login-username-input').fill(fixture.users[alias].username);await p.locator('input[type=password]').fill(fixture.passwords[alias]);await p.locator('button.login-button').click({noWaitAfter:true});await p.waitForURL(u=>u.hostname==='portal.finance.hlong.wang'&&u.pathname==='/',{timeout:60000});await p.locator('body[data-page=home]').waitFor();return p;}
async function api(p,url,body,csrf=true){return p.evaluate(async({url,body,csrf})=>{const b=await fetch('/api/bootstrap',{cache:'no-store'}).then(r=>r.json());const r=await fetch(url,{method:body===undefined?'GET':'POST',cache:'no-store',headers:{'Content-Type':'application/json',...(csrf?{'X-CSRF-Token':b.csrf}:{})},body:body===undefined?undefined:JSON.stringify(body)});return {status:r.status,data:await r.json()};},{url,body,csrf});}
async function imageRequest(p,url){const tab=await p.context().newPage();try{const response=await tab.goto(url,{waitUntil:'load',timeout:30000});const status=response.status(),body=await response.body();return {status:()=>status,body:async()=>body};}finally{await tab.close();}}
try{
const admin=await login('manager');let r=await api(admin,'/api/themes');check([3,11],'global default and two builtin themes present',r.status===200&&r.data.default===fixture.global_theme&&['original','soft'].every(id=>r.data.themes.some(t=>t.id===id)));
const exported=await api(admin,'/api/themes/soft/export');check([3],'administrator exports portable builtin package',exported.status===200);const custom=exported.data;custom.id=fixture.theme_id;custom.name={en:'Finance theme acceptance'};
if(phase==='before-restart'){
 r=await api(admin,'/api/themes',custom,false);check([4],'missing CSRF cannot import',r.status===403);
 r=await api(admin,'/api/themes',custom);check([4,7,13],'current ANAS admin imports without activation',r.status===200);
 r=await api(admin,'/api/me');check([13],'import leaves account choice unchanged',!r.data.preferences.icon_theme);
 await admin.goto(fixture.origin+'/#/account/profile');await admin.reload({waitUntil:'domcontentloaded'});await admin.locator('[name=icon_theme]').selectOption(fixture.theme_id);await expect.poll(async()=>((await api(admin,'/api/me')).data.preferences.icon_theme)).toBe(fixture.theme_id);check([8,14],'real page saves personal icon choice',true);
 await admin.screenshot({path:path.join(root,'evidence/theme-settings.png'),fullPage:true});
}
await admin.context().close();
const other=await login('manager');r=await api(other,'/api/me');check([7,8],'fresh browser loads persisted icon choice',r.status===200&&r.data.preferences.icon_theme===fixture.theme_id);
r=await api(other,'/api/themes');check([7],'imported theme survives process phase',r.status===200&&r.data.themes.some(t=>t.id===fixture.theme_id));
const ordinary=await login('normal');r=await api(ordinary,'/api/themes',custom);check([4],'ordinary user cannot import',r.status===403);r=await api(ordinary,'/api/me');check([8,13],'other account preference is unchanged',!r.data.preferences.icon_theme);
const anonymous=await browser.newContext({ignoreHTTPSErrors:true});contexts.push(anonymous);const anonymousPage=await anonymous.newPage();await anonymousPage.goto(fixture.origin,{waitUntil:'domcontentloaded'});r=await api(anonymousPage,'/api/themes',custom,false);check([4],'anonymous import is refused',[401,403].includes(r.status));
r=await api(ordinary,'/api/themes');check([10],'ordinary theme list contains no icon inventory',r.status===200&&!('icons' in r.data)&&r.data.themes.every(t=>!('icons' in t)&&!('sources' in t)));
const apps=(await api(ordinary,'/api/apps')).data.groups.flatMap(g=>g.categories.flatMap(c=>c.applications));const app=apps.find(a=>a.name===fixture.visible_application);check([10,11],'native filtered catalog contains fixture-visible app',!!app);
const icon=await imageRequest(ordinary,fixture.origin+app.icons['512'].path+'&theme='+encodeURIComponent(fixture.global_theme));const native=await imageRequest(ordinary,fixture.iam_origin+'/files/anas-catalog/'+fixture.native_icon_path);check([11],'Casdoor and portal use identical frozen global pixels',icon.status()===200&&native.status()===200&&hash(await icon.body())===hash(await native.body()));
const personal=await imageRequest(other,fixture.origin+app.icons['512'].path+'&theme='+encodeURIComponent(fixture.theme_id));check([8,11],'personal theme does not replace Casdoor global icon',personal.status()===200&&hash(await personal.body())!==hash(await native.body()));
const hidden=await imageRequest(ordinary,fixture.origin+'/api/assets/'+encodeURIComponent(fixture.hidden_application)+'/128?theme='+fixture.theme_id);check([10],'hidden application artwork remains denied',hidden.status()===404);
if(phase==='after-restart'){check([7],'damaged theme excluded and reported',r.status===200&&r.data.invalid_themes>=1&&!r.data.themes.some(t=>t.id==='broken-acceptance'));}
report.completed_at=new Date().toISOString();report.passed=true;
}catch(error){report.passed=false;report.error='Acceptance failed; inspect named check and private diagnostic on host.';process.exitCode=1;}finally{for(const c of contexts)await c.close().catch(()=>{});await browser.close();fs.writeFileSync(reportFile,JSON.stringify(report,null,2));console.log(JSON.stringify({phase,checks:report.checks.length,passed:report.passed}));}
