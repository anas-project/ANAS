// TEST_CASES: MAILPOC-T-004, MAILPOC-T-006
import {chromium} from 'playwright';
import fs from 'node:fs/promises';
import assert from 'node:assert/strict';
import path from 'node:path';
const root=process.env.MAIL_GO_EVIDENCE;
assert.ok(root?.startsWith('/')&&root.includes('mail-go-poc'));
const secrets=JSON.parse(await fs.readFile(path.join(root,'secrets.json')));
const origin='https://manage.mailtest.invalid:18884';
const webmail='https://webmail.mailtest.invalid:18884';
const iam='https://auth.mailtest.invalid:18884';
const mode=process.env.MAIL_GO_MODE||'initial';
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH||'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',args:['--host-resolver-rules=MAP *.mailtest.invalid 127.0.0.1']});
const results=[];
let lastPage;
const record=(name,passed)=>{results.push({name,passed});assert.ok(passed,name)};
try {
 for (const user of (['initial','restored'].includes(mode)?['alice','bob']:['alice'])) {
  const context=await browser.newContext({ignoreHTTPSErrors:true,serviceWorkers:'block'});
  const page=await context.newPage();lastPage=page;page.setDefaultTimeout(45000);
  const compose=async()=>{await page.goto(webmail+'/?_task=mail',{waitUntil:'networkidle'});await page.locator('a').filter({hasText:/^Compose$/}).first().click();await page.locator('#compose-subject').waitFor();await page.waitForLoadState('networkidle');};
  let passwordPosts=0;
  context.on('request',r=>{if(r.method()==='POST'&&r.url().startsWith(iam)&&/(?:^|&)password=/.test(r.postData()||''))passwordPosts++});
  await page.goto(iam);
  await page.locator('input[name=user]').fill(user);
  await page.locator('input[name=password]').fill(secrets[user]);
  await page.locator('button[type=submit],input[type=submit]').first().click();
  await page.waitForFunction(()=>!document.querySelector('input[name=password]'));
  await page.goto(origin);
  await page.locator('h1').filter({hasText:'邮件管理验证'}).waitFor();
  record(user+'_real_iam_go_login',passwordPosts===1);
  const boxes=user==='alice'?['box-01','box-02']:['box-03'];
  record(user+'_mailbox_list',await page.locator('section').count()===boxes.length);
  await fs.writeFile(path.join(root,'session-'+user+'.json'),JSON.stringify({cookies:await context.cookies(origin),csrf:await page.locator('input[name=csrf]').first().inputValue()}),{mode:0o600});
  if(user==='alice'&&mode==='initial'){
   const section=page.locator('section').first();
   await section.locator('input[name=address]').fill('webui@mailtest.invalid');
   await section.locator('button[value=add]').click();
   await page.locator('p').filter({hasText:'webui@mailtest.invalid'}).waitFor();
   record('alias_added_through_go_web_ui',true);
  }
  for(const box of boxes){
   if(mode==='refresh'&&box!=='box-01')continue;
   await page.goto(origin);
   await page.locator('#open-'+box).click();
   await page.locator('#messagelist,#messagelist-content').first().waitFor({state:'visible'});
   await page.waitForLoadState('networkidle');
   record(user+'_'+box+'_handoff_and_imap_login',page.url().startsWith(webmail)&&passwordPosts===1);
   const visible=await page.locator('body').innerText();
   const publicAddress=box==='box-02'?'projects@mailtest.invalid':box==='box-03'?'bob@mailtest.invalid':mode==='continuity'?'alice.new@mailtest.invalid':'alice@mailtest.invalid';
   record(box+'_public_address_display',visible.includes(publicAddress)&&!visible.includes('@boxes.mailtest.invalid'));
   if(mode==='refresh'){
    await new Promise(r=>setTimeout(r,100000));
    await page.reload({waitUntil:'networkidle'});
    await page.locator('#messagelist,#messagelist-content').first().waitFor({state:'visible'});
    record('roundcube_works_after_90_second_token_expiry',passwordPosts===1);
   }
   await compose();
   const addresses=await page.locator('#_from option').allTextContents();
   if(box==='box-01'){
    record('primary_box_has_aliases',addresses.some(s=>s.includes('shopping@'))&&addresses.some(s=>s.includes('work@')));
    record('primary_box_does_not_offer_second_box',!addresses.some(s=>s.includes('projects@')));
   } else if(box==='box-02') record('second_box_only_own_from',addresses.length===1&&addresses[0].includes('projects@'));
   if(mode==='initial'&&user==='alice'){
    const senders=box==='box-01'?['shopping','work']:['projects'];
    for(const sender of senders){
     await compose();
     const label=(await page.locator('#_from option').allTextContents()).find(s=>s.includes(sender+'@mailtest.invalid'));
     await page.locator('#_from').selectOption({label});
     const to=page.locator('#_to').locator('..').locator('input').first();await to.fill('bob@mailtest.invalid');await to.press('Enter');
     await page.locator('#compose-subject').fill('Go UI '+sender);
     await page.locator('#composebody').fill('Synthetic Go middleware UI test '+sender);
     await page.locator('button#rcmbtn111').click();
     await page.locator('#messagelist,#messagelist-content').first().waitFor({state:'visible'});
     record('real_ui_send_'+sender,true);
    }
   }
   if(box==='box-01'&&mode==='initial'){
    await page.goto(webmail+'/?_task=mail',{waitUntil:'networkidle'});
    await page.locator('#listmenulink').click();
    await page.locator('select[name=sort_col]:visible').selectOption({label:'Subject'});
    await page.locator('button:visible').filter({hasText:'Save'}).click();
    await page.waitForLoadState('networkidle');record('preference_saved',true);
   }
  }
  // Private session material lets independent protocol tests request real
  // mailbox-scoped Go credentials using this verified person session.
  await page.goto(origin);
  const csrf=await page.locator('input[name=csrf]').first().inputValue();
  await fs.writeFile(path.join(root,'session-'+user+'.json'),JSON.stringify({cookies:await context.cookies(origin),csrf}),{mode:0o600});
  await context.close();
 }
}catch(e){await fs.writeFile(path.join(root,'browser-'+mode+'-error.txt'),String(e.stack).replace(/([?&](?:code|state|mailgo_code|mailgo_state)=)[^\s&]*/g,'$1[redacted]'),{mode:0o600});if(lastPage)await fs.writeFile(path.join(root,'browser-'+mode+'-body.txt'),await lastPage.locator('body').innerText(),{mode:0o600});results.push({name:'browser_completed',passed:false});}
finally{await browser.close();await fs.writeFile(path.join(root,'browser-'+mode+'-result.json'),JSON.stringify(results,null,2),{mode:0o600});console.log(JSON.stringify(results));}
if(results.some(r=>!r.passed))process.exitCode=1;
