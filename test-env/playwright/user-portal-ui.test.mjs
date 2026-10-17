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
async function fixture(admin=false,delayRefresh=false) {
  const context=await browser.newContext(),page=await context.newPage();
  let saved=false,releaseRefresh,refreshStarted;
  const waiting=new Promise(resolve=>{refreshStarted=resolve;});
  const gate=new Promise(resolve=>{releaseRefresh=resolve;});
  const person={anchor:'actor',username:'alice',given_name:'Alice',surname:'Wang',display_name:'Alice Wang',email:'',groups:[],password_state_known:true};
  const me={person,csrf:'fixture-csrf',actions:admin?['users.read']:[],administration:admin,profile_editable:true,profile_version:'v1'};
  await context.route('**/*',async route=>{
    const request=route.request(),url=new URL(request.url()),path=url.pathname;
    const json=(data,status=200)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)});
    if(path==='/')return route.fulfill({contentType:'text/html',body:await readFile(new URL('index.html',directory),'utf8')});
    if(path.startsWith('/static/'))return route.fulfill({contentType:path.endsWith('.js')?'text/javascript':'text/css',body:await readFile(new URL(path.slice(8),directory),'utf8')});
    if(path==='/api/bootstrap')return json({language:'en',authenticated:true,csrf:me.csrf});
    if(path==='/api/apps')return json({groups:[]});
    if(path==='/api/me/profile'||path==='/api/administration/execute'){
      saved=true;return json({directory:'saved',audit:'saved',request_id:'saved-write-id'});
    }
    if(path==='/api/logout')return json({iam_endpoint:origin+'/mock-iam-logout'});
    if(path==='/mock-iam-logout')return json({status:'ok'});
    if(path==='/api/users')return json({users:[person],count:1});
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
  await expect(page.getByRole('heading',{name:'Your workspace'})).toBeVisible();
  if(admin){
    await page.getByRole('button',{name:'User management',exact:true}).click();
    await page.getByRole('button',{name:'Details',exact:true}).click();
    await page.getByLabel('Reason',{exact:true}).fill('Fixture profile correction');
  }else await page.getByRole('button',{name:'My account',exact:true}).click();
  await page.getByLabel('Given name',{exact:true}).fill('Alicia');
  return {context,page,waiting,releaseRefresh};
}

for(const admin of [false,true])test(`${admin?'administrator':'personal'} profile keeps the confirmed write result when refresh fails`,async()=>{
  const {context,page}=await fixture(admin);
  try{
    await page.getByRole('button',{name:'Save',exact:true}).click();
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
    await expect(page.getByText('Portal and current IAM browser session signed out.',{exact:true})).toBeVisible();
    const refreshed=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/me');
    releaseRefresh();await (await refreshed).finished();
    // Let the fetch/json handler finish before the language control reads me.
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    await expect(page.locator('#navigation')).toBeHidden();
    await page.locator('#language').selectOption('zh-CN');
    await expect(page.getByRole('heading',{name:'欢迎使用 ANAS',exact:true})).toBeVisible();
    await expect(page.locator('#logout')).toBeHidden();
  }finally{releaseRefresh();await context.close();}
});
