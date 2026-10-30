"use strict";
const words=window.ANAS_ADDRESS_WORDS;
let language=words[new URLSearchParams(location.search).get('lang')]?new URLSearchParams(location.search).get('lang'):'en';
let revision=0;
const t=(key,args={})=>{let text=(words[language]||words.en)[key]||key;for(const [name,value]of Object.entries(args))text=text.replaceAll('{'+name+'}',value);return text;};
function el(tag,text,attrs={}){const node=document.createElement(tag);if(text!=null)node.textContent=text;for(const [key,value]of Object.entries(attrs))node.setAttribute(key,value);return node;}
function button(text,run,style=''){const node=el('button',text,{type:'button',class:style});node.addEventListener('click',run);return node;}
function notice(text,bad=false){const node=document.getElementById('notice');node.textContent=text;node.className=bad?'notice error':'notice';}
async function api(path,data){
 const response=await fetch(path,{credentials:'same-origin',cache:'no-store',...(data?{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(data)}:{})});
 const result=await response.json();
 if(!response.ok){
  if(response.status===401){location.assign('/addresses'+location.search);throw Error(t('authentication_required'));}
  if(result.error==='recent-authentication-required'){const link=document.getElementById('reauth');link.hidden=false;throw Error(t('recent_authentication_required'));}
  throw Error(t('mail_management_unavailable'));
 }
 if(result.audit==='unavailable')throw Error(t('auditFailure'));
 return result;
}
async function mailManagement(parent,owner,current) {
  const panel=el('section',null,{class:'card mail-management'});parent.append(panel);
  const path='/api/addresses'+(owner?'?owner='+encodeURIComponent(owner):'');
  const active=()=>revision===current&&panel.isConnected;
  let view=null,lastRequest=null,busy=false;
  const outcome=code=>t(({saved:'mailOutcomeSaved',denied:'access_denied',quota:'mailOutcomeQuota',protected:'mailOutcomeProtected',conflict:'mailOutcomeConflict',unknown:'mailOutcomeUnknown'})[code]||'mail_management_unavailable');
  async function submit(request) {
    if(busy||!active())return;busy=true;lastRequest=request;
    panel.querySelectorAll('button').forEach(node=>node.disabled=true);
    try {
      const result=await api(path,request);if(!active())return;
      notice(outcome(result.directory),result.directory!=='saved');
      if(result.directory==='saved')lastRequest=null;
    } catch(error) {if(active())notice(error.message,true);}
    finally {busy=false;if(active())await load();}
  }
  function request(action,mailbox_id,address) {
    return {operation_id:crypto.randomUUID(),version:view.version,action,...(mailbox_id?{mailbox_id}:{}),address};
  }
  function removeAlias(box,address) {
    const dialog=el('dialog',null,{class:'operation-dialog'}),row=el('div',null,{class:'row dialog-actions'});
    let closed=false;
    function finish(confirmed) {if(closed)return;closed=true;dialog.close();dialog.remove();if(confirmed&&active())submit(request('alias.remove',box,address));}
    const cancel=button(t('cancel'),()=>finish(false)),confirm=button(t('confirmAction'),()=>finish(true),'primary');
    row.append(cancel,confirm);dialog.append(el('h2',t('mailRemoveAlias')),el('p',address,{class:'mono'}),el('p',t('mailRemoveConfirm')),row);
    dialog.addEventListener('cancel',event=>{event.preventDefault();finish(false);});dialog.addEventListener('close',()=>finish(false));
    document.body.append(dialog);dialog.showModal();cancel.focus();
  }
  function addressForm(action,box) {
    const form=el('form',null,{class:'profile-form'}),label=el('label',t('email'));
    const input=el('input',null,{type:'email',name:'address',required:true,maxlength:254,placeholder:'name@'+view.policy.domains[0]});
    input.disabled=!!view.pending;label.append(input);
    const save=el('button',t(action==='additional.create'?'mailNewBox':'mailNewAlias'),{type:'submit',class:'primary'});save.disabled=!!view.pending;
    form.append(label,save);form.addEventListener('submit',event=>{event.preventDefault();if(form.reportValidity())submit(request(action,box,input.value));});return form;
  }
  async function load() {
    try {view=(await api(path)).view;}catch(error){if(active())panel.replaceChildren(el('p',error.message,{class:'notice error'}));return;}
    if(!active())return;
    panel.replaceChildren(el('h2',t('mailManageTitle')),el('p',t('mailDirectoryOnly'),{class:'muted'}),el('p',t('mailLimits',{boxes:view.policy.max_additional,aliases:view.policy.max_aliases}),{class:'muted'}));
    
    const pending=view.pending||lastRequest;
    if(pending)panel.append(el('p',t('mailPendingOperation'),{class:'notice warning'}),button(t('mailRetry'),()=>submit(pending),'secondary'));
    for(const box of view.mailboxes) {
      const card=el('section',null,{class:'card'});card.append(el('h3',box.default_address),el('p',t(box.kind==='primary'?'primaryAddress':'mailIndependent'),{class:'muted'}));
      if(box.state!=='active')card.append(el('p',box.state==='pending'?t('mailPendingOperation'):t('disabled'),{class:'muted'}));
      for(const address of view.addresses.filter(item=>item.mailbox_id===box.id)) {
        const row=el('div',null,{class:'mail-row'});row.append(el('span',address.address,{class:'mono'}));
        if(address.state==='retained')row.append(el('span',t('mailRetained'),{class:'badge'}));
        if(address.removable) {const remove=button(t('mailRemoveAlias'),()=>removeAlias(box.id,address.address),'ghost');remove.disabled=!!view.pending;row.append(remove);}
        card.append(row);
      }
      if(['active','disabled'].includes(box.state))card.append(addressForm('alias.add',box.id));
      panel.append(card);
    }
    panel.append(addressForm('additional.create',''));
  }
  await load();
}

async function startAddresses(){
 const params=new URLSearchParams(location.search),owner=params.get('owner')||'';
 const response=await api('/api/addresses'+(owner?'?owner='+encodeURIComponent(owner):''));
 if(!params.has('lang')&&words[response.language])language=response.language;
 document.documentElement.lang=language;
 document.getElementById('title').textContent=t('mailManageTitle');
 document.getElementById('person').textContent=[response.display_name,response.username].filter(Boolean).join(' · ');
 document.getElementById('reauth').href='/reauth'+(owner?'?owner='+encodeURIComponent(owner):'');
 if(response.can_manage_others){const form=document.getElementById('people-search');form.hidden=false;form.querySelector('label span').textContent=t('users');form.querySelector('button').textContent=t('search');form.onsubmit=async event=>{event.preventDefault();try{const result=await api('/api/addresses/people?q='+encodeURIComponent(form.querySelector('input').value));const list=document.getElementById('people');list.replaceChildren();for(const person of result.people)list.append(el('a',person.display_name||person.username,{href:'/addresses?owner='+encodeURIComponent(person.anchor)+'&lang='+encodeURIComponent(language)}));}catch(error){notice(error.message,true);}};}
 document.getElementById('home').textContent=t('mailOpenService');
 document.getElementById('reauth').textContent=t('login');
 const select=document.getElementById('language');select.value=language;
 select.onchange=()=>{const url=new URL(location.href);url.searchParams.set('lang',select.value);location.assign(url.pathname+url.search);};
 await mailManagement(document.getElementById('content'),owner,++revision);
}
startAddresses().catch(error=>notice(error.message,true));
