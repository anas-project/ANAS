// Production portal UI with mock identity and credential APIs. No real secrets.
import {after, before, test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {chromium, expect} from '@playwright/test';

let browser;
before(async()=>{browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}:{})});});
after(async()=>{await browser?.close();});
const origin='https://portal.example.test';
const directory=new URL('../../modules/user_portal/portal/ui/',import.meta.url);

async function fixture({admin=true,available=true,recent=true,hold=false,fail=false,lateReauth=false}={}) {
  const context=await browser.newContext(),page=await context.newPage();
  await context.addInitScript(()=>{
    window.copied=[];
    Object.defineProperty(navigator,'clipboard',{value:{writeText:async value=>{window.copied.push(value);}}});
  });
  const requests=[];
  let release,started;
  const gate=new Promise(resolve=>{release=resolve;}),waiting=new Promise(resolve=>{started=resolve;});
  const person={anchor:'actor',username:'alice',display_name:'Alice',groups:[],password_state_known:true};
  const me={person,csrf:'fixture-csrf',actions:admin?['users.read']:[],administration:admin,preferences:{language:'en',theme:'light',show_categories:true,pinned:[]}};
  await context.route('**/*',async route=>{
    const request=route.request(),url=new URL(request.url()),path=url.pathname;
    const json=(data,status=200)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)});
    if(path==='/')return route.fulfill({contentType:'text/html',body:await readFile(new URL('index.html',directory),'utf8')});
    if(path.startsWith('/static/'))return route.fulfill({contentType:path.endsWith('.js')?'text/javascript':path.endsWith('.svg')?'image/svg+xml':'text/css',body:await readFile(new URL(path.slice(8),directory),'utf8')});
    if(path==='/api/bootstrap')return json({language:'en',authenticated:true,csrf:me.csrf});
    if(path==='/api/me')return json(me);
    if(path==='/api/themes')return json({themes:[],default:'original'});
    if(path==='/api/apps')return json({groups:[{id:'admin',kind:'admin',display_name:'Administration',categories:[{id:'other',display_name:'Other',applications:[{name:'tool',display_name:'Tool',summary:'Backend settings',url:'https://tool.example.test',categories:['other'],credentials_available:available,icons:{}}]}]}]});
    if(path==='/api/apps/tool/credentials'){
      requests.push({path,body:request.postDataJSON(),headers:request.headers()});started();
      if(hold)await gate;
      if(!recent||lateReauth)return json({error:'recent_authentication_required'},403);
      if(fail)return json({error:'credentials_unavailable'},503);
      return json({application:'tool',username:'local-admin',password:'Fixture-Backend-Password9!',url:'https://tool.example.test'});
    }
    if(path==='/api/reauth/password'){
      assert.equal(request.postDataJSON().password,'Actor-Password9!');recent=true;return json({status:'ok'});
    }
    if(path==='/api/me/preferences')return json({preferences:request.postDataJSON()});
    if(path==='/api/logout/complete')return json({iam_endpoint:origin+'/mock-iam-logout'});
    if(path==='/mock-iam-logout')return json({status:'ok'});
    if(path==='/api/get-account')return json({status:'error',msg:'Please login first'});
    return route.abort();
  });
  await page.goto(origin);
  await expect(page.locator('body[data-page=home]')).toHaveCount(1);
  await expect(page.locator('.app-card')).toHaveCount(1);
  return {context,page,requests,waiting,release};
}

test('only an administrator sees a declared credential action',async()=>{
  for(const options of [{admin:false},{available:false},{admin:true,available:true}]){
    const f=await fixture(options);
    try{await expect(f.page.getByRole('button',{name:'Get login details for Tool',exact:true})).toHaveCount(options.admin!==false&&options.available!==false?1:0);}
    finally{await f.context.close();}
  }
});

test('recent verification permits copying both values and closing clears DOM without browser storage',async()=>{
  const f=await fixture();
  try{
    await f.page.getByRole('button',{name:'Get login details for Tool',exact:true}).click();
    const dialog=f.page.locator('.credentials-dialog');
    await expect(dialog.locator('[data-credential=username]')).toHaveValue('local-admin');
    await expect(dialog.locator('[data-credential=password]')).toHaveValue('Fixture-Backend-Password9!');
    await expect(f.page.locator('.reauth-dialog')).toHaveCount(0);
    await dialog.getByRole('button',{name:'Copy username',exact:true}).click();
    await dialog.getByRole('button',{name:'Copy password',exact:true}).click();
    assert.deepEqual(await f.page.evaluate(()=>window.copied),['local-admin','Fixture-Backend-Password9!']);
    assert.equal(f.requests.length,1);
    assert.deepEqual(f.requests[0].body,{});
    assert.equal(f.requests[0].headers['x-csrf-token'],'fixture-csrf');
    assert.equal(f.requests[0].headers.authorization,undefined);
    const storage=await f.page.evaluate(()=>JSON.stringify({local:{...localStorage},session:{...sessionStorage}}));
    assert.equal(storage.includes('Fixture-Backend-Password9!'),false);
    assert.equal(storage.includes('local-admin'),false);
    await dialog.getByRole('button',{name:'Cancel',exact:true}).click();
    await expect(dialog).toHaveCount(0);
    await expect(f.page.locator('[data-credential]')).toHaveCount(0);
  }finally{await f.context.close();}
});

test('expired recent verification uses the existing own-password dialog and retries once',async()=>{
  const f=await fixture({recent:false});
  try{
    await f.page.getByRole('button',{name:'Get login details for Tool',exact:true}).click();
    const reauth=f.page.locator('.reauth-dialog');
    await expect(reauth).toBeVisible();
    await reauth.getByLabel('Current password',{exact:true}).fill('Actor-Password9!');
    await reauth.getByRole('button',{name:'Confirm operation',exact:true}).click();
    await expect(f.page.locator('[data-credential=password]')).toHaveValue('Fixture-Backend-Password9!');
    assert.equal(f.requests.length,2);
    await expect(reauth).toHaveCount(0);
  }finally{await f.context.close();}
});

for(const action of ['close','navigate','logout'])test(`a late credential response cannot restore secrets after ${action}`,async()=>{
  const f=await fixture({hold:true});
  try{
    await f.page.getByRole('button',{name:'Get login details for Tool',exact:true}).click();await f.waiting;
    if(action==='close')await f.page.locator('.credentials-dialog').getByRole('button',{name:'Cancel',exact:true}).click();
    else if(action==='navigate')await f.page.evaluate(()=>{location.hash='#/account/profile';});
    else await f.page.evaluate(()=>document.getElementById('logout').click());
    await expect(f.page.locator('.credentials-dialog')).toHaveCount(0);
    const response=f.page.waitForResponse(r=>new URL(r.url()).pathname==='/api/apps/tool/credentials');
    f.release();await (await response).finished();
    await f.page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    await expect(f.page.locator('.credentials-dialog,[data-credential]')).toHaveCount(0);
    assert.equal(await f.page.locator('body').textContent().then(v=>v.includes('Fixture-Backend-Password9!')),false);
  }finally{f.release();await f.context.close();}
});

test('a dismissed request cannot later open a password verification dialog',async()=>{
  const f=await fixture({hold:true,lateReauth:true});
  try{
    await f.page.getByRole('button',{name:'Get login details for Tool',exact:true}).click();await f.waiting;
    await f.page.locator('.credentials-dialog').getByRole('button',{name:'Cancel',exact:true}).click();
    const response=f.page.waitForResponse(r=>new URL(r.url()).pathname==='/api/apps/tool/credentials');
    f.release();await (await response).finished();
    await f.page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    await expect(f.page.locator('.reauth-dialog,.credentials-dialog')).toHaveCount(0);
  }finally{f.release();await f.context.close();}
});

test('service failure is visible and leaves no secret input',async()=>{
  const f=await fixture({fail:true});
  try{
    await f.page.getByRole('button',{name:'Get login details for Tool',exact:true}).click();
    await expect(f.page.locator('.credentials-dialog')).toContainText('Login details are temporarily unavailable. Please try again later.');
    await expect(f.page.locator('[data-credential]')).toHaveCount(0);
  }finally{await f.context.close();}
});
