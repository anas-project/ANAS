// TEST_CASES: PORTALUX-T-003
// Production portal UI with isolated mock APIs: languages, themes, account
// preferences, pinned applications, quick search and page structure.
import {after, before, test} from 'node:test';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';
import {chromium, expect} from '@playwright/test';

let browser;
before(async()=>{browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}:{})});});
after(async()=>{await browser?.close();});
const origin='https://portal.example.test';
const directory=new URL('../../modules/user_portal/portal/ui/',import.meta.url);
const app=(name,kind='user')=>({name,display_name:name.toUpperCase(),summary:name+' summary',description:name+' description',url:'https://'+name+'.example.test/start?x=1#frag',documentation_url:'https://docs.example.test/'+name,categories:['files'],icons:{}});
const groups=[{kind:'user',id:'user',display_name:'User applications',categories:[{id:'files',display_name:'Files',applications:['alpha','beta','gamma'].map(n=>app(n))}]},{kind:'admin',id:'admin',display_name:'Administration',categories:[{id:'identity',display_name:'Identity',applications:[app('console','admin')]}]}];

async function portal({admin=false,authenticated=true,scheme='light',preferences={language:'',theme:'system',show_categories:true,pinned:[]},width=1280,height=900,storage}={}) {
  const context=await browser.newContext({colorScheme:scheme,viewport:{width,height}}),page=await context.newPage();
  const saved=[];let prefs={...preferences};
  const person={anchor:'actor',username:'alice',display_name:'Alice Wang',email:'alice@example.test',groups:[],password_state_known:true};
  const me=()=>({person,csrf:'csrf',actions:admin?['users.read','password.reset','account.unlock','profile.update']:[],administration:admin,profile_editable:true,profile_version:'v1',mail_domain:'example.test',preferences:prefs});
  await context.route('**/*',async route=>{
    const r=route.request(),u=new URL(r.url()),p=u.pathname,json=(d,s=200)=>route.fulfill({status:s,contentType:'application/json',body:JSON.stringify(d)});
    if(p==='/')return route.fulfill({contentType:'text/html',body:await readFile(new URL('index.html',directory),'utf8')});
    if(p.startsWith('/static/'))return route.fulfill({contentType:p.endsWith('.js')?'text/javascript':p.endsWith('.svg')?'image/svg+xml':'text/css',body:await readFile(new URL(p.slice(8),directory),'utf8')});
    if(p==='/api/bootstrap')return json({language:u.searchParams.get('lang')||'en',authenticated,csrf:'csrf'});
    if(p==='/api/me')return authenticated?json(me()):json({error:'authentication_required'},401);
    if(p==='/api/apps')return json({groups});
    if(p==='/api/me/preferences'){const body=r.postDataJSON();saved.push(body);prefs=body;return json({preferences:body});}
    if(p==='/api/users')return json({users:[{...person,anchor:'bob',username:'bob',display_name:'Bob Lee'}],count:1});
    if(p==='/api/users/bob')return json({person:{...person,anchor:'bob',username:'bob',display_name:'Bob Lee'},actions:['profile.update','password.reset','account.unlock'],profile_version:'v1',requires_domain:false,administration:true});
    if(p==='/api/groups')return json({groups:[]});
    if(p==='/api/audit')return json({records:[]});
    if(p==='/api/me/profile')return json({directory:'saved',iam:'pending',external_sessions:'unverified',audit:'saved',request_id:'rid'});
    return route.abort();
  });
  if(storage)await context.addInitScript(values=>{for(const [k,v] of Object.entries(values))localStorage.setItem(k,v);},storage);
  return {context,page,saved};
}
const background=page=>page.evaluate(()=>getComputedStyle(document.body).backgroundColor);
const ready=(page,name)=>expect(page.locator('body[data-page='+name+']')).toHaveCount(1);

// TEST_CASES: PORTALUX-T-001
test('every interface key exists in all eight languages and every referenced key is defined',async()=>{
  const sandbox={window:{}};vm.runInNewContext(await readFile(new URL('i18n.js',directory),'utf8'),sandbox);
  const {languages,words}=sandbox.window.ANAS_I18N;
  if(JSON.stringify(languages.map(l=>l.code))!==JSON.stringify(['zh-CN','zh-TW','en','ja','ko','fr','de','es']))throw Error('language list changed');
  const keys=Object.keys(words.en);
  for(const {code} of languages)for(const key of keys)if(typeof words[code][key]!=='string'||!words[code][key].trim())throw Error(code+' lacks '+key);
  for(const {code} of languages)if(Object.keys(words[code]).length!==keys.length)throw Error(code+' has extra keys');
  const source=await readFile(new URL('app.js',directory),'utf8');
  for(const [,key] of source.matchAll(/\bt\('([A-Za-z_.]+)'/g))if(!(key in words.en))throw Error('undefined key '+key);
});

test('system theme follows the operating system live and fixed themes ignore it',async()=>{
  const {context,page}=await portal({authenticated:false,scheme:'dark'});
  try{
    await page.goto(origin);await ready(page,'login');
    if(await page.evaluate(()=>document.documentElement.dataset.theme)!=='system')throw Error('default theme');
    const dark=await background(page);await page.emulateMedia({colorScheme:'light'});
    await expect.poll(()=>background(page)).not.toBe(dark);
    await page.getByRole('button',{name:/^Theme: /}).click();await page.getByRole('button',{name:/^Theme: /}).click();
    if(await page.evaluate(()=>document.documentElement.dataset.theme)!=='dark')throw Error('theme did not cycle to dark');
    const fixed=await background(page);await page.emulateMedia({colorScheme:'light'});
    if(await background(page)!==fixed)throw Error('fixed dark followed the system');
  }finally{await context.close();}
});

test('anonymous pages apply the cached theme before first paint and recovery uses it',async()=>{
  const {context,page}=await portal({authenticated:false,scheme:'light',storage:{'anas.portal.theme':'dark'}});
  try{
    await page.addInitScript(()=>document.addEventListener('DOMContentLoaded',()=>{window.__firstTheme=document.documentElement.dataset.theme;window.__firstBackground=getComputedStyle(document.documentElement).backgroundColor;}));
    await page.goto(origin+'/#/recover');await ready(page,'recover');
    if(await page.evaluate(()=>window.__firstTheme)!=='dark')throw Error('cached theme applied late');
    if(await page.evaluate(()=>window.__firstBackground)!=='rgb(15, 17, 21)')throw Error('first paint was not dark');
  }finally{await context.close();}
});

test('account preference wins after sign-in and is cached for this browser',async()=>{
  const {context,page}=await portal({storage:{'anas.portal.theme':'light','anas.portal.language':'en'},preferences:{language:'ja',theme:'dark',show_categories:false,pinned:[]}});
  try{
    await page.goto(origin);await ready(page,'home');
    if(await page.evaluate(()=>document.documentElement.dataset.theme)!=='dark')throw Error('account theme ignored');
    await expect(page.locator('#language')).toHaveValue('ja');await expect(page.locator('html')).toHaveAttribute('lang','ja');
    await expect(page.getByLabel('カテゴリを表示',{exact:true})).not.toBeChecked();
    if(JSON.stringify(await page.evaluate(()=>[localStorage.getItem('anas.portal.theme'),localStorage.getItem('anas.portal.language')]))!=='["dark","ja"]')throw Error('browser cache not updated');
  }finally{await context.close();}
});

test('preferences are saved to the account with the whole whitelisted object',async()=>{
  const {context,page,saved}=await portal();
  try{
    await page.goto(origin+'/#/account/profile');await ready(page,'account-profile');
    await page.getByRole('radio',{name:'Dark',exact:true}).check();
    await expect.poll(()=>saved.length).toBe(1);
    if(JSON.stringify(Object.keys(saved[0]).sort())!=='["language","pinned","show_categories","theme"]'||saved[0].theme!=='dark')throw Error('unexpected preference body '+JSON.stringify(saved[0]));
    await page.getByLabel('Language',{exact:true}).last().selectOption('de');
    await expect.poll(()=>saved.at(-1)?.language).toBe('de');
    await expect(page.locator('html')).toHaveAttribute('lang','de');
  }finally{await context.close();}
});

test('both themes keep body and secondary text at 4.5:1 or better',async()=>{
  for(const theme of ['light','dark']){
    const {context,page}=await portal({preferences:{language:'',theme,show_categories:true,pinned:['alpha']}});
    try{
      await page.goto(origin);await ready(page,'home');
      const ratios=await page.evaluate(()=>{
        const css=getComputedStyle(document.documentElement),rgb=v=>{const n=document.createElement('i');n.style.color=v;document.body.append(n);const c=getComputedStyle(n).color.match(/\d+/g).map(Number);n.remove();return c;};
        const lum=c=>{const [r,g,b]=c.map(x=>{x/=255;return x<=.03928?x/12.92:((x+.055)/1.055)**2.4;});return .2126*r+.7152*g+.0722*b;};
        const ratio=(a,b)=>{const [x,y]=[lum(rgb(css.getPropertyValue(a))),lum(rgb(css.getPropertyValue(b)))].sort((p,q)=>q-p);return (x+.05)/(y+.05);};
        return [ratio('--text','--bg'),ratio('--text-2','--surface'),ratio('--text-3','--bg'),ratio('--text-3','--surface'),ratio('--accent-text','--accent-soft'),ratio('--on-primary','--primary'),ratio('--warn','--warn-bg'),ratio('--danger','--danger-bg'),ratio('--ok','--ok-bg')];
      });
      if(ratios.some(r=>r<4.5))throw Error(theme+' contrast '+ratios.map(r=>r.toFixed(2)).join(','));
    }finally{await context.close();}
  }
});

test('applications can be pinned, reordered by keyboard buttons and by drag, and unpinned',async()=>{
  const {context,page,saved}=await portal();
  try{
    await page.goto(origin);await ready(page,'home');
    for(const name of ['ALPHA','BETA','GAMMA'])await page.getByRole('button',{name:'Pin: '+name,exact:true}).click();
    await expect.poll(()=>JSON.stringify(saved.at(-1)?.pinned)).toBe('["alpha","beta","gamma"]');
    await page.getByRole('button',{name:'Edit',exact:true}).click();
    await page.getByRole('button',{name:'Move later: ALPHA',exact:true}).click();
    await expect.poll(()=>JSON.stringify(saved.at(-1).pinned)).toBe('["beta","alpha","gamma"]');
    await page.getByRole('button',{name:'Done',exact:true}).click();
    await page.locator('.pin-tile').nth(2).dragTo(page.locator('.pin-tile').nth(0));
    await expect.poll(()=>JSON.stringify(saved.at(-1).pinned)).toBe('["gamma","beta","alpha"]');
    await page.getByRole('button',{name:'Unpin: BETA',exact:true}).first().click();
    await expect.poll(()=>JSON.stringify(saved.at(-1).pinned)).toBe('["gamma","alpha"]');
    await expect(page.locator('.pin-tile')).toHaveCount(2);
  }finally{await context.close();}
});

test('quick search opens with the keyboard, selects applications and pages, and Esc leaves the page unchanged',async()=>{
  const {context,page}=await portal();
  try{
    await page.goto(origin);await ready(page,'home');
    await page.evaluate(()=>{window.__opened=[];window.open=(url,...rest)=>{window.__opened.push([url,...rest]);return null;};});
    await page.keyboard.press('Control+k');
    const dialog=page.getByRole('dialog',{name:'Quick search'});await expect(dialog).toBeVisible();
    await page.keyboard.type('gam');await page.keyboard.press('Enter');
    await expect(dialog).toHaveCount(0);
    const opened=await page.evaluate(()=>window.__opened);
    if(opened.length!==1||opened[0][0]!=='https://gamma.example.test/start?x=1#frag'||opened[0][1]!=='_blank')throw Error('application not opened as declared '+JSON.stringify(opened));
    await page.keyboard.press('Meta+k');await page.keyboard.type('change password');
    await expect(page.getByRole('option',{name:'Change password'})).toBeVisible();
    await page.keyboard.press('Escape');await expect(page.getByRole('dialog')).toHaveCount(0);
    if(new URL(page.url()).hash!=='')throw Error('Esc changed the page');
    await page.keyboard.press('Control+k');await page.keyboard.type('Change password');await page.keyboard.press('ArrowDown');await page.keyboard.press('ArrowUp');await page.keyboard.press('Enter');
    await ready(page,'account-security');
  }finally{await context.close();}
});

test('people appear in quick search only for users who can query people',async()=>{
  for(const admin of [false,true]){
    const {context,page}=await portal({admin});
    try{
      await page.goto(origin);await ready(page,'home');
      await page.keyboard.press('Control+k');await page.keyboard.type('bob');
      if(admin){await expect(page.getByRole('option',{name:/Bob Lee/})).toBeVisible();await page.keyboard.press('Enter');await ready(page,'admin-user');}
      else{await page.waitForTimeout(500);await expect(page.locator('.palette-group',{hasText:'People'})).toHaveCount(0);await expect(page.getByRole('option',{name:/Bob Lee/})).toHaveCount(0);}
    }finally{await context.close();}
  }
});

test('every page has its own address that survives reload, back and forward; unknown addresses go home',async()=>{
  const {context,page}=await portal({admin:true});
  try{
    await page.goto(origin+'/#/account/security');await ready(page,'account-security');
    await page.reload();await ready(page,'account-security');
    await page.goto(origin+'/#/admin/users/bob');await ready(page,'admin-user');
    await page.goBack();await ready(page,'account-security');
    await page.goForward();await ready(page,'admin-user');
    await page.goto(origin+'/#/does/not/exist');await ready(page,'home');
    if(new URL(page.url()).hash!=='#/apps')throw Error('unknown route not replaced');
  }finally{await context.close();}
});

test('manage mode is offered only with personnel rights and lists administration applications',async()=>{
  for(const admin of [false,true]){
    const {context,page}=await portal({admin});
    try{
      await page.goto(origin);await ready(page,'home');
      await expect(page.getByRole('link',{name:'Manage',exact:true})).toHaveCount(admin?1:0);
      await page.goto(origin+'/#/admin/users');
      if(!admin){await ready(page,'home');continue;}
      await ready(page,'admin-users');
      await expect(page.locator('.admin-sidebar').getByRole('link',{name:/CONSOLE/})).toHaveAttribute('href','https://console.example.test/start?x=1#frag');
    }finally{await context.close();}
  }
});

test('the whole card opens the declared entry in a new window and documentation is a separate link',async()=>{
  const {context,page}=await portal();
  try{
    await page.goto(origin);await ready(page,'home');
    const card=page.locator('.app-card').first(),main=card.locator('a.app-main');
    await expect(main).toHaveAttribute('href','https://alpha.example.test/start?x=1#frag');await expect(main).toHaveAttribute('target','_blank');
    await expect(main).toHaveAttribute('rel','noopener noreferrer');
    await card.getByText('Details',{exact:true}).click();
    await expect(card.getByRole('link',{name:'Documentation',exact:true})).toHaveAttribute('href','https://docs.example.test/alpha');
    if(await card.locator('a.app-main a').count())throw Error('documentation nested in the entry link');
  }finally{await context.close();}
});

test('write results show only the confirmed operation outcome',async()=>{
  const {context,page}=await portal();
  try{
    await page.goto(origin+'/#/account/profile');await ready(page,'account-profile');
    await page.getByRole('button',{name:'Save',exact:true}).click();
    await expect(page.locator('.form-result')).toContainText('Saved.');
    await expect(page.locator('.form-result .steps')).toHaveCount(0);
    await expect(page.locator('.form-result')).not.toContainText('IAM');
    await expect(page.locator('.form-result')).not.toContainText('Application sessions');
  }finally{await context.close();}
});

test('reset and unlock live in the danger zone and still require page confirmation',async()=>{
  const {context,page}=await portal({admin:true});
  try{
    await page.goto(origin+'/#/admin/users/bob');await ready(page,'admin-user');
    const zone=page.locator('section.danger-zone');
    await expect(zone.getByRole('heading',{name:'Danger zone'})).toBeVisible();
    await expect(zone.getByRole('button',{name:'Reset password',exact:true})).toBeVisible();
    await expect(zone.getByRole('button',{name:'Unlock account',exact:true})).toBeVisible();
    await zone.getByRole('button',{name:'Reset password',exact:true}).click();
    const dialog=page.getByRole('dialog');await expect(dialog).toContainText('Reset password');
    await dialog.getByRole('button',{name:'Cancel',exact:true}).click();await expect(page.getByRole('dialog')).toHaveCount(0);
  }finally{await context.close();}
});

test('no page scrolls horizontally at 390px and navigation sits at the bottom',async()=>{
  for(const language of ['en','de','ja']){
    const {context,page}=await portal({admin:true,width:390,height:844,preferences:{language,theme:'system',show_categories:true,pinned:['alpha']}});
    try{
      for(const [hash,name] of [['#/apps','home'],['#/account/profile','account-profile'],['#/account/security','account-security'],['#/admin/users','admin-users'],['#/admin/users/bob','admin-user']]){
        await page.goto(origin+'/'+hash);await ready(page,name);
        if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error(language+' '+hash+' scrolls horizontally');
        const nav=await page.locator('.bottom-nav').boundingBox();
        if(!nav||Math.abs(nav.y+nav.height-844)>1)throw Error('bottom navigation not at the bottom on '+hash);
      }
    }finally{await context.close();}
  }
});

// TEST_CASES: APPTHEME-T-007
test('sign-in passes the current resolved color mode to the native IAM page',async()=>{
 for(const [stored,scheme,want] of [['system','dark','dark'],['system','light','default'],['dark','light','dark'],['light','dark','default']]){
  const {context,page}=await portal({authenticated:false,scheme,storage:{'anas.portal.theme':stored}});
  try{
   await page.goto(origin);await ready(page,'login');
   await page.route('**/login?theme=*',route=>route.fulfill({contentType:'text/html',body:'native login handoff'}));
   await page.getByRole('link',{name:'Sign in',exact:true}).click();
   await expect(page).toHaveURL(origin+'/login?theme='+want);
  }finally{await context.close();}
 }
});

test('password success removes unobserved status rows and keeps a sign-in action',async()=>{
 const {context,page}=await portal({authenticated:false});
 try{
  await page.route('**/api/password/recover*',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({directory:'saved',iam:'pending',external_sessions:'unverified',audit:'saved',reauthenticate:true})}));
  await page.goto(origin+'/#/recover');await ready(page,'recover');
  await page.locator('[name=username]').fill('alice');await page.locator('[name=old_password]').fill('Old-test-123!');await page.locator('[name=new_password]').fill('New-test-456!');await page.locator('[name=confirm_password]').fill('New-test-456!');
  await page.locator('form button[type=submit]').click();
  await expect(page.locator('.form-result')).toContainText('Password changed.');
  await expect(page.locator('.form-result .steps')).toHaveCount(0);
  await expect(page.locator('.form-result')).not.toContainText('IAM');
  await expect(page.locator('.form-result a')).toHaveAttribute('href','/login');
 }finally{await context.close();}
});
