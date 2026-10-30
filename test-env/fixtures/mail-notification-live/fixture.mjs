// TEST_CASES: MAILMOD-T-078
// Test-only short-lived event adapter. The production email handler owns
// reception and notification; no public anonymous injection is provided.
import worker from './worker.mjs';
export default {
 async fetch(request,env){
  if(Date.now()>=Number(env.EXPIRES))return Response.json({error:'expired'},{status:410});
  const url=new URL(request.url);
  if(!url.pathname.startsWith('/__'))return worker.fetch(request,env);
  if(request.headers.get('Authorization')!==`Bearer ${env.TEST_TOKEN}`)return Response.json({error:'denied'},{status:401});
  if(url.pathname==='/__health')return Response.json({revision:env.REVISION});
  if(url.pathname==='/__cleanup'&&request.method==='POST'){const page=await env.MAIL.list({limit:100});if(page.objects.length)await env.MAIL.delete(page.objects.map(o=>o.key));return Response.json({deleted:page.objects.length});}
  if(url.pathname==='/__event'&&request.method==='POST'){
   const raw=await request.arrayBuffer();if(raw.byteLength>32768)return Response.json({error:'size'},{status:413});
   let rejected='';const pending=[];
   try{await worker.email({from:'probe@sender.mail-poc.bengbu.cc.cd',to:url.searchParams.get('to'),raw,rawSize:raw.byteLength,setReject:r=>{rejected=r;}},env,{waitUntil:p=>pending.push(p)});await Promise.all(pending);return Response.json({rejected});}catch{return Response.json({error:'event-not-confirmed'},{status:503});}
  }
  return Response.json({error:'not-found'},{status:404});
 }
};
