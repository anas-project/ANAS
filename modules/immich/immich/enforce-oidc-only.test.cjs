"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const vm = require("node:vm");
const { patchSource, rules } = require("./enforce-oidc-only.cjs");

function fixture(name) {
  if (name === 'auth.service.js') {
    return 'class Service {\n    async backchannelLogout() {\n'+rules[name][0][0]+'\n    }\n'+rules[name].slice(1).map(([needle])=>needle+"\n        return 'upstream';\n    }").join('\n')+'\n}\nService;';
  }
  return "class Service {\n" + rules[name].map(([needle]) => needle + "\n        return 'upstream';\n    }").join("\n") + "\n}\nService;";
}
class ForbiddenException extends Error {}
const fakeRequire = () => ({ ForbiddenException });

test("all confirmed account/password/binding write paths reject before mutation", async () => {
  for (const name of Object.keys(rules).filter((name)=>!name.startsWith("../"))) {
    const Service = vm.runInNewContext(patchSource(name,fixture(name)), { require: fakeRequire });
    const service = new Service();
    if (name === "base.service.js") {
      for (const dto of [{},{oauthId:""},{oauthId:"anchor",password:"secret"}]) {
        await assert.rejects(service.createUser(dto),/immutable OIDC accounts/);
      }
      assert.equal(await service.createUser({oauthId:"anchor",isAdmin:true}),"upstream");
    } else if (name === "auth.service.js") {
      await assert.rejects(service.changePassword({},{}),/immutable OIDC accounts/);
      await assert.rejects(service.link({}, {}, {}),/immutable OIDC accounts/);
      await assert.rejects(service.unlink({}),/immutable OIDC accounts/);
    } else if (name === "auth-admin.service.js") {
      await assert.rejects(service.unlinkAll({}),/immutable OIDC accounts/);
    } else if (name === "user-admin.service.js") {
      await assert.rejects(service.update({},"id",{password:"secret"}),/immutable OIDC accounts/);
      assert.equal(await service.update({},"id",{name:"Label"}),"upstream");
    } else {
      await assert.rejects(service.updateMe({user:{}},{password:"secret"}),/immutable OIDC accounts/);
      assert.equal(await service.updateMe({user:{}},{name:"Label"}),"upstream");
    }
  }
});
test("unknown, missing, duplicate and already patched upstream anchors fail closed", () => {
  for (const name of Object.keys(rules)) {
    assert.throws(()=>patchSource(name,"class Service {}"),/expected one/);
    assert.throws(()=>patchSource(name,fixture(name)+fixture(name)),/expected one/);
    assert.throws(()=>patchSource(name,patchSource(name,fixture(name))),/already contains/);
  }
  assert.throws(()=>patchSource("new.service.js",""),/unknown patch target/);
});

test("confirmed concurrent registration defect is guarded by an idempotent pre-listen unique constraint", () => {
  const name="../repositories/database.repository.js";
  const source=patchSource(name,rules[name][0][0]);
  assert.match(source,/ADD CONSTRAINT.*user_oauthId_uq.*UNIQUE/);
  assert.match(source,/pg_constraint/);
  assert.ok(source.indexOf("sql.raw")<source.indexOf("Finished running migrations"));
  const table=patchSource("../schema/tables/user.table.js",rules["../schema/tables/user.table.js"][0][0]);
  assert.match(table,/default: '', unique: true/);
});
test('verified logout jti is consumed atomically with session deletion and survives process restart', () => {
  const repository='../repositories/session.repository.js';
  const patched=patchSource(repository,rules[repository].map(([needle])=>needle).join('\n'));
  assert.match(patched,/transaction\(\)\.execute/);
  assert.match(patched,/ON CONFLICT DO NOTHING RETURNING jti/);
  assert.match(patched,/now\(\) \+ interval '5 minutes'/);
  assert.match(patched,/clock_timestamp\(\).*issuedAt.*\+ 126/);
  assert.ok(patched.indexOf('consumed.rows.length')<patched.indexOf("db.deleteFrom('session')"));
  const oauth='../repositories/oauth.repository.js';
  assert.match(patchSource(oauth,rules[oauth].map(([needle])=>needle).join('\n')),/requires jti and issued-at claims/);
});

test('logout receipt includes the entire JOSE tolerance window and rejects delayed requests before deleting sessions', async () => {
  const repository='../repositories/session.repository.js';
  const upstream=`class Repository {
${rules[repository][0][0]}
        if (oauthId) query = query.where('user.oauthId', '=', oauthId);
${rules[repository][1][0]} { return undefined; }
}
Repository;`;
  const issuedAt=2_000_000_000;
  for (const [receivedAt,expiresAt,valid] of [
    [issuedAt+125.9,undefined,true], [issuedAt+126,undefined,false],
    [issuedAt+301,undefined,false], [issuedAt-6,undefined,false],
    [issuedAt+34.999,issuedAt+30,true], [issuedAt+35,issuedAt+30,false],
    [issuedAt+35.999,issuedAt+30.25,true], [issuedAt+36,issuedAt+30.25,false],
    [issuedAt+60,issuedAt+30,false], [issuedAt+125.9,issuedAt+300,true],
  ]) {
    const statements=[];
    let deleteCount=0;
    const db={
      transaction:()=>({execute:async(callback)=>callback(db)}),
      deleteFrom:()=>{deleteCount++;const query={returning:()=>query,where:()=>query,execute:async()=>[{id:'prior-session'}]};return query;},
    };
    const sql=(strings,...values)=>({execute:async()=>{
      const statement=strings.join('?');statements.push({statement,values});
      if(statement.startsWith('INSERT')) return {rows:[{jti:'token'}]};
      if(statement.startsWith('SELECT')) {
        assert.deepEqual(values,[issuedAt,issuedAt,expiresAt ?? null,expiresAt ?? null]);
        assert.match(statement,/floor\(EXTRACT\(EPOCH FROM clock_timestamp\(\)\)\) - 5/);
        return {rows:[{valid:receivedAt>=issuedAt-5 && receivedAt<issuedAt+126 && (expiresAt === undefined || expiresAt > Math.floor(receivedAt)-5)}]};
      }
      return {rows:[]};
    }});
    class BadRequestException extends Error {}
    const Repository=vm.runInNewContext(patchSource(repository,upstream),{require:(name)=>name==='kysely'?{sql}:{BadRequestException}});
    const operation=Repository.prototype.invalidateOAuth.call({db},{oauthId:'anchor',logoutToken:{issuer:'issuer',audience:'immich',jti:'token',issuedAt,expiresAt}});
    if(valid) assert.deepEqual([...await operation],['prior-session']);
    else await assert.rejects(operation,/expired before session invalidation/);
    assert.equal(deleteCount,valid?1:0);
    assert.ok(statements[1].statement.includes("now() + interval '5 minutes'"));
  }
  for (const expiresAt of [null,'2030000000',true,NaN,Infinity]) {
    const Repository=vm.runInNewContext(patchSource(repository,upstream),{require:()=>({BadRequestException:class extends Error {}})});
    await assert.rejects(()=>Repository.prototype.invalidateOAuth.call({db:{transaction:()=>{throw new Error('unexpected DB access');}}},{oauthId:'anchor',logoutToken:{issuer:'issuer',audience:'immich',jti:'token',issuedAt,expiresAt}}),/Invalid logout token replay claims/);
  }
});

test('verified logout claims reject nonce presence and non-object events', () => {
  const target='../repositories/oauth.repository.js';
  const upstream=`class Repository { validate(payload) { const events=payload.events;
${rules[target][1][0]}
} }
Repository;`;
  const logoutRule=rules[target][1];
  const Repository=vm.runInNewContext(upstream.replace(logoutRule[0],logoutRule[1]));
  const valid={sub:'anchor',iss:'issuer',jti:'token',iat:2_000_000_000,events:{'http://schemas.openid.net/event/backchannel-logout':{}}};
  assert.equal(new Repository().validate(valid).sub,'anchor');
  assert.equal(new Repository().validate({...valid,exp:2_000_000_030.25}).expiresAt,2_000_000_030.25);
  for (const nonce of ['',null,false]) {
    assert.throws(()=>new Repository().validate({...valid,nonce}),/must not contain a nonce/);
  }
  for (const events of [null,[], 'wrong-type',{'http://schemas.openid.net/event/backchannel-logout':'wrong-type'},{'http://schemas.openid.net/event/backchannel-logout':[]},{'http://schemas.openid.net/event/backchannel-logout':{unexpected:true}}]) {
    assert.throws(()=>new Repository().validate({...valid,events}),/requires a backchannel-logout event object/);
  }
  for (const target of [{sub:null},{sub:[]},{sub:' '},{sub:undefined,sid:undefined},{sid:42}]) {
    assert.throws(()=>new Repository().validate({...valid,...target}),/valid subject or session identifier/);
  }
});

test('directory revocation requires a policy event, fixed subject and original event time', () => {
  const target='../repositories/oauth.repository.js';
  const logoutRule=rules[target][1];
  const Repository=vm.runInNewContext(`class Repository { validate(payload) { const events=payload.events;
${logoutRule[1]}
} } Repository;`);
  const eventName='https://schemas.openid.net/secevent/caep/event-type/session-revoked';
  const issuedAt=2_000_000_000;
  const valid={sub:'anchor',iss:'issuer',jti:'token',iat:issuedAt,
    sub_id:{format:'iss_sub',iss:'issuer',sub:'anchor'},
    events:{'http://schemas.openid.net/event/backchannel-logout':{},[eventName]:{initiating_entity:'policy',event_timestamp:issuedAt-0.25}}};
  assert.equal(new Repository().validate(valid).revokedBefore,issuedAt-0.25);
  for (const changes of [{sid:'sid'},{sid:null},{sub:''},{sub_id:{format:'iss_sub',iss:'other',sub:'anchor'}},{sub_id:{format:'iss_sub',iss:'issuer',sub:'other'}},{sub_id:null}]) {
    assert.throws(()=>new Repository().validate({...valid,...changes}),/Invalid directory session-revoked event|valid subject or session identifier/);
  }
  for (const event of [null,[],{initiating_entity:'user',event_timestamp:issuedAt},{initiating_entity:'policy',event_timestamp:issuedAt+6},{initiating_entity:'policy',event_timestamp:'2000000000'},{initiating_entity:'policy',event_timestamp:0}]) {
    assert.throws(()=>new Repository().validate({...valid,events:{...valid.events,[eventName]:event}}),/Invalid directory session-revoked event/);
  }
});
