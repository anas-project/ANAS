'use strict';
const words = {
  en: {portal:'Portal', footer:'Your applications and account', apps:'Applications', account:'My account', password:'Change password', users:'User management', audit:'Security records', logout:'Sign out', welcome:'Your workspace', intro:'Applications available to your account, in one place.', search:'Search name or summary', all:'All categories', empty:'No applications available', open:'Open application', details:'Details', docs:'Documentation', loginTitle:'Welcome to ANAS', loginIntro:'Sign in to view your applications and manage your account.', login:'Sign in', recover:'First sign-in or expired password', username:'Username', display_name:'Display name', email:'Email', status:'Account status', active:'Active', disabled:'Disabled', expired:'Expired', locked:'Locked', expires:'Password expires', password_status:'Password status', must_change:'Change required', password_expired:'Expired', unknown:'Unknown', none:'Not set', groups:'Groups', direct:'Direct', inherited:'Inherited', primary:'Primary group', source:'Source', old_password:'Current password', new_password:'New password', confirm_password:'Confirm new password', submit:'Save', reason:'Reason', target:'User', reset:'Reset password', unlock:'Unlock account', force:'Require password change at next sign-in', recent:'Confirm your identity', recentIntro:'Password and group administration requires recent authentication.', add:'Add member', remove:'Remove direct member', back:'Back', records:'Records', action:'Action', result:'Result', time:'Time', loading:'Loading…', confirm:'Confirm this operation?', saved:'Directory saved.', pending:'IAM synchronization pending. Other application sessions have not been verified.', rejected:'The directory rejected this operation.', conflict:'Membership changed. Reload before trying again.', unknownResult:'The result is unknown. Do not repeat this operation automatically.', auditFailure:'Audit storage is unavailable. Further writes are paused.', reauthenticate:'Sign in again to continue.', oneTime:'Temporary password — shown once. Store it securely before leaving this page.', copy:'Copy', copied:'Copied', localLogout:'Your portal session has ended.', iamPending:'IAM sign-out has not been confirmed.', iamDone:'Portal and current IAM browser session signed out.', retry:'Retry IAM sign-out', password_change_rejected:'The password change could not be completed or confirmed. Check the credentials and directory policy; do not automatically repeat an interrupted request.', password_result_unknown:'Password result is unknown. Do not automatically retry.', authentication_required:'Sign in to continue.', authentication_rejected:'Authentication was rejected. Re-enter your password and try again.', recent_authentication_required:'Confirm your identity before this operation.', csrf_rejected:'The request expired or did not come from this portal. Reload and try again.', access_denied:'This operation is outside your permissions.', identity_unavailable:'Identity service unavailable. No permissions can be confirmed.', invalid_request:'Check the form and try again.', rate_limited:'Too many attempts. Try again later.', capacity_reached:'Service capacity reached. Try again later.', asset_unavailable:'An application icon is unavailable.', not_found:'Not found', noUsers:'No users in your permitted scope', membershipIntro:'Only direct members can be removed. Directory changes and application synchronization have separate results.'},
  'zh-CN': {portal:'用户门户', footer:'你的应用与个人账号', apps:'应用', account:'我的账号', password:'修改密码', users:'用户管理', audit:'安全记录', logout:'退出登录', welcome:'你的工作空间', intro:'在这里访问你有权限的应用。', search:'搜索应用名称或简介', all:'全部分类', empty:'无可用应用', open:'打开应用', details:'详情', docs:'使用文档', loginTitle:'欢迎使用 ANAS', loginIntro:'登录后查看应用与管理个人账号。', login:'登录', recover:'首次登录或密码已过期', username:'登录名', display_name:'显示名', email:'邮箱', status:'账号状态', active:'正常', disabled:'已停用', expired:'已过期', locked:'已锁定', expires:'密码到期时间', password_status:'密码状态', must_change:'需要修改', password_expired:'已过期', unknown:'未知', none:'未设置', groups:'分组', direct:'直接成员', inherited:'继承成员', primary:'主要组', source:'来源', old_password:'原密码', new_password:'新密码', confirm_password:'确认新密码', submit:'保存', reason:'操作理由', target:'用户', reset:'重置密码', unlock:'解锁账号', force:'要求下次登录修改密码', recent:'重新确认身份', recentIntro:'密码与分组管理需要近期认证。', add:'加入成员', remove:'移除直接成员', back:'返回', records:'记录', action:'操作', result:'结果', time:'时间', loading:'加载中…', confirm:'确认执行这项操作？', saved:'目录已保存。', pending:'IAM 同步待完成，其他应用会话状态尚未核实。', rejected:'目录拒绝了这项操作。', conflict:'成员状态已变化，请重新加载后再操作。', unknownResult:'结果未知，请勿自动重复提交。', auditFailure:'审计存储不可用，后续写入已暂停。', reauthenticate:'请重新登录后继续。', oneTime:'临时密码仅显示本次，请在离开页面前妥善保存。', copy:'复制', copied:'已复制', localLogout:'门户会话已退出。', iamPending:'IAM 登出尚未确认。', iamDone:'门户与当前浏览器的 IAM 会话已退出。', retry:'重试 IAM 登出', password_change_rejected:'无法完成或确认密码修改。请检查凭据及目录策略；请求曾中断时，请先核实，不要自动重复提交。', password_result_unknown:'密码结果未知，请勿自动重试。', authentication_required:'请先登录。', authentication_rejected:'认证被拒绝，请重新输入密码后再试。', recent_authentication_required:'请先重新确认身份。', csrf_rejected:'请求已过期或并非来自本门户，请重新加载。', access_denied:'这项操作不在你的权限范围内。', identity_unavailable:'身份服务不可用，当前无法确认权限。', invalid_request:'请检查表单内容。', rate_limited:'尝试次数过多，请稍后再试。', capacity_reached:'服务容量已达上限，请稍后再试。', asset_unavailable:'应用图标暂时不可用。', not_found:'未找到', noUsers:'授权范围内没有用户', membershipIntro:'只能移除直接成员。目录变更与应用同步会分别显示结果。'}
};
Object.assign(words.en, {
  recover:'Password expired or a change required?', recoverIntro:'Use your current password or the temporary password from your administrator. For a forgotten password, contact an administrator. A normal first sign-in uses the Sign in button.',
  language:'Language', showCategories:'Show categories', given_name:'Given name', surname:'Surname', profile:'Personal information', profileHelp:'Your username and email are managed separately. Changing your name does not change either.',
  mailHelp:'Mail service is not configured. The primary address is read-only; personal alias management will be available with the mail module.', edit:'Edit information', advanced:'Advanced: system and other groups', advancedHelp:'These groups can grant domain, host or other system privileges. Membership changes require a separate confirmation and your own domain credentials. Removing a group can also remove access.',
  applications:'Application access', capabilities:'Application capabilities', access:'Files and resources', roles:'Administration roles', system:'System and other groups', domainRisk:'Domain privileges', adminRole:'ANAS administrator role', resourceRisk:'Broad file access',
  groupRisk:'Review the effect before changing membership. Permissions can also be inherited through other groups.', domainTitle:'Confirm domain operation', domainHelp:'This operation uses your own AD permissions. Enter the domain password of the account signed in to this portal. A different administrator’s credentials are not accepted.', actor_password:'Your domain password', cancel:'Cancel', confirmAction:'Confirm operation',
  credentials_rejected:'The supplied credentials were rejected.', password_policy_rejected:'The directory password policy rejected this password. Check length, complexity, history and minimum password age.',
  self_administration_denied:'This change could remove your own administrator access. Use personal password change for your own password.', last_administrator:'This change would remove the last available administrator.', confirmation_required:'The confirmation expired, changed, or was already used. Review the operation again.', domain_administration_disabled:'Domain administration is not enabled for this deployment.',
  unexpected_response:'The service returned an unexpected response.', request_failed:'The request failed or its result could not be confirmed. Do not automatically repeat a write.', profile_refresh_failed:'Your information was saved, but the updated view could not be loaded. Reload the page before editing again.', conflict:'The information or membership changed. Reload before trying again.',
  'profile.update':'Profile update', 'password.change':'Password change', 'password.recover':'Password recovery', 'password.reset':'Password reset', 'account.unlock':'Account unlock', 'groups.member.add':'Member added', 'groups.member.remove':'Member removed', intent:'Pending', denied:'Denied'
});
Object.assign(words['zh-CN'], {
  recover:'密码已过期或被要求修改？', recoverIntro:'使用当前密码或管理员提供的临时密码。忘记密码请联系管理员；普通首次登录请使用“登录”按钮。',
  language:'语言', showCategories:'显示分类', given_name:'名', surname:'姓', profile:'个人资料', profileHelp:'登录名和邮箱分别管理。修改姓名不会改变登录名或邮箱。',
  mailHelp:'尚未配置邮箱服务。主邮箱暂为只读；个人别名管理将在邮件模块接入后提供。', edit:'修改资料', advanced:'高级：系统和其他分组', advancedHelp:'这些分组可能授予域、主机或其他系统权限。修改前需要单独确认并验证本人的域凭据；移除成员也可能导致失去访问权限。',
  applications:'应用访问', capabilities:'应用能力', access:'文件与资源访问', roles:'管理角色', system:'系统和其他分组', domainRisk:'域级权限', adminRole:'ANAS 管理员角色', resourceRisk:'广泛文件权限',
  groupRisk:'修改前请核对权限影响。用户也可能通过其他分组继承权限。', domainTitle:'确认域级操作', domainHelp:'本次操作使用你本人已有的 AD 权限。请输入当前门户登录账号的域密码，不能使用其他管理员的凭据。', actor_password:'本人域密码', cancel:'取消', confirmAction:'确认执行',
  credentials_rejected:'提供的凭据被拒绝。', password_policy_rejected:'目录密码策略拒绝了新密码，请检查长度、复杂度、历史和密码最短使用时间。',
  self_administration_denied:'这项变更可能移除你自己的管理权限。修改本人密码请使用个人改密入口。', last_administrator:'这项变更会移除最后一个可用管理员。', confirmation_required:'确认已过期、参数已改变或已经使用，请重新核对操作。', domain_administration_disabled:'此部署尚未启用域级管理操作。',
  unexpected_response:'服务返回了无法识别的响应。', request_failed:'请求失败或结果无法确认，请勿自动重复写入。', profile_refresh_failed:'资料已保存，但刷新显示失败。再次编辑前请重新加载页面。', conflict:'资料或成员关系已变化，请重新加载后再操作。',
  'profile.update':'资料修改', 'password.change':'本人改密', 'password.recover':'受限改密', 'password.reset':'密码重置', 'account.unlock':'账号解锁', 'groups.member.add':'加入成员', 'groups.member.remove':'移除成员', intent:'待确认', denied:'拒绝'
});

let language='en', csrf='', me=null, page='apps', target=null, logoutEndpoint='', revision=0;
const content=document.getElementById('content'), navigation=document.getElementById('navigation');
const t=key=>words[language][key]??words.en[key]??key;
function preference(key,value) {
  try { if(value===undefined)return localStorage.getItem(key); localStorage.setItem(key,value); } catch {}
  return null;
}
function el(tag,text,attrs={}) {
  const n=document.createElement(tag);
  if(text!==null&&text!==undefined)n.textContent=text;
  for(const [key,value] of Object.entries(attrs))n.setAttribute(key,value);
  return n;
}
function button(text,fn,primary=false) {
  const n=el('button',text,{type:'button',...(primary?{class:'primary'}:{})});
  n.addEventListener('click',fn);return n;
}
function notice(text,error=false) {
  const n=document.getElementById('notice');n.className='notice'+(error?' error':'');n.textContent=text;n.hidden=!text;
  n.setAttribute('role',error?'alert':'status');
}
function heading(title,description) {
  content.append(el('h1',title));if(description)content.append(el('p',description,{class:'muted'}));
}
function safeLink(url,text) {
  const n=el('a',text);
  try { if(['http:','https:'].includes(new URL(url,location.origin).protocol)) {
    n.setAttribute('href',url);n.setAttribute('target','_blank');n.setAttribute('rel','noopener noreferrer');
  }} catch {}
  return n;
}
async function api(path,body) {
  let response,data;
  try {
    response=await fetch(path+(path.includes('?')?'&':'?')+'lang='+encodeURIComponent(language), {
      method:body===undefined?'GET':'POST', credentials:'same-origin',cache:'no-store',
      headers:body===undefined?{}:{'Content-Type':'application/json','X-CSRF-Token':csrf},
      body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(60000)
    });
    data=await response.json();
  } catch { throw new Error(t('request_failed')); }
  if(response.status===401) { me=null;csrf='';loginPage();notice(t('authentication_required'),true); }
  if(!response.ok&&!data.directory) {
    const error=new Error(t(data.error||'unexpected_response'));error.code=data.error;throw error;
  }
  return data;
}
function shell() {
  document.documentElement.lang=language;
  document.getElementById('portal-title').textContent=t('portal');
  document.getElementById('footer-text').textContent=t('footer');
  document.getElementById('logout').textContent=t('logout');document.getElementById('logout').hidden=!me;
  document.getElementById('language').setAttribute('aria-label',t('language'));
  document.querySelector('.layout').classList.toggle('guest',!me);
  navigation.hidden=!me;navigation.replaceChildren();
  if(!me)return;
  const entries=[['apps','apps'],['account','account'],['password','password'],...(me.actions.includes('users.read')?[['users','users']]:[]),['audit','audit']];
  for(const [id,label] of entries) {
    const n=button(t(label),()=>show(id));
    if(page===id){n.className='active';n.setAttribute('aria-current','page');}
    navigation.append(n);
  }
}
async function show(id) {
  const current=++revision;page=id;target=null;notice('');shell();content.replaceChildren(el('p',t('loading'),{class:'muted'}));
  try {
    const fresh=await api('/api/me');if(current!==revision)return;
    me=fresh;csrf=me.csrf;shell();content.replaceChildren();
    if(id==='apps')await apps();else if(id==='account')account();else if(id==='password')passwordForm(false);else if(id==='users')await users();else if(id==='audit')await audit();
  } catch(error) { if(current===revision)notice(error.message,true); }
}
function loginPage() {
  ++revision;shell();content.replaceChildren();
  const box=el('section',null,{class:'panel hero'});
  box.append(el('h1',t('loginTitle')),el('p',t('loginIntro'),{class:'muted'}));
  box.append(el('a',t('login'),{href:'/login',class:'button primary'}));
  const recovery=button(t('recover'),async()=>{
    try {const b=await api('/api/bootstrap');csrf=b.csrf;content.replaceChildren();passwordForm(true);}catch(error){notice(error.message,true);}
  });
  recovery.className='text-button';box.append(recovery);content.append(box);
}
function applicationCard(app) {
  const card=el('article',null,{class:'card'}),image=el('img',null,{src:app.icons['48'].path,srcset:app.icons['48'].path+' 1x, '+app.icons['128'].path+' 2x',width:'48',height:'48',alt:'',loading:'lazy'});
  const text=el('div',null,{class:'card-content'});
  text.append(el('h3',app.display_name),el('p',app.summary),safeLink(app.url,t('open')));
  const details=el('details');details.append(el('summary',t('details')),el('p',app.description));
  if(app.documentation_url)details.append(safeLink(app.documentation_url,t('docs')));
  text.append(details);card.append(image,text);return card;
}
async function apps() {
  heading(t('welcome'),t('intro'));
  const controls=el('div',null,{class:'controls app-controls'}),search=el('input',null,{type:'search',placeholder:t('search'),'aria-label':t('search')}),category=el('select',null,{'aria-label':t('all')});
  category.append(el('option',t('all'),{value:''}));
  const toggle=el('label',null,{class:'check category-toggle'}),checkbox=el('input',null,{type:'checkbox'});
  checkbox.checked=preference('anas.portal.showCategories')!=='false';toggle.append(checkbox,el('span',t('showCategories')));
  controls.append(search,category,toggle);content.append(controls);
  const cards=el('div');content.append(cards);let data=await api('/api/apps');if(!cards.isConnected)return;
  for(const group of data.groups)for(const c of group.categories)if(![...category.options].some(o=>o.value===c.id))category.append(el('option',c.display_name,{value:c.id}));
  function render() {
    cards.replaceChildren();if(!data.groups.length){cards.append(el('div',t('empty'),{class:'panel empty'}));return;}
    for(const group of data.groups) {
      const section=el('section',null,{'data-kind':group.kind||group.id});section.append(el('h2',group.display_name));
      // BuildView uses kind as the top-level ID. Preserve its existing order.
      if((group.kind==='user'||group.id==='user')&&!checkbox.checked) {
        const grid=el('div',null,{class:'cards'});for(const c of group.categories)for(const app of c.applications)grid.append(applicationCard(app));section.append(grid);
      } else for(const c of group.categories) {
        section.append(el('h3',c.display_name,{class:'category-heading'}));
        const grid=el('div',null,{class:'cards'});for(const app of c.applications)grid.append(applicationCard(app));section.append(grid);
      }
      cards.append(section);
    }
  }
  render();checkbox.addEventListener('change',()=>{preference('anas.portal.showCategories',String(checkbox.checked));render();});
  let timer,queryRevision=0;
  async function update() {
    const current=++queryRevision;
    try {const next=await api('/api/apps?q='+encodeURIComponent(search.value)+(category.value?'&category='+encodeURIComponent(category.value):''));if(current===queryRevision&&cards.isConnected){data=next;render();}}
    catch(error){if(cards.isConnected){cards.replaceChildren();notice(error.message,true);}}
  }
  search.addEventListener('input',()=>{clearTimeout(timer);timer=setTimeout(update,250);});category.addEventListener('change',update);
}
function personDetails(p) {
  const panel=el('section',null,{class:'panel person-details','data-anchor':p.anchor}),dl=el('dl');
  for(const [key,value] of [['username',p.username],['given_name',p.given_name],['surname',p.surname],['display_name',p.display_name],['email',p.email],['status',p.disabled?t('disabled'):p.account_expired?t('expired'):p.locked?t('locked'):t('active')],['password_status',p.must_change_password?t('must_change'):p.password_expired?t('password_expired'):p.password_state_known?t('active'):t('unknown')],['expires',p.password_expires_at?new Date(p.password_expires_at).toLocaleString(language):t('unknown')]])dl.append(el('dt',t(key)),el('dd',value||t('none')));
  panel.append(dl);if(p.domain_risk)panel.append(el('p',t('domainRisk'),{class:'badge risk'}));return panel;
}
function groupTable(p) {
  const table=el('table'),head=el('tr');for(const key of ['groups','status','source'])head.append(el('th',t(key)));table.append(head);
  for(const g of p.groups) {
    const row=el('tr'),names=g.sources.map(id=>p.groups.find(v=>v.anchor===id||v.reference===id)?.name??id);
    row.append(el('td',g.name),el('td',t(g.primary?'primary':g.direct?'direct':'inherited')),el('td',names.join(', ')||'—'));table.append(row);
  }
  const wrap=el('div',null,{class:'panel table-wrap'});wrap.append(table);return wrap;
}
function field(form,key,type='text',required=true,value='') {
  const label=el('label',t(key)),input=el(type==='textarea'?'textarea':'input',null,{name:key,...(type==='textarea'?{}:{type}),...(required?{required:''}:{}),autocomplete:type==='password'?(key==='old_password'||key==='actor_password'?'current-password':'new-password'):'off'});
  if(type==='text')input.maxLength=64;input.value=value;label.append(input);form.append(label);return input;
}
function resultSlot(form) {const slot=el('div',null,{class:'form-result'});form.append(slot);return slot;}
function renderResult(data,slot) {
  const bad=data.directory!=='saved',box=el('div',null,{class:'result'+(bad?' error':''),role:bad?'alert':'status',tabindex:'-1'});
  const labels={saved:t('saved'),rejected:t('rejected'),denied:t('access_denied'),unavailable:t('identity_unavailable'),conflict:t('conflict'),unknown:t('unknownResult'),not_attempted:t('auditFailure')};
  box.append(el('p',labels[data.directory]??(t(data.directory)||t('unknownResult'))));
  if(data.directory==='saved')box.append(el('p',t('pending')));
  if(data.audit==='unavailable'||data.audit===false)box.append(el('p',t('auditFailure')));
  if(data.request_id)box.append(el('small',data.request_id));
  if(data.temporary_password) {
    box.append(el('p',t('oneTime')));const secret=el('input',null,{type:'text',readonly:'',value:data.temporary_password,'aria-label':t('oneTime')});
    box.append(secret,button(t('copy'),async event=>{try{await navigator.clipboard.writeText(secret.value);event.target.textContent=t('copied');}catch{secret.focus();secret.select();}}));
  }
  if(data.reauthenticate){me=null;shell();box.append(el('p',t('reauthenticate')),el('a',t('login'),{href:'/login',class:'button primary'}));}
  if(slot)slot.replaceChildren(box);else content.append(box);box.focus({preventScroll:true});box.scrollIntoView({block:'nearest',behavior:'smooth'});return box;
}
function formError(slot,error) {
  slot.replaceChildren(el('div',error.message||t('request_failed'),{class:'result error',role:'alert',tabindex:'-1'}));slot.firstChild.focus({preventScroll:true});slot.scrollIntoView({block:'nearest'});
}
function recentForm() {
  const form=el('form',null,{method:'post',action:'/api/reauth',class:'reauth-form'});
  form.append(el('p',t('recentIntro'),{class:'muted'}),el('input',null,{type:'hidden',name:'csrf',value:csrf}),el('button',t('recent'),{type:'submit'}));
  form.addEventListener('submit',()=>{try{sessionStorage.setItem('anas.portal.return',JSON.stringify({page,target}));}catch{}});return form;
}
function profileForm(person,version,admin=false,requiresDomain=false) {
  const form=el('form',null,{class:'panel spaced'});form.append(el('h2',t('edit')),el('p',t('profileHelp'),{class:'muted'}));
  field(form,'given_name','text',false,person.given_name||'');field(form,'surname','text',false,person.surname||'');field(form,'display_name','text',true,person.display_name||'');
  if(admin)field(form,'reason','textarea');
  const submit=el('button',t('submit'),{type:'submit',class:'primary'});form.append(submit);const slot=resultSlot(form);
  form.addEventListener('submit',async event=>{
    event.preventDefault();submit.disabled=true;
    try {
      const values=Object.fromEntries(new FormData(form)),profile={given_name:values.given_name.trim(),surname:values.surname.trim(),display_name:values.display_name.trim()};
      const op={action:'profile.update',target:person.anchor,reason:values.reason||'self profile',profile,profile_version:version};
      const data=admin?await executeAdmin(op,requiresDomain):await api('/api/me/profile',op);
      if(!data||!form.isConnected)return;const result=renderResult(data,slot);
      if(data.directory==='saved') {
        try {
          const fresh=await api(admin?'/api/users/'+encodeURIComponent(person.anchor):'/api/me');
          if(!form.isConnected)return;
          version=fresh.profile_version;
          if(!admin){me=fresh;csrf=me.csrf;}
          const details=content.querySelector('.person-details');
          if(details?.getAttribute('data-anchor')===fresh.person.anchor) {
            details.replaceWith(personDetails(fresh.person));
            if(admin)content.querySelector('h1').textContent=fresh.person.display_name||fresh.person.username;
          }
        }catch {
          if(form.isConnected)result.append(el('p',t('profile_refresh_failed'),{role:'alert'}));
        }
      }
    }catch(error){formError(slot,error);}finally{submit.disabled=false;}
  });return form;
}
function account() {
  heading(t('account'));content.append(personDetails(me.person));
  if(me.profile_editable)content.append(profileForm(me.person,me.profile_version),recentForm());
  content.append(el('h2',t('email')),el('p',t('mailHelp'),{class:'muted'}),el('h2',t('groups')),groupTable(me.person));
}
function passwordForm(recover) {
  if(recover)page='recover';heading(t(recover?'recover':'password'),recover?t('recoverIntro'):undefined);
  const form=el('form',null,{class:'panel spaced'});if(recover)field(form,'username');field(form,'old_password','password');field(form,'new_password','password');field(form,'confirm_password','password');
  const submit=el('button',t('submit'),{type:'submit',class:'primary'});form.append(submit);const slot=resultSlot(form);content.append(form);
  form.addEventListener('submit',async event=>{
    event.preventDefault();submit.disabled=true;
    try{const data=await api('/api/password/'+(recover?'recover':'change'),Object.fromEntries(new FormData(form)));renderResult(data,slot);}
    catch(error){formError(slot,error);}finally{for(const input of form.querySelectorAll('input[type=password]'))input.value='';submit.disabled=false;}
  });
}
async function users() {
  heading(t('users'));const controls=el('form',null,{class:'controls'}),input=el('input',null,{type:'search',placeholder:t('search'),'aria-label':t('search')}),search=el('button',t('search'),{type:'submit'});
  controls.append(input,search);content.append(controls);const panel=el('div',null,{class:'panel table-wrap'});content.append(panel);let loadRevision=0;
  async function load(offset) {
    const current=++loadRevision;panel.replaceChildren(el('p',t('loading')));
    try {
      const data=await api('/api/users?q='+encodeURIComponent(input.value)+'&offset='+offset);if(current!==loadRevision||!panel.isConnected)return;panel.replaceChildren();
      if(!data.users.length){panel.append(el('p',t('noUsers')));return;}
      const table=el('table'),head=el('tr');for(const key of ['username','display_name','email','status','details'])head.append(el('th',t(key)));table.append(head);
      for(const p of data.users) {
        const row=el('tr'),actions=el('td');actions.append(button(t('details'),()=>userDetails(p.anchor)));
        const name=el('td',p.username);if(p.administrator)name.append(el('span',t('adminRole'),{class:'badge'}));
        row.append(name,el('td',p.display_name),el('td',p.email),el('td',p.disabled?t('disabled'):p.locked?t('locked'):p.account_expired?t('expired'):t('active')),actions);table.append(row);
      }
      panel.append(table);const pages=el('div',null,{class:'row pagination'});pages.append(el('span',String(data.count)));
      if(offset>0)pages.append(button('←',()=>load(Math.max(0,offset-25))));if(offset+25<data.count)pages.append(button('→',()=>load(offset+25)));panel.append(pages);
    }catch(error){if(panel.isConnected){panel.replaceChildren();notice(error.message,true);}}
  }
  controls.addEventListener('submit',event=>{event.preventDefault();load(0);});await load(0);
}
async function domainPassword(preview) {
  return new Promise(resolve=>{
    const dialog=el('dialog',null,{class:'domain-dialog'}),form=el('form');
    form.append(el('h2',t('domainTitle')),el('p',t('domainHelp')),el('p',t(preview.action)+' · '+preview.target+(preview.group?' · '+preview.group:''),{class:'notice'}));
    const password=field(form,'actor_password','password'),row=el('div',null,{class:'row'}),cancel=button(t('cancel'),()=>dialog.close());
    row.append(cancel,el('button',t('confirmAction'),{type:'submit',class:'primary'}));form.append(row);dialog.append(form);document.body.append(dialog);
    let settled=false;
    const finish=value=>{if(settled)return;settled=true;password.value='';dialog.close();dialog.remove();resolve(value);};
    form.addEventListener('submit',event=>{event.preventDefault();finish(password.value);});dialog.addEventListener('close',()=>finish(null));
    dialog.showModal();password.focus();
  });
}
async function executeAdmin(op,domain) {
  if(!me.administration)return null;
  if(domain) {
    const preview=await api('/api/administration/prepare',op),password=await domainPassword(preview);if(password===null)return null;
    op.proof=preview.proof;op.actor_password=password;
  }
  try{return await api('/api/administration/execute',op);}finally{op.actor_password='';op.proof='';}
}
async function userDetails(anchor) {
  const current=++revision;target=anchor;content.replaceChildren();notice('');
  try {
    const data=await api('/api/users/'+encodeURIComponent(anchor));if(current!==revision)return;
    heading(data.person.display_name||data.person.username);content.append(button(t('back'),()=>show('users')),personDetails(data.person),recentForm());
    if(data.actions.includes('profile.update'))content.append(profileForm(data.person,data.profile_version,true,data.requires_domain&&data.person.anchor!==me.person.anchor));
    if(data.actions.includes('password.reset')||data.actions.includes('account.unlock')) {
      const form=el('form',null,{class:'panel spaced'});field(form,'reason','textarea');
      if(data.actions.includes('password.reset')) {
        const label=el('label',null,{class:'check'}),input=el('input',null,{type:'checkbox',name:'force_change'});input.checked=true;label.append(input,el('span',t('force')));form.append(label);
      }
      const slot=el('div',null,{class:'form-result'});
      async function operate(action) {
        if(!form.reportValidity()||!confirm(t('confirm')+' '+data.person.username))return;
        const buttons=form.querySelectorAll('button');buttons.forEach(b=>b.disabled=true);
        try {
          const reason=form.querySelector('[name=reason]').value,force=!!form.querySelector('[name=force_change]')?.checked;
          const result=data.administration?await executeAdmin({action,target:anchor,reason,...(action==='password.reset'?{force_change:force}:{})},data.requires_domain):await api('/api/users/'+encodeURIComponent(anchor)+'/'+(action==='password.reset'?'reset':'unlock'),action==='password.reset'?{reason,force_change:force}:{reason});
          if(result)renderResult(result,slot);
        }catch(error){formError(slot,error);}finally{buttons.forEach(b=>b.disabled=false);}
      }
      if(data.actions.includes('password.reset'))form.append(button(t('reset'),()=>operate('password.reset'),true));
      if(data.actions.includes('account.unlock'))form.append(button(t('unlock'),()=>operate('account.unlock')));
      form.append(slot);content.append(form);
    }
    content.append(el('h2',t('groups')),el('p',t('groupRisk'),{class:'muted'}));
    const advanced=el('details',null,{class:'advanced'});advanced.append(el('summary',t('advanced')),el('p',t('advancedHelp'),{class:'notice warning'}));
    if(data.administration)content.append(advanced);
    const membership=el('div');content.append(membership);
    async function loadGroups() {
      const memberships=await api('/api/groups?target='+encodeURIComponent(anchor)+(advanced.open?'&advanced=true':''));if(current!==revision)return;membership.replaceChildren();
      for(const category of ['applications','capabilities','access','roles','system']) {
        const items=memberships.groups.filter(item=>(item.group.category||'access')===category);if(!items.length)continue;
        membership.append(el('h3',t(category),{class:'category-heading'}));const wrap=el('div',null,{class:'panel table-wrap'}),table=el('table');
        for(const item of items) {
          const row=el('tr'),name=el('td',item.group.name),state=el('td',t(item.primary?'primary':item.direct?'direct':item.inherited?'inherited':'none')),actions=el('td');
          if(item.group.domain_risk)name.append(el('span',t('domainRisk'),{class:'badge risk'}));
          if(item.group.admin_role)name.append(el('span',t('adminRole'),{class:'badge'}));
          if(category==='access'&&item.group.name==='FS Admins')name.append(el('span',t('resourceRisk'),{class:'badge risk'}));
          async function change(add) {
            const reason=prompt(t('reason'));if(!reason?.trim()||!confirm(t('confirm')+' '+item.group.name))return;
            for(const b of actions.querySelectorAll('button'))b.disabled=true;
            try {
              const result=data.administration?await executeAdmin({action:add?'groups.member.add':'groups.member.remove',target:anchor,group:item.group.reference,expected_direct:item.direct,reason},item.requires_domain):await api('/api/groups/'+encodeURIComponent(item.group.anchor)+'/member',{target:anchor,add,expected_direct:item.direct,reason});
              if(result){renderResult(result,operationSlot);if(result.directory==='saved')await loadGroups();}
            }catch(error){formError(operationSlot,error);}finally{for(const b of actions.querySelectorAll('button'))b.disabled=false;}
          }
          if(item.can_add)actions.append(button(t('add'),()=>change(true)));if(item.can_remove)actions.append(button(t('remove'),()=>change(false)));
          row.append(name,state,actions);table.append(row);
        }
        wrap.append(table);membership.append(wrap);
      }
    }
    const operationSlot=el('div',null,{class:'form-result'});content.append(operationSlot);
    advanced.addEventListener('toggle',()=>loadGroups().catch(error=>formError(operationSlot,error)));await loadGroups();
  }catch(error){if(current===revision)notice(error.message,true);}
}
async function audit() {
  const current=revision;heading(t('audit'));const data=await api('/api/audit'),table=el('table'),head=el('tr');
  if(page!=='audit'||current!==revision)return;for(const key of ['time','action','target','result'])head.append(el('th',t(key)));table.append(head);
  for(const record of data.records){const row=el('tr');row.append(el('td',new Date(record.time).toLocaleString(language)),el('td',t(record.action)),el('td',record.target||'—'),el('td',t(record.directory||record.phase)));table.append(row);}
  const panel=el('div',null,{class:'panel table-wrap'});panel.append(table);content.append(panel);
}
async function iamLogout() {
  notice(t('localLogout')+' '+t('iamPending'));
  try {
    const response=await fetch(logoutEndpoint,{method:'POST',credentials:'include',signal:AbortSignal.timeout(15000)}),data=await response.json();
    if(!response.ok||data.status!=='ok')throw new Error();notice(t('iamDone'));document.getElementById('iam-retry')?.remove();
  }catch {notice(t('localLogout')+' '+t('iamPending'),true);if(!document.getElementById('iam-retry')){const retry=button(t('retry'),iamLogout);retry.id='iam-retry';content.append(retry);}}
}
document.getElementById('logout').addEventListener('click',async()=>{
  try{const data=await api('/api/logout',{});logoutEndpoint=data.iam_endpoint;me=null;csrf='';++revision;shell();content.replaceChildren();heading(t('logout'));await iamLogout();}catch(error){notice(error.message,true);}
});
document.getElementById('language').addEventListener('change',async event=>{
  language=event.target.value;preference('anas.portal.language',language);
  if(me){const previousTarget=target;await show(page);if(previousTarget&&page==='users')await userDetails(previousTarget);}
  else if(page==='recover'){content.replaceChildren();shell();passwordForm(true);}else loginPage();
});
async function start() {
  try {
    const selected=new URLSearchParams(location.search).get('lang')||preference('anas.portal.language')||'';
    const bootstrap=await fetch('/api/bootstrap'+(selected?'?lang='+encodeURIComponent(selected):''),{cache:'no-store'}).then(r=>r.json());
    language=bootstrap.language;csrf=bootstrap.csrf;document.getElementById('language').value=language;shell();
    if(!bootstrap.authenticated){loginPage();return;}
    let resume;try{resume=JSON.parse(sessionStorage.getItem('anas.portal.return'));sessionStorage.removeItem('anas.portal.return');}catch{}
    const selectedPage=['apps','account','password','users','audit'].includes(resume?.page)?resume.page:'apps';
    await show(selectedPage);if(me&&selectedPage==='users'&&/^[A-Za-z0-9_.:-]{1,128}$/.test(resume?.target||''))await userDetails(resume.target);
  }catch(error){loginPage();notice(error.message,true);}
}
start();
