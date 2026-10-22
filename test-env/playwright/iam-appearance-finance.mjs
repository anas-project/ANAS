// TEST_CASES: APPTHEME-T-008, PORTALUX-T-006
// Same-host disposable finance accounts. No production credentials in the browser.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
const {chromium,expect}=await import(process.env.PORTAL_PLAYWRIGHT_MODULE||'@playwright/test');
const root=process.env.PORTAL_THEME_ROOT;
if(!/^\/home\/whl\/anas-portal-themes-[0-9]{8}$/.test(root||''))throw Error('owned test directory required');
const f=JSON.parse(fs.readFileSync(path.join(root,'private/browser.private.json'),'utf8'));
if(!/^pua[0-9a-f]{8}$/.test(f.prefix)||new URL(f.origin).hostname!=='portal.finance.hlong.wang')throw Error('owned finance fixture required');
const report={baseline:f.baseline,checks:[],started_at:new Date().toISOString()},out=path.join(root,'evidence/iam-appearance.json');
function check(requirements,name,pass){report.checks.push({requirements,name,pass:!!pass});fs.writeFileSync(out,JSON.stringify(report,null,2));if(!pass)throw Error(name);}
const T15=['APPTHEME-R-015'],T16=['APPTHEME-R-016'],P32=['PORTALUX-R-032'],P33=['PORTALUX-R-033'];
const browser=await chromium.launch({headless:true,executablePath:f.executable,args:['--no-sandbox','--no-proxy-server','--host-resolver-rules=MAP *.finance.hlong.wang '+f.entry_ip+',EXCLUDE localhost']});
const contexts=[];
async function page(scheme='light',width=1280){const c=await browser.newContext({ignoreHTTPSErrors:true,locale:'en',colorScheme:scheme,viewport:{width,height:1000}});contexts.push(c);await c.addInitScript(()=>{localStorage.setItem('anas.portal.language','en');localStorage.setItem('anas.portal.theme','system');localStorage.setItem('language','en');});return c.newPage();}
async function start(p){await p.goto(f.origin,{waitUntil:'domcontentloaded'});await p.getByRole('link',{name:'Sign in',exact:true}).click();await p.locator('input[type=password]').waitFor();}
async function login(p,alias,password=f.passwords[alias]){await p.locator('.login-username-input input,input.login-username-input').fill(f.users[alias].username);await p.locator('input[type=password]').fill(password);await p.locator('button.login-button').click({noWaitAfter:true});try{await p.waitForURL(u=>u.hostname===new URL(f.origin).hostname&&u.pathname==='/',{timeout:60000,waitUntil:'domcontentloaded'});}catch(e){const u=new URL(p.url());let notice=await p.locator('.ant-message,.ant-alert').allTextContents();for(const secret of [...Object.values(f.passwords),f.recovered_password])notice=notice.map(s=>s.replaceAll(secret,'<redacted>'));console.error(JSON.stringify({login_host:u.hostname,login_path:u.pathname,notice}));await p.screenshot({path:path.join(root,'evidence/login-failure.png'),fullPage:true,mask:[p.locator('input')]});throw e;}await p.locator('body[data-page=home]').waitFor();}
async function api(p,url){return p.evaluate(async u=>{const r=await fetch(u,{cache:'no-store'});return {status:r.status,data:await r.json()};},url);}
async function pixels(p,url){return p.evaluate(async u=>{const i=new Image();i.src=u;await i.decode();const c=document.createElement('canvas');c.width=i.naturalWidth;c.height=i.naturalHeight;const x=c.getContext('2d');x.drawImage(i,0,0);return {width:c.width,height:c.height,corner:x.getImageData(0,0,1,1).data[3],visiblePixels:x.getImageData(0,0,c.width,c.height).data.filter((v,n)=>n%4===3&&v>0).length};},url);}
try{
 for(const scheme of ['dark','light']){
  const p=await page(scheme,scheme==='dark'?390:1280);await start(p);
  const mode=scheme==='dark'?'dark':'default';check(T16,scheme+' native query and stored mode',new URL(p.url()).searchParams.get('theme')===mode&&await p.evaluate(()=>JSON.parse(localStorage.getItem('themeAlgorithm'))?.[0])===mode);
  const logo=p.locator('img.panel-logo');await expect(logo).toBeVisible();const size=await logo.evaluate(i=>({natural:i.naturalWidth,width:i.getBoundingClientRect().width,height:i.getBoundingClientRect().height}));check(T15,scheme+' portal login has a sharp square logo',size.natural===512&&size.width===80&&size.height===80);
  check(T16,scheme+' native color panel applied',await p.locator(scheme==='dark'?'.login-panel-dark':'.login-panel').isVisible());
  const recovery=p.locator('a[href="'+f.origin+'/#/recover"]');await expect(recovery).toBeVisible();check(P33,scheme+' native password entry visible',true);
  check(T15,scheme+' login fits viewport',await p.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await p.screenshot({path:path.join(root,'evidence/iam-login-'+scheme+'.png'),fullPage:true});
  if(scheme==='dark'){
   await recovery.click();await expect.poll(()=>p.context().pages().some(q=>q.url()===f.origin+'/#/recover')).toBe(true);
   const destination=p.context().pages().find(q=>q.url()===f.origin+'/#/recover');await destination.locator('body[data-page=recover]').waitFor();check(P33,'native link opens restricted portal recovery',true);
  }
  await p.context().close();
 }
 const manager=await page();await start(manager);await login(manager,'manager');
 const native=await manager.context().newPage();await native.goto(f.iam_origin+'/applications',{waitUntil:'domcontentloaded'});
 for(const app of f.native_applications){
  const result=await api(native,'/api/get-application?id='+encodeURIComponent('admin/'+app.name));const a=result.data.data;
  check(T15,'native logo configured: '+app.name,result.status===200&&a?.logo===app.logo);
  const px=await pixels(native,a.logo);check(T15,'native 512px rounded pixels: '+app.name,px.width===512&&px.height===512&&px.corner===0&&px.visiblePixels>512);
  if(app.name==='app-built-in')check(P33,'local recovery keeps native password flow',!a.forgetUrl||a.forgetUrl==='/forget');
  else if(!app.disableSignin)check(P33,'directory recovery configured: '+app.name,a.forgetUrl===f.origin+'/#/recover');
 }
 await native.screenshot({path:path.join(root,'evidence/iam-applications.png'),fullPage:true});await native.close();
 const apps=await api(manager,'/api/apps');check(T15,'hidden portal branding creates no visible self-link',apps.status===200&&!apps.data.groups.flatMap(g=>g.categories.flatMap(c=>c.applications)).some(a=>a.name==='user-portal'));
 await manager.goto(f.origin+'/#/account/profile');const profile=manager.locator('form').filter({has:manager.locator('[name=given_name]')});await profile.locator('button[type=submit]').click();await expect(profile.locator('.form-result')).toContainText('Saved.');check(P32,'profile shows confirmed result without untracked status',!/(IAM sync|Application sessions|Directory:)/.test(await profile.locator('.form-result').innerText()));
 const normal=await page();await normal.goto(f.origin+'/#/recover');await normal.locator('body[data-page=recover]').waitFor();
 await normal.locator('[name=username]').fill(f.users.normal.username);await normal.locator('[name=old_password]').fill('Wrong-Fixture-Password-123!');await normal.locator('[name=new_password]').fill(f.recovered_password);await normal.locator('[name=confirm_password]').fill(f.recovered_password);await normal.locator('form button[type=submit]').click();await expect(normal.locator('.form-result .error')).toBeVisible();check(P32,'incorrect temporary password displays an in-page failure',true);
 await normal.locator('[name=old_password]').fill(f.passwords.normal);await normal.locator('[name=new_password]').fill(f.recovered_password);await normal.locator('[name=confirm_password]').fill(f.recovered_password);await normal.locator('form button[type=submit]').click();await expect(normal.locator('.form-result')).toContainText('Password changed.',{timeout:60000});check([...P32,...P33],'mandatory password change succeeds with plain success message',!/(IAM sync|Application sessions|Directory:)/.test(await normal.locator('.form-result').innerText()));
 await expect(normal.locator('.form-result a')).toBeVisible();await normal.screenshot({path:path.join(root,'evidence/password-result.png'),fullPage:true});await normal.locator('.form-result a').click();await normal.locator('input[type=password]').waitFor();await login(normal,'normal',f.recovered_password);check(P33,'recovered user signs in through native OIDC with the new password',true);
 const app=(await api(normal,'/api/apps')).data.groups.flatMap(g=>g.categories.flatMap(c=>c.applications)).find(a=>a.name==='nextcloud');
 async function imageBytes(p,url){const t=await p.context().newPage();try{const r=await t.goto(url);return await r.body();}finally{await t.close();}}
 const global=await imageBytes(normal,f.origin+app.icons['512'].path+'&theme=original');const iam=await imageBytes(normal,f.iam_origin+f.native_applications.find(a=>a.name==='app-anas-nextcloud').logo);const sha=x=>crypto.createHash('sha256').update(x).digest('hex');check(['APPTHEME-R-011',...T15],'native and portal global image bytes match at 512px',sha(global)===sha(iam));
 report.passed=true;report.completed_at=new Date().toISOString();
}catch(e){report.passed=false;report.error='See named check and private diagnostic on finance.';console.error(e.stack);process.exitCode=1;}finally{for(const c of contexts)await c.close().catch(()=>{});await browser.close();fs.writeFileSync(out,JSON.stringify(report,null,2));console.log(JSON.stringify({checks:report.checks.length,passed:report.passed}));}
