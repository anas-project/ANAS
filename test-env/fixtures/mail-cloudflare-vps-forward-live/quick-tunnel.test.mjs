// TEST_CASES: MAILMOD-T-009 MAILMOD-T-080
import assert from 'node:assert/strict';
import {test} from 'node:test';
import {quickTunnelOrigin} from './quick-tunnel.mjs';
test('recognizes only the framed provisioned tunnel endpoint',()=>{
 assert.equal(quickTunnelOrigin('2026-10-28T21:33:40Z INF |  https://review-directories-participated-fitness.trycloudflare.com  |'),'https://review-directories-participated-fitness.trycloudflare.com');
 for(const line of ['failed to request quick Tunnel: Post "https://api.trycloudflare.com/tunnel": timeout','| https://api.trycloudflare.com |','https://review-directories.trycloudflare.com','| https://evil.example |'])assert.equal(quickTunnelOrigin(line),null);
});
