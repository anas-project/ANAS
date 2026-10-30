// TEST_CASES: MAILMOD-T-009 MAILMOD-T-080
import assert from 'node:assert/strict';
import {mkdtemp,readFile,rm,stat} from 'node:fs/promises';
import {spawnSync} from 'node:child_process';
import {join} from 'node:path';
import {nativeReadyPayload,nativeReadyWriter} from './native-signal.mjs';

const dir=await mkdtemp('/private/tmp/anas-native-signal-');
try {
  const path=join(dir,'public-ready.json');
  const command=nativeReadyWriter(path);
  const input=nativeReadyPayload('verification-1','message-2');
  let result=spawnSync('sh',['-c',command],{input});
  assert.equal(result.status,0,'fresh signal must publish');
  assert.deepEqual(JSON.parse(await readFile(path,'utf8')),{VerificationID:'verification-1',MessageID:'message-2'});
  assert.equal((await stat(path)).mode&0o077,0);
  result=spawnSync('sh',['-c',command],{input:nativeReadyPayload('verification-1','message-3')});
  assert.notEqual(result.status,0,'existing signal must not be replaced');
  assert.equal(JSON.parse(await readFile(path,'utf8')).MessageID,'message-2');
  assert.throws(()=>nativeReadyPayload('same','same'));
  assert.throws(()=>nativeReadyPayload('a\nsecret','other'));
  assert.throws(()=>nativeReadyWriter('/tmp/../other/public-ready.json'));
  assert.throws(()=>nativeReadyWriter('/tmp/x;echo-secret/public-ready.json'));
  console.log('PASS private one-time native signal');
} finally {
  await rm(dir,{recursive:true,force:true});
}
