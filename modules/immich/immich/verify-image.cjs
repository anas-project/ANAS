"use strict";
// Executes actual compiled v3.2.4 methods inside the patched image. Repository
// doubles isolate callback rules; this does not claim real OIDC/PG acceptance.
const assert = require("node:assert/strict");
const root = "/usr/src/app/server/dist/services/";
const { AuthService } = require(root+"auth.service.js");
const { BaseService } = require(root+"base.service.js");
const { UserAdminService } = require(root+"user-admin.service.js");
const { UserService } = require(root+"user.service.js");
const { AuthAdminService } = require(root+"auth-admin.service.js");
const { AssetMediaService } = require(root+"asset-media.service.js");
const { OAuthRepository } = require("/usr/src/app/server/dist/repositories/oauth.repository.js");
const { SessionRepository } = require("/usr/src/app/server/dist/repositories/session.repository.js");

async function main() {
  // Execute the real compiled upload failure path: both enqueue operations
  // fail, yet the newly created checksum row must be removed before cleanup.
  const calls=[];
  const uploadContext={
    requireAccess:async()=>{},requireQuota(){},
    assetRepository:{create:async()=>({id:'new-failed-asset'}),upsertExif:async()=>{},remove:async({id})=>{assert.equal(id,'new-failed-asset');calls.push('rollback');}},
    storageRepository:{utimes:async()=>{}},
    jobRepository:{queue:async()=>{calls.push('queue');throw new Error('queue cannot write');}},
    logger:{error(){},warn(){}},
  };
  await assert.rejects(()=>AssetMediaService.prototype.uploadAsset.call(uploadContext,{user:{id:'user'}},{fileCreatedAt:'2026-10-03',fileModifiedAt:'2026-10-03'},{originalPath:'/data/upload/test.png',size:1,checksum:Buffer.from('fixture'),originalName:'test.png'}),/queue cannot write/);
  assert.deepEqual(calls,['queue','rollback','queue']);
  // Use a real signature and the actual compiled JOSE/native validator. This
  // isolates protocol validation; full HTTP/PG checks belong to integration.py.
  const { SignJWT } = await import("/usr/src/app/server/node_modules/jose/dist/webapi/index.js");
  const clientSecret="compiled-method-fixture-key-with-sufficient-bytes";
  const logoutLogs=[];
  const oauthContext={
    getClient:async()=>({clientMetadata:()=>({id_token_signed_response_alg:"HS256"}),serverMetadata:()=>({issuer:"fixture-issuer"})}),
    logger:{error:(...args)=>logoutLogs.push(args),warn:(...args)=>logoutLogs.push(args)},
  };
  for (const extra of [{nonce:""},{nonce:null},{events:{"http://schemas.openid.net/event/backchannel-logout":"wrong-type"}},{events:{"http://schemas.openid.net/event/backchannel-logout":[]}}]) {
    const token=await new SignJWT({sub:"anchor",jti:"fixture-token",events:{"http://schemas.openid.net/event/backchannel-logout":{}},...extra})
      .setProtectedHeader({alg:"HS256"}).setIssuer("fixture-issuer").setAudience("immich").setIssuedAt().sign(new TextEncoder().encode(clientSecret));
    await assert.rejects(()=>OAuthRepository.prototype.validateLogoutToken.call(oauthContext,{clientId:"immich",clientSecret},token),/Error validating JWT logout token/);
  }
  const privateMarker='test-only-logout-payload-must-stay-private';
  const wrongAudience=await new SignJWT({sub:'private-test-sub',jti:'private-test-jti',privateMarker,events:{'http://schemas.openid.net/event/backchannel-logout':{}}})
    .setProtectedHeader({alg:'HS256'}).setIssuer('fixture-issuer').setAudience('other-client').setIssuedAt().sign(new TextEncoder().encode(clientSecret));
  await assert.rejects(()=>OAuthRepository.prototype.validateLogoutToken.call(oauthContext,{clientId:'immich',clientSecret},wrongAudience),(error)=>error.message==='Error validating JWT logout token' && error.cause===undefined);
  const logText=JSON.stringify(logoutLogs);
  for(const secret of [wrongAudience,privateMarker,'private-test-sub','private-test-jti']) assert.equal(logText.includes(secret),false);
  // Execute actual native JOSE -> service -> repository methods. A simulated
  // lock delay must honour a shorter exp and its fractional NumericDate;
  // these DB doubles do not replace the real PostgreSQL waiting fixture.
  const issuedAt=Math.floor(Date.now()/1000);
  const expiresAt=issuedAt+30.25;
  const shortExpiry=await new SignJWT({sub:'anchor',jti:'short-expiry',events:{'http://schemas.openid.net/event/backchannel-logout':{}}})
    .setProtectedHeader({alg:'HS256'}).setIssuer('fixture-issuer').setAudience('immich').setIssuedAt(issuedAt).setExpirationTime(expiresAt).sign(new TextEncoder().encode(clientSecret));
  const config={enabled:true,clientId:'immich',clientSecret};
  assert.equal((await OAuthRepository.prototype.validateLogoutToken.call(oauthContext,config,shortExpiry)).expiresAt,expiresAt);
  const Module=require('node:module');
  const originalLoad=Module._load;
  const nativeKysely=require('/usr/src/app/server/node_modules/kysely');
  for(const [receivedAt,valid] of [[issuedAt+35.999,true],[issuedAt+36,false]]) {
    let consumed=0,deleted=0;
    const db={
      transaction:()=>({execute:async(work)=>{
        const initial=consumed;
        try{return await work(db);}catch(error){consumed=initial;throw error;}
      }}),
      deleteFrom:()=>{
        deleted++;
        const query={returning:()=>query,where:()=>query,whereRef:()=>query,using:()=>query,execute:async()=>[{id:'prior-session'}]};
        return query;
      },
    };
    const sql=(strings,...values)=>({execute:async()=>{
      const statement=strings.join('?');
      if(statement.startsWith('INSERT')){consumed++;return {rows:[{jti:'short-expiry'}]};}
      if(statement.startsWith('SELECT')){
        assert.deepEqual(values,[issuedAt,issuedAt,expiresAt,expiresAt]);
        assert.match(statement,/floor\(EXTRACT\(EPOCH FROM clock_timestamp\(\)\)\) - 5/);
        return {rows:[{valid:receivedAt>=issuedAt-5 && receivedAt<issuedAt+126 && expiresAt>Math.floor(receivedAt)-5}]};
      }
      return {rows:[]};
    }});
    const service={
      getConfig:async()=>({oauth:config}),logger:{error(){}},
      oauthRepository:{validateLogoutToken:(...args)=>OAuthRepository.prototype.validateLogoutToken.call(oauthContext,...args)},
      sessionRepository:{invalidateOAuth:(args)=>{
        assert.equal(args.logoutToken.expiresAt,expiresAt);
        return SessionRepository.prototype.invalidateOAuth.call({db},args);
      }},
      websocketRepository:{clientDisconnect(){}},eventRepository:{emit:async()=>{}},
    };
    Module._load=function(name,...args){return name==='kysely'?{...nativeKysely,sql}:originalLoad.call(this,name,...args);};
    try{
      const operation=AuthService.prototype.backchannelLogout.call(service,{logout_token:shortExpiry});
      if(valid) await operation;
      else await assert.rejects(operation,(error)=>error.getStatus()===400 && /expired before session invalidation/.test(error.message));
      assert.equal(deleted,valid?1:0);
      assert.equal(consumed,valid?1:0);
    }finally{Module._load=originalLoad;}
  }
  console.log('PASS: actual JOSE/service/repository methods preserve fractional exp, reject post-wait expiry before session deletion and roll back replay consumption (database-clock doubles)');
  for (const operation of [
    ()=>AuthService.prototype.changePassword.call({}, {}, {}),
    ()=>AuthService.prototype.link.call({}, {}, {}, {}),
    ()=>AuthService.prototype.unlink.call({}, {}),
    ()=>AuthAdminService.prototype.unlinkAll.call({}, {}),
    ()=>BaseService.prototype.createUser.call({}, { email:"x@example.test" }),
    ()=>UserAdminService.prototype.update.call({}, {}, "id", {password:"secret"}),
    ()=>UserService.prototype.updateMe.call({}, {user:{}}, {password:"secret"}),
  ]) {
    await assert.rejects(operation,(error)=>error.getStatus()===403);
  }
  const users=[];
  let profile;
  const ctx = {
    getConfig:async()=>({oauth:{enabled:true,autoRegister:true,roleClaim:"anas_role",storageLabelClaim:"",storageQuotaClaim:"",defaultStorageQuota:null,mobileOverrideEnabled:false}}),
    oauthRepository:{getProfileAndOAuthSid:async()=>({profile})},
    userRepository:{
      getByOAuthId:async(sub)=>users.find(user=>user.oauthId===sub),
      getByEmail:async(email)=>users.find(user=>user.email===email),
      getAdmin:async()=>users.find(user=>user.isAdmin),
      create:async(dto)=>{const user={...dto,id:`internal-user-${users.length+1}`};users.push(user);return user;},
      update:async(id,changes)=>{const user=users.find(user=>user.id===id);Object.assign(user,changes);return user;},
    },
    logger:{debug(){},warn(){},log(){}},
    clusterGroupRepository:{create:async()=>({id:"cluster"})},eventRepository:{emit:async()=>{}},
    createLoginResponse:async(user)=>user,
  };
  for (const method of ["getRoleClaim","getClaim","resolveRedirectUri"]) ctx[method]=AuthService.prototype[method];
  ctx.createUser=BaseService.prototype.createUser;
  const callback = ()=>AuthService.prototype.callback.call(ctx,{state:"state",codeVerifier:"verifier",url:"https://photos.example.test/auth/login"},{},{});
  profile={sub:"anchor-ordinary",email:"ordinary@example.test",anas_role:"user"};
  await assert.rejects(callback(),/first registered account/);
  assert.equal(users.length,0);
  profile={sub:"anchor-admin",email:"admin@example.test",name:"Directory Admin",anas_role:"admin"};
  const admin=await callback();
  assert.equal(admin.isAdmin,true);assert.equal(admin.oauthId,"anchor-admin");assert.notEqual(admin.id,admin.oauthId);assert.equal(admin.password,undefined);
  profile={sub:"anchor-ordinary",email:"ordinary@example.test",anas_role:"user"};
  const user=await callback();assert.equal(user.isAdmin,false);
  profile={...profile,email:"renamed@example.test"};assert.equal((await callback()).id,user.id);assert.equal(users.length,2);
  profile={sub:"anchor-conflict",email:"ordinary@example.test",anas_role:"user"};
  await assert.rejects(callback(),/OAuth authentication failed/);assert.equal(users.length,2);
  console.log("PASS: actual v3.2.4 methods reject local/password/link/unlink writes and malformed signed logout claims; native first ordinary denied, OIDC admin/user created, anchor retained, email conflict denied");
}
main().catch((error)=>{console.error(error);process.exitCode=1;});
