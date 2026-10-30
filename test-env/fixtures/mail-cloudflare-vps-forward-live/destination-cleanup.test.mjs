// TEST_CASES: MAILMOD-T-009
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,rm} from 'node:fs/promises';
import {join} from 'node:path';
import {cleanupDestination} from './destination-cleanup.mjs';
const expected={id:'a'.repeat(32),address:'cf-vps-test-012345abcdef@mail-poc.bengbu.cc.cd',target:'anas-'+'b'.repeat(32)+'@vps-target.mail-poc.bengbu.cc.cd'};
async function fixture(fn){const d=await mkdtemp('/private/tmp/anas-dest-test-');try{await writeFile(join(d,'destination-issued.json'),JSON.stringify({address:expected.address,target:expected.target}));await writeFile(join(d,'create-destination.json'),JSON.stringify({phase:'create-destination',destination_id:expected.id}));return await fn(d)}finally{await rm(d,{recursive:true,force:true})}}
function provider(){let exists=true,changed=false,lost=false;const calls=[];return {calls,change:()=>changed=true,lose:()=>lost=true,api:async(method)=>{calls.push(method);if(method==='GET')return exists?{success:true,result:{id:expected.id,email:changed?'foreign@example.test':expected.target}}:{missing:true};if(method==='DELETE'){exists=false;if(lost)throw Error('lost');return {success:true,result:{id:expected.id}}}throw Error('unexpected')}}}
test('removes only exact recorded test target and independently reads absence',()=>fixture(async d=>{const p=provider();await cleanupDestination(p.api,d,expected);assert.deepEqual(p.calls,['GET','DELETE','GET']);await cleanupDestination(p.api,d,expected);assert.equal(p.calls.length,3)}));
test('does not delete changed target',()=>fixture(async d=>{const p=provider();p.change();await assert.rejects(cleanupDestination(p.api,d,expected),/identity mismatch/);assert.deepEqual(p.calls,['GET'])}));
test('does not retry a deletion with a lost response',()=>fixture(async d=>{const p=provider();p.lose();await assert.rejects(cleanupDestination(p.api,d,expected));await assert.rejects(cleanupDestination(p.api,d,expected),/outcome unknown/);assert.deepEqual(p.calls,['GET','DELETE'])}));
test('does not delete when rule cleanup or destination creation is unknown',()=>fixture(async d=>{const p=provider();await writeFile(join(d,'rule-issued.json'),'{}');await assert.rejects(cleanupDestination(p.api,d,expected),/rule absence unconfirmed/);assert.equal(p.calls.length,0)}));
test('rejects foreign address and non-private directories',()=>fixture(async d=>{const p=provider();await assert.rejects(cleanupDestination(p.api,d,{...expected,target:'someone@example.test'}),/invalid destination/);assert.equal(p.calls.length,0);await assert.rejects(cleanupDestination(p.api,'/tmp',expected),/private owned journal/)}));

test('records explicit rate limit and never repeats automatically',()=>fixture(async d=>{
 const p=provider();const api=async(m,path)=>m==='DELETE'?{rejected:true,status:429}:p.api(m,path);
 await assert.rejects(cleanupDestination(api,d,expected),/rate limited/);
 await assert.rejects(cleanupDestination(api,d,expected),/outcome unknown/);
}));
