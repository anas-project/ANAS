import {after,before,test} from 'node:test';
import {readFile} from 'node:fs/promises';
import {chromium,expect} from '@playwright/test';
let browser;
before(async()=>{browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}:{})});});
after(async()=>{await browser?.close();});
const origin='https://portal.example.test',directory=new URL('../../modules/user_portal/portal/ui/',import.meta.url);
async function fixture(result='required',start='/',authenticated=false){
 const context=await browser.newContext(),page=await context.newPage();let checks=0,expired=false;
 await context.route('**/*',async route=>{
  const url=new URL(route.request().url()),path=url.pathname;
  const json=data=>route.fulfill({contentType:'application/json',body:JSON.stringify(data)});
  if(path==='/'||path==='/signed-out')return route.fulfill({contentType:'text/html',body:await readFile(new URL('index.html',directory),'utf8')});
  if(path.startsWith('/static/'))return route.fulfill({contentType:path.endsWith('.js')?'text/javascript':path.endsWith('.svg')?'image/svg+xml':'text/css',body:await readFile(new URL(path.slice(8),directory),'utf8')});
  if(path==='/api/bootstrap')return json({language:'en',authenticated,csrf:'fixture'});
  if(path==='/login'){
   if(url.searchParams.get('check')!=='1')throw Error('Automatic login lost check flag');checks++;
   if(result==='success'){authenticated=true;return route.fulfill({status:303,headers:{location:origin+'/'} ,body:''});}
   return route.fulfill({status:303,headers:{location:origin+'/?iam_check='+result},body:''});
  }
  if(path==='/api/me'&&expired){expired=false;return route.fulfill({status:401,contentType:'application/json',body:JSON.stringify({error:'authentication_required'})});}
  if(path==='/api/me')return json({person:{anchor:'alice',username:'alice',display_name:'Alice',groups:[]},csrf:'fixture',actions:[],preferences:{}});
  if(path==='/api/apps')return json({groups:[]});
  if(path==='/api/themes')return json({themes:[]});
  return route.fulfill({status:404,body:'{}'});
 });
 await page.goto(origin+start);return {context,page,checks:()=>checks,expire:()=>{authenticated=false;expired=true;}};
}
test('anonymous entry checks IAM once and returns to a usable login page',async()=>{const f=await fixture();try{await expect(f.page.locator('body[data-page=login]')).toHaveCount(1);await expect(f.page.getByRole('link',{name:'Sign in',exact:true})).toBeVisible();await expect(f.page).toHaveURL(origin+'/');if(f.checks()!==1)throw Error('Missing check or redirect loop');}finally{await f.context.close();}});
test('successful IAM check enters the portal automatically',async()=>{const f=await fixture('success');try{await expect(f.page.locator('body[data-page=home]')).toHaveCount(1);if(f.checks()!==1)throw Error('Missing automatic login');}finally{await f.context.close();}});
test('failed IAM check stays on login and supports explicit retry',async()=>{const f=await fixture('failed');try{const retry=f.page.getByRole('button',{name:'Retry sign-in check'});await expect(retry).toBeVisible();if(f.checks()!==1)throw Error('Failed check loop');await retry.click();await expect(retry).toBeVisible();if(f.checks()!==2)throw Error('Explicit retry missing');}finally{await f.context.close();}});
for(const [start,authenticated]of [['/signed-out',false],['/#/recover',false],['/',true]])test(`no IAM check for ${start} authenticated=${authenticated}`,async()=>{const f=await fixture('required',start,authenticated);try{await expect(f.page.locator('body[data-page]')).toHaveCount(1);await f.page.waitForLoadState('networkidle');if(f.checks())throw Error('Protected path triggered login');}finally{await f.context.close();}});

test('session lost during portal navigation checks IAM and restores the requested route',async()=>{const f=await fixture('success','/',true);try{await expect(f.page.locator('body[data-page=home]')).toHaveCount(1);f.expire();await f.page.evaluate(()=>{location.hash='#/account/profile';});await expect(f.page).toHaveURL(origin+'/#/account/profile');await expect(f.page.locator('body[data-page=account-profile]')).toHaveCount(1);if(f.checks()!==1)throw Error('Expired portal session did not trigger exactly one IAM check');}finally{await f.context.close();}});
