'use strict';
// Executed at the app origin. Cookies and native token-bearing responses never
// cross postMessage; the parent receives only a nonce-bound completion status.
(() => {
  const protocol='anas.browser-logout/v1';let started=false;
  const send=(type,status)=>window.parent.postMessage({protocol,type,nonce:config.nonce,...(status?{status}:{})},config.parent);
  const request=(path,options={})=>fetch(path,{credentials:'same-origin',cache:'no-store',signal:AbortSignal.timeout(15000),...options});
  async function authenticated() {
    const r=await request(config.adapter==='immich'?'/api/users/me':'/ocs/v2.php/cloud/user?format=json',config.adapter==='nextcloud'?{headers:{'OCS-APIRequest':'true'}}:{});
    if(r.status===401)return false;
    if(r.status!==200)throw Error('Unavailable');
    const j=await r.json();
    if(config.adapter==='immich'?typeof j.id==='string'&&!!j.id:[100,200].includes(j.ocs?.meta?.statuscode)&&typeof j.ocs?.data?.id==='string'&&!!j.ocs.data.id)return true;
    throw Error('Invalid identity response');
  }
  async function logout() {
    if(!await authenticated())return;
    if(config.adapter==='immich') {
      // Native logout deletes exactly this session. Discard its IAM redirect URI.
      await request('/api/auth/logout',{method:'POST',redirect:'manual'});
    }else {
      // Core logout is local even when OIDC discovery is unavailable. Obtain
      // its native CSRF token on this origin and never follow the login redirect.
      const tokenResponse=await request('/csrftoken'),token=await tokenResponse.json();
      if(!tokenResponse.ok||typeof token.token!=='string'||!token.token)throw Error('No CSRF token');
      await request('/logout?requesttoken='+encodeURIComponent(token.token),{redirect:'manual'});
    }
    if(await authenticated())throw Error('Session remains active');
  }
  window.addEventListener('message',async event=>{
    const d=event.data;
    if(started||event.source!==window.parent||event.origin!==config.parent||d?.protocol!==protocol||d.type!=='start'||d.nonce!==config.nonce)return;
    started=true;
    try{await logout();send('result','signed_out');}catch{send('result','failed');}
  });
  send('ready');
})();
