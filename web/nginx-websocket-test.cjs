const {execFileSync,spawn}=require('node:child_process'),{once}=require('node:events'),http=require('node:http'),net=require('node:net'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {WebSocketServer,WebSocket}=require('ws'),ts=require('typescript');
const root=path.resolve(__dirname,'..'),temp=fs.mkdtempSync(path.join(root,'data/build/websocket-'));
(async()=>{let nginx,server,wss;try{
 const exports={};new Function('exports',ts.transpileModule(fs.readFileSync(path.join(__dirname,'src/nginx-routes.ts'),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS}}).outputText)(exports);
 server=http.createServer((req,res)=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(req.headers))});wss=new WebSocketServer({noServer:true});server.on('upgrade',(req,socket,head)=>wss.handleUpgrade(req,socket,head,ws=>{ws.on('message',data=>ws.send(data));}));server.listen(0,'127.0.0.1');await once(server,'listening');
 const upstream='http://127.0.0.1:'+server.address().port,form=exports.defaultRoutes();form.locations=[exports.newRoute('/one/'),exports.newRoute('/two/')];for(const location of form.locations){location.handler='proxy';location.upstream=upstream}
 const routing=exports.renderRoutes(form);assert.equal((routing.match(/proxy_set_header Upgrade/g)||[]).length,2);assert.equal((routing.match(/proxy_set_header Connection/g)||[]).length,2);
 const probe=net.createServer();probe.listen(0,'127.0.0.1');await once(probe,'listening');const port=probe.address().port;await new Promise(resolve=>probe.close(resolve));
 fs.mkdirSync(path.join(temp,'logs'));const config=path.join(temp,'nginx.conf');fs.writeFileSync(config,`pid "${temp}/nginx.pid"; error_log "${temp}/logs/error.log"; events {} http { access_log off; map $http_upgrade $connection_upgrade { default upgrade; '' close; } server { listen 127.0.0.1:${port}; ${routing}\n } }`);
 const binary=path.join(root,'tools/nginx/sbin/nginx'),args=['-p',temp+'/', '-c',config];execFileSync(binary,[...args,'-t'],{stdio:'pipe'});nginx=spawn(binary,[...args,'-g','daemon off;'],{stdio:'ignore'});
 for(let i=0;i<50;i++){try{if((await fetch(`http://127.0.0.1:${port}/one/`)).ok)break}catch{}await new Promise(resolve=>setTimeout(resolve,50))}
 for(const location of ['/one/','/two/']){const ws=new WebSocket(`ws://127.0.0.1:${port}${location}`);await Promise.race([once(ws,'open'),new Promise((_,reject)=>setTimeout(()=>reject(Error('WebSocket handshake timeout')),5000).unref())]);ws.send('ownpanel-websocket');assert.equal(String((await once(ws,'message'))[0]),'ownpanel-websocket');ws.close();await once(ws,'close');const headers=await (await fetch(`http://127.0.0.1:${port}${location}`)).json();assert.equal(headers.connection,'close');assert.equal(headers.upgrade,undefined)}
 console.log('PASS both generated proxy locations: real Nginx WebSocket handshake/echo and ordinary HTTP headers');
 }finally{if(nginx&&nginx.exitCode===null){nginx.kill('SIGTERM');await once(nginx,'exit')}wss?.close();if(server)await new Promise(resolve=>server.close(resolve));fs.rmSync(temp,{recursive:true,force:true})}
})().catch(error=>{console.error(error);process.exitCode=1});
