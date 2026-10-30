// TEST_CASES: MAILMOD-T-059
// Controlled browser only; this does not prove a public relay installation or delivery.
import {test} from 'node:test';
import {readFile} from 'node:fs/promises';
import {chromium,expect} from '@playwright/test';

test('VPS setup attaches a saved draft without persisting credentials',async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}:{})});
 const context=await browser.newContext(),page=await context.newPage(),root=new URL('../../modules/mail/service/ui/',import.meta.url),requests=[],installs=[];let saved={revision:0,draft:{}};
 try{
  await context.route('**/*',async route=>{const req=route.request(),url=new URL(req.url());
   if(url.pathname==='/setup')return route.fulfill({contentType:'text/html',body:await readFile(new URL('setup.html',root),'utf8')});
   if(url.pathname.startsWith('/assets/'))return route.fulfill({contentType:url.pathname.endsWith('.js')?'text/javascript':'text/css',body:await readFile(new URL(url.pathname.slice(8),root),'utf8')});
   if(url.pathname==='/api/ingress')return route.fulfill({contentType:'application/json',body:'{"sources":[],"public_ready":false}'});
   if(url.pathname==='/api/setup/relay-install'){const input=req.postDataJSON();installs.push(input);return route.fulfill({contentType:'application/json',body:JSON.stringify({state:input.action==='prepare'?'prepared':'configured',source_id:input.source_id,enabled:false})});}
   if(url.pathname==='/api/setup'){if(req.method()==='PUT'){const body=req.postDataJSON();requests.push(body);saved={revision:saved.revision+1,draft:body.draft};}return route.fulfill({contentType:'application/json',body:JSON.stringify({state:saved,language:'zh-CN',checks:{},relay_install_available:true})});}
   throw Error('Unexpected request '+url.pathname);
  });
  await page.goto('https://mail.example.test/setup');await expect(page.locator('#setup-form')).toBeVisible();
  await page.locator('#inbound').selectOption('vps-direct');await expect(page.locator('#relay-attach')).toBeVisible();await expect(page.locator('#relay-prepare')).toBeDisabled();
  await page.locator('#domain').fill('example.test');await page.locator('#source_url').fill('https://relay.example.test');
  for(const [id,value] of Object.entries({additional_mailboxes:2,aliases:10,mailbox_bytes:2147483648,messages_per_minute:30,cloud_bytes:2147483648,delivered_days:7}))await page.locator('#'+id).fill(String(value));
  await page.locator('#limits_confirmed').check();await page.locator('#save').click();await expect(page.locator('#relay-prepare')).toBeEnabled();
  const operation=await page.locator('#relay-operation').inputValue();if(!/^cfg-[0-9a-f]{32}$/.test(operation))throw Error('operation ID missing');
  await page.locator('#relay-source-id').fill('11111111-1111-4111-8111-111111111111');await page.locator('#relay-generation').fill('22222222-2222-4222-8222-222222222222');
  for(const action of ['prepare','commit']){await page.locator('#relay-reader').fill('a'.repeat(64));await page.locator('#relay-control').fill('b'.repeat(64));await page.locator('#relay-'+action).click();await expect(page.locator('#relay-result')).toContainText(action==='prepare'?'来源已检查':'配置已保存');await expect(page.locator('#relay-reader')).toBeEmpty();await expect(page.locator('#relay-control')).toBeEmpty();}
  if(installs.length!==2||installs.some(i=>i.operation_id!==operation||i.reader_token!=='a'.repeat(64)))throw Error('incorrect request');
  if(JSON.stringify(requests).includes('a'.repeat(64))||await page.evaluate(()=>JSON.stringify({...localStorage,...sessionStorage}))!=='{}')throw Error('credential persisted');
  await expect(page.locator('#enable')).toBeDisabled();await page.locator('#domain').fill('changed.test');await expect(page.locator('#relay-prepare')).toBeDisabled();
 }finally{await context.close();await browser.close();}
});

for(const mode of ['changed-source','wrong-result'])test(`VPS attach ${mode} cannot show success`,async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}:{})});
 const context=await browser.newContext(),page=await context.newPage(),root=new URL('../../modules/mail/service/ui/',import.meta.url);let state={revision:0,draft:{}},release;
 try{
  await context.route('**/*',async route=>{const req=route.request(),path=new URL(req.url()).pathname;
   if(path==='/setup')return route.fulfill({contentType:'text/html',body:await readFile(new URL('setup.html',root),'utf8')});
   if(path.startsWith('/assets/'))return route.fulfill({contentType:path.endsWith('.js')?'text/javascript':'text/css',body:await readFile(new URL(path.slice(8),root),'utf8')});
   if(path==='/api/ingress')return route.fulfill({contentType:'application/json',body:'{"sources":[],"public_ready":false}'});
   if(path==='/api/setup/relay-install'){await new Promise(resolve=>{release=resolve;});return route.fulfill({contentType:'application/json',body:JSON.stringify({state:'prepared',source_id:mode==='wrong-result'?'33333333-3333-4333-8333-333333333333':'11111111-1111-4111-8111-111111111111',enabled:false})});}
   if(path==='/api/setup'){if(req.method()==='PUT')state={revision:state.revision+1,draft:req.postDataJSON().draft};return route.fulfill({contentType:'application/json',body:JSON.stringify({state,language:'zh-CN',checks:{},relay_install_available:true})});}throw Error('unexpected');
  });
  await page.goto('https://mail.example.test/setup');await page.locator('#inbound').selectOption('vps-direct');
  for(const [id,v]of Object.entries({domain:'example.test',source_url:'https://relay.example.test',additional_mailboxes:'2',aliases:'10',mailbox_bytes:'2147483648',messages_per_minute:'30',cloud_bytes:'2147483648',delivered_days:'7'}))await page.locator('#'+id).fill(v);
  await page.locator('#limits_confirmed').check();await page.locator('#save').click();await page.locator('#relay-source-id').fill('11111111-1111-4111-8111-111111111111');await page.locator('#relay-generation').fill('22222222-2222-4222-8222-222222222222');await page.locator('#relay-reader').fill('a'.repeat(64));await page.locator('#relay-control').fill('b'.repeat(64));
  const originalOperation=await page.locator('#relay-operation').inputValue();await page.locator('#relay-prepare').click();await expect.poll(()=>typeof release).toBe('function');if(mode==='changed-source')await page.locator('#relay-source-id').fill('33333333-3333-4333-8333-333333333333');release();
  await expect(page.locator('#relay-result')).toContainText('结果未知');await expect(page.locator('#relay-result')).toContainText(originalOperation);await page.locator('#relay-generation').fill('44444444-4444-4444-8444-444444444444');await expect(page.locator('#relay-result')).toContainText(originalOperation);await expect(page.locator('#relay-reader')).toBeEmpty();await expect(page.locator('#relay-control')).toBeEmpty();
 }finally{await context.close();await browser.close();}
});
