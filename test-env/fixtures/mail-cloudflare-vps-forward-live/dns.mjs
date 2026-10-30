// TEST_CASES: MAILMOD-T-009
// Isolated fixture only. The caller supplies a Cloudflare API transport and an
// existing, private, persistent journal directory. Unknown writes are never
// retried or adopted automatically.
import {open, readFile, lstat} from 'node:fs/promises';
import {join, isAbsolute} from 'node:path';
import {randomBytes} from 'node:crypto';

const names = {
  AAAA: 'vps-relay.mail-poc.bengbu.cc.cd',
  MX: 'vps-target.mail-poc.bengbu.cc.cd',
};
const zone = '0d84033826bc3cd0c283efdb57a90f7a';
const idPattern = /^[a-f0-9]{32}$/;
const tagPattern = /^anas-vps-test-[a-f0-9]{24}$/;

export function testRecords(tag) {
  if (!tagPattern.test(tag)) throw Error('invalid test tag');
  return [
    {type:'AAAA', name:names.AAAA, content:'2603:c023:8:a900::a', ttl:120, proxied:false, comment:tag},
    {type:'MX', name:names.MX, content:names.AAAA, ttl:120, priority:10, comment:tag},
  ];
}

async function privateDirectory(dir) {
  if (!isAbsolute(dir)) throw Error('absolute private journal required');
  const s = await lstat(dir);
  if (!s.isDirectory() || (s.mode & 0o077) !== 0 || (process.getuid && s.uid !== process.getuid()))
    throw Error('private owned journal required');
}
async function persist(dir, file, body) {
  const f = await open(join(dir,file),'wx',0o600);
  try { await f.writeFile(JSON.stringify(body)); await f.sync(); } finally { await f.close(); }
  const d = await open(dir,'r');
  try { await d.sync(); } finally { await d.close(); }
}
async function load(dir,file) {
  try { return JSON.parse(await readFile(join(dir,file),'utf8')); }
  catch (e) { if (e.code === 'ENOENT') return null; throw Error('journal unreadable'); }
}
function same(record, expected, id) {
  return record && record.id === id && idPattern.test(id) && record.type === expected.type &&
    record.name === expected.name && record.content === expected.content && record.ttl === expected.ttl &&
    record.comment === expected.comment && (expected.type !== 'AAAA' || record.proxied === false) &&
    (expected.type !== 'MX' || record.priority === expected.priority);
}
function result(response) {
  if (!response || response.success !== true || !response.result) throw Error('provider result unconfirmed');
  return response.result;
}
async function absent(api, name) {
  // Query all types for the exact name, including CNAME and records owned by
  // someone else. Conflicts must not be silently adopted or overwritten.
  let total;
  for (let page=1; page<=100; page++) {
    const body = await api('GET',`/zones/${zone}/dns_records?name.exact=${encodeURIComponent(name)}&per_page=100&page=${page}`);
    const rows = result(body), info = body.result_info;
    if (!Array.isArray(rows) || !info || info.page !== page || info.per_page !== 100 ||
        !Number.isInteger(info.total_count) || info.total_count < 0 || !Number.isInteger(info.count) ||
        info.count !== rows.length || rows.length > 100 || (total !== undefined && total !== info.total_count))
      throw Error('provider list incomplete');
    total = info.total_count;
    if (rows.some(r => typeof r.name !== 'string' || r.name.toLowerCase() === name)) throw Error('DNS name occupied');
    if (rows.length || total !== 0) throw Error('provider list inconsistent');
    return;
  }
  throw Error('provider list incomplete');
}

// A failed or interrupted write leaves an issued journal. The operator must
// reconcile it manually; no later invocation may turn that into a fresh POST.
export async function createTestDNS(api, dir, tag) {
  await privateDirectory(dir);
  const records = testRecords(tag);
  for (const r of records) {
    if (await load(dir,`${r.type}.issued.json`)) throw Error('DNS creation already issued');
    await absent(api,r.name);
  }
  const created=[];
  for (const r of records) {
    await persist(dir,`${r.type}.issued.json`,r);
    const createdRecord = result(await api('POST',`/zones/${zone}/dns_records`,r));
    const id = createdRecord.id;
    if (!same(createdRecord,r,id)) throw Error('DNS creation response unconfirmed');
    // Save the ID before the readback so a read failure remains recoverable.
    await persist(dir,`${r.type}.created.json`,{id, record:r});
    const observed = result(await api('GET',`/zones/${zone}/dns_records/${id}`));
    if (!same(observed,r,id)) throw Error('DNS readback mismatch');
    await persist(dir,`${r.type}.confirmed.json`,{id, record:r});
    created.push(id);
  }
  return created;
}

export async function cleanupTestDNS(api, dir, tag) {
  await privateDirectory(dir);
  for (const r of testRecords(tag).reverse()) {
    const issued = await load(dir,`${r.type}.issued.json`);
    if (!issued) continue;
    if (JSON.stringify(issued) !== JSON.stringify(r)) throw Error('DNS intent mismatch');
    const saved = await load(dir,`${r.type}.created.json`);
    if (!saved || !same({...saved.record,id:saved.id},r,saved.id)) throw Error('DNS creation outcome unknown');
    if (await load(dir,`${r.type}.deleted.json`)) continue;
    if (await load(dir,`${r.type}.delete-issued.json`)) throw Error('DNS deletion outcome unknown');
    const observed = result(await api('GET',`/zones/${zone}/dns_records/${saved.id}`));
    if (!same(observed,r,saved.id)) throw Error('DNS cleanup identity mismatch');
    await persist(dir,`${r.type}.delete-issued.json`,{id:saved.id,record:r});
    const deleted = result(await api('DELETE',`/zones/${zone}/dns_records/${saved.id}`));
    if (deleted.id !== saved.id) throw Error('DNS deletion response unconfirmed');
    const missing = await api('GET',`/zones/${zone}/dns_records/${saved.id}`);
    if (!missing || missing.missing !== true) throw Error('DNS deletion readback unconfirmed');
    await persist(dir,`${r.type}.deleted.json`,{id:saved.id,record:r});
  }
}

// The transport does not print provider responses, authorization headers or
// tokens. A non-2xx, timeout, redirect, malformed body or network error is an
// unknown outcome for mutations and is not retried.
export function cloudflareDNS(token) {
  if (typeof token !== 'string' || !/^[A-Za-z0-9_-]{20,200}$/.test(token)) throw Error('invalid token');
  return async (method,path,body) => {
    if (!['GET','POST','DELETE'].includes(method) || !path.startsWith(`/zones/${zone}/dns_records`)) throw Error('invalid request');
    let response;
    try {
      response = await fetch('https://api.cloudflare.com/client/v4'+path,{method,redirect:'error',signal:AbortSignal.timeout(30000),headers:{Authorization:`Bearer ${token}`,'Content-Type':'application/json'},body:body ? JSON.stringify(body) : undefined});
      if (method === 'GET' && response.status === 404 && !path.includes('?')) return {missing:true};
      if (!response.ok) throw Error();
      const text = await response.text();
      if (text.length > 1048576) throw Error();
      return JSON.parse(text);
    } catch { throw Error('Cloudflare DNS request unconfirmed'); }
  };
}
export const newTestTag=()=>`anas-vps-test-${randomBytes(12).toString('hex')}`;
