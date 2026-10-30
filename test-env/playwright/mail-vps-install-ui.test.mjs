// TEST_CASES: MAILMOD-T-073
import {after,before,test} from 'node:test';
import {readFile} from 'node:fs/promises';
import {chromium,expect} from '@playwright/test';
let browser;
before(async()=>{browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}:{})});});
after(async()=>{await browser?.close();});
test('VPS four steps bind the saved draft and do not persist private keys',async()=>{
 const context=await browser.newContext(),page=await context.newPage(),root=new URL('../../modules/mail/service/ui/',import.meta.url),installs=[],drafts=[];
 let state={revision:0,draft:{}};
 try {
  await context.route('**/*',async route=>{const req=route.request(),path=new URL(req.url()).pathname;
   if(path==='/setup')return route.fulfill({contentType:'text/html',body:await readFile(new URL('setup.html',root),'utf8')});
   if(path.startsWith('/assets/'))return route.fulfill({contentType:path.endsWith('.js')?'text/javascript':'text/css',body:await readFile(new URL(path.slice(8),root),'utf8')});
   if(path==='/api/ingress')return route.fulfill({contentType:'application/json',body:'{"sources":[],"public_ready":false}'});
   if(path==='/api/setup/vps-install'){const body=req.postDataJSON();installs.push(body);return route.fulfill({contentType:'application/json',body:JSON.stringify({state:{publish:'published',activate:'activated',prepare:'prepared',commit:'configured'}[body.action],source_id:'11111111-1111-4111-8111-111111111111',enabled:false})});}
   if(path==='/api/setup'){if(req.method()==='PUT'){const body=req.postDataJSON();drafts.push(body);state={revision:state.revision+1,draft:body.draft};}return route.fulfill({contentType:'application/json',body:JSON.stringify({state,language:'zh-CN',enabled:false,checks:{},vps_install_available:true})});}
   throw Error('Unexpected request '+path);
  });
  await page.goto('https://mail.example.test/setup');await expect(page.locator('#setup-form')).toBeVisible();await page.locator('#inbound').selectOption('vps-direct');await expect(page.locator('#vps-installer')).toBeVisible();await expect(page.locator('#vps-publish')).toBeDisabled();
  for(const [id,value] of Object.entries({domain:'example.test',source_url:'https://relay.example.test',additional_mailboxes:'2',aliases:'10',mailbox_bytes:'2147483648',messages_per_minute:'30',cloud_bytes:'2147483648',delivered_days:'7'}))await page.locator('#'+id).fill(value);
  await page.locator('#limits_confirmed').check();await page.locator('#save').click();await expect(page.locator('#vps-publish')).toBeEnabled();
  for(const [id,value] of Object.entries({'vps-host':'relay.example.test','vps-user':'installer','vps-hostkey':'synthetic-host-key','vps-cert':'synthetic-certificate','vps-smtp':'relay.example.test'}))await page.locator('#'+id).fill(value);
  const operation=await page.locator('#vps-operation').inputValue();
  for(const action of ['publish','activate','prepare','commit']){await page.locator('#vps-sshkey').fill('synthetic-ssh-private');await page.locator('#vps-tlskey').fill('synthetic-tls-private');await page.locator('#vps-'+action).click();await expect(page.locator('#vps-result')).toContainText('此步骤已确认');await expect(page.locator('#vps-sshkey')).toBeEmpty();await expect(page.locator('#vps-tlskey')).toBeEmpty();}
  if(installs.length!==4||installs.some((r,i)=>r.operation_id!==operation||r.action!==['publish','activate','prepare','commit'][i]||r.ssh_private_key!=='synthetic-ssh-private'||r.tls_private_key!=='synthetic-tls-private'))throw Error('incorrect installation requests');
  await page.locator('#inbound').selectOption('cloud-relay');await expect(page.locator('#vps-forward-fields')).toBeVisible();await expect(page.locator('#vps-publish')).toBeDisabled();await page.locator('#save').click();await expect(page.locator('#vps-publish')).toBeEnabled();
  for(const [id,value] of Object.entries({'vps-account':'1'.repeat(32),'vps-zone':'2'.repeat(32),'vps-domain':'delivery.example.test'}))await page.locator('#'+id).fill(value);
  for(const action of ['publish','activate','prepare','commit']){await page.locator('#vps-sshkey').fill('synthetic-ssh-private');await page.locator('#vps-tlskey').fill('synthetic-tls-private');await page.locator('#vps-routing').fill('r'.repeat(40));await page.locator('#vps-destination').fill('d'.repeat(40));await page.locator('#vps-'+action).click();await expect(page.locator('#vps-result')).toContainText('此步骤已确认');await expect(page.locator('#vps-routing')).toBeEmpty();await expect(page.locator('#vps-destination')).toBeEmpty();}
  if(installs.length!==8||installs.slice(4).some(r=>r.account_id!=='1'.repeat(32)||r.zone_id!=='2'.repeat(32)||r.forward_domain!=='delivery.example.test'||r.routing_token!=='r'.repeat(40)||r.destination_token!=='d'.repeat(40)))throw Error('forwarding inputs not passed to every step');
  if(JSON.stringify(drafts).includes('synthetic-')||await page.evaluate(()=>JSON.stringify({...localStorage,...sessionStorage}))!=='{}')throw Error('secret persisted');
  if(JSON.stringify(drafts).includes('r'.repeat(40))||JSON.stringify(drafts).includes('d'.repeat(40)))throw Error('forward tokens persisted in draft');
  await expect(page.locator('#enable')).toBeDisabled();await page.locator('#domain').fill('changed.test');await expect(page.locator('#vps-publish')).toBeDisabled();await page.locator('#vps-publish').click({force:true});if(installs.length!==8)throw Error('unsaved draft installed');
 } finally {await context.close();}
});

for(const field of ['vps-operation','vps-host','vps-domain'])test(`VPS in-flight ${field} edit cannot claim success`,async()=>{
 const context=await browser.newContext(),page=await context.newPage(),root=new URL('../../modules/mail/service/ui/',import.meta.url);let state={revision:0,draft:{}},release;
 try{
  await context.route('**/*',async route=>{const req=route.request(),path=new URL(req.url()).pathname;
   if(path==='/setup')return route.fulfill({contentType:'text/html',body:await readFile(new URL('setup.html',root),'utf8')});
   if(path.startsWith('/assets/'))return route.fulfill({contentType:path.endsWith('.js')?'text/javascript':'text/css',body:await readFile(new URL(path.slice(8),root),'utf8')});
   if(path==='/api/ingress')return route.fulfill({contentType:'application/json',body:'{"sources":[],"public_ready":false}'});
   if(path==='/api/setup/vps-install'){await new Promise(resolve=>{release=resolve;});return route.fulfill({contentType:'application/json',body:JSON.stringify({state:'published',source_id:'11111111-1111-4111-8111-111111111111',enabled:false})});}
   if(path==='/api/setup'){if(req.method()==='PUT')state={revision:state.revision+1,draft:req.postDataJSON().draft};return route.fulfill({contentType:'application/json',body:JSON.stringify({state,language:'zh-CN',checks:{},vps_install_available:true})});}throw Error('unexpected');
  });
  await page.goto('https://mail.example.test/setup');await page.locator('#inbound').selectOption('cloud-relay');
  for(const [id,v]of Object.entries({domain:'example.test',source_url:'https://relay.example.test',additional_mailboxes:'2',aliases:'10',mailbox_bytes:'2147483648',messages_per_minute:'30',cloud_bytes:'2147483648',delivered_days:'7'}))await page.locator('#'+id).fill(v);
  await page.locator('#limits_confirmed').check();await page.locator('#save').click();
  for(const [id,v]of Object.entries({'vps-host':'relay.example.test','vps-user':'installer','vps-hostkey':'test-host-key','vps-cert':'test-certificate','vps-smtp':'relay.example.test','vps-account':'1'.repeat(32),'vps-zone':'2'.repeat(32),'vps-domain':'delivery.example.test','vps-routing':'r'.repeat(40),'vps-destination':'d'.repeat(40),'vps-sshkey':'private-ssh','vps-tlskey':'private-tls'}))await page.locator('#'+id).fill(v);
  const originalOperation=await page.locator('#vps-operation').inputValue();await page.locator('#vps-publish').click();await expect.poll(()=>typeof release).toBe('function');await page.locator('#'+field).fill(field==='vps-operation'?'cfg-99999999999999999999999999999999':'changed.example.test');release();
  await expect(page.locator('#vps-result')).toContainText('结果未知');await expect(page.locator('#vps-result')).toContainText(originalOperation);await expect(page.locator('#vps-sshkey')).toBeEmpty();await expect(page.locator('#vps-tlskey')).toBeEmpty();await expect(page.locator('#vps-routing')).toBeEmpty();await expect(page.locator('#vps-destination')).toBeEmpty();
 }finally{await context.close();}
});
