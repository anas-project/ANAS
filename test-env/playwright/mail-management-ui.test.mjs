// TEST_CASES: MAILMOD-T-014
// Production mail address UI and portal creation UI with mock APIs. Native LDAPS results are separate.
import {after,before,test} from 'node:test';
import {readFile} from 'node:fs/promises';
import {chromium,expect} from '@playwright/test';

let browser;
before(async()=>{browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}:{})});});
after(async()=>{await browser?.close();});
const origin='https://portal.example.test',directory=new URL('../../modules/user_portal/portal/ui/',import.meta.url);
const primary='11111111-1111-4111-8111-111111111111';
function initialView(){return {version:'22222222-2222-4222-8222-222222222222',policy:{domains:['example.test'],max_additional:1,max_aliases:2},mailboxes:[{id:primary,kind:'primary',state:'active',default_address:'alice@example.test'}],addresses:[{address:'alice@example.test',mailbox_id:primary,kind:'primary',state:'active',removable:false},{address:'alias@example.test',mailbox_id:primary,kind:'alias',state:'active',removable:true}],mail_ready:false,management_url:'https://mail.example.test'};}
const mailDirectory=new URL('../../modules/mail/service/ui/',import.meta.url);
async function fixture(language='en',mail,creation,mailAdmin=false){
 const context=await browser.newContext(),page=await context.newPage();
 const person={anchor:'actor',username:'alice',display_name:'Alice',email:'alice@example.test',groups:[],password_state_known:true};
 const me={person,csrf:'mail-csrf',actions:[],administration:false,profile_editable:false,mail_conflict_check:true,mail_domain:'example.test'};
 if(creation){me.administration=true;me.actions=['user.create','users.read'];}
 await context.route('**/*',async route=>{
  const request=route.request(),url=new URL(request.url()),path=url.pathname;
  const json=(body,status=200)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(path==='/api/addresses'&&request.method()==='GET'&&status===200?{view:body,language,can_manage_others:mailAdmin,display_name:url.searchParams.has('owner')?'Bob':'Alice'}:body)});
  if(path==='/addresses')return route.fulfill({contentType:'text/html',body:await readFile(new URL('addresses.html',mailDirectory),'utf8')});
  if(path.startsWith('/assets/'))return route.fulfill({contentType:path.endsWith('.js')?'text/javascript':'text/css',body:await readFile(new URL(path.slice(8),mailDirectory),'utf8')});
  if(path==='/')return route.fulfill({contentType:'text/html',body:await readFile(new URL('index.html',directory),'utf8')});
  if(path.startsWith('/static/'))return route.fulfill({contentType:path.endsWith('.js')?'text/javascript':path.endsWith('.svg')?'image/svg+xml':'text/css',body:await readFile(new URL(path.slice(8),directory),'utf8')});
  if(path==='/api/bootstrap')return json({language,authenticated:true,csrf:me.csrf});
  if(path==='/api/me')return json(me);
  if(path==='/api/apps')return json({groups:[]});
  if(path==='/api/addresses/people')return json({people:[{anchor:'33333333-3333-4333-8333-333333333333',username:'bob',display_name:'Bob'}]});
  if(path==='/api/addresses')return mail?mail(route,json):json(initialView());
  if(path==='/api/users/mail-conflict'||path==='/api/users')return creation?creation(route,json):json({error:'not_found'},404);
  return json({error:'not_found'},404);
 });
 await page.goto(origin+(creation?'/?lang='+language+'#/admin/users/new':'/addresses?lang='+language));
 return {context,page};
}
for(const language of ['zh-CN','zh-TW','en','ja','ko','fr','de','es'])test(`mail address management renders in ${language}`,async()=>{
 const {context,page}=await fixture(language);
 try{
  const panel=page.locator('.mail-management');await expect(panel.locator('h2')).toBeVisible();
  await expect(panel.locator('form')).toHaveCount(2);
  await expect(panel.getByText('alice@example.test',{exact:true})).toHaveCount(2);
  await expect(panel.locator('button')).toHaveCount(3);
  const missing=await page.evaluate(()=>['mailManageTitle','mailDirectoryOnly','mailNewBox','mailNewAlias','mailRemoveAlias','mailRetry','mailRetained','mailOutcomeSaved'].filter(key=>t(key)===key));
  if(missing.length)throw new Error('Missing mail translations: '+missing.join(','));
 }finally{await context.close();}
});
test('unknown create retries the same immutable request after pending refresh',async()=>{
 const view=initialView(),writes=[];
 const {context,page}=await fixture('en',async(route,json)=>{
  if(route.request().method()==='GET')return json(view);
  writes.push({body:route.request().postDataJSON(),method:route.request().method()});
  if(writes.length===1){view.pending=writes[0].body;return json({directory:'unknown',audit:'saved'});}
  delete view.pending;view.mailboxes.push({id:'33333333-3333-4333-8333-333333333333',kind:'additional',state:'active',default_address:'project@example.test'});
  return json({directory:'saved',audit:'saved',mail:{mail_ready:false}});
 });
 try{
  const panel=page.locator('.mail-management'),create=panel.locator('form').filter({has:page.getByRole('button',{name:'Create mailbox',exact:true})});
  await create.locator('input').fill('project@example.test');await create.getByRole('button').click();
  await expect(panel.getByRole('button',{name:'Retry request',exact:true})).toBeVisible();
  await expect(panel.getByRole('button',{name:'Create mailbox',exact:true})).toBeDisabled();
  await panel.getByRole('button',{name:'Retry request',exact:true}).click();
  await expect(panel.getByRole('heading',{name:'project@example.test',exact:true})).toBeVisible();
  if(writes.length!==2||JSON.stringify(writes[0])!==JSON.stringify(writes[1])||writes[0].method!=='POST'||Object.hasOwn(writes[0].body,'owner'))throw new Error('Retry changed operation or identity');
  await expect(page.locator('#notice')).toContainText('Directory saved.');
  const stored=await page.evaluate(()=>JSON.stringify({...localStorage,...sessionStorage}));if(stored.includes('project@example.test'))throw new Error('Address intent persisted in browser storage');
 }finally{await context.close();}
});
test('alias removal uses page confirmation and never offers primary removal',async()=>{
 const view=initialView(),writes=[];
 const {context,page}=await fixture('en',(route,json)=>{
  if(route.request().method()==='GET')return json(view);
  const r=route.request().postDataJSON();writes.push(r);view.addresses[1].state='retained';view.addresses[1].removable=false;return json({directory:'saved',audit:'saved'});
 });
 try{
  page.on('dialog',()=>{throw new Error('Native confirm is not permitted');});
  const panel=page.locator('.mail-management');await expect(panel.getByRole('button',{name:'Remove alias',exact:true})).toHaveCount(1);
  await panel.getByRole('button',{name:'Remove alias',exact:true}).click();
  await page.getByRole('dialog').getByRole('button',{name:'Cancel',exact:true}).click();if(writes.length)throw new Error('Cancel wrote directory');
  await panel.getByRole('button',{name:'Remove alias',exact:true}).click();
  await page.getByRole('dialog').getByRole('button',{name:'Confirm operation',exact:true}).click();
  await expect(panel.getByRole('button',{name:'Remove alias',exact:true})).toHaveCount(0);
  if(writes.length!==1||writes[0].action!=='alias.remove'||writes[0].mailbox_id!==primary||writes[0].address!=='alias@example.test')throw new Error('Wrong removal context');
  await expect(panel.getByText('alice@example.test',{exact:true})).toHaveCount(2);
 }finally{await context.close();}
});
test('expired authentication offers reauthentication without replaying a write',async()=>{
 let writes=0;
 const {context,page}=await fixture('en',(route,json)=>{if(route.request().method()==='GET')return json(initialView());writes++;return json({error:'recent-authentication-required'},403);});
 try{
  const panel=page.locator('.mail-management'),form=panel.locator('form').last();
  await form.locator('input').fill('extra@example.test');await form.getByRole('button').click();
  await expect(page.locator('#reauth')).toBeVisible();await expect(page.locator('#reauth')).toHaveAttribute('href','/reauth');
  if(writes!==1)throw Error('Expired authorization retried a write');
 }finally{await context.close();}
});
test('long mailbox addresses fit a phone viewport',async()=>{
 const view=initialView(),address='a'.repeat(64)+'@example.test';view.mailboxes[0].default_address=address;view.addresses[0].address=address;
 const {context,page}=await fixture('en',(route,json)=>json(view));
 try{
  await page.setViewportSize({width:390,height:844});
  await expect(page.locator('.mail-management h3')).toHaveText(address);
  const size=await page.evaluate(()=>({content:document.documentElement.scrollWidth,viewport:innerWidth}));
  if(size.content>size.viewport)throw new Error('Mail address overflows viewport: '+JSON.stringify(size));
 }finally{await context.close();}
});

test('account address conflict offers authorized maintenance and always rechecks',async()=>{
 let checks=0,writes=0;
 const {context,page}=await fixture('en',undefined,(route,json)=>{
  const u=new URL(route.request().url());
  if(u.pathname==='/api/users'){writes++;return json({directory:'saved'});}
  checks++;return json({address:'alias@example.test',occupied:true,kind:'alias',state:'active',management_url:checks===1?'https://mail.example.test/addresses?owner=owner-anchor':undefined,owner:{anchor:'owner-anchor',username:'owner',display_name:'Original Owner'}});
 });
 try{
  const form=page.locator('body[data-page=admin-new-user] form');
  await form.locator('[name=username]').fill('alias');await form.locator('[name=display_name]').fill('New Person');
  await form.getByRole('button',{name:'New user',exact:true}).click();
  const conflict=page.locator('.mail-address-conflict');await expect(conflict.getByText('alias@example.test',{exact:true})).toBeVisible();
  await expect(conflict).toContainText('Personal alias');await expect(conflict).toContainText('Original Owner');
  const maintain=conflict.getByRole('link',{name:'Manage existing mail address',exact:true});
  await expect(maintain).toHaveAttribute('href','https://mail.example.test/addresses?owner=owner-anchor');await expect(maintain).toHaveAttribute('target','_blank');
  await conflict.getByRole('button',{name:'Choose another username',exact:true}).click();await expect(form.locator('[name=username]')).toBeFocused();
  await conflict.getByRole('button',{name:'Check address again',exact:true}).click();
  await expect(conflict.getByRole('link',{name:'Manage existing mail address',exact:true})).toHaveCount(0);
  if(checks!==2||writes!==0)throw new Error('Conflict view wrote an account or did not reread');
  await form.getByRole('button',{name:'New user',exact:true}).click();
  await expect(conflict).toBeVisible();if(checks!==3||writes!==0)throw new Error('Repeated submission reused stale occupancy');
 }finally{await context.close();}
});
test('unknown address lookup cannot create an account or claim availability',async()=>{
 let writes=0;
 const {context,page}=await fixture('en',undefined,(route,json)=>{
  if(new URL(route.request().url()).pathname==='/api/users'){writes++;return json({directory:'saved'});}
  return json({error:'mail_management_unavailable'},503);
 });
 try{
  const form=page.locator('body[data-page=admin-new-user] form');
  await form.locator('[name=username]').fill('new');await form.locator('[name=display_name]').fill('New Person');
  await form.getByRole('button',{name:'New user',exact:true}).click();
  await expect(form.getByRole('alert')).toContainText('Mailbox management is unavailable');
  if(writes)throw new Error('Unknown lookup reached account creation');
 }finally{await context.close();}
});

test('mail administrators search personnel and keep the displayed owner in writes',async()=>{
 const writes=[];
 const {context,page}=await fixture('en',(route,json)=>{if(route.request().method()==='GET')return json(initialView());writes.push(new URL(route.request().url()).searchParams.get('owner'));return json({directory:'saved',audit:'saved'});},undefined,true);
 try{
  const search=page.locator('#people-search');await expect(search).toBeVisible();await search.locator('input').fill('bob');await search.locator('button').click();
  await page.locator('#people').getByRole('link',{name:'Bob'}).click();await expect(page.locator('#person')).toHaveText('Bob');
  const form=page.locator('.mail-management form').last();await form.locator('input').fill('project@example.test');await form.locator('button').click();
  await expect(page.locator('#notice')).toContainText('Directory saved.');
  if(writes.length!==1||writes[0]!=='33333333-3333-4333-8333-333333333333')throw Error('Administrator wrote the wrong owner');
 }finally{await context.close();}
});
