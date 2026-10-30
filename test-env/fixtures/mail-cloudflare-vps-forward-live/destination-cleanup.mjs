// TEST_CASES: MAILMOD-T-009
// Test-only cleanup for a recorded Cloudflare destination. Never adopt an
// address by searching the account after an uncertain creation.
import {open,readFile,lstat} from 'node:fs/promises';
import {join,isAbsolute} from 'node:path';
const account='aa7aa9753a4fffabb1d6536bcf62fb95';
const idPattern=/^[a-f0-9]{32}$/;
const addressPattern=/^cf-vps-test-[a-f0-9]{12}@mail-poc\.bengbu\.cc\.cd$/;
const targetPattern=/^anas-[a-f0-9]{32}@vps-target\.mail-poc\.bengbu\.cc\.cd$/;
async function read(dir,name){try{return JSON.parse(await readFile(join(dir,name),'utf8'))}catch(e){if(e.code==='ENOENT')return null;throw Error('test destination journal unreadable')}}
async function record(dir,name,data){const f=await open(join(dir,name),'wx',0o600);try{await f.writeFile(JSON.stringify(data));await f.sync()}finally{await f.close()}const d=await open(dir,'r');try{await d.sync()}finally{await d.close()}}
export async function cleanupDestination(api,dir,expected){
 if(!isAbsolute(dir))throw Error('absolute private journal required');
 const stat=await lstat(dir);if(!stat.isDirectory()||(stat.mode&0o077)!==0||(process.getuid&&stat.uid!==process.getuid()))throw Error('private owned journal required');
 if(!expected||!addressPattern.test(expected.address)||!targetPattern.test(expected.target)||!idPattern.test(expected.id))throw Error('invalid destination binding');
 const issued=await read(dir,'destination-issued.json'),created=await read(dir,'create-destination.json');
 if(!issued||issued.address!==expected.address||issued.target!==expected.target||!created||created.phase!=='create-destination'||created.destination_id!==expected.id)throw Error('destination creation outcome unknown');
 if(await read(dir,'rule-issued.json')&&!await read(dir,'rule-deleted.json'))throw Error('rule absence unconfirmed');
 if(await read(dir,'destination-deleted.json'))return;
 if(await read(dir,'destination-delete-issued.json'))throw Error('destination deletion outcome unknown');
 const path=`/accounts/${account}/email/routing/addresses/${expected.id}`;
 const observed=await api('GET',path);
 if(!observed||observed.success!==true||!observed.result||observed.result.id!==expected.id||observed.result.email!==expected.target||(observed.result.tag&&observed.result.tag!==expected.id))throw Error('destination cleanup identity mismatch');
 await record(dir,'destination-delete-issued.json',expected);
 const deleted=await api('DELETE',path);
 if(deleted?.rejected===true && deleted.status===429){await record(dir,'destination-delete-rejected.json',{id:expected.id,status:429});throw Error('destination deletion rate limited')}
 if(!deleted||deleted.success!==true||!deleted.result||(deleted.result.id!==expected.id&&deleted.result.tag!==expected.id))throw Error('destination deletion response unconfirmed');
 const missing=await api('GET',path);if(!missing||missing.missing!==true)throw Error('destination deletion readback unconfirmed');
 await record(dir,'destination-deleted.json',expected);
}
export function cloudflareDestination(token){
 if(typeof token!=='string'||!/^[A-Za-z0-9_-]{20,200}$/.test(token))throw Error('invalid token');
 return async(method,path)=>{
  if(!['GET','DELETE'].includes(method)||!new RegExp(`^/accounts/${account}/email/routing/addresses/[a-f0-9]{32}$`).test(path))throw Error('invalid request');
  try{const r=await fetch('https://api.cloudflare.com/client/v4'+path,{method,redirect:'error',signal:AbortSignal.timeout(30000),headers:{Authorization:`Bearer ${token}`}});
   if(method==='GET'&&r.status===404)return {missing:true};if(method==='DELETE'&&r.status===429)return {rejected:true,status:429};if(!r.ok)throw Error();const text=await r.text();if(text.length>65536)throw Error();return JSON.parse(text);
  }catch{throw Error('Cloudflare destination request unconfirmed')}
 };
}
