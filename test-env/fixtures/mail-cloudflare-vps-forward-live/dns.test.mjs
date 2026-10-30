// TEST_CASES: MAILMOD-T-009
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,readFile,rm,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {createTestDNS,cleanupTestDNS,testRecords} from './dns.mjs';
const tag='anas-vps-test-0123456789abcdef01234567';
const ids=['a'.repeat(32),'b'.repeat(32)];
async function fixture(fn){const dir=await mkdtemp('/private/tmp/anas-dns-test-');try{return await fn(dir)}finally{await rm(dir,{recursive:true,force:true})}}
function fake(){
 const rows=new Map(), calls=[];let lost=false,foreign=false;
 const api=async(method,path,body)=>{
  calls.push({method,path});
  if(method==='GET'&&path.includes('?')) {
   const name=new URL('https://example.test'+path).searchParams.get('name.exact');
   const data=foreign?[{name}]:[...rows.values()].filter(r=>r.name===name);
   return {success:true,result:data,result_info:{page:1,per_page:100,total_count:data.length,count:data.length}};
  }
  if(method==='POST') {const r={...body,id:ids[rows.size]};rows.set(r.id,r);if(lost)throw Error('lost');return {success:true,result:r}}
  const id=path.split('/').pop();
  if(method==='GET')return rows.has(id)?{success:true,result:rows.get(id)}:{missing:true};
  if(method==='DELETE'){rows.delete(id);return {success:true,result:{id}}}
  throw Error('unexpected call');
 };
 return {api,rows,calls,lose:()=>{lost=true},occupy:()=>{foreign=true}};
}
test('creates exact records and removes only the recorded objects',()=>fixture(async dir=>{
 const f=fake();assert.deepEqual(await createTestDNS(f.api,dir,tag),ids);assert.equal(f.rows.size,2);
 await cleanupTestDNS(f.api,dir,tag);assert.equal(f.rows.size,0);
 assert.equal(f.calls.filter(x=>x.method==='DELETE').length,2);
 await cleanupTestDNS(f.api,dir,tag);assert.equal(f.calls.filter(x=>x.method==='DELETE').length,2);
}));
test('foreign DNS blocks any mutation',()=>fixture(async dir=>{
 const f=fake();f.occupy();await assert.rejects(createTestDNS(f.api,dir,tag),/occupied/);
 assert.equal(f.calls.filter(x=>x.method==='POST').length,0);
}));
test('lost create response is not retried or cleaned up by guessing',()=>fixture(async dir=>{
 const f=fake();f.lose();await assert.rejects(createTestDNS(f.api,dir,tag));
 await assert.rejects(createTestDNS(f.api,dir,tag),/already issued/);
 await assert.rejects(cleanupTestDNS(f.api,dir,tag),/outcome unknown/);
 assert.equal(f.calls.filter(x=>x.method==='POST').length,1);assert.equal(f.calls.filter(x=>x.method==='DELETE').length,0);
}));
test('changed provider record is not deleted',()=>fixture(async dir=>{
 const f=fake();await createTestDNS(f.api,dir,tag);f.rows.get(ids[1]).content='other.example';
 await assert.rejects(cleanupTestDNS(f.api,dir,tag),/identity mismatch/);
 assert.equal(f.calls.filter(x=>x.method==='DELETE').length,0);
}));
test('lost delete response cannot cause a second DELETE',()=>fixture(async dir=>{
 const f=fake();await createTestDNS(f.api,dir,tag);
 let once=true;const api=async(m,p,b)=>{const r=await f.api(m,p,b);if(m==='DELETE'&&once){once=false;throw Error('lost')}return r};
 await assert.rejects(cleanupTestDNS(api,dir,tag));
 await assert.rejects(cleanupTestDNS(api,dir,tag),/deletion outcome unknown/);
 assert.equal(f.calls.filter(x=>x.method==='DELETE').length,1);
}));
test('tampered journal and incomplete listings fail closed',()=>fixture(async dir=>{
 const f=fake();await createTestDNS(f.api,dir,tag);
 const p=join(dir,'MX.created.json'), x=JSON.parse(await readFile(p,'utf8'));x.id='c'.repeat(32);await writeFile(p,JSON.stringify(x));
 await assert.rejects(cleanupTestDNS(f.api,dir,tag));assert.equal(f.calls.filter(x=>x.method==='DELETE').length,0);
 await fixture(async d=>{
  const bad=async()=>({success:true,result:[],result_info:{page:1,per_page:100,total_count:1,count:0}});
  await assert.rejects(createTestDNS(bad,d,tag),/inconsistent/);
 });
}));
test('only fixed isolated DNS records and tags are allowed',()=>{
 assert.throws(()=>testRecords('business'));assert.deepEqual(testRecords(tag).map(x=>x.name),['vps-relay.mail-poc.bengbu.cc.cd','vps-target.mail-poc.bengbu.cc.cd']);
});
