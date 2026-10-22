'use strict';
// ANAS user portal. Plain DOM rendering: every text value is assigned with
// textContent; the server remains the only authority for permissions.
const I18N=window.ANAS_I18N;
let categoryNames={};
let language='en', csrf='', me=null, prefs=null, catalog=null, iconThemeCatalog=null, logoutState=null, logoutRunning=false, revision=0, content=null;
const app=document.getElementById('app'), noticeNode=document.getElementById('notice');
const t=(key,vars)=>{
  let text=I18N.words[language]?.[key]??I18N.words.en[key]??key;
  if(vars)for(const [k,v] of Object.entries(vars))text=text.replaceAll('{'+k+'}',String(v));
  return text;
};
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
function button(text,fn,variant='') {
  const n=el('button',text,{type:'button',...(variant?{class:variant}:{})});
  n.addEventListener('click',fn);return n;
}
function loginLink(text,attrs={}) {
  const a=el('a',text,{href:'/login',...attrs});
  // Resolve system mode at navigation time, including a live OS theme change.
  a.addEventListener('click',()=>{
    const mode=document.documentElement.dataset.theme;
    const dark=mode==='dark'||mode==='system'&&matchMedia('(prefers-color-scheme: dark)').matches;
    a.href='/login?theme='+(dark?'dark':'default');
  });
  return a;
}
const ICONS={
  search:'M11 4a7 7 0 1 0 0 14a7 7 0 1 0 0-14|M20 20l-3.5-3.5', user:'M12 4a4 4 0 1 0 0 8a4 4 0 1 0 0-8|M4 21c1.5-4 4.5-6 8-6s6.5 2 8 6',
  lock:'M5 10h14v11H5z|M8 10V7a4 4 0 0 1 8 0v3', users:'M9 4.5a3.5 3.5 0 1 0 0 7a3.5 3.5 0 1 0 0-7|M3 20c1-3.5 3.5-5 6-5s5 1.5 6 5|M16 5a3 3 0 0 1 0 6|M17 14c2.2.4 3.6 2 4 5',
  mail:'M3 5h18v14H3z|M3 7l9 6 9-6', clock:'M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18|M12 7v5l3 2', grid:'M4 4h6.5v6.5H4z|M13.5 4H20v6.5h-6.5z|M4 13.5h6.5V20H4z|M13.5 13.5H20V20h-6.5z',
  shield:'M12 3L4 6v6c0 4.5 3.4 8 8 9c4.6-1 8-4.5 8-9V6z', external:'M7 17L17 7|M8 7h9v9', arrow:'M5 12h14|M13 6l6 6-6 6', back:'M19 12H5|M11 6l-6 6 6 6',
  sun:'M12 8a4 4 0 1 0 0 8a4 4 0 1 0 0-8|M12 2v2|M12 20v2|M4.9 4.9l1.4 1.4|M17.7 17.7l1.4 1.4|M2 12h2|M20 12h2|M4.9 19.1l1.4-1.4|M17.7 6.3l1.4-1.4',
  moon:'M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z', monitor:'M3 4h18v12H3z|M8 20h8|M12 16v4', logout:'M15 4h4v16h-4|M10 8l-4 4 4 4|M6 12h10',
  pin:'M12 3l2.6 5.3 5.9.9-4.3 4.1 1 5.9L12 16.4 6.8 19.2l1-5.9L3.5 9.2l5.9-.9z', left:'M15 6l-6 6 6 6', right:'M9 6l6 6-6 6', close:'M6 6l12 12|M18 6L6 18',
  info:'M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18|M12 11v5|M12 8h.01',
  check:'M5 12l5 5 9-10', sync:'M21 12a9 9 0 1 1-3-6.7|M21 4v5h-5', alert:'M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18|M12 8v4|M12 16h.01', plus:'M12 5v14|M5 12h14',
};
function icon(name,size=18) {
  const ns='http://www.w3.org/2000/svg',svg=document.createElementNS(ns,'svg');
  for(const [k,v] of Object.entries({viewBox:'0 0 24 24',width:size,height:size,fill:'none',stroke:'currentColor','stroke-width':'2','stroke-linecap':'round','stroke-linejoin':'round','aria-hidden':'true',focusable:'false'}))svg.setAttribute(k,String(v));
  for(const d of ICONS[name].split('|')){const p=document.createElementNS(ns,'path');p.setAttribute('d',d);svg.append(p);}
  return svg;
}
function withIcon(node,name,size) {node.prepend(icon(name,size));return node;}
function notice(text,error=false) {
  noticeNode.className='notice'+(error?' error':'');noticeNode.textContent=text;noticeNode.hidden=!text;
  noticeNode.setAttribute('role',error?'alert':'status');
}
function heading(title,description,eyebrow) {
  const head=el('div',null,{class:'page-head'});
  if(eyebrow)head.append(el('p',eyebrow,{class:'eyebrow'}));
  head.append(el('h1',title));if(description)head.append(el('p',description,{class:'muted'}));
  content.append(head);return head;
}
function safeLink(url,text,attrs={}) {
  const n=el('a',text,attrs);
  try { if(['http:','https:'].includes(new URL(url,location.origin).protocol)) {
    n.setAttribute('href',url);n.setAttribute('target','_blank');n.setAttribute('rel','noopener noreferrer');
  }} catch {}
  return n;
}
function initial(person) {return [...(person?.display_name||person?.username||'?').trim()][0]?.toUpperCase()||'?';}
function avatar(person,size='') {return el('span',initial(person),{class:'avatar'+(size?' '+size:''),'aria-hidden':'true'});}
async function api(path,body,retry=true) {
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
  // An expired recent authentication asks for the person's own password in
  // the page, then repeats this request once.
  if(response.status===403&&data.error==='recent_authentication_required'&&retry&&me&&await reauthDialog())return api(path,body,false);
  if(!response.ok&&!data.directory) {
    const error=new Error(t(data.error||'unexpected_response'));error.code=data.error;error.status=response.status;throw error;
  }
  return data;
}

// --- preferences and theme -------------------------------------------------
function applyTheme(theme) {
  document.documentElement.dataset.theme=['light','dark'].includes(theme)?theme:'system';
  preference('anas.portal.theme',document.documentElement.dataset.theme);
  applyIconThemeColors();
}
function localPreferences() {
  const theme=preference('anas.portal.theme');
  return {language:'',theme:['light','dark'].includes(theme)?theme:'system',show_categories:preference('anas.portal.showCategories')!=='false',pinned:[]};
}
// Account preferences win after sign-in; this browser keeps a cache for
// anonymous pages and the first paint of the next visit.
function adoptPreferences(next) {
  prefs={language:next.language||'',theme:next.theme||'system',show_categories:next.show_categories!==false,pinned:Array.isArray(next.pinned)?next.pinned:[]};
  if(next.icon_theme)prefs.icon_theme=next.icon_theme;
  applyTheme(prefs.theme);preference('anas.portal.showCategories',String(prefs.show_categories));
  preference('anas.portal.language',prefs.language);
}
async function savePreferences(changes) {
  const previous=prefs;
  prefs={...(prefs||localPreferences()),...changes};
  if('theme' in changes)applyTheme(prefs.theme);
  if('show_categories' in changes)preference('anas.portal.showCategories',String(prefs.show_categories));
  if('language' in changes)preference('anas.portal.language',prefs.language);
  if(!me)return true;
  try{const data=await api('/api/me/preferences',prefs);prefs=data.preferences;applyIconThemeColors();return true;}
  catch(error){if('icon_theme' in changes){prefs=previous;applyIconThemeColors();}notice(error.message,true);return false;}
}
function cycleTheme() {
  const order=['system','light','dark'],next=order[(order.indexOf(document.documentElement.dataset.theme)+1)%3];
  savePreferences({theme:next}).then(()=>renderCurrentShellControls());
}
function themeLabel(theme) {return t({system:'themeSystem',light:'themeLight',dark:'themeDark'}[theme]);}

// Imported themes contain data only. Token names and colors are validated here
// as well as by the server; no arbitrary styles or image URLs are evaluated.
const themeColorTokens=['bg','surface','surface-2','text','text-2','text-3','line','line-strong','primary','on-primary','accent-soft','accent-text','ok-bg','ok','warn-bg','warn-line','warn','danger-bg','danger-line','danger','sidebar','sidebar-text','sidebar-active','focus'];
function effectiveIconTheme() {
  const selected=prefs?.icon_theme;
  return iconThemeCatalog?.themes.some(item=>item.id===selected)?selected:iconThemeCatalog?.default||'original';
}
function iconThemeName(item) {return item.name?.[language]||item.name?.en||item.id;}
async function loadIconThemes() {iconThemeCatalog=await api('/api/themes');return iconThemeCatalog;}
function applyIconThemeColors() {
  const item=iconThemeCatalog?.themes.find(item=>item.id===effectiveIconTheme());
  let colors=item?.colors||{};
  if(!me&&!item){try{colors=JSON.parse(preference('anas.portal.themeColors')||'{}');}catch{}}
  const dark=document.documentElement.dataset.theme==='dark'||document.documentElement.dataset.theme==='system'&&matchMedia('(prefers-color-scheme: dark)').matches;
  const palette=colors[dark?'dark':'light']||{};
  for(const key of themeColorTokens){document.documentElement.style.removeProperty('--'+key);if(/^#[0-9a-fA-F]{6}$/.test(palette[key]||''))document.documentElement.style.setProperty('--'+key,palette[key]);}
  if(item)preference('anas.portal.themeColors',JSON.stringify(colors));
}
matchMedia('(prefers-color-scheme: dark)').addEventListener('change',applyIconThemeColors);
function iconThemeCard(manage) {
  const card=el('section',null,{class:'icon-theme-settings'});card.append(el('h3',t('iconThemes')));
  if(!iconThemeCatalog){card.append(el('p',t('theme_load_failed'),{class:'notice warning'}),button(t('themeRetry'),async()=>{try{await loadIconThemes();rerender();}catch(error){notice(error.message,true);}}));return card;}
  if(!manage){
    const label=el('label',t('iconThemeChoice')),select=el('select',null,{name:'icon_theme'});
    select.append(el('option',t('themeDefault'),{value:''}));
    for(const item of iconThemeCatalog.themes)select.append(el('option',iconThemeName(item),{value:item.id}));
    select.value=iconThemeCatalog.themes.some(item=>item.id===prefs?.icon_theme)?prefs.icon_theme:'';label.append(select);card.append(label);
    if(prefs?.icon_theme&&!iconThemeCatalog.themes.some(item=>item.id===prefs.icon_theme)){
      const warning=el('div',null,{class:'notice warning',role:'status'});warning.append(el('p',t('theme_missing')),button(t('themeDefault'),async()=>{if(await savePreferences({icon_theme:''}))rerender();}));card.append(warning);
    }
    select.addEventListener('change',async()=>{select.disabled=true;try{if(await savePreferences({icon_theme:select.value}))rerender();}finally{select.disabled=false;}});
    card.append(el('p',t('themePersonalHelp'),{class:'muted'}));
  }
  if(manage){
    const form=el('form',null,{class:'card form-card'}),label=el('label',t('themeImport')),file=el('input',null,{type:'file',accept:'.json,application/json',name:'theme_file'});
    label.append(file);const submit=el('button',t('themeImport'),{type:'submit',class:'primary'}),slot=el('div',null,{role:'status'});form.append(label,el('p',t('themeImportHelp'),{class:'muted'}),submit,slot);card.append(form);
    form.addEventListener('submit',async event=>{
      event.preventDefault();submit.disabled=true;slot.replaceChildren();
      try{
        const selected=file.files[0];if(!selected)throw new Error(t('invalid_theme'));if(selected.size>32*1024*1024)throw new Error(t('theme_too_large'));
        let data;try{data=JSON.parse(await selected.text());}catch{throw new Error(t('invalid_theme'));}
        await api('/api/themes',data);await loadIconThemes();card.replaceWith(iconThemeCard(true));notice(t('themeImported'));
      }catch(error){slot.className='notice error';slot.textContent=error.message;}finally{submit.disabled=false;}
    });
    if(iconThemeCatalog.invalid_themes)card.append(el('p',t('theme_corrupt'),{class:'notice warning'}));
    const list=el('div',null,{class:'theme-library'});
    for(const item of iconThemeCatalog.themes){
      const row=el('section',null,{class:'card'});row.append(el('h3',iconThemeName(item)),el('p',item.id,{class:'muted mono'}));
      const preview=el('div',null,{class:'theme-preview'});for(const application of (catalog||[]).filter(a=>a.icons?.['48']).slice(0,5))preview.append(appIcon(application,40,item.id));row.append(preview);
      if(item.exportable){const download=el('a',t('themeExport'),{href:'/api/themes/'+encodeURIComponent(item.id)+'/export',download:item.id+'.anas-theme.json',class:'button'});row.append(download);}
      const details=el('details'),summary=el('summary',t('themeConfig'));details.append(summary,el('p',t('themeConfigHelp'),{class:'muted'}));
      const builtin=item.id==='original'||item.id==='soft';
      details.append(el('pre','application_catalog:\n  icon_theme: '+(builtin?item.id:'custom')+(builtin?'':'\n  theme_file: themes/'+item.id+'.anas-theme.json')));row.append(details);list.append(row);
    }
    card.append(list);
  }
  return card;
}

// --- shell -------------------------------------------------------------------
function canManage() {return !!me&&me.actions.includes('users.read');}
function route() {return location.hash.replace(/^#\/?/,'').split('/').filter(Boolean).map(part=>{try{return decodeURIComponent(part);}catch{return '';}});}
function brand(href,suffix) {
  const a=el('a',null,{href,class:'brand'}),mark=el('span',null,{class:'brand-mark','aria-hidden':'true'});
  for(let i=0;i<4;i++)mark.append(el('span'));
  a.append(mark,el('span','ANAS'));if(suffix)a.append(el('span',suffix,{class:'brand-suffix'}));return a;
}
function languageSelect() {
  const label=el('label',null,{class:'compact-select'}),select=el('select',null,{id:'language','aria-label':t('language')});
  for(const item of I18N.languages)select.append(el('option',item.label,{value:item.code,lang:item.code}));
  select.value=language;label.append(select);
  select.addEventListener('change',async()=>{
    language=select.value;preference('anas.portal.language',language);
    if(me)await savePreferences({language});
    rerender();
  });
  return label;
}
function themeButton() {
  const theme=document.documentElement.dataset.theme,b=button('',cycleTheme,'icon-button theme-button');
  b.setAttribute('aria-label',t('theme')+': '+themeLabel(theme));b.title=t('theme')+': '+themeLabel(theme);
  b.append(icon(theme==='dark'?'moon':theme==='light'?'sun':'monitor'));return b;
}
function renderCurrentShellControls() {
  const old=document.querySelector('.theme-button');if(old)old.replaceWith(themeButton());
  const radios=document.querySelectorAll('input[name=theme]');for(const r of radios)r.checked=r.value===document.documentElement.dataset.theme;
}
function navLink(href,text,iconName,active,extra='') {
  const a=withIcon(el('a',null,{href,class:'nav-link'+(extra?' '+extra:'')}),iconName);a.append(el('span',text));
  if(active)a.setAttribute('aria-current','page');return a;
}
function header(mode) {
  const bar=el('header',null,{class:'topbar'}),inner=el('div',null,{class:'topbar-inner'});
  inner.append(brand(me?'#/apps':'/',mode==='admin'?t('manage'):''));
  if(me) {
    const search=withIcon(button('',openPalette,'search-trigger'),'search');
    search.append(el('span',t('commandPlaceholder'),{class:'search-label'}),el('kbd',navigator.platform.includes('Mac')?'⌘K':'Ctrl K'));
    search.setAttribute('aria-label',t('commandOpen'));inner.append(search);
  }
  const actions=el('div',null,{class:'topbar-actions'});
  if(me&&canManage()) {
    const modes=el('nav',null,{class:'mode-switch','aria-label':t('mode')});
    modes.append(el('a',t('workbench'),{href:'#/apps',...(mode!=='admin'?{'aria-current':'page'}:{})}),el('a',t('manage'),{href:'#/admin/users',...(mode==='admin'?{'aria-current':'page'}:{})}));
    actions.append(modes);
  }
  actions.append(themeButton(),languageSelect());
  if(me) {
    const account=el('a',null,{href:'#/account/profile',class:'account-chip','aria-label':t('account')});
    account.append(avatar(me.person),el('span',me.person.display_name||me.person.username,{class:'account-name'}));
    const out=withIcon(button(t('logout'),completeNativeLogout,'ghost logout-button'),'logout');out.id='logout';
    actions.append(account,out);
  }
  inner.append(actions);bar.append(inner);return bar;
}
function bottomNav(active) {
  const nav=el('nav',null,{class:'bottom-nav','aria-label':t('mainNav')});
  nav.append(navLink('#/apps',t('apps'),'grid',active==='apps'),navLink('#/account/profile',t('account'),'user',active==='account'));
  if(canManage())nav.append(navLink('#/admin/users',t('manage'),'shield',active==='admin'));
  return nav;
}
function renderShell(mode,active) {
  document.documentElement.lang=language;document.title=me?'ANAS · '+t('portal'):'ANAS';
  app.replaceChildren();app.className='shell shell-'+mode;
  app.append(header(mode));
  const body=el('div',null,{class:'shell-body'});
  if(mode==='admin')body.append(adminSidebar(active));
  const main=el('main',null,{class:'page',id:'main'});main.append(noticeNode);
  content=el('div',null,{class:'page-content',id:'content'});main.append(content);body.append(main);app.append(body);
  if(me)app.append(bottomNav(mode==='admin'?'admin':active==='account'?'account':'apps'));
  return content;
}
function adminSidebar(active) {
  const side=el('aside',null,{class:'admin-sidebar','aria-label':t('adminNav')}),nav=el('nav');
  nav.append(el('p',t('identitySection'),{class:'side-label'}),navLink('#/admin/users',t('users'),'users',active==='users'));
  if(me.actions.includes('audit.read'))nav.append(navLink('#/admin/audit',t('audit'),'clock',active==='audit'));
  if(me.administration)nav.append(navLink('#/admin/themes',t('iconThemes'),'monitor',active==='themes'));
  nav.append(navLink('#/apps',t('backToWorkbench'),'back',false,'side-back'));
  side.append(nav);return side;
}
function rerender() {
  if(me)return render();
  if(location.pathname==='/signed-out'&&logoutState)return renderLogout();
  if(document.body.dataset.page==='native-logout')return nativeLogoutPage();
  if(route()[0]==='recover')return recoverPage();
  return loginPage();
}

// --- routing -------------------------------------------------------------------
async function loadCatalog() {
  const data=await api('/api/apps'),list=[];
  for(const group of data.groups)for(const c of group.categories){categoryNames[c.id]=c.display_name;for(const a of c.applications)list.push({...a,kind:group.kind||group.id,categoryName:c.display_name});}
  catalog=list;return data;
}
async function render() {
  const current=++revision;notice('');document.body.dataset.page='';
  const parts=route();
  try {
    const fresh=await api('/api/me');if(current!==revision)return;
    const first=!prefs;me=fresh;csrf=me.csrf;
    if(first){adoptPreferences(me.preferences||localPreferences());if(prefs.language&&prefs.language!==language){language=prefs.language;me=await api('/api/me');csrf=me.csrf;if(current!==revision)return;}}
    else if(me.preferences)prefs={...prefs,pinned:me.preferences.pinned};
    if(!catalog)await loadCatalog().catch(()=>{catalog=[];});
    if(!iconThemeCatalog)await loadIconThemes().catch(()=>{});
    applyIconThemeColors();
    if(current!==revision)return;
    const [section,sub,third]=parts;
    if(!section||section==='apps')return await homePage(current);
    if(section==='account'&&['profile','security','groups','mail','log',undefined].includes(sub))return await accountPage(sub||'profile',current);
    if(section==='admin'&&canManage()) {
      if(sub==='themes'&&me.administration){renderShell('admin','themes');document.body.dataset.page='admin-themes';heading(t('iconThemes'),t('themeLibraryHelp'));content.append(iconThemeCard(true));return;}
      if((!sub||sub==='users')&&!third)return await usersPage(current);
      if(sub==='users'&&third==='new'&&me.actions.includes('user.create'))return newUserPage();
      if(sub==='users'&&third)return await userDetails(third,current);
      if(sub==='audit'&&me.actions.includes('audit.read')){renderShell('admin','audit');document.body.dataset.page='admin-audit';return await auditTable(current);}
    }
    location.replace('#/apps');
  } catch(error) { if(current===revision){if(!content)renderShell(me?'work':'guest');notice(error.message,true);} }
}
window.addEventListener('hashchange',()=>{
  if(me)render();
  else if(route()[0]==='recover')recoverPage();
  else if(document.body.dataset.page==='recover')loginPage();
});

// --- anonymous pages -------------------------------------------------------------
function loginPage() {
  ++revision;document.body.dataset.page='login';renderShell('guest');
  const box=el('section',null,{class:'card hero'});
  box.append(brand('/'),el('h1',t('loginTitle')),el('p',t('loginIntro'),{class:'muted'}));
  box.append(withIcon(loginLink(t('login'),{class:'button primary large'}),'arrow'));
  const recovery=button(t('recover'),()=>{location.hash='#/recover';},'text-button');
  box.append(recovery);content.append(box);
}
async function recoverPage() {
  const current=++revision;document.body.dataset.page='recover';renderShell('guest');
  try {const b=await api('/api/bootstrap');if(current!==revision)return;csrf=b.csrf;}catch(error){notice(error.message,true);return;}
  const back=withIcon(el('a',t('back'),{href:'#/',class:'back-link'}),'back');
  content.append(back);passwordForm(true);
}

// --- home ------------------------------------------------------------------------
function greeting() {
  const h=new Date().getHours();return t(h<5?'greetingEvening':h<12?'greetingMorning':h<18?'greetingAfternoon':'greetingEvening',{name:me.person.display_name||me.person.username});
}
function passwordAlert(person) {
  let text='';
  if(person.must_change_password)text=t('mustChangeAlert');
  else if(person.password_expires_at) {
    const days=Math.ceil((new Date(person.password_expires_at)-Date.now())/86400000);
    if(days>=0&&days<=14)text=t('expiryAlert',{days});
  }
  if(!text)return null;
  const a=withIcon(el('a',null,{href:'#/account/security',class:'alert-strip'}),'lock');
  a.append(el('span',text),el('strong',t('changeNow')));return a;
}
function appIcon(a,size=44,theme=effectiveIconTheme()) {
  const icons=a.icons||{};
  const themed=path=>{const url=new URL(path,location.origin);url.searchParams.set('theme',theme);return url.pathname+url.search;};
  if(icons['48']?.path) {
    const img=el('img',null,{src:themed(icons[size>48?'128':'48']?.path||icons['48'].path),width:String(size),height:String(size),alt:'',loading:'lazy',class:'app-icon'});
    if(size<=48&&icons['128']?.path)img.setAttribute('srcset',themed(icons['48'].path)+' 1x, '+themed(icons['128'].path)+' 2x');
    return img;
  }
  return el('span',[...(a.display_name||a.name)][0]||'?',{class:'app-icon monogram','aria-hidden':'true'});
}
function pinButton(a) {
  const pinned=prefs.pinned.includes(a.name),b=withIcon(button('',async()=>{
    const list=pinned?prefs.pinned.filter(n=>n!==a.name):[...prefs.pinned,a.name].slice(0,24);
    if(await savePreferences({pinned:list}))homePage(revision);
  },'icon-button pin'+(pinned?' active':'')),'pin',16);
  b.setAttribute('aria-pressed',String(pinned));b.setAttribute('aria-label',t(pinned?'unpin':'pin')+': '+a.display_name);b.title=t(pinned?'unpin':'pin');
  b.disabled=!pinned&&prefs.pinned.length>=24;return b;
}
function infoButton(a) {
  const b=withIcon(button('',()=>appInfo(a),'icon-button info'),'info',16);
  b.setAttribute('aria-label',t('appInfo',{name:a.display_name}));b.title=t('appInfo',{name:a.display_name});return b;
}
function applicationCard(a) {
  const card=el('article',null,{class:'card app-card'});
  const main=safeLink(a.url,null,{class:'app-main'});
  const text=el('span',null,{class:'app-text'}),name=el('span',null,{class:'app-name'});
  name.append(el('span',a.display_name),icon('external',13));
  text.append(name,el('span',a.summary,{class:'app-summary'}));
  main.append(appIcon(a),text);
  const actions=el('div',null,{class:'app-actions'});actions.append(infoButton(a),pinButton(a));
  card.append(main,actions);return card;
}
// Application details open in the page; the card itself stays a plain entry.
function appInfo(a) {
  document.querySelector('dialog.app-info')?.remove();
  const dialog=el('dialog',null,{class:'app-info','aria-labelledby':'app-info-title'}),head=el('div',null,{class:'app-info-head'});
  const title=el('div');title.append(el('h2',a.display_name,{id:'app-info-title'}),el('p',a.summary,{class:'muted'}));
  const close=withIcon(button('',()=>finish(),'icon-button'),'close',16);close.setAttribute('aria-label',t('cancel'));
  head.append(appIcon(a,48),title,close);dialog.append(head);
  if(a.description)dialog.append(el('p',a.description,{class:'app-info-description'}));
  const names=(a.categories||[]).map(id=>categoryNames[id]||id).filter(Boolean);
  if(names.length){const chips=el('div',null,{class:'chip-list','aria-label':t('categoriesLabel')});for(const n of names)chips.append(el('span',n,{class:'badge'}));dialog.append(chips);}
  const foot=el('div',null,{class:'app-info-actions'});
  if(a.documentation_url)foot.append(withIcon(safeLink(a.documentation_url,t('docs'),{class:'button'}),'external',14));
  const pinned=prefs.pinned.includes(a.name),pin=withIcon(button(t(pinned?'unpin':'pin'),async()=>{
    const list=pinned?prefs.pinned.filter(n=>n!==a.name):[...prefs.pinned,a.name].slice(0,24);
    if(await savePreferences({pinned:list})){finish();if(document.body.dataset.page==='home')homePage(revision);}
  }),'pin',16);
  pin.disabled=!pinned&&prefs.pinned.length>=24;foot.append(pin);
  const open=safeLink(a.url,t('openApp'),{class:'button primary'});open.append(icon('external',14));foot.append(open);
  dialog.append(foot);document.body.append(dialog);
  function finish(){if(dialog.open)dialog.close();dialog.remove();}
  dialog.addEventListener('cancel',event=>{event.preventDefault();finish();});
  dialog.addEventListener('click',event=>{if(event.target===dialog)finish();});
  dialog.showModal();close.focus();
}
function pinnedSection() {
  const section=el('section',null,{class:'section','aria-labelledby':'pinned-title'}),head=el('div',null,{class:'section-head'});
  head.append(el('h2',t('pinned'),{id:'pinned-title'}));
  const apps=prefs.pinned.map(name=>catalog.find(a=>a.name===name)).filter(Boolean);
  let editing=section.dataset.editing==='true';
  const edit=button(t('editPinned'),()=>{editing=!editing;draw();},'ghost small');
  if(apps.length)head.append(edit);section.append(head);
  const grid=el('div',null,{class:'pinned-grid'});section.append(grid);
  async function move(from,to) {
    const list=[...prefs.pinned];const [item]=list.splice(from,1);list.splice(to,0,item);
    if(await savePreferences({pinned:list}))draw(to);
  }
  function draw(focusIndex) {
    edit.textContent=t(editing?'done':'editPinned');edit.setAttribute('aria-pressed',String(editing));
    grid.replaceChildren();const list=prefs.pinned.map(name=>catalog.find(a=>a.name===name)).filter(Boolean);
    if(!list.length){grid.append(el('p',t('pinnedEmpty'),{class:'muted empty-inline'}));return;}
    list.forEach((a,index)=>{
      const tile=el('div',null,{class:'pin-tile'+(editing?' editing':''),draggable:'true','data-index':String(index)});
      const link=safeLink(a.url,null,{class:'pin-main'});link.append(appIcon(a,48),el('span',a.display_name,{class:'app-name'}),el('span',a.categoryName||'',{class:'app-summary'}));
      tile.append(link);
      if(editing) {
        const tools=el('div',null,{class:'pin-tools'});
        const left=withIcon(button('',()=>move(index,index-1),'icon-button'),'left',16),right=withIcon(button('',()=>move(index,index+1),'icon-button'),'right',16),remove=withIcon(button('',async()=>{if(await savePreferences({pinned:prefs.pinned.filter(n=>n!==a.name)}))draw();},'icon-button'),'close',16);
        left.setAttribute('aria-label',t('moveEarlier')+': '+a.display_name);right.setAttribute('aria-label',t('moveLater')+': '+a.display_name);remove.setAttribute('aria-label',t('unpin')+': '+a.display_name);
        left.disabled=index===0;right.disabled=index===list.length-1;tools.append(left,right,remove);tile.append(tools);
      }
      tile.addEventListener('dragstart',event=>{event.dataTransfer.setData('text/plain',String(index));tile.classList.add('dragging');});
      tile.addEventListener('dragend',()=>tile.classList.remove('dragging'));
      tile.addEventListener('dragover',event=>event.preventDefault());
      tile.addEventListener('drop',event=>{event.preventDefault();const from=Number(event.dataTransfer.getData('text/plain'));if(Number.isInteger(from)&&from!==index)move(from,index);});
      grid.append(tile);
    });
    if(focusIndex!==undefined)grid.querySelectorAll('.pin-tile')[focusIndex]?.querySelector('button:not(:disabled)')?.focus();
  }
  draw();return section;
}
async function homePage(current=revision) {
  renderShell('work','apps');document.body.dataset.page='home';
  const head=heading(greeting(),null,new Date().toLocaleDateString(language,{weekday:'long',month:'long',day:'numeric'}));head.classList.add('home-head');
  const alert=passwordAlert(me.person);if(alert)content.append(alert);
  content.append(pinnedSection());
  const section=el('section',null,{class:'section','aria-labelledby':'all-title'}),head2=el('div',null,{class:'section-head wrap'});
  head2.append(el('h2',t('allApps'),{id:'all-title'}));section.append(head2);
  const controls=el('div',null,{class:'controls'}),search=el('input',null,{type:'search',placeholder:t('search'),'aria-label':t('search')});
  const chips=el('div',null,{class:'chips',role:'group','aria-label':t('all')});
  const toggle=el('label',null,{class:'check'}),checkbox=el('input',null,{type:'checkbox'});
  checkbox.checked=prefs.show_categories;toggle.append(checkbox,el('span',t('showCategories')));
  controls.append(withIcon(el('div',null,{class:'search-field'}),'search',16),toggle);controls.firstChild.append(search);
  section.append(controls,chips);
  const cards=el('div');section.append(cards);content.append(section);
  let data;
  try{data=await api('/api/apps');}catch(error){notice(error.message,true);return;}
  if(current!==revision||!cards.isConnected)return;
  let selected='';const seen=new Set(),categories=[];
  for(const group of data.groups)for(const c of group.categories)if(!seen.has(c.id)){seen.add(c.id);categories.push(c);}
  function drawChips() {
    chips.replaceChildren();
    for(const c of [{id:'',display_name:t('all')},...categories]){const chip=button(c.display_name,()=>{selected=c.id;drawChips();update();},'chip');chip.setAttribute('aria-pressed',String(selected===c.id));chips.append(chip);}
  }
  function draw() {
    cards.replaceChildren();if(!data.groups.length){cards.append(el('div',t('empty'),{class:'card empty'}));return;}
    for(const group of data.groups) {
      const block=el('section',null,{'data-kind':group.kind||group.id,class:'app-group'});block.append(el('h3',group.display_name,{class:'group-title'}));
      if(!checkbox.checked) {
        const grid=el('div',null,{class:'cards'});for(const c of group.categories)for(const a of c.applications)grid.append(applicationCard(a));block.append(grid);
      } else for(const c of group.categories) {
        block.append(el('h4',c.display_name,{class:'category-heading'}));
        const grid=el('div',null,{class:'cards'});for(const a of c.applications)grid.append(applicationCard(a));block.append(grid);
      }
      cards.append(block);
    }
  }
  drawChips();draw();
  checkbox.addEventListener('change',()=>{savePreferences({show_categories:checkbox.checked});draw();});
  let timer,queryRevision=0;
  async function update() {
    const q=++queryRevision;
    try {const next=await api('/api/apps?q='+encodeURIComponent(search.value)+(selected?'&category='+encodeURIComponent(selected):''));if(q===queryRevision&&cards.isConnected){data=next;draw();}}
    catch(error){if(cards.isConnected){cards.replaceChildren();notice(error.message,true);}}
  }
  search.addEventListener('input',()=>{clearTimeout(timer);timer=setTimeout(update,250);});
}

// --- shared account widgets ------------------------------------------------------
function statusBadge(p) {
  const key=p.disabled?'disabled':p.account_expired?'expired':p.locked?'locked':'active';
  return el('span',t(key),{class:'badge '+(key==='active'?'ok':'warn')});
}
function identityCard(p,title=false) {
  const card=el('section',null,{class:'card identity'});
  const text=el('div',null,{class:'identity-text'}),line=el('div',null,{class:'identity-line'});
  line.append(el(title?'h1':'h2',p.display_name||p.username),statusBadge(p));
  if(p.must_change_password)line.append(el('span',t('must_change'),{class:'badge warn'}));
  if(p.administrator)line.append(el('span',t('adminRole'),{class:'badge accent'}));
  if(p.domain_risk)line.append(el('span',t('domainRisk'),{class:'badge danger'}));
  text.append(line,el('p',p.username+(p.email?' · '+p.email:''),{class:'mono muted'}));
  card.append(avatar(p,'large'),text);return card;
}
function personDetails(p) {
  const panel=el('section',null,{class:'card person-details','data-anchor':p.anchor}),dl=el('dl');
  for(const [key,value] of [['username',p.username],['given_name',p.given_name],['surname',p.surname],['display_name',p.display_name],['email',p.email],['status',p.disabled?t('disabled'):p.account_expired?t('expired'):p.locked?t('locked'):t('active')],['password_status',p.must_change_password?t('must_change'):p.password_expired?t('password_expired'):p.password_state_known?t('active'):t('unknown')],['expires',p.password_expires_at?new Date(p.password_expires_at).toLocaleString(language):t('unknown')]])dl.append(el('dt',t(key)),el('dd',value||t('none')));
  panel.append(dl);return panel;
}
function groupTable(p) {
  const table=el('table'),head=el('tr');for(const key of ['groups','status','source'])head.append(el('th',t(key)));table.append(head);
  for(const g of p.groups) {
    const row=el('tr'),names=g.sources.map(id=>p.groups.find(v=>v.anchor===id||v.reference===id)?.name??id);
    const name=el('td',g.display_name||g.name);name.append(el('p',g.description||t('noDescription'),{class:'group-description muted'}));
    row.append(name,el('td',t(g.primary?'primary':g.direct?'direct':'inherited')),el('td',names.join(', ')||'—'));table.append(row);
  }
  const wrap=el('div',null,{class:'card table-wrap'});wrap.append(table);return wrap;
}
function field(form,key,type='text',required=true,value='') {
  const label=el('label',t(key)),input=el(type==='textarea'?'textarea':'input',null,{name:key,...(type==='textarea'?{}:{type}),...(required?{required:''}:{}),autocomplete:type==='password'?(key==='old_password'||key==='actor_password'?'current-password':'new-password'):'off'});
  if(type==='text')input.maxLength=64;input.value=value;label.append(input);form.append(label);return input;
}
function resultSlot(form) {const slot=el('div',null,{class:'form-result'});form.append(slot);return slot;}
function renderResult(data,slot,account,successKey='saved') {
  const bad=data.directory!=='saved',box=el('div',null,{class:'result'+(bad?' error':''),role:bad?'alert':'status',tabindex:'-1'});
  const labels={saved:t(successKey),rejected:t('rejected'),denied:t('access_denied'),unavailable:t('identity_unavailable'),conflict:t('conflict'),unknown:t('unknownResult'),not_attempted:t('auditFailure')};
  box.append(el('p',labels[data.directory]??(t(data.directory)||t('unknownResult')),{class:'result-title'}));
  // Report the operation we actually completed. Unobserved downstream states
  // are not progress, so they do not belong in the person's result message.
  if(data.ownership)box.append(el('p',t('ownershipStep')+': '+(data.ownership==='not_attempted'?t('not_attempted'):(labels[data.ownership]??t(data.ownership)))),el('p',t('membershipStep')+': '+(data.membership==='not_attempted'?t('not_attempted'):(labels[data.membership]??t(data.membership)))));
  if(data.ownership==='saved'&&data.membership!=='saved')box.append(el('p',t('ownershipRetained')));
  if(data.audit==='unavailable'||data.audit===false)box.append(el('p',t('auditFailure')));
  if(data.request_id)box.append(el('small',data.request_id,{class:'mono'}));
  if(data.temporary_password) {
    box.append(el('p',t('oneTime')));
    const row=el('div',null,{class:'secret-row'}),secret=el('input',null,{type:'text',readonly:'',value:data.temporary_password,'aria-label':t('tempPassword'),class:'mono'});
    row.append(secret,copyButton(t('copyPassword'),t('copied'),()=>secret.value,secret));box.append(row);
    if(account?.force)box.append(el('p',t('firstLoginNote'),{class:'notice warning'}));
    if(account)box.append(copyButton(t('copyAll'),t('copiedAll'),()=>accountSummary(account,data.temporary_password),secret));
  }
  if(data.reauthenticate){me=null;box.append(el('p',t('reauthenticate')),loginLink(t('login'),{class:'button primary'}));}
  if(slot)slot.replaceChildren(box);else content.append(box);box.focus({preventScroll:true});box.scrollIntoView({block:'nearest',behavior:'smooth'});return box;
}
function copyButton(label,done,text,fallback) {
  const b=button(label,async()=>{
    try{await navigator.clipboard.writeText(text());b.textContent=done;}
    catch{if(fallback){fallback.focus();fallback.select();}}
  },'small');return b;
}
// Everything an administrator hands to the person, in the page language.
function accountSummary(account,password) {
  const portal=location.origin+'/',lines=[[t('username'),account.username],[t('display_name'),account.display_name],[t('email'),account.email],[t('tempPassword'),password],[t('loginUrl'),portal]];
  if(account.force)lines.push([t('changeUrl'),portal+'#/recover']);
  const text=lines.filter(([,v])=>v).map(([k,v])=>k+': '+v);
  if(account.force)text.push('',t('firstLoginNote'));
  return text.join('\n');
}
function formError(slot,error) {
  slot.replaceChildren(el('div',error.message||t('request_failed'),{class:'result error',role:'alert',tabindex:'-1'}));slot.firstChild.focus({preventScroll:true});slot.scrollIntoView({block:'nearest'});
}
function profileForm(person,version,admin=false,requiresDomain=false) {
  const form=el('form',null,{class:'card form-card'});form.append(el('h2',t('edit')),el('p',t('profileHelp'),{class:'muted'}));
  const grid=el('div',null,{class:'form-grid'});form.append(grid);
  field(grid,'given_name','text',false,person.given_name||'');field(grid,'surname','text',false,person.surname||'');field(grid,'display_name','text',true,person.display_name||'');
  const submit=el('button',t('submit'),{type:'submit',class:'primary'});form.append(el('div',null,{class:'form-actions'}));form.lastChild.append(submit);const slot=resultSlot(form);
  form.addEventListener('submit',async event=>{
    event.preventDefault();submit.disabled=true;
    try {
      const values=Object.fromEntries(new FormData(form)),profile={given_name:values.given_name.trim(),surname:values.surname.trim(),display_name:values.display_name.trim()};
      const op={action:'profile.update',target:person.anchor,profile,profile_version:version};
      const data=admin?await executeAdmin(op,requiresDomain,{person}):await api('/api/me/profile',op);
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
            const card=content.querySelector('.identity');if(card)card.replaceWith(identityCard(fresh.person,admin));
          }
        }catch {
          if(form.isConnected)result.append(el('p',t('profile_refresh_failed'),{role:'alert'}));
        }
      }
    }catch(error){formError(slot,error);}finally{submit.disabled=false;}
  });return form;
}
function passwordForm(recover) {
  if(recover)heading(t('recover'),t('recoverIntro'));
  const form=el('form',null,{class:'card form-card narrow'});if(!recover)form.append(el('h2',t('password')));
  if(recover)field(form,'username');field(form,'old_password','password');field(form,'new_password','password');field(form,'confirm_password','password');
  const submit=el('button',t('submit'),{type:'submit',class:'primary'});form.append(submit);const slot=resultSlot(form);content.append(form);
  form.addEventListener('submit',async event=>{
    event.preventDefault();submit.disabled=true;
    try{const data=await api('/api/password/'+(recover?'recover':'change'),Object.fromEntries(new FormData(form)));renderResult(data,slot,undefined,'passwordChanged');}
    catch(error){formError(slot,error);}finally{for(const input of form.querySelectorAll('input[type=password]'))input.value='';submit.disabled=false;}
  });
}

// --- account ------------------------------------------------------------------------
function preferencesCard() {
  const card=el('section',null,{class:'card form-card'});card.append(el('h2',t('preferences')),el('p',t('preferencesHelp'),{class:'muted'}));
  const lang=el('label',t('language')),select=el('select',null,{name:'preferred_language'});
  select.append(el('option',t('languageAuto'),{value:''}));for(const item of I18N.languages)select.append(el('option',item.label,{value:item.code,lang:item.code}));
  select.value=prefs.language;lang.append(select);
  select.addEventListener('change',async()=>{if(await savePreferences({language:select.value})){if(select.value)language=select.value;else{const b=await api('/api/bootstrap?lang=');language=b.language;}render();}});
  const fieldset=el('fieldset',null,{class:'segmented'});fieldset.append(el('legend',t('theme')));
  for(const value of ['system','light','dark']) {
    const label=el('label'),radio=el('input',null,{type:'radio',name:'theme',value});radio.checked=document.documentElement.dataset.theme===value;
    radio.addEventListener('change',()=>savePreferences({theme:value}).then(renderCurrentShellControls));
    label.append(radio,icon(value==='dark'?'moon':value==='light'?'sun':'monitor',16),el('span',themeLabel(value)));fieldset.append(label);
  }
  const show=el('label',null,{class:'check'}),box=el('input',null,{type:'checkbox',name:'show_categories'});box.checked=prefs.show_categories;
  box.addEventListener('change',()=>savePreferences({show_categories:box.checked}));show.append(box,el('span',t('showCategories')));
  card.append(lang,fieldset,show,iconThemeCard(false));return card;
}
async function accountPage(section,current) {
  renderShell('work','account');document.body.dataset.page='account-'+section;
  const layout=el('div',null,{class:'settings'}),nav=el('nav',null,{class:'settings-nav','aria-label':t('account')});
  nav.append(el('h1',t('account')));
  for(const [id,key,name] of [['profile','profileNav','user'],['security','securityNav','lock'],['groups','groupsNav','users'],['mail','mailNav','mail'],['log','audit','clock']])nav.append(navLink('#/account/'+id,t(key),name,section===id));
  // The header hides its sign-out button on phones; this one is shown only there.
  const out=withIcon(button(t('logout'),completeNativeLogout,'nav-link mobile-only'),'logout');nav.append(out);
  const body=el('div',null,{class:'settings-body'});layout.append(nav,body);content.append(layout);
  const outer=content;content=body;
  try {
    if(section==='profile') {
      content.append(identityCard(me.person),personDetails(me.person));
      if(me.profile_editable)content.append(profileForm(me.person,me.profile_version));
      content.append(preferencesCard());
    } else if(section==='security') {
      const p=me.person,card=el('section',null,{class:'card'}),stats=el('div',null,{class:'stats'});
      card.append(el('h2',t('password_status')));
      for(const [key,value] of [['password_status',p.must_change_password?t('must_change'):p.password_expired?t('password_expired'):p.password_state_known?t('active'):t('unknown')],['expires',p.password_expires_at?new Date(p.password_expires_at).toLocaleString(language):t('unknown')],['status',p.disabled?t('disabled'):p.account_expired?t('expired'):p.locked?t('locked'):t('active')]]){const s=el('div',null,{class:'stat'});s.append(el('span',t(key),{class:'muted'}),el('strong',value));stats.append(s);}
      card.append(stats);content.append(card);passwordForm(false);
    } else if(section==='groups') {
      const apps=(catalog||[]).filter(a=>a.kind!=='admin'),card=el('section',null,{class:'card'});card.append(el('h2',t('accessibleApps')));
      const list=el('div',null,{class:'chip-list'});for(const a of apps){const chip=el('span',null,{class:'app-chip'});chip.append(appIcon(a,22),el('span',a.display_name));list.append(chip);}
      if(!apps.length)list.append(el('p',t('empty'),{class:'muted'}));
      card.append(list);content.append(card,el('h2',t('groups'),{class:'section-title'}),groupTable(me.person));
    } else if(section==='mail') {
      const card=el('section',null,{class:'card'}),row=el('div',null,{class:'mail-row'});
      card.append(el('h2',t('email')));row.append(el('span',me.person.email||t('none'),{class:'mono'}));if(me.person.email)row.append(el('span',t('primaryAddress'),{class:'badge accent'}));
      card.append(row,el('p',t('mailHelp'),{class:'muted'}));content.append(card);
    } else await auditTable(current);
  } finally {content=outer;}
}
async function auditTable(current) {
  if(content.closest?.('.shell-admin'))heading(t('audit'));
  const data=await api('/api/audit');if(current!==revision)return;
  const table=el('table'),head=el('tr');
  for(const key of ['time','action','target','result'])head.append(el('th',t(key)));table.append(head);
  for(const record of data.records){
    const row=el('tr'),result=el('td',t(record.directory||record.phase));
    if(record.ownership)result.append(el('p',t('ownershipStep')+': '+t(record.ownership)),el('p',t('membershipStep')+': '+t(record.membership)));
    row.append(el('td',new Date(record.time).toLocaleString(language)),el('td',t(record.action)),el('td',record.target||'—',{class:'mono'}),result);table.append(row);
  }
  if(!data.records.length){const row=el('tr');row.append(el('td',t('none'),{colspan:'4',class:'muted'}));table.append(row);}
  const panel=el('div',null,{class:'card table-wrap'});panel.append(table);content.append(panel);
}

// --- administration -------------------------------------------------------------------
async function usersPage(current) {
  renderShell('admin','users');document.body.dataset.page='admin-users';heading(t('users'));
  if(me.actions.includes('user.create'))content.querySelector('.page-head').append(withIcon(el('a',t('createUser'),{href:'#/admin/users/new',class:'button primary',role:'button'}),'plus'));
  const controls=el('form',null,{class:'controls'}),input=el('input',null,{type:'search',placeholder:t('searchPeople'),'aria-label':t('searchPeople')}),search=el('button',t('find'),{type:'submit'});
  controls.append(withIcon(el('div',null,{class:'search-field'}),'search',16),search);controls.firstChild.append(input);content.append(controls);
  const panel=el('div',null,{class:'card table-wrap'});content.append(panel);let loadRevision=0;
  async function load(offset) {
    const mine=++loadRevision;panel.replaceChildren(el('p',t('loading'),{class:'muted'}));
    try {
      const data=await api('/api/users?q='+encodeURIComponent(input.value)+'&offset='+offset);if(mine!==loadRevision||!panel.isConnected||current!==revision)return;panel.replaceChildren();
      if(!data.users.length){panel.append(el('p',t('noUsers'),{class:'muted empty-inline'}));return;}
      const table=el('table'),head=el('tr');for(const key of ['username','display_name','email','status','details'])head.append(el('th',t(key)));table.append(head);
      for(const p of data.users) {
        const row=el('tr'),actions=el('td');actions.append(button(t('details'),()=>{location.hash='#/admin/users/'+encodeURIComponent(p.anchor);},'small'));
        const name=el('td');const who=el('div',null,{class:'who'});who.append(avatar(p,'small'),el('span',p.username,{class:'mono'}));name.append(who);if(p.administrator)name.append(el('span',t('adminRole'),{class:'badge accent'}));
        const status=el('td');status.append(statusBadge(p));
        row.append(name,el('td',p.display_name),el('td',p.email),status,actions);table.append(row);
      }
      panel.append(table);const pages=el('div',null,{class:'row pagination'});pages.append(el('span',t('totalCount',{count:data.count}),{class:'muted'}));
      if(offset>0)pages.append(withIcon(button('',()=>load(Math.max(0,offset-25)),'icon-button'),'left'));if(offset+25<data.count)pages.append(withIcon(button('',()=>load(offset+25),'icon-button'),'right'));
      for(const b of pages.querySelectorAll('button'))b.setAttribute('aria-label',b.querySelector('path')?.getAttribute('d')===ICONS.left?t('previous'):t('next'));
      panel.append(pages);
    }catch(error){if(panel.isConnected){panel.replaceChildren();notice(error.message,true);}}
  }
  controls.addEventListener('submit',event=>{event.preventDefault();load(0);});await load(0);
}
function newUserPage() {
  renderShell('admin','users');document.body.dataset.page='admin-new-user';
  content.append(withIcon(el('a',t('users'),{href:'#/admin/users',class:'back-link'}),'back'));heading(t('createUser'),t('createIntro'));
  const form=el('form',null,{class:'card form-card narrow'}),username=field(form,'username');
  username.maxLength=20;username.pattern='[A-Za-z0-9_][A-Za-z0-9._-]{0,19}';
  form.append(el('p',t('usernameHelp'),{class:'muted'}));
  const email=el('p','—',{'data-generated-email':'',class:'generated mono'});
  form.append(el('strong',t('generatedEmail')),email);
  username.addEventListener('input',()=>{email.textContent=username.value&&username.checkValidity()?username.value.toLowerCase()+'@'+me.mail_domain:'—';});
  field(form,'given_name','text',false);field(form,'surname','text',false);field(form,'display_name');
  const force=el('input',null,{type:'checkbox',name:'force_change'}),label=el('label',null,{class:'check'});force.checked=true;label.append(force,el('span',t('force')));form.append(label);
  const submit=el('button',t('createUser'),{type:'submit',class:'primary'});form.append(submit);const slot=resultSlot(form);
  form.addEventListener('submit',async event=>{
    event.preventDefault();submit.disabled=true;
    try {
      const values=Object.fromEntries(new FormData(form)),op={username:values.username,profile:{given_name:values.given_name.trim(),surname:values.surname.trim(),display_name:values.display_name.trim()},force_change:force.checked};
      if(!await operationDialog(op,false,{person:{username:op.username,...op.profile},action:'user.create'}))return;
      const result=await api('/api/users',op);if(!form.isConnected)return;
      const box=renderResult(result,slot,{username:op.username,display_name:op.profile.display_name,email:op.username.toLowerCase()+'@'+me.mail_domain,force:op.force_change});
      if(result.directory==='saved'){
        // Do not navigate away automatically: the temporary password is only
        // displayed in this result. Explicitly opening details discards it.
        for(const input of form.querySelectorAll('input:not([readonly]),textarea'))input.disabled=true;
        // Details open in a new tab so this one-time result stays available.
        const details=el('a',t('details'),{href:'/#/admin/users/'+encodeURIComponent(result.target),target:'_blank',rel:'noopener',class:'button small'});
        details.append(icon('external',14),el('span',t('openNewTab'),{class:'sr-only'}));box.append(details);return;
      }
      if(result.directory==='conflict')box.append(el('p',t('accountConflict')));
    }catch(error){if(form.isConnected)formError(slot,error);}finally{if(!form.querySelector('input:disabled'))submit.disabled=false;}
  });content.append(form);
}
async function operationDialog(op,domain,context={}) {
  const confirmed=domain||Boolean(context.adminRole);
  const preview=confirmed?await api('/api/administration/prepare',op):null;
  return new Promise(resolve=>{
    const dialog=el('dialog',null,{class:'operation-dialog','aria-labelledby':'operation-title'}),form=el('form');
    form.append(el('h2',t(context.adminRole?'adminRoleTitle':domain?'domainTitle':'operationTitle'),{id:'operation-title'}));
    const summary=el('dl',null,{class:'operation-summary'}),person=context.person||{};
    const entry=(key,value)=>{summary.append(el('dt',t(key)),el('dd',value||'—'));};
    const action=context.action||op.action,actionLabel={'groups.member.add':'add','groups.member.remove':'remove','password.reset':'reset','account.unlock':'unlock','user.create':'createUser','profile.update':'edit'};
    entry('action',t(actionLabel[action]||action));entry('target',person.username||preview?.target||op.target);
    if(op.group)entry('group',context.group?.name||preview?.group||op.group);
    if(op.profile){for(const key of ['given_name','surname','display_name'])entry(key,op.profile[key]);}
    if(Object.hasOwn(op,'force_change'))entry('force',op.force_change?'☑':'☐');form.append(summary);
    if(context.group?.description)form.append(el('p',context.group.description,{class:'muted'}));
    if(confirmed)form.append(el('p',t(context.adminRole?'adminRoleHelp':'domainHelp'),{class:'notice warning'}));
    if(preview?.ownership_transfer)form.append(el('p',t('ownershipWarning'),{class:'notice warning'}));
    const password=domain?field(form,'actor_password','password'):null;
    const row=el('div',null,{class:'row dialog-actions'}),cancel=button(t('cancel'),()=>finish(false)),submit=el('button',t('confirmAction'),{type:'submit',class:'primary'});
    row.append(cancel,submit);form.append(row);const slot=resultSlot(form);dialog.append(form);document.body.append(dialog);
    let settled=false,busy=false;
    const finish=accepted=>{
      if(settled)return;settled=true;
      if(password)password.value='';dialog.close();dialog.remove();resolve(accepted);
    };
    form.addEventListener('submit',async event=>{
      event.preventDefault();if(busy)return;busy=true;submit.disabled=true;
      try {
        if(settled)return;
        if(confirmed)op.proof=preview.proof;
        if(domain)op.actor_password=password.value;
        finish(true);
      }catch(error){if(!settled)formError(slot,error);}finally{busy=false;submit.disabled=false;}
    });
    dialog.addEventListener('cancel',event=>{event.preventDefault();finish(false);});
    dialog.addEventListener('close',()=>finish(false));
    dialog.showModal();cancel.focus();
  });
}
async function executeAdmin(op,domain,context={},confirm=true) {
  if(!me.administration)return null;
  try {
    if((confirm||domain)&&!await operationDialog(op,domain,context))return null;
    return await api('/api/administration/execute',op);
  }finally{op.actor_password='';op.proof='';}
}
async function userDetails(anchor,current=revision) {
  renderShell('admin','users');document.body.dataset.page='admin-user';
  const data=await api('/api/users/'+encodeURIComponent(anchor));if(current!==revision)return;
  const crumbs=el('nav',null,{class:'crumbs','aria-label':t('breadcrumb')});crumbs.append(el('a',t('users'),{href:'#/admin/users'}),el('span','/',{'aria-hidden':'true'}),el('span',data.person.display_name||data.person.username));
  content.append(crumbs,identityCard(data.person,true),personDetails(data.person));
  if(data.actions.includes('profile.update'))content.append(profileForm(data.person,data.profile_version,true,data.requires_domain&&data.person.anchor!==me.person.anchor));
  const operationSlot=el('div',null,{class:'form-result'});
  if(data.actions.includes('password.reset')||data.actions.includes('account.unlock')) {
    const zone=el('section',null,{class:'card danger-zone','aria-labelledby':'danger-title'});zone.append(el('h2',t('dangerZone'),{id:'danger-title'}));
    const form=el('form',null,{class:'danger-form'});
    const slot=el('div',null,{class:'form-result'});
    async function operate(action) {
      if(!form.reportValidity())return;
      const buttons=form.querySelectorAll('button');buttons.forEach(b=>b.disabled=true);
      try {
        const force=!!form.querySelector('[name=force_change]')?.checked;
        const op={action,target:anchor,...(action==='password.reset'?{force_change:force}:{})};
        let result;
        if(data.administration)result=await executeAdmin(op,data.requires_domain,{person:data.person});
        else if(await operationDialog(op,false,{person:data.person}))result=await api('/api/users/'+encodeURIComponent(anchor)+'/'+(action==='password.reset'?'reset':'unlock'),action==='password.reset'?{force_change:force}:{});
        if(result)renderResult(result,slot,action==='password.reset'?{username:data.person.username,display_name:data.person.display_name,email:data.person.email,force}:undefined);
      }catch(error){formError(slot,error);}finally{buttons.forEach(b=>b.disabled=false);}
    }
    if(data.actions.includes('password.reset')) {
      const row=el('div',null,{class:'danger-row'}),text=el('div');text.append(el('strong',t('reset')),el('p',t('resetHelp'),{class:'muted'}));
      const label=el('label',null,{class:'check'}),input=el('input',null,{type:'checkbox',name:'force_change'});input.checked=true;label.append(input,el('span',t('force')));text.append(label);
      row.append(text,button(t('reset'),()=>operate('password.reset'),'danger'));form.append(row);
    }
    if(data.actions.includes('account.unlock')) {
      const row=el('div',null,{class:'danger-row'}),text=el('div');text.append(el('strong',t('unlock')),el('p',t(data.person.locked?'unlockHelp':'notLocked'),{class:'muted'}));
      row.append(text,button(t('unlock'),()=>operate('account.unlock'),'danger-outline'));form.append(row);
    }
    form.append(slot);zone.append(form);content.append(zone);
  }
  const groupsHead=el('div',null,{class:'section-head'});groupsHead.append(el('h2',t('groups')));
  content.append(groupsHead,el('p',t('groupRisk'),{class:'muted'}));
  const advanced=el('details',null,{class:'advanced card'});advanced.append(el('summary',t('advanced')),el('p',t('advancedHelp'),{class:'notice warning'}));
  const membership=el('div'),advancedMembership=el('div');advanced.append(advancedMembership);content.append(membership);
  let groupRevision=0;
  async function loadGroups() {
    const load=++groupRevision;const memberships=await api('/api/groups?target='+encodeURIComponent(anchor)+(advanced.open?'&advanced=true':''));if(current!==revision||load!==groupRevision)return;membership.replaceChildren();advancedMembership.replaceChildren();
    for(const category of ['applications','capabilities','access','roles','system']) {
      const items=memberships.groups.filter(item=>(item.group.category||'access')===category);if(!items.length)continue;
      const container=category==='system'?advancedMembership:membership;
      container.append(el('h3',t(category),{class:'category-heading'}));const wrap=el('div',null,{class:'card table-wrap flush'}),table=el('table',null,{class:'group-table'});
      for(const item of items) {
        const row=el('tr'),name=el('td'),state=el('td',t(item.primary?'primary':item.direct?'direct':item.inherited?'inherited':'none'),{class:'group-state'}),actions=el('td',null,{class:'group-actions'});
        name.append(el('strong',item.group.display_name||item.group.name));
        if(item.group.display_name&&item.group.display_name!==item.group.name)name.append(el('small',item.group.name,{class:'group-name mono'}));
        name.append(el('p',item.group.description||t('noDescription'),{class:'group-description muted'}));
        if(item.group.domain_risk)name.append(el('span',t('domainRisk'),{class:'badge danger'}));
        if(item.group.admin_role)name.append(el('span',t('adminRole'),{class:'badge accent'}));
        if(item.group.risk==='resource')name.append(el('span',t('resourceRisk'),{class:'badge warn'}));
        async function change(add) {
          const personal=Boolean(item.requires_domain||item.requires_password),needsConfirmation=Boolean(!add||!item.group.managed||personal||item.group.admin_role);
          for(const b of actions.querySelectorAll('button'))b.disabled=true;
          try {
            const op={action:add?'groups.member.add':'groups.member.remove',target:anchor,group:item.group.reference,expected_direct:item.direct},context={person:data.person,group:item.group,adminRole:item.group.admin_role&&!item.requires_domain};
            let result;
            if(data.administration)result=await executeAdmin(op,personal,context,needsConfirmation);
            else if(!needsConfirmation||await operationDialog(op,false,context))result=await api('/api/groups/'+encodeURIComponent(item.group.anchor)+'/member',{target:anchor,add,expected_direct:item.direct});
            if(result){renderResult(result,operationSlot);if(result.directory==='saved')await loadGroups();}
          }catch(error){formError(operationSlot,error);}finally{for(const b of actions.querySelectorAll('button'))b.disabled=false;}
        }
        if(item.can_add)actions.append(button(t('add'),()=>change(true),'small'));if(item.can_remove)actions.append(button(t('remove'),()=>change(false),'small'));
        row.append(name,state,actions);table.append(row);
      }
      wrap.append(table);container.append(wrap);
    }
  }
  content.append(operationSlot);if(data.administration)content.append(advanced);
  advanced.addEventListener('toggle',()=>loadGroups().catch(error=>formError(operationSlot,error)));await loadGroups();
}

// --- in-page recent authentication ----------------------------------------------------
let reauthPending=null;
function reauthDialog() {
  if(reauthPending)return reauthPending;
  reauthPending=new Promise(resolve=>{
    const dialog=el('dialog',null,{class:'operation-dialog reauth-dialog','aria-labelledby':'reauth-title'}),form=el('form');
    form.append(el('h2',t('reauthTitle'),{id:'reauth-title'}),el('p',t('reauthIntro'),{class:'muted'}));
    const who=el('div',null,{class:'who'});who.append(avatar(me.person,'small'),el('span',me.person.username,{class:'mono'}));form.append(who);
    const password=field(form,'old_password','password');password.setAttribute('autocomplete','current-password');
    const slot=resultSlot(form),row=el('div',null,{class:'row dialog-actions'}),cancel=button(t('cancel'),()=>finish(false)),submit=el('button',t('confirmAction'),{type:'submit',class:'primary'});
    row.append(cancel,submit);form.append(row);dialog.append(form);document.body.append(dialog);
    let settled=false;
    function finish(ok){if(settled)return;settled=true;password.value='';dialog.close();dialog.remove();reauthPending=null;resolve(ok);}
    form.addEventListener('submit',async event=>{
      event.preventDefault();submit.disabled=true;
      try{
        const r=await fetch('/api/reauth/password?lang='+encodeURIComponent(language),{method:'POST',credentials:'same-origin',cache:'no-store',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify({password:password.value}),signal:AbortSignal.timeout(30000)});
        const data=await r.json().catch(()=>({}));
        if(r.ok){finish(true);return;}
        password.value='';formError(slot,new Error(t(data.error||'request_failed')));password.focus();
      }catch{formError(slot,new Error(t('request_failed')));}
      finally{submit.disabled=false;}
    });
    dialog.addEventListener('cancel',event=>{event.preventDefault();finish(false);});
    dialog.showModal();password.focus();
  });
  return reauthPending;
}

// --- quick search (⌘K / Ctrl+K) ---------------------------------------------------------
function paletteItems(query) {
  const q=query.trim().toLowerCase(),pages=[['apps','#/apps','grid'],['account','#/account/profile','user'],['password','#/account/security','lock'],['audit','#/account/log','clock']];
  if(canManage())pages.push(['users','#/admin/users','users']);
  if(me.actions.includes('user.create'))pages.push(['createUser','#/admin/users/new','plus']);
  const items=[];
  for(const a of catalog||[])if(!q||(a.display_name+' '+a.name+' '+a.summary).toLowerCase().includes(q))items.push({group:'commandApps',label:a.display_name,sub:a.summary,app:a,run:()=>{const link=safeLink(a.url);if(link.href)window.open(link.href,'_blank','noopener,noreferrer');}});
  for(const [key,hash,name] of pages)if(!q||t(key).toLowerCase().includes(q))items.push({group:'commandPages',label:t(key),icon:name,run:()=>{location.hash=hash;}});
  return items;
}
function openPalette() {
  if(!me||document.querySelector('.palette'))return;
  const dialog=el('dialog',null,{class:'palette','aria-label':t('commandOpen')}),box=el('div',null,{class:'palette-input'});
  const input=el('input',null,{type:'text',role:'combobox','aria-expanded':'true','aria-controls':'palette-list','aria-autocomplete':'list',placeholder:t('commandPlaceholder'),'aria-label':t('commandPlaceholder')});
  box.append(icon('search'),input,el('kbd','Esc'));
  const list=el('ul',null,{id:'palette-list',role:'listbox','aria-label':t('commandResults')});
  dialog.append(box,list);document.body.append(dialog);
  let items=[],active=0,people=[],peopleRevision=0,timer;
  function draw() {
    items=[...paletteItems(input.value),...people];list.replaceChildren();active=Math.min(active,Math.max(0,items.length-1));
    let group='';
    items.forEach((item,index)=>{
      if(item.group!==group){group=item.group;list.append(el('li',t(group),{class:'palette-group',role:'presentation'}));}
      const li=el('li',null,{role:'option',id:'palette-'+index,class:'palette-item','aria-selected':String(index===active)});
      li.append(item.app?appIcon(item.app,24):item.person?avatar(item.person,'small'):icon(item.icon,18));
      const text=el('span',null,{class:'palette-text'});text.append(el('span',item.label));if(item.sub)text.append(el('small',item.sub,{class:'muted'}));li.append(text);
      li.addEventListener('mousedown',event=>{event.preventDefault();choose(index);});li.addEventListener('mousemove',()=>{if(active!==index){active=index;mark();}});
      list.append(li);
    });
    if(!items.length)list.append(el('li',t('noResults'),{class:'palette-empty',role:'presentation'}));
    mark();
  }
  function mark() {
    for(const li of list.querySelectorAll('[role=option]'))li.setAttribute('aria-selected',String(li.id==='palette-'+active));
    input.setAttribute('aria-activedescendant',items.length?'palette-'+active:'');
    list.querySelector('[aria-selected=true]')?.scrollIntoView({block:'nearest'});
  }
  function choose(index) {const item=items[index];if(!item)return;close();item.run();}
  function close() {dialog.close();dialog.remove();}
  // People come from the existing scoped personnel query; nothing new is exposed.
  async function searchPeople() {
    const mine=++peopleRevision,q=input.value.trim();
    if(!canManage()||q.length<2){people=[];draw();return;}
    try{const data=await api('/api/users?q='+encodeURIComponent(q));if(mine!==peopleRevision)return;
      people=data.users.slice(0,8).map(p=>({group:'commandPeople',label:p.display_name||p.username,sub:p.username,person:p,run:()=>{location.hash='#/admin/users/'+encodeURIComponent(p.anchor);}}));draw();}catch{}
  }
  input.addEventListener('input',()=>{active=0;draw();clearTimeout(timer);timer=setTimeout(searchPeople,250);});
  input.addEventListener('keydown',event=>{
    if(event.key==='ArrowDown'){event.preventDefault();active=Math.min(items.length-1,active+1);mark();}
    else if(event.key==='ArrowUp'){event.preventDefault();active=Math.max(0,active-1);mark();}
    else if(event.key==='Enter'){event.preventDefault();choose(active);}
  });
  dialog.addEventListener('cancel',event=>{event.preventDefault();close();});
  dialog.addEventListener('click',event=>{if(event.target===dialog)close();});
  draw();dialog.showModal();input.focus();
  if(!catalog)loadCatalog().then(draw).catch(()=>{});
}
document.addEventListener('keydown',event=>{
  if((event.metaKey||event.ctrlKey)&&!event.altKey&&event.key.toLowerCase()==='k'){if(me){event.preventDefault();openPalette();}}
});

// --- sign-out ---------------------------------------------------------------------------
function saveLogout() {
  try{sessionStorage.setItem('anas.portal.logout',JSON.stringify(logoutState));}catch{}
}
function renderLogout() {
  me=null;csrf='';document.body.dataset.page='signed-out';renderShell('guest');
  const box=el('section',null,{class:'card hero'});content.append(box);
  const outer=content;content=box;heading(t('logout'));content=outer;
  box.append(el('a',t('home'),{href:'/',class:'button primary'}));
  const list=el('ul',null,{class:'logout-results'});
  for(const item of logoutState.results){const li=el('li',null,{class:'step '+(item.status==='signed_out'?'done':item.status==='working'?'pending':'unknown')});li.append(icon(item.status==='signed_out'?'check':item.status==='working'?'sync':'alert',16),el('span',item.origin+': '+t(item.status)));list.append(li);}
  box.append(list);
  const complete=logoutState.results.every(item=>item.status==='signed_out');
  notice(t(logoutRunning?'logoutWorking':complete?'logoutComplete':'logoutPartial'),!logoutRunning&&!complete);
  if(!logoutRunning&&!complete){const retry=button(t('logoutRetry'),retryLogout);retry.id='iam-retry';box.append(retry);}
}
async function logoutBridge(target) {
  const url=new URL(target.url),site=location.hostname.slice(location.hostname.indexOf('.')+1);
  if(url.protocol!=='https:'||url.username||url.password||url.origin!==target.origin||url.pathname!=='/.well-known/anas/browser-logout'||!url.hostname.endsWith('.'+site)||(url.port||'443')!==(location.port||'443')||!/^[A-Za-z0-9_-]{43}$/.test(target.nonce))return false;
  return new Promise(resolve=>{
    const frame=el('iframe',null,{hidden:'',title:'Sign out '+url.host,referrerpolicy:'no-referrer'});
    const finish=value=>{clearTimeout(timer);window.removeEventListener('message',receive);frame.remove();resolve(value);};
    const receive=event=>{
      const d=event.data;
      if(event.origin!==target.origin||event.source!==frame.contentWindow||d?.protocol!=='anas.browser-logout/v1'||d.nonce!==target.nonce)return;
      if(d.type==='ready')frame.contentWindow.postMessage({protocol:d.protocol,type:'start',nonce:target.nonce},target.origin);
      else if(d.type==='result')finish(d.status==='signed_out');
    };
    const timer=setTimeout(()=>finish(false),70000);
    window.addEventListener('message',receive);frame.src=target.url;document.body.append(frame);
  });
}
async function runLogout(data) {
  logoutRunning=true;
  logoutState={results:[{origin:new URL(data.iam_endpoint).host,status:'working'},...(data.targets||[]).map(t=>({origin:new URL(t.origin).host,status:'working'}))]};
  saveLogout();renderLogout();
  try{
    const r=await fetch(data.iam_endpoint,{method:'POST',credentials:'include',headers:{'Accept-Language':'en'},signal:AbortSignal.timeout(15000)}),j=await r.json();
    if(!r.ok||j.status!=='ok'&&!(j.status==='error'&&j.msg==='Please login first'))throw Error();
    const check=await fetch(new URL('/api/get-account',data.iam_endpoint),{credentials:'include',headers:{'Accept-Language':'en'},cache:'no-store',signal:AbortSignal.timeout(15000)}),identity=await check.json();
    if(!check.ok||identity.status!=='error'||identity.msg!=='Please login first')throw Error();
    logoutState.results[0].status='signed_out';
  }catch{logoutState.results[0].status='failed';}
  saveLogout();renderLogout();
  await Promise.all((data.targets||[]).map(async(target,index)=>{
    try{logoutState.results[index+1].status=await logoutBridge(target)?'signed_out':'failed';}catch{logoutState.results[index+1].status='failed';}
    saveLogout();renderLogout();
  }));
  logoutRunning=false;saveLogout();renderLogout();
}
async function completeNativeLogout() {
  if(logoutRunning)return;
  logoutRunning=true;me=null;prefs=null;catalog=null;++revision;nativeLogoutPage();notice(t('logoutWorking'));
  try{
    let data;
    for(let attempt=0;attempt<2;attempt++){
      const bootstrap=await api('/api/bootstrap');csrf=bootstrap.csrf;
      try{data=await api('/api/logout/complete',{});break;}
      catch(error){
        // A native IAM backchannel can revoke the session after bootstrap.
        // Only this explicit rejection is known to precede all logout effects.
        if(attempt!==0||error.status!==403||error.code!=='csrf_rejected')throw error;
      }
    }
    csrf='';history.replaceState(null,'','/signed-out');await runLogout(data);
  }catch(error){logoutRunning=false;nativeLogoutPage();notice(error.message,true);}
}
function nativeLogoutPage() {
  me=null;document.body.dataset.page='native-logout';renderShell('guest');
  const box=el('section',null,{class:'card hero'});content.append(box);
  const outer=content;content=box;heading(t('logout'));content=outer;
  const proceed=button(t('nativeLogout'),completeNativeLogout,'primary');proceed.disabled=logoutRunning;
  box.append(proceed,el('a',t('home'),{href:'/',class:'button'}));
}
function retryLogout(){return completeNativeLogout();}

// --- start ----------------------------------------------------------------------------
async function start() {
  try {
    const selected=new URLSearchParams(location.search).get('lang')||preference('anas.portal.language')||'';
    const bootstrap=await fetch('/api/bootstrap'+(selected?'?lang='+encodeURIComponent(selected):''),{cache:'no-store'}).then(r=>r.json());
    language=bootstrap.language;csrf=bootstrap.csrf;
    if(location.pathname==='/signed-out'&&new URLSearchParams(location.search).get('native')==='1'){
      const mode=document.querySelector('meta[name=anas-native-logout]')?.content;
      nativeLogoutPage();if(mode==='automatic')await completeNativeLogout();return;
    }
    if(location.pathname==='/signed-out'&&!bootstrap.authenticated){
      try{const saved=JSON.parse(sessionStorage.getItem('anas.portal.logout'));if(saved&&Array.isArray(saved.results)&&saved.results.length){logoutState=saved;renderLogout();return;}}catch{}
    }
    if(!bootstrap.authenticated){if(route()[0]==='recover')recoverPage();else loginPage();return;}
    try{sessionStorage.removeItem('anas.portal.logout');}catch{}
    if(location.pathname==='/signed-out')history.replaceState(null,'','/');
    let resume='';try{resume=sessionStorage.getItem('anas.portal.return')||'';sessionStorage.removeItem('anas.portal.return');}catch{}
    if(/^#\/[A-Za-z0-9_.:\/%-]{0,300}$/.test(resume))history.replaceState(null,'',resume);
    await render();
  }catch(error){loginPage();notice(error.message,true);}
}
start();
