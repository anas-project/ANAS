// TEST_CASES: MAILMOD-T-009
import assert from 'node:assert/strict';
import {test} from 'node:test';
import {phaseFailure,retryDestinationPreflight,recipientProbeFailure} from './phase-diagnostic.mjs';

test('retains only an unambiguous known failure and redacts other output', () => {
  assert.equal(phaseFailure('read-verification', 'private-token\nverification message unavailable\n', ''),
    'isolated route client read-verification failed: verification message unavailable');
  assert.equal(phaseFailure('read-verification', 'private-token', ''),
    'isolated route client read-verification failed');
  assert.equal(phaseFailure('read-verification', 'verification message unavailable\nisolated verification inbox unconfirmed', ''),
    'isolated route client read-verification failed');
  assert.equal(phaseFailure('create-rule', 'verification message unavailable', ''),
    'isolated route client create-rule failed');
  assert.equal(phaseFailure('destination-preflight', 'private-token\ndestination-preflight-unavailable', ''),
    'isolated route client destination-preflight failed: destination-preflight-unavailable');
  assert.equal(phaseFailure('destination-preflight', 'destination-preflight-unavailable destination-preflight-conflict', ''),
    'isolated route client destination-preflight failed');
  assert.equal(phaseFailure('create-destination', 'destination-preflight-unavailable', ''),
    'isolated route client create-destination failed');
});

test('recipient probe preserves only explicit bounded SMTP codes', () => {
  assert.equal(recipientProbeFailure('secret\nsmtp-recipient-rejected:550/5.1.1,450/4.2.0\n'),
    'recipient-preflight-smtp-recipient-rejected:550/5.1.1,450/4.2.0');
  for(const value of ['smtp-pre-data-failed', 'smtp-recipient-rejected:550 private-token', 'smtp-recipient-rejected:250', 'smtp-recipient-rejected:550\nsmtp-recipient-rejected:450', '']) {
    assert.equal(recipientProbeFailure(value), 'recipient-preflight-unconfirmed');
  }
});

test('only retries the read-only unavailable preflight, with a strict bound', async () => {
  const unavailable=()=>Error('isolated route client destination-preflight failed: destination-preflight-unavailable');
  let calls=0,waits=0;
  const result=await retryDestinationPreflight(async()=>{if(++calls<3)throw unavailable();return 'observed'},async ms=>{assert.equal(ms,2000);waits++});
  assert.equal(result,'observed');assert.equal(calls,3);assert.equal(waits,2);
  calls=0;waits=0;
  await assert.rejects(retryDestinationPreflight(async()=>{calls++;throw unavailable()},async()=>{waits++}));
  assert.equal(calls,3);assert.equal(waits,2);
  for(const reason of ['authentication','rate-limited','conflict','identity','unknown']){
    calls=0;waits=0;
    await assert.rejects(retryDestinationPreflight(async()=>{calls++;throw Error(`isolated route client destination-preflight failed: destination-preflight-${reason}`)},async()=>{waits++}));
    assert.equal(calls,1);assert.equal(waits,0);
  }
});
