'use strict';
// Test-only Socket.IO/Engine.IO websocket client. It deliberately ignores the
// application logout event, requiring server-side disconnect instead.
const fs=require('node:fs');
const {createRequire}=require('node:module');
const socketRequire=createRequire(require.resolve('/usr/src/app/server/node_modules/socket.io'));
const WebSocket=createRequire(socketRequire.resolve('engine.io'))('ws');
const config=JSON.parse(fs.readFileSync(process.argv[2],'utf8'));
const status={};
const save=()=>fs.writeFileSync(process.argv[3],JSON.stringify(status));
for(const [name,headers] of Object.entries(config.clients)) {
  status[name]='connecting';
  const socket=new WebSocket(config.url,{headers});
  socket.on('message',(data)=>{
    const packet=data.toString();
    if(packet.startsWith('0')) socket.send('40');
    else if(packet==='2') socket.send('3');
    else if(packet.startsWith('40')) {status[name]='connected';save();}
    else if(packet==='41') {status[name]='disconnected';save();socket.close();}
  });
  socket.on('close',()=>{status[name]='disconnected';save();});
  socket.on('error',()=>{status[name]='error';save();});
}
save();
// Keep the disposable client alive to inspect its state after notifications.
setInterval(()=>{},1000);
