'use strict';
// Test-only authorization server. Public product accounts are always created
// by Immich's real authorization-code callback, never a database fixture insert.
const https=require('node:https');
const crypto=require('node:crypto');
const fs=require('node:fs');
const issuer='https://oidc:8443';
const key=crypto.createPrivateKey(fs.readFileSync('/fixture/tls.key'));
const publicKey=crypto.createPublicKey(key).export({format:'jwk'});
const codes=new Map();
const encode=(x)=>Buffer.from(JSON.stringify(x)).toString('base64url');
function jwt(claims){const body=encode({alg:'RS256',kid:'fixture',typ:'JWT'})+'.'+encode(claims);return body+'.'+crypto.sign('RSA-SHA256',Buffer.from(body),key).toString('base64url');}
function send(res,status,value){res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));}
https.createServer({key:fs.readFileSync('/fixture/tls.key'),cert:fs.readFileSync('/fixture/tls.crt')},async(req,res)=>{
 try {
 const url=new URL(req.url,issuer);let raw='';for await(const part of req) raw+=part;
 if(url.pathname==='/.well-known/openid-configuration') return send(res,200,{issuer,authorization_endpoint:issuer+'/authorize',token_endpoint:issuer+'/token',jwks_uri:issuer+'/jwks',end_session_endpoint:issuer+'/logout',response_types_supported:['code'],grant_types_supported:['authorization_code'],subject_types_supported:['public'],id_token_signing_alg_values_supported:['RS256'],token_endpoint_auth_methods_supported:['client_secret_post','client_secret_basic'],code_challenge_methods_supported:['S256'],scopes_supported:['openid','profile','email']});
 if(url.pathname==='/jwks') return send(res,200,{keys:[{...publicKey,kid:'fixture',alg:'RS256',use:'sig'}]});
 if(url.pathname==='/fixture/authorize'){
   const {authorizationUrl,profile}=JSON.parse(raw);const auth=new URL(authorizationUrl);
   if(auth.origin!==issuer || auth.searchParams.get('client_id')!=='immich')return send(res,400,{error:'invalid_client'});
   const code=crypto.randomBytes(24).toString('hex');codes.set(code,{profile,redirect:auth.searchParams.get('redirect_uri'),challenge:auth.searchParams.get('code_challenge')});
   const callback=new URL(auth.searchParams.get('redirect_uri'));callback.searchParams.set('code',code);callback.searchParams.set('state',auth.searchParams.get('state'));return send(res,200,{url:callback.href});
 }
 if(url.pathname==='/token'){
   const params=new URLSearchParams(raw);const record=codes.get(params.get('code'));codes.delete(params.get('code'));
   if(!record || params.get('grant_type')!=='authorization_code' || params.get('client_id')!=='immich' || params.get('client_secret')!=='fixture-client-secret' || params.get('redirect_uri')!==record.redirect)return send(res,400,{error:'invalid_grant'});
   const challenge=crypto.createHash('sha256').update(params.get('code_verifier')||'').digest('base64url');if(record.challenge && challenge!==record.challenge)return send(res,400,{error:'invalid_pkce'});
   const now=Math.floor(Date.now()/1000);const claims={iss:issuer,aud:'immich',iat:record.profile.issuedAt??now,exp:now+600,sub:record.profile.sub,email:record.profile.email,name:record.profile.name||'Fixture User',anas_role:record.profile.role||'user',sid:record.profile.sid||'sid-'+record.profile.sub};
   return send(res,200,{token_type:'Bearer',expires_in:600,access_token:jwt(claims),id_token:jwt(claims)});
 }
 if(url.pathname==='/fixture/logout-token'){
   const input=JSON.parse(raw);const now=Math.floor(Date.now()/1000);return send(res,200,{token:jwt({iss:issuer,aud:'immich',iat:now,exp:now+120,jti:crypto.randomUUID(),events:{'http://schemas.openid.net/event/backchannel-logout':{}},...input})});
 }
 send(res,404,{error:'not_found'});
 }catch(error){send(res,500,{error:error.message});}
}).listen(8443,'0.0.0.0');
