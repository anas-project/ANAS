// TEST_CASES: MAILMOD-T-009
// A public resolver observation is a preflight, not proof of every resolver's cache.
import {spawnSync} from 'node:child_process';
const expected = [
  ['vps-relay.mail-poc.bengbu.cc.cd', 28, '2603:c023:8:a900::a'],
  ['vps-target.mail-poc.bengbu.cc.cd', 15, '10 vps-relay.mail-poc.bengbu.cc.cd.'],
];

// Keep only bounded, non-secret facts useful for diagnosing cache visibility.
export function dnsObservation(name, type, response) {
  const record = expected.find(([n, t]) => n === name && t === type);
  if (!record) throw Error('unexpected isolated DNS query');
  const answers = Array.isArray(response?.Answer)
    ? response.Answer.filter(a => a?.type === type && typeof a.name === 'string' && a.name.replace(/\.$/, '').toLowerCase() === name)
    : [];
  return {type, status: Number.isInteger(response?.Status) ? response.Status : null,
    answers: answers.length, expected: answers.length === 1 && answers[0].data === record[2]};
}

export async function dnsReady(query) {
  for (const [name, type, data] of expected) {
    const response = await query(name, type);
    if (response?.Status !== 0 || !Array.isArray(response.Answer)) return false;
    const answers = response.Answer.filter(value => value?.type === type && typeof value.name === 'string' && value.name.replace(/\.$/, '').toLowerCase() === name);
    if (answers.length !== 1 || answers[0].data !== data) return false;
  }
  return true;
}

export async function queryCloudflareDNS(name, type) {
  const url = new URL('https://cloudflare-dns.com/dns-query');
  url.searchParams.set('name', name);
  url.searchParams.set('type', String(type));
  const response = await fetch(url, {headers: {accept: 'application/dns-json'}, signal: AbortSignal.timeout(5000)});
  if (!response.ok) throw Error('DNS readiness unconfirmed');
  return response.json();
}

// Optional test-only route to the same public resolver through the user's
// explicitly supplied loopback SOCKS proxy. No credential enters curl args.
export function queryCloudflareDNSViaSocks(name, type, port) {
  if (!expected.some(([n,t]) => n === name && t === type) ||
      !Number.isInteger(port) || port < 1 || port > 65535) throw Error('invalid isolated DNS proxy query');
  const url = new URL('https://cloudflare-dns.com/dns-query');
  url.searchParams.set('name', name);
  url.searchParams.set('type', String(type));
  const response = spawnSync('/usr/bin/curl', ['--silent','--show-error','--fail',
    '--proto','=https','--socks5-hostname',`127.0.0.1:${port}`,'--noproxy','',
    '--max-time','8','--header','accept: application/dns-json',url.href],
    {encoding:'utf8',timeout:10000,maxBuffer:65536});
  if (response.status !== 0 || response.error) throw Error('DNS readiness unconfirmed');
  return JSON.parse(response.stdout);
}
