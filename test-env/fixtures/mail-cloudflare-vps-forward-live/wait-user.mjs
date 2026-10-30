// TEST_CASES: MAILMOD-T-009
// Only an explicit complete line advances the isolated fixture. A timeout or
// closed controller never counts as confirmation and leaves no open listener.
export function confirmationWindow(createdAt,now){
 if(!Number.isFinite(createdAt)||!Number.isFinite(now)||now<createdAt)return 0;
 return Math.max(0,Math.min(540000,createdAt+590000-now));
}
export function waitForConfirmation(stream,timeoutMs){
 return new Promise((resolve,reject)=>{
  let finished=false,buffer='';
  const timer=setTimeout(()=>finish(Error('verification confirmation timed out')),timeoutMs);
  function finish(error){
   if(finished)return;finished=true;clearTimeout(timer);
   stream.off('data',receive);stream.off('end',closed);stream.off('error',failed);stream.pause();
   error?reject(error):resolve();
  }
  function receive(chunk){
   buffer+=chunk.toString();
   if(buffer.length>64)return finish(Error('invalid confirmation'));
   if(!buffer.includes('\n'))return;
   if(buffer.trim()!=='verified')return finish(Error('verification not confirmed'));
   finish();
  }
  function closed(){finish(Error('confirmation input closed'))}
  function failed(){finish(Error('confirmation input failed'))}
  stream.on('data',receive);stream.once('end',closed);stream.once('error',failed);stream.resume();
 });
}
