// TEST_CASES: PORTAL-T-006
// Production UI with isolated mock APIs; no IAM credentials or real LDAP writes.
import {after, before, test} from 'node:test';
import {readFile} from 'node:fs/promises';
import {chromium, expect} from '@playwright/test';

let browser;
before(async()=>{browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}:{})});});
after(async()=>{await browser?.close();});
const origin='https://portal.example.test';
const directory=new URL('../../modules/user_portal/portal/ui/',import.meta.url);
async function fixture(admin=false,delayRefresh=false,creation=false) {
  const context=await browser.newContext(),page=await context.newPage();
  let saved=false,releaseRefresh,refreshStarted;
  const waiting=new Promise(resolve=>{refreshStarted=resolve;});
  const gate=new Promise(resolve=>{releaseRefresh=resolve;});
  const person={anchor:'actor',username:'alice',given_name:'Alice',surname:'Wang',display_name:'Alice Wang',email:'',groups:[],password_state_known:true};
  let creationWrites=0;
  const me={person,mail_domain:'finance.hlong.wang',csrf:'fixture-csrf',actions:admin?['users.read',...(creation?['user.create']:[])]:[],administration:admin,profile_editable:true,profile_version:'v1'};
  await context.route('**/*',async route=>{
    const request=route.request(),url=new URL(request.url()),path=url.pathname;
    const json=(data,status=200)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)});
    if(path==='/')return route.fulfill({contentType:'text/html',body:await readFile(new URL('index.html',directory),'utf8')});
    if(path.startsWith('/static/'))return route.fulfill({contentType:path.endsWith('.js')?'text/javascript':path.endsWith('.svg')?'image/svg+xml':'text/css',body:await readFile(new URL(path.slice(8),directory),'utf8')});
    if(path==='/api/bootstrap')return json({language:url.searchParams.get('lang')==='zh-CN'?'zh-CN':'en',authenticated:true,csrf:me.csrf});
    if(path==='/api/apps')return json({groups:[]});
    if(path==='/api/me/preferences')return json({preferences:request.postDataJSON()});
    if(path==='/api/me/profile'||path==='/api/administration/execute'){
      saved=true;return json({directory:'saved',audit:'saved',request_id:'saved-write-id'});
    }
    if(path==='/api/logout'||path==='/api/logout/complete')return json({iam_endpoint:origin+'/mock-iam-logout'});
    if(path==='/mock-iam-logout')return json({status:'ok'});
    if(path==='/api/get-account')return json({status:'error',msg:'Please login first'});
    if(path==='/api/users'){if(request.method()==='POST'){creationWrites++;return json({directory:'saved',audit:'saved',request_id:'created-write-id',target:'created-anchor',temporary_password:'OneTime-Fixture-Only9!'});}return json({users:[person],count:1});}
    if(path==='/api/groups')return json({groups:[]});
    if(path==='/api/me'||path==='/api/users/actor'){
      if(saved){
        refreshStarted();
        if(delayRefresh){await gate;return json({...me,profile_version:'v2'});}
        return json({error:'identity_unavailable'},503);
      }
      return json(path==='/api/me'?me:{...me,actions:['profile.update'],requires_domain:false});
    }
    return route.abort();
  });
  await page.goto(origin);
  await expect(page.locator('body[data-page=home]')).toHaveCount(1);
  if(admin){
    await page.getByRole('link',{name:'Manage',exact:true}).click();
    await page.getByRole('button',{name:'Details',exact:true}).click();
  }else await page.getByRole('link',{name:'My account',exact:true}).click();
  await page.getByLabel('Given name',{exact:true}).fill('Alicia');
  return {context,page,waiting,releaseRefresh,creationWrites:()=>creationWrites};
}

for(const admin of [false,true])test(`${admin?'administrator':'personal'} profile keeps the confirmed write result when refresh fails`,async()=>{
  const {context,page}=await fixture(admin);
  try{
    await page.getByRole('button',{name:'Save',exact:true}).click();
    if(admin)await page.getByRole('dialog').getByRole('button',{name:'Confirm operation',exact:true}).click();
    const result=page.locator('form').filter({has:page.getByLabel('Given name',{exact:true})}).locator('.form-result');
    await expect(result).toContainText('Directory saved.');
    await expect(result).toContainText('saved-write-id');
    await expect(result.getByRole('alert')).toContainText('Your information was saved, but the updated view could not be loaded.');
  }finally{await context.close();}
});

test('a late profile refresh cannot restore the browser account after sign-out',async()=>{
  const {context,page,waiting,releaseRefresh}=await fixture(false,true);
  try{
    await page.getByRole('button',{name:'Save',exact:true}).click();await waiting;
    await page.getByRole('button',{name:'Sign out',exact:true}).click();
    await expect(page.getByText('This browser has signed out of IAM and the connected applications.',{exact:true})).toBeVisible();
    const refreshed=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/me');
    releaseRefresh();await (await refreshed).finished();
    // Let the fetch/json handler finish before the language control reads me.
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    await expect(page.locator('.bottom-nav')).toHaveCount(0);
    await page.locator('#language').selectOption('zh-CN');
    await expect(page.getByRole('heading',{name:'退出登录',exact:true})).toBeVisible();
    await expect(page.getByRole('link',{name:'返回主页',exact:true})).toBeVisible();
    await expect(page.locator('#logout')).toBeHidden();
  }finally{releaseRefresh();await context.close();}
});


test('creation keeps its one-time password usable and disables repeat submission',async()=>{
  const {context,page,creationWrites}=await fixture(true,false,true);
  try{
    await page.getByRole('link',{name:'Manage',exact:true}).click();
    await page.getByRole('button',{name:'New user',exact:true}).click();
    const form=page.locator('form').filter({has:page.locator('input[name=username]')});
    await form.getByLabel('Username',{exact:true}).fill('New.Person');
    await expect(form.locator('[data-generated-email]')).toHaveText('new.person@finance.hlong.wang');
    await expect(form.locator('input[name=mail],input[name=email]')).toHaveCount(0);
    await form.getByLabel('Display name',{exact:true}).fill('New Person');
    await form.getByRole('button',{name:'New user',exact:true}).click();
    await expect(page.getByRole('dialog').locator('[name=reason],textarea')).toHaveCount(0);
    await page.getByRole('dialog').getByRole('button',{name:'Confirm operation',exact:true}).click();
    const result=form.locator('.form-result');
    await expect(result.locator('input[readonly]')).toHaveValue('OneTime-Fixture-Only9!');
    await expect(result.locator('input[readonly]')).toBeEnabled();
    await expect(form.getByRole('button',{name:'New user',exact:true})).toBeDisabled();
    await expect(result.getByRole('button',{name:'Details',exact:true})).toBeVisible();
    await expect(result).toContainText('created-write-id');
    if(creationWrites()!==1)throw new Error('creation repeated');
  }finally{await context.close();}
});

// TEST_CASES: PORTAL-T-008
for (const language of ['en','zh-CN']) test(`domain ownership confirmation and partial result in ${language}`,async()=>{
  const {context,page}=await fixture(true);
  try {
    await page.locator('#language').selectOption(language);
    await page.route('**/api/administration/prepare?*',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({proof:'once',action:'groups.member.add',target:'person',group:'Domain Admins',ownership_transfer:true})}));
    await page.route('**/api/administration/execute?*',route=>route.fulfill({status:409,contentType:'application/json',body:JSON.stringify({directory:'denied',ownership:'saved',membership:'denied',audit:'saved',request_id:'partial-id'})}));
    const operation=page.evaluate(async()=>{const result=await executeAdmin({action:'groups.member.add',target:'person',group:'domain-group'},true);renderResult(result);});
    const dialog=page.getByRole('dialog');
    await expect(dialog).toContainText(language==='en'?'Membership is added only after the owner is verified.':'回读确认后再加组');
    await dialog.locator('input[type=password]').fill('Mock-Only-Personal-Password');
    await dialog.getByRole('button',{name:language==='en'?'Confirm operation':'确认执行',exact:true}).click();
    await operation;
    const result=page.locator('#content > .result');
    await expect(result).toContainText(language==='en'?'Object ownership: Directory saved.':'对象所有权: 目录已保存。');
    await expect(result).toContainText(language==='en'?'Group membership: This operation is outside your permissions.':'组成员关系: 这项操作不在你的权限范围内。');
    await expect(result).toContainText(language==='en'?'The owner was not reverted.':'未回退所有者');
    await expect(page.locator('dialog')).toHaveCount(0);
    await page.route('**/api/audit?*',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({records:[{time:new Date().toISOString(),action:'groups.member.add',target:'person',directory:'denied',ownership:'saved',membership:'denied'}]})}));
    await page.evaluate(()=>{location.hash='#/account/log';});
    await expect(page.locator('#content table')).toContainText(language==='en'?'Object ownership: Directory saved.':'对象所有权: 目录已保存。');
    await expect(page.locator('#content table')).toContainText(language==='en'?'Group membership: Denied':'组成员关系: 拒绝');
  } finally {await context.close();}
});

// TEST_CASES: PORTAL-T-009
for(const language of ['en','zh-CN'])test(`group placement, direct ordinary add and page confirmation in ${language}`,async()=>{
 const {context,page}=await fixture(true);
 const writes=[];
 try{
  page.on('dialog',()=>{throw new Error('Native browser dialog must never open');});
  const row=(name,managed,description)=>({group:{name,reference:name,anchor:name,display_name:name,description,managed,category:managed?'applications':'system',domain_risk:!managed},can_add:true,direct:false,requires_domain:!managed});
  await page.route('**/api/groups?*',route=>{
   const advanced=new URL(route.request().url()).searchParams.get('advanced')==='true';
   return route.fulfill({contentType:'application/json',body:JSON.stringify({groups:[row('APP_cloud',true,'Use cloud files <script>unsafe()</script>'),...(advanced?[row('System access',false,'Privileged system access')]:[])]})});
  });
  await page.route('**/api/administration/execute?*',async route=>{writes.push(route.request().postDataJSON());await route.fulfill({contentType:'application/json',body:JSON.stringify({directory:'saved',audit:'saved'})});});
  await page.route('**/api/administration/prepare?*',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({proof:'bound-proof',action:'groups.member.add',target:'alice',group:'System access',ownership_transfer:true})}));
  await page.locator('#language').selectOption(language);
  const details=page.locator('details.advanced');
  await expect(details).not.toHaveAttribute('open','');
  await expect(page.locator('#content').getByText('System access',{exact:true})).toHaveCount(0);
  await expect(page.getByText('Use cloud files <script>unsafe()</script>',{exact:true})).toBeVisible();
  const ordinary=page.getByRole('row').filter({has:page.getByText('APP_cloud',{exact:true})});
  await ordinary.getByRole('button',{name:language==='en'?'Add member':'加入成员',exact:true}).click();
  await expect.poll(()=>writes.length).toBe(1);if(Object.hasOwn(writes[0],'reason'))throw new Error('ordinary add still sends a reason');
  await expect(page.locator('dialog')).toHaveCount(0);
  await details.locator('summary').click();
  const system=details.getByRole('row').filter({has:page.getByText('System access',{exact:true})});
  await expect(system).toBeVisible();
  if(!await details.evaluate(node=>node===node.parentElement.lastElementChild))throw new Error('advanced groups must be at the bottom');
  await system.getByRole('button',{name:language==='en'?'Add member':'加入成员',exact:true}).click();
  const dialog=page.getByRole('dialog');await expect(dialog).toContainText('System access');
  await dialog.getByRole('button',{name:language==='en'?'Cancel':'取消',exact:true}).click();
  if(writes.length!==1)throw new Error('cancel wrote a group');
  await system.getByRole('button',{name:language==='en'?'Add member':'加入成员',exact:true}).click();
  await expect(dialog.locator('[name=reason],textarea')).toHaveCount(0);
  await dialog.locator('input[type=password]').fill('Mock-Personal-Password');
  await dialog.getByRole('button',{name:language==='en'?'Confirm operation':'确认执行',exact:true}).click();
  await expect.poll(()=>writes.length).toBe(2);if(Object.hasOwn(writes[1],'reason')||writes[1].proof!=='bound-proof')throw new Error('reason retained or native confirmation lost');
  await expect(page.locator('dialog')).toHaveCount(0);
 }finally{await context.close();}
});

test('domain confirmation keeps the prepared request without reasons',async()=>{
 const {context,page}=await fixture(true);let prepares=0,operation;
 try{
  await page.route('**/api/administration/prepare?*',route=>{prepares++;return route.fulfill({contentType:'application/json',body:JSON.stringify({proof:'bound-proof',identity_version:'original',action:'password.reset',target:'bob',group:'',ownership_transfer:false})});});
  await page.route('**/api/administration/execute?*',route=>{operation=route.request().postDataJSON();return route.fulfill({contentType:'application/json',body:JSON.stringify({directory:'saved',audit:'saved'})});});
  const request=page.evaluate(()=>executeAdmin({action:'password.reset',target:'same-anchor'},true));
  const dialog=page.getByRole('dialog');await expect(dialog.locator('[name=reason],textarea')).toHaveCount(0);await dialog.locator('input[type=password]').fill('Mock-Only-Password');
  await dialog.getByRole('button',{name:'Confirm operation',exact:true}).click();await request;
  if(prepares!==1||operation.proof!=='bound-proof'||Object.hasOwn(operation,'reason'))throw new Error('confirmation request changed');
 }finally{await context.close();}
});

// TEST_CASES: PORTAL-T-009
for(const language of ['en','zh-CN'])test(`administrator role add needs page confirmation without repeated password in ${language}`,async()=>{
 const {context,page}=await fixture(true);const writes=[];let prepares=0;
 try{
  page.on('dialog',()=>{throw new Error('Native browser dialog must never open');});
  await page.route('**/api/groups?*',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({groups:[{group:{name:'Admins',reference:'admins',anchor:'admins',display_name:'ANAS administrators',description:'Platform administration',managed:true,category:'roles',admin_role:true,domain_risk:false},can_add:true,direct:false,requires_domain:false,requires_password:false}]})}));
  await page.route('**/api/administration/prepare?*',route=>{prepares++;return route.fulfill({contentType:'application/json',body:JSON.stringify({proof:'role-proof',action:'groups.member.add',target:'alice',group:'Admins',ownership_transfer:false})});});
  await page.route('**/api/administration/execute?*',async route=>{writes.push(route.request().postDataJSON());await route.fulfill({contentType:'application/json',body:JSON.stringify({directory:'saved',audit:'saved'})});});
  await page.locator('#language').selectOption(language);
  const role=page.getByRole('row').filter({has:page.getByText('ANAS administrators',{exact:true})});
  await role.getByRole('button',{name:language==='en'?'Add member':'加入成员',exact:true}).click();
  const dialog=page.getByRole('dialog');
  await expect(dialog.getByRole('heading')).toHaveText(language==='en'?'Confirm administrator role change':'确认管理员角色变更');
  if(writes.length!==0||prepares!==1)throw new Error('administrator role was written before confirmation');
  await expect(dialog.locator('input[type=password]')).toHaveCount(0);
  await dialog.getByRole('button',{name:language==='en'?'Confirm operation':'确认执行',exact:true}).click();
  await expect.poll(()=>writes.length).toBe(1);
  if(writes[0].proof!=='role-proof'||Boolean(writes[0].actor_password))throw new Error('role confirmation was not sent');
 }finally{await context.close();}
});

for(const language of ['en','zh-CN'])for(const failed of [false,true])test(`${language}: sign-out offers a homepage link when IAM logout ${failed?'fails':'succeeds'}`,async()=>{
 const {context,page}=await fixture();
 try{
  await page.locator('#language').selectOption(language);
  if(failed)await page.route('**/mock-iam-logout',route=>route.fulfill({status:503,contentType:'application/json',body:'{}'}));
  await page.getByRole('button',{name:language==='en'?'Sign out':'退出登录',exact:true}).click();
  const home=page.getByRole('link',{name:language==='en'?'Return to homepage':'返回主页',exact:true});
  await expect(home).toBeVisible();await expect(home).toHaveAttribute('href','/');
  if(failed)await expect(page.getByRole('button',{name:language==='en'?'Retry sign-out':'重试退出'})).toBeVisible();
  else await expect(page.getByText(language==='en'?'This browser has signed out of IAM and the connected applications.':'当前浏览器已退出 IAM 和已接入的应用。',{exact:true})).toBeVisible();
 }finally{await context.close();}
});

for(const language of ['en','zh-CN'])test(`${language}: category switch flattens both application groups and persists`,async()=>{
 const {context,page}=await fixture(true);
 const card=name=>({name,display_name:name,summary:'Summary',description:'Description',url:'https://apps.example.test/'+name,icons:{'48':{path:'/static/chevron-down.svg'},'128':{path:'/static/chevron-down.svg'}}});
 const groups=['user','admin'].map(kind=>({id:kind,kind,display_name:kind,categories:[0,1].map(i=>({id:kind+i,display_name:kind+' category '+i,applications:[card(kind+i)]}))}));
 try{
  await page.route('**/api/apps?*',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({groups})}));
  await page.evaluate(()=>{location.hash='#/apps';});
  await page.locator('#language').selectOption(language);
  const toggle=page.getByLabel(language==='en'?'Show categories':'显示分类',{exact:true});
  await expect(page.locator('.category-heading')).toHaveCount(4);
  await toggle.uncheck();
  await expect(page.locator('.category-heading')).toHaveCount(0);
  for(const kind of ['user','admin']){
   const section=page.locator('[data-kind='+kind+']');
   await expect(section.locator('.group-title')).toHaveText(kind);
   await expect(section.locator('.app-card .app-name')).toHaveText([kind+'0',kind+'1']);
   await expect(section.locator('.cards')).toHaveCount(1);
  }
  await page.reload();
  await expect(toggle).not.toBeChecked();
  await expect(page.locator('.category-heading')).toHaveCount(0);
  await toggle.check();
  await expect(page.locator('.category-heading')).toHaveCount(4);
 }finally{await context.close();}
});

for(const adapter of ['immich','nextcloud'])test(`${adapter}: native bridge revokes an old session and keeps credentials off the parent`,async()=>{
 const {context,page}=await fixture();
 const appOrigin='https://'+(adapter==='immich'?'photos':'nc')+'.example.test',nonce='n'.repeat(43);
 let active=true,writes=0,messages=[];
 await page.exposeFunction('recordLogoutMessage',data=>messages.push(data));
 await page.evaluate(()=>window.addEventListener('message',e=>window.recordLogoutMessage(e.data)));
 const target={origin:appOrigin,url:appOrigin+'/.well-known/anas/browser-logout?ticket=fixture',nonce};
 await context.route(appOrigin+'/**',async route=>{
  const p=new URL(route.request().url()).pathname;
  const json=(data,status=200)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)});
  if(p==='/.well-known/anas/browser-logout')return route.fulfill({contentType:'text/html',body:'<script>const config='+JSON.stringify({parent:origin,nonce,adapter})+';'+await readFile(new URL('logout-bridge.js',directory),'utf8')+'</script>'});
  if(p==='/api/users/me'||p==='/ocs/v2.php/cloud/user')return json(active?(adapter==='immich'?{id:'native-user-id'}:{ocs:{meta:{statuscode:200},data:{id:'native-user-id'}}}):{},active?200:401);
  if(p==='/csrftoken')return json({token:'native-csrf-only'});
  if(p==='/api/auth/logout'||p==='/logout'){
   if(adapter==='nextcloud'&&new URL(route.request().url()).searchParams.get('requesttoken')!=='native-csrf-only')throw Error('Missing native CSRF');
   active=false;writes++;return json({successful:true,redirectUri:'https://iam.example.test/logout?id_token_hint=private-native-token'});
  }
  return route.abort();
 });
 await page.route('**/api/logout/complete?*',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({iam_endpoint:origin+'/mock-iam-logout',targets:[target],flow:'fixture-flow'})}));
 try{
  await page.getByRole('button',{name:'Sign out',exact:true}).click();
  await expect(page.getByText('This browser has signed out of IAM and the connected applications.',{exact:true})).toBeVisible();
  if(active||writes!==1)throw Error('Native session was not revoked exactly once');
  if(JSON.stringify(messages).includes('private-native-token')||JSON.stringify(messages).includes('native-user-id'))throw Error('Native credential or identity leaked');
  await page.locator('#language').selectOption('zh-CN');
  await expect(page.getByText('当前浏览器已退出 IAM 和已接入的应用。',{exact:true})).toBeVisible();
 }finally{await context.close();}
});

test('unavailable bridge is partial; same-origin fresh tickets allow retry after local logout and language change',async()=>{
 const {context,page}=await fixture();const appOrigin='https://photos.example.test',nonce='r'.repeat(43);let failed=true,retries=0;
 const data={iam_endpoint:origin+'/mock-iam-logout',flow:'fixture-flow',targets:[{origin:appOrigin,url:appOrigin+'/.well-known/anas/browser-logout?ticket=fixture',nonce}]};
 await context.route(appOrigin+'/**',async route=>{
  if(new URL(route.request().url()).pathname==='/.well-known/anas/browser-logout')return route.fulfill({contentType:'text/html',body:'<script>const config='+JSON.stringify({parent:origin,nonce,adapter:'immich'})+';'+await readFile(new URL('logout-bridge.js',directory),'utf8')+'</script>'});
  return route.fulfill({status:failed?503:401,contentType:'application/json',body:'{}'});
 });
 await context.route('**/api/logout/complete?*',route=>{retries++;if(route.request().headers()['x-csrf-token']!=='fixture-csrf')throw Error('Retry omitted refreshed CSRF');return route.fulfill({contentType:'application/json',body:JSON.stringify(data)});});
 try{
  await page.getByRole('button',{name:'Sign out',exact:true}).click();
  await expect(page.getByRole('alert')).toContainText('Some sign-outs could not be confirmed.');
  await expect(page.getByRole('button',{name:'Retry sign-out'})).toBeVisible();
  await page.locator('#language').selectOption('zh-CN');
  await expect(page.getByRole('button',{name:'重试退出'})).toBeVisible();
  failed=false;await page.getByRole('button',{name:'重试退出'}).click();
  await expect(page.getByText('当前浏览器已退出 IAM 和已接入的应用。',{exact:true})).toBeVisible();
  if(retries!==2)throw Error('Initial completion and retry did not each execute once');
 }finally{await context.close();}
});


test('forged bridge messages cannot report completion and a silent bridge times out',async()=>{
 const {context,page}=await fixture();const appOrigin='https://photos.example.test',nonce='s'.repeat(43);
 await page.clock.install();
 await page.route('**/api/logout/complete?*',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({iam_endpoint:origin+'/mock-iam-logout',flow:'fixture-flow',targets:[{origin:appOrigin,url:appOrigin+'/.well-known/anas/browser-logout?ticket=fixture',nonce}]})}));
 await context.route(appOrigin+'/**',route=>route.fulfill({contentType:'text/html',body:'<title>Silent bridge</title>'}));
 try{
  await page.getByRole('button',{name:'Sign out',exact:true}).click();
  await expect(page.locator('iframe')).toHaveCount(1);
  await page.evaluate(nonce=>window.postMessage({protocol:'anas.browser-logout/v1',type:'result',nonce,status:'signed_out'},location.origin),nonce);
  await expect.poll(()=>page.frames().some(frame=>frame.url().startsWith(appOrigin))).toBe(true);
  const frame=page.frames().find(f=>f.url().startsWith(appOrigin));
  await frame.evaluate(parent=>window.parent.postMessage({protocol:'anas.browser-logout/v1',type:'result',nonce:'wrong-nonce',status:'signed_out'},parent),origin);
  await expect(page.locator('#notice')).toHaveText('Signing out of this browser’s applications…');
  await page.clock.fastForward(71000);
  await expect(page.getByRole('alert')).toContainText('Some sign-outs could not be confirmed.');
  await expect(page.locator('iframe')).toHaveCount(0);
  await expect(page.getByRole('link',{name:'Return to homepage',exact:true})).toBeVisible();
 }finally{await context.close();}
});

for(const mode of ['manual','automatic'])test(`anonymous native callback: ${mode} completion uses the portal CSRF flow`,async()=>{
 const context=await browser.newContext(),page=await context.newPage();let completions=0;
 await context.route('**/*',async route=>{
  const path=new URL(route.request().url()).pathname;
  const json=data=>route.fulfill({contentType:'application/json',body:JSON.stringify(data)});
  if(path==='/signed-out')return route.fulfill({contentType:'text/html',body:(await readFile(new URL('index.html',directory),'utf8')).replace('<head>','<head><meta name="anas-native-logout" content="'+mode+'">')});
  if(path.startsWith('/static/'))return route.fulfill({contentType:path.endsWith('.js')?'text/javascript':'text/css',body:await readFile(new URL(path.slice(8),directory),'utf8')});
  if(path==='/api/bootstrap')return json({language:'en',authenticated:false,csrf:'anonymous-native-csrf'});
  if(path==='/api/logout/complete'){
   if(route.request().headers()['x-csrf-token']!=='anonymous-native-csrf')throw Error('Native callback omitted CSRF');
   completions++;return json({iam_endpoint:origin+'/mock-iam-logout',targets:[],flow:'native-flow'});
  }
  if(path==='/mock-iam-logout')return json({status:'ok'});
  if(path==='/api/get-account')return json({status:'error',msg:'Please login first'});
  return route.abort();
 });
 try{
  await page.goto(origin+'/signed-out?native=1');
  if(mode==='manual'){
   await expect(page.getByRole('button',{name:'Finish signing out of this browser',exact:true})).toBeVisible();
   if(completions!==0)throw Error('A manual callback ran automatically');
   await page.getByRole('button',{name:'Finish signing out of this browser',exact:true}).click();
  }
  await expect(page.getByText('This browser has signed out of IAM and the connected applications.',{exact:true})).toBeVisible();
  if(completions!==1)throw Error('Native callback was repeated');
  await expect(page.getByRole('link',{name:'Return to homepage',exact:true})).toBeVisible();
 }finally{await context.close();}
});

for(const outcome of ['revoked','repeated-csrf','server-csrf','unavailable'])test(`native sign-out session race: ${outcome}`,async()=>{
 const context=await browser.newContext(),page=await context.newPage();let bootstraps=0,accepted=0,iamWrites=0;const tokens=[];
 await context.route('**/*',async route=>{
  const path=new URL(route.request().url()).pathname;
  const json=(data,status=200)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)});
  if(path==='/signed-out')return route.fulfill({contentType:'text/html',body:(await readFile(new URL('index.html',directory),'utf8')).replace('<head>','<head><meta name="anas-native-logout" content="automatic">')});
  if(path.startsWith('/static/'))return route.fulfill({contentType:path.endsWith('.js')?'text/javascript':'text/css',body:await readFile(new URL(path.slice(8),directory),'utf8')});
  if(path==='/api/bootstrap')return json({language:'en',authenticated:++bootstraps<3,csrf:bootstraps<3?'revoked-session-csrf':'fresh-anonymous-csrf'});
  if(path==='/api/logout/complete'){
   tokens.push(route.request().headers()['x-csrf-token']);
   if(outcome==='unavailable')return json({error:'identity_unavailable'},503);
   if(outcome==='server-csrf')return json({error:'csrf_rejected'},500);
   if(outcome==='repeated-csrf'||tokens.length===1)return json({error:'csrf_rejected'},403);
   accepted++;return json({iam_endpoint:origin+'/mock-iam-logout',targets:[]});
  }
  if(path==='/mock-iam-logout'){iamWrites++;return json({status:'ok'});}
  if(path==='/api/get-account')return json({status:'error',msg:'Please login first'});
  return route.abort();
 });
 try{
  await page.goto(origin+'/signed-out?native=1');
  if(outcome==='revoked')await expect(page.getByText('This browser has signed out of IAM and the connected applications.',{exact:true})).toBeVisible();
  else{
   await expect(page.getByRole('alert')).toBeVisible();
   await expect(page.getByRole('button',{name:'Finish signing out of this browser',exact:true})).toBeEnabled();
  }
  const expected=['revoked','repeated-csrf'].includes(outcome)?['revoked-session-csrf','fresh-anonymous-csrf']:['revoked-session-csrf'];
  if(JSON.stringify(tokens)!==JSON.stringify(expected))throw Error('Sign-out did not limit recovery to one refreshed CSRF attempt');
  if(accepted!==(outcome==='revoked'?1:0)||iamWrites!==accepted)throw Error('Rejected completion triggered a sign-out write');
  await expect(page.getByRole('link',{name:'Return to homepage',exact:true})).toBeVisible();
 }finally{await context.close();}
});

for(const authenticated of [true,false])test(`cached sign-out results respect a ${authenticated?'new authenticated':'still anonymous'} portal session`,async()=>{
 const {context,page}=await fixture();
 try{
  await page.route(origin+'/signed-out',async route=>route.fulfill({contentType:'text/html',body:await readFile(new URL('index.html',directory),'utf8')}));
  await page.route(origin+'/api/bootstrap*',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({language:'en',authenticated,csrf:'fixture-csrf'})}));
  await page.evaluate(status=>sessionStorage.setItem('anas.portal.logout',JSON.stringify({results:[{origin:'iam.example.test',status}]})),authenticated?'signed_out':'failed');
  await page.goto(origin+'/signed-out');
  if(authenticated){
   await expect(page.locator('body[data-page=home]')).toHaveCount(1);
   await expect(page.locator('#logout')).toBeVisible();
   await expect(page.getByText('This browser has signed out of IAM and the connected applications.',{exact:true})).toHaveCount(0);
   if(new URL(page.url()).pathname!=='/')throw Error('An authenticated session retained the stale result URL');
  }else{
   await expect(page.getByRole('alert')).toContainText('Some sign-outs could not be confirmed.');
   await expect(page.getByRole('button',{name:'Retry sign-out',exact:true})).toBeVisible();
   await expect(page.getByRole('link',{name:'Return to homepage',exact:true})).toBeVisible();
   await expect(page.locator('#logout')).toBeHidden();
  }
 }finally{await context.close();}
});
