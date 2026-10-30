// TEST_CASES: MAILMOD-T-009
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {PassThrough} from 'node:stream';
import {confirmationWindow,waitForConfirmation} from './wait-user.mjs';
test('confirmation is bounded by both creation time and maximum wait',()=>{
 assert.equal(confirmationWindow(1000,1000),540000);
 assert.equal(confirmationWindow(1000,101000),490000);
 assert.equal(confirmationWindow(1000,591000),0);
 assert.equal(confirmationWindow(1000,999),0);
 assert.equal(confirmationWindow(NaN,1000),0);
});
test('explicit line may arrive in fragments and releases listeners',async()=>{const s=new PassThrough();const p=waitForConfirmation(s,500);s.write('ver');s.write('ified\n');await p;assert.equal(s.listenerCount('data'),0);assert.equal(s.isPaused(),true)});
test('rejects closed, incorrect and incomplete input',async()=>{
 for(const mode of ['closed','incorrect','timeout']){const s=new PassThrough();const p=waitForConfirmation(s,20);if(mode==='closed')s.end();if(mode==='incorrect')s.write('no\n');if(mode==='timeout')s.write('verified');await assert.rejects(p);assert.equal(s.listenerCount('data'),0);assert.equal(s.isPaused(),true)}
});
test('bounds input',async()=>{const s=new PassThrough();const p=waitForConfirmation(s,100);s.write('x'.repeat(65));await assert.rejects(p,/invalid/)});
