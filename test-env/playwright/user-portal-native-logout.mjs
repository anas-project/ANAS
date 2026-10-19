// TEST_CASES: PORTAL-T-010
// Real anonymous backend and native APIs. No authentication responses or cookies
// are fabricated; an extra native completion invalidates the pending session.
import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
import net from 'node:net';
import {createHash} from 'node:crypto';
import {chromium,expect} from '@playwright/test';

const root=process.env.PORTAL_NATIVE_LOGOUT_ROOT;
if(!root||!/^\/tmp\/anas-portal-browser-logout-[A-Za-z0-9-]+$/.test(root))throw Error('Owned evidence directory required');
const expected=process.env.PORTAL_NATIVE_LOGOUT_JS_SHA256;
if(!/^[0-9a-f]{64}$/.test(expected||''))throw Error('Expected deployed UI digest required');
const origin='https://portal.finance.hlong.wang:10000';
const allowed=['portal','auth','nc','photos'].map(n=>n+'.finance.hlong.wang:10000');
const evidence=path.join(root,'evidence','native-csrf-'+new Date().toISOString().replace(/[:.]/g,'-'));
fs.mkdirSync(evidence,{recursive:true,mode:0o700});
const report={started_at:new Date().toISOString(),scope:'Anonymous native callback and real CSRF revocation recovery; not authenticated cross-app logout acceptance',driver_sha256:createHash('sha256').update(fs.readFileSync(new URL(import.meta.url))).digest('hex'),expected_app_js_sha256:expected,completion_statuses:[],checks:[]};
const sockets=new Set();let browser;
const proxy=http.createServer((_,r)=>r.writeHead(405).end());
proxy.on('connect',(request,client,head)=>{
 if(!allowed.includes(request.url)){client.destroy();return;}
 const u=new URL('https://'+request.url);
 const upstream=net.connect(Number(u.port),u.hostname,()=>{client.write('HTTP/1.1 200 Connection Established\r\n\r\n');if(head.length)upstream.write(head);client.pipe(upstream);upstream.pipe(client);});
 for(const socket of [client,upstream]){sockets.add(socket);socket.on('close',()=>sockets.delete(socket));socket.on('error',()=>{client.destroy();upstream.destroy();});}
});
await new Promise(resolve=>proxy.listen(0,'127.0.0.1',resolve));
const save=()=>fs.writeFileSync(path.join(evidence,'native-csrf.json'),JSON.stringify(report,null,2),{mode:0o600});
const check=(name,pass)=>{report.checks.push({name,pass:!!pass});save();if(!pass)throw Error(name);};
try{
 browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}:{})});
 report.browser_version=browser.version();report.http1_only=false;
 const context=await browser.newContext({ignoreHTTPSErrors:true,locale:'en',proxy:{server:'http://127.0.0.1:'+proxy.address().port}}),page=await context.newPage();
 let posts=0;
 await context.route(origin+'/api/logout/complete?*',async route=>{
  posts++;
  if(posts===1){
   // Let the real server remove this session, then release the original request
   // with its now-stale CSRF. The browser receives the server's actual 403.
   const invalidation=await route.fetch({maxRedirects:0});
   report.native_session_invalidation_status=invalidation.status();save();
  }
  await route.continue();
 });
 page.on('response',response=>{
  const u=new URL(response.url());
  if(u.origin===origin&&u.pathname==='/api/logout/complete'){report.completion_statuses.push(response.status());save();}
 });
 await page.goto('https://photos.finance.hlong.wang:10000/api/server/config',{waitUntil:'domcontentloaded',timeout:60000});
 const script=page.waitForResponse(response=>new URL(response.url()).origin===origin&&new URL(response.url()).pathname==='/static/app.js');
 await page.evaluate(url=>location.assign(url),origin+'/signed-out?native=1&lang=en');
 await page.waitForURL(u=>u.origin===origin&&u.pathname==='/signed-out',{waitUntil:'domcontentloaded',timeout:60000});
 report.actual_app_js_sha256=createHash('sha256').update(await(await script).body()).digest('hex');
 check('the deployed production UI matches the candidate digest',report.actual_app_js_sha256===expected);
 check('a same-site native callback is automatic',await page.locator('meta[name=anas-native-logout]').getAttribute('content')==='automatic');
 await expect(page.getByText('This browser has signed out of IAM and the connected applications.',{exact:true})).toBeVisible({timeout:120000});
 check('native session invalidation executed successfully',report.native_session_invalidation_status===200);
 check('the browser received a real rejection then one successful fresh completion',JSON.stringify(report.completion_statuses)===JSON.stringify([403,200])&&posts===2);
 report.logout_results=await page.locator('.logout-results li').allTextContents();
 check('all three native targets are confirmed signed out',report.logout_results.length===3&&report.logout_results.every(s=>s.endsWith(': Signed out')));
 report.business_catalog_status=await page.evaluate(async()=> (await fetch('/api/apps',{cache:'no-store'})).status);
 check('anonymous recovery cannot read the business catalogue',report.business_catalog_status===401);
 await expect(page.getByRole('link',{name:'Return to homepage',exact:true})).toBeVisible();
 await page.screenshot({path:path.join(evidence,'native-csrf.png')});
 await page.getByRole('link',{name:'Return to homepage',exact:true}).click();
 await expect(page.getByRole('link',{name:'Sign in',exact:true})).toBeVisible();
 check('the homepage remains available after recovery',new URL(page.url()).pathname==='/');
 report.status='passed';
}catch(error){report.status='failed';report.error=String(error.message).replace(/https?:\S+/g,'[URL]').slice(0,2400);}
finally{
 report.finished_at=new Date().toISOString();save();await browser?.close();for(const socket of sockets)socket.destroy();await new Promise(resolve=>proxy.close(resolve));
}
console.log(JSON.stringify({status:report.status,evidence,checks:report.checks.length,completion_statuses:report.completion_statuses}));
if(report.status!=='passed')process.exitCode=1;
