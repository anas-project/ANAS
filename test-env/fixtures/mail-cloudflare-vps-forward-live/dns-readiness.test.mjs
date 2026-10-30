// TEST_CASES: MAILMOD-T-009
import assert from 'node:assert/strict';
import {test} from 'node:test';
import {dnsReady, dnsObservation, queryCloudflareDNSViaSocks} from './dns-readiness.mjs';

function answer(name, type, data) { return {Status: 0, Answer: [{name: `${name}.`, type, data}]}; }
const good = async (name, type) => answer(name, type,
  type === 28 ? '2603:c023:8:a900::a' : '10 vps-relay.mail-poc.bengbu.cc.cd.');

test('requires both exact public MX and relay AAAA records', async () => {
  assert.equal(await dnsReady(good), true);
  assert.equal(await dnsReady(async (name,type) => {const r=await good(name,type);r.Answer[0].name=name.toUpperCase();return r;}), true);
  assert.equal(await dnsReady(async (name,type) => type === 28 ? answer(name,type,'::1') : good(name,type)), false);
  assert.equal(await dnsReady(async (name,type) => type === 15 ? {Status: 3} : good(name,type)), false);
  assert.equal(await dnsReady(async (name,type) => type === 15 ? answer('foreign.example',type,'10 vps-relay.mail-poc.bengbu.cc.cd.') : good(name,type)), false);
  assert.equal(await dnsReady(async (name,type) => {const r=await good(name,type);r.Answer.push(r.Answer[0]);return r;}), false);
});

test('diagnostics distinguish negative cache from the exact public record', () => {
  const name = 'vps-target.mail-poc.bengbu.cc.cd';
  assert.deepEqual(dnsObservation(name, 15, {Status: 3}), {type:15,status:3,answers:0,expected:false});
  assert.deepEqual(dnsObservation(name, 15, answer(name,15,'10 vps-relay.mail-poc.bengbu.cc.cd.')),
    {type:15,status:0,answers:1,expected:true});
  assert.throws(() => dnsObservation('foreign.example',15,{Status:0}));
});

test('DNS proxy is confined to the isolated names and loopback port', () => {
  assert.throws(() => queryCloudflareDNSViaSocks('foreign.example',15,17142));
  assert.throws(() => queryCloudflareDNSViaSocks('vps-target.mail-poc.bengbu.cc.cd',15,65536));
  assert.throws(() => queryCloudflareDNSViaSocks('vps-target.mail-poc.bengbu.cc.cd',15,'17142'));
});
