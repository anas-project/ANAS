// TEST_CASES: MAILPOC-T-005
import {chromium} from 'playwright';
import fs from 'node:fs/promises';
import path from 'node:path';
import {spawn} from 'node:child_process';
import assert from 'node:assert/strict';

const root=process.env.MAIL_GO_EVIDENCE;
assert.ok(root?.startsWith('/')&&root.includes('mail-go-poc-directory'));
const secrets=JSON.parse(await fs.readFile(path.join(root,'secrets.json')));
const origin='https://manage.mailtest.invalid:18884';
const webmail='https://webmail.mailtest.invalid:18884';
const iam='https://auth.mailtest.invalid:18884';
const mode=process.argv[2]||process.env.MAIL_GO_MODE||'directory';
const previous=(await fs.readdir(root)).filter(name=>name.startsWith(mode+'-'));
if(previous.length){
 const archive=path.join(root,'attempt-'+mode+'-'+Date.now());await fs.mkdir(archive,{mode:0o700});
 for(const name of previous)if((await fs.stat(path.join(root,name))).isFile())await fs.copyFile(path.join(root,name),path.join(archive,name));
}
const browser=await chromium.launch({headless:true,executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',args:['--host-resolver-rules=MAP *.mailtest.invalid 127.0.0.1']});
const results=[];
const check=(name,value)=>{results.push({name,passed:!!value});console.log(JSON.stringify(results.at(-1)));assert.ok(value,name)};
const actors={};let lastPage;
async function login(name){
 const user=name==='alice2'?'alice':name;
 const context=await browser.newContext({ignoreHTTPSErrors:true,serviceWorkers:'block'});
 const page=await context.newPage();lastPage=page;page.setDefaultTimeout(25000);
 await page.goto(iam);await page.locator('input[name=user]').fill(user);await page.locator('input[name=password]').fill(secrets[user]);await page.locator('button[type=submit],input[type=submit]').first().click();
 await page.waitForFunction(()=>!document.querySelector('input[name=password]'));
 await page.goto(origin);await page.locator('h1').filter({hasText:'邮件管理验证'}).waitFor();
 await fs.writeFile(path.join(root,'session-'+name+'.json'),JSON.stringify({cookies:await context.cookies(origin),csrf:await page.locator('input[name=csrf]').first().inputValue()}),{mode:0o600});
 await page.locator('#open-'+(user==='alice'?'box-01':'box-03')).click();
 await page.locator('#messagelist,#messagelist-content').first().waitFor({state:'visible'});
 const sessionFile=path.join(root,'session-'+name+'.json');
 const saved=JSON.parse(await fs.readFile(sessionFile));saved.webmail_cookies=await context.cookies(webmail);
 await fs.writeFile(sessionFile,JSON.stringify(saved),{mode:0o600});
 check(name+'_real_iam_go_roundcube_login',true);actors[name]={context,page};
}
async function protocol(){
 const child=spawn('python3',['test-env/mail-go-poc/directory_protocol.py',mode],{env:process.env,stdio:['ignore','pipe','pipe']});
 let stdout='',stderr='';child.stdout.on('data',d=>{stdout+=d;process.stdout.write(d)});child.stderr.on('data',d=>stderr+=d);
 const done=new Promise(resolve=>child.once('close',code=>resolve(code)));
 if(mode==='iam'){
  for(let i=0;i<60;i++){
   if(await fs.stat(path.join(root,'iam-ready')).catch(()=>null))break;
   await new Promise(r=>setTimeout(r,500));
  }
  await actors.alice.page.goto(iam+'/?logout=1');
 }
 const code=await done;
 await fs.writeFile(path.join(root,mode+'-protocol-output.txt'),stdout+stderr,{mode:0o600});
 check(mode+'_protocol_matrix',code===0);
}
try{
 await fs.rm(path.join(root,'iam-ready'),{force:true});
 for(const name of ['alice','alice2','bob'])await login(name);
 if(mode==='directory'){
  const page=actors.alice.page;lastPage=page;
  await page.goto(webmail+'/?_task=addressbook',{waitUntil:'networkidle'});
  await page.getByText('Company directory',{exact:true}).first().click();
  await page.getByText(/Bob (PoC|Directory Updated)/,{exact:false}).first().waitFor();
  check('roundcube_company_directory_visible',true);
 }
 await protocol();
 if(mode==='directory'){
  const page=actors.alice.page;lastPage=page;
  await page.goto(webmail+'/?_task=addressbook&_source=mailgo_company',{waitUntil:'networkidle'});
  await page.getByText('Bob Directory Updated',{exact:false}).first().waitFor();
  check('roundcube_real_ad_profile_update_visible',true);
  await page.goto(webmail+'/?_task=mail',{waitUntil:'networkidle'});
  await page.locator('a').filter({hasText:/^Compose$/}).first().click();
  await page.locator('#compose-subject').waitFor();
  await page.waitForLoadState('networkidle');
  const to=page.locator('#_to').locator('..').locator('input').first();await to.pressSequentially('Bob Directory',{delay:70});
  await page.locator('#rcmKSearchpane').getByText('Bob Directory Updated',{exact:false}).first().waitFor();
  check('roundcube_recipient_autocomplete_from_go',true);
 }else if(mode!=='recovery'){
  const response=await actors.alice.page.goto(webmail+'/?_task=mail&_action=refresh');
  check(mode+'_old_roundcube_cookie_denied',response.status()===403);
  const other=await actors.bob.page.goto(webmail+'/?_task=mail');
  check(mode+'_bob_roundcube_preserved',other.status()===200);
  if(['logout','iam'].includes(mode)){
   const second=await actors.alice2.page.goto(webmail+'/?_task=mail');
   check(mode+'_other_alice_roundcube_preserved',second.status()===200);
  }
 }
}catch(e){
 await fs.writeFile(path.join(root,mode+'-browser-error.txt'),String(e.stack).replace(/([?&](?:code|state|mailgo_code|mailgo_state)=)[^\s&]*/g,'$1[redacted]'),{mode:0o600});
 if(lastPage){
  await fs.writeFile(path.join(root,mode+'-browser-body.txt'),await lastPage.locator('body').innerText().catch(()=>''),{mode:0o600});
  await fs.writeFile(path.join(root,mode+'-browser-source.html'),await lastPage.content().catch(()=>''),{mode:0o600});
 }
 results.push({name:mode+'_browser_completed',passed:false});
}finally{
 await browser.close();await fs.writeFile(path.join(root,mode+'-browser.json'),JSON.stringify(results,null,2),{mode:0o600});console.log(JSON.stringify(results));
}
if(results.some(r=>!r.passed))process.exitCode=1;
