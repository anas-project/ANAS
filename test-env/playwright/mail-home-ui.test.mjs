// TEST_CASES: MAILMOD-T-018
// Candidate Go UI with controlled APIs; native Go/DMS/Roundcube is separate.
import {after,before,test} from 'node:test';
import {readFile} from 'node:fs/promises';
import {chromium,expect} from '@playwright/test';
let browser;
before(async()=>{browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}:{})});});
after(async()=>{await browser?.close();});
const origin='https://mail.example.test',root=new URL('../../modules/mail/service/ui/',import.meta.url);
function view(language='en'){return {language,public_delivery_status:'unverified',display_name:'<img src=x onerror=alert(1)>',mail:'alice@example.test',portal_url:'https://portal.example.test',address_management:true,mailboxes:[{id:'main',kind:'primary',address:'alice@example.test',addresses:['alice@example.test','alias@example.test'],status:'ready'},{id:'work',kind:'additional',address:'work@example.test',addresses:['work@example.test'],status:'pending'},{id:'old',kind:'additional',address:'old@example.test',addresses:['old@example.test'],status:'disabled'}]};}
async function fixture(language='en',api){
 const context=await browser.newContext(),page=await context.newPage();
 await context.route('**/*',async route=>{
  const request=route.request(),url=new URL(request.url());
  const json=(body,status=200)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(body)});
  if(url.pathname==='/')return route.fulfill({contentType:'text/html',body:await readFile(new URL('index.html',root),'utf8')});
  if(url.pathname.startsWith('/assets/'))return route.fulfill({contentType:url.pathname.endsWith('.js')?'text/javascript':'text/css',body:await readFile(new URL(url.pathname.slice(8),root),'utf8')});
  if(url.pathname==='/api/mailboxes')return api?api(route,json):json(view(language));
  if(url.pathname==='/api/session/logout')return json({local:'revoked',iam:'unavailable'});
  return json({error:'not_found'},404);
 });
 await page.goto(origin+'/');return {context,page};
}
for(const [language,title] of Object.entries({'en':'Your mailboxes','zh-CN':'我的邮箱','zh-TW':'我的信箱','de':'Ihre Postfächer','es':'Tus buzones','fr':'Vos boîtes mail','ja':'自分のメールボックス','ko':'내 메일함'}))test(`mail home in ${language} keeps unavailable boxes closed`,async()=>{
 const {context,page}=await fixture(language);try{
  await expect(page.getByRole('heading',{level:1})).toHaveText(title);
  await expect(page.locator('.mailbox')).toHaveCount(3);
  await expect(page.locator('#portal')).toHaveAttribute('href','https://portal.example.test/#/account/profile');
  await expect(page.locator('#delivery-notice')).toBeVisible();
  await expect(page.locator('#delivery-notice')).not.toBeEmpty();
  await expect(page.locator('.mailbox').nth(0).getByRole('button')).toBeEnabled();
  for(const n of [1,2])await expect(page.locator('.mailbox').nth(n).getByRole('button')).toBeDisabled();
  await expect(page.locator('#person')).toHaveText('<img src=x onerror=alert(1)>');
  await expect(page.locator('#person img')).toHaveCount(0);
  const stored=await page.evaluate(()=>JSON.stringify({...localStorage,...sessionStorage}));if(stored!=='{}')throw new Error('Mail data persisted in browser');
 }finally{await context.close();}
});
test('opening a mailbox sends only its ID to Go then POSTs the signed start',async()=>{
 const {context,page}=await fixture();try{
  let start,posted;
  await context.route(origin+'/api/webmail/start',async route=>{start=route.request().postData();await route.fulfill({contentType:'application/json',body:JSON.stringify({post_to:'https://webmail.example.test/?_task=login&_action=plugin.anas_begin',fields:{mailbox_id:'main',start_nonce:'browser-challenge',issued_at:'1234567890',signature:'signed-start'}})});});
  await context.route('https://webmail.example.test/**',async route=>{posted={method:route.request().method(),body:route.request().postData(),url:route.request().url()};await route.fulfill({contentType:'text/html',body:'Mailbox handoff'});});
  await page.locator('.mailbox').first().getByRole('button').click();await expect(page).toHaveURL(/webmail\.example\.test/);
  if(start!=='mailbox_id=main'||posted.method!=='POST'||posted.url.includes('signed-start')||!posted.body.includes('signature=signed-start'))throw new Error('Handoff authority or transport changed');
 }finally{await context.close();}
});
test('an unavailable directory shows retry and no stale mailbox controls',async()=>{
 let fail=true;const {context,page}=await fixture('en',(route,json)=>json(fail?{error:'directory-unavailable'}:view(),fail?503:200));try{
  await expect(page.locator('#retry')).toBeVisible();await expect(page.locator('.mailbox')).toHaveCount(0);
  fail=false;await page.locator('#retry').click();await expect(page.locator('.mailbox')).toHaveCount(3);
 }finally{await context.close();}
});
test('local logout clears displayed personnel and mailbox data',async()=>{
 const {context,page}=await fixture();try{
  await expect(page.locator('.mailbox')).toHaveCount(3);await page.locator('#logout').click();
  await expect(page.locator('.mailbox')).toHaveCount(0);await expect(page.locator('#person')).toBeEmpty();await expect(page.locator('#portal')).toBeHidden();
  await expect(page.locator('#delivery-notice')).toBeHidden();
  await expect(page.locator('#status')).toContainText('Local mail session ended');
 }finally{await context.close();}
});
test('local mailbox availability does not imply public delivery works',async()=>{
 const {context,page}=await fixture('zh-CN');try{
  await expect(page.locator('.mailbox').first().locator('.state')).toHaveText('本地邮箱可用');
  await expect(page.locator('#delivery-notice')).toHaveText('无法读取发信服务状态。 公网收发尚未通过验证，请联系管理员。');
  await expect(page.locator('#setup')).toBeHidden();
 }finally{await context.close();}
});
test('confirmed missing outbound route is distinct from an unknown route',async()=>{
 const {context,page}=await fixture('zh-CN',(route,json)=>json({...view('zh-CN'),outbound_status:'not-configured'}));try{
  await expect(page.locator('#delivery-notice')).toContainText('外部发信服务未配置');
  await expect(page.locator('#delivery-notice')).toContainText('公网收发尚未通过验证');
 }finally{await context.close();}
});
test('central logout navigates only after local withdrawal response',async()=>{
 const {context,page}=await fixture();try{
  await context.route(origin+'/api/session/logout',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({local:'revoked',iam_redirect:'https://iam.example.test/logout?state=one-time-state'})}));
  await context.route('https://iam.example.test/**',route=>route.fulfill({contentType:'text/html',body:'Central sign-out'}));
  await expect(page.locator('.mailbox')).toHaveCount(3);await page.locator('#logout').click();await expect(page).toHaveURL('https://iam.example.test/logout?state=one-time-state');
 }finally{await context.close();}
});
test('a delayed mailbox response cannot restore data after logout',async()=>{
 let release;const pending=new Promise(resolve=>{release=resolve});
 const {context,page}=await fixture('en',async(route,json)=>{await pending;return json(view());});
 try{
  await page.locator('#logout').click();await expect(page.locator('#status')).toContainText('Local mail session ended');release();
  await page.waitForTimeout(100);await expect(page.locator('.mailbox')).toHaveCount(0);await expect(page.locator('#person')).toBeEmpty();
 }finally{release();await context.close();}
});
