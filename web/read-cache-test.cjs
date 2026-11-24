const assert=require('node:assert/strict'),fs=require('node:fs'),ts=require('typescript');
const storage=new Map();global.sessionStorage={getItem:k=>storage.get(k),setItem:(k,v)=>storage.set(k,v)};
let now=Date.now();const originalNow=Date.now;Date.now=()=>now;
function moduleCache(){const m={exports:{}};const code=ts.transpileModule(fs.readFileSync(__dirname+'/src/read-cache.ts','utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText;new Function('exports','module',code)(m.exports,m);return m.exports}
(async()=>{
 try{
  const {cachedRead,clearReadCache}=moduleCache();let calls=0;
  const first=await cachedRead('/api/nodes/a/systemd',async()=>{calls++;return {units:[{unit:'A.service'}]}});first.units[0].unit='mutated';
  assert.equal((await cachedRead('/api/nodes/a/systemd',async()=>{calls++;return {}})).units[0].unit,'A.service');assert.equal(calls,1);
  let finish;const delayed=new Promise(r=>finish=r);now+=11000;let fresh;
  const cached=await cachedRead('/api/nodes/a/systemd',()=>{calls++;return delayed},value=>fresh=value);
  assert.equal(cached.units[0].unit,'A.service');assert.equal(fresh,undefined);finish({units:[{unit:'A-v2.service'}]});await new Promise(r=>setImmediate(r));assert.equal(fresh.units[0].unit,'A-v2.service');
  assert.equal((await cachedRead('/api/nodes/b/systemd',async()=>({units:[{unit:'B.service'}]}))).units[0].unit,'B.service');
  const reloaded=moduleCache();assert.equal((await reloaded.cachedRead('/api/nodes/a/systemd',async()=>{throw Error('should use session snapshot')})).units[0].unit,'A-v2.service');
  await cachedRead('/api/nodes/a/nginx/sites',async()=>({sites:[{site:{id:'site'},files:{'server.conf':'secret config'}}]}));const persisted=JSON.parse([...storage.values()][0]);assert.ok(!persisted.find(([k])=>k.endsWith('/nginx/sites'))[1].value.sites[0].files);
  let current=1,resolveOld,counter=0;const fetcher=()=>{counter++;return counter===1?new Promise(r=>resolveOld=r):Promise.resolve({version:current})};const old=cachedRead('/api/nodes/a/status',fetcher);clearReadCache('/api/nodes/a');current=2;assert.equal((await cachedRead('/api/nodes/a/status',async()=>({version:2}))).version,2);resolveOld({version:1});await old;assert.equal((await cachedRead('/api/nodes/a/status',async()=>({version:3}))).version,2);
  let freshReads=0;for(let i=0;i<2;i++)await cachedRead('/api/nodes/a/systemd/logs?unit=x',async()=>{freshReads++;return {text:'live'}});assert.equal(freshReads,2);
  let endForce;const forced=cachedRead('/api/nodes/a/status',()=>new Promise(r=>endForce=r),undefined,true);let during;
  assert.equal((await cachedRead('/api/nodes/a/status',async()=>{throw Error('duplicate force request')},v=>during=v)).version,2);endForce({version:4});await forced;assert.equal(during.version,4);
  let flushOnHide;
  const idle=[];
  global.window={requestIdleCallback:fn=>idle.push(fn),addEventListener:(name,fn)=>{if(name==='pagehide')flushOnHide=fn}};
  try{
   const deferred=moduleCache();
   await deferred.cachedRead('/api/nodes/c/status',async()=>({fixture:'idle-only'}));
   await deferred.cachedRead('/api/nodes/d/status',async()=>({fixture:'idle-only'}));
   assert.equal(idle.length,1);
   assert.ok(!storage.get('ownpanel-read-cache-v1').includes('idle-only'));
   flushOnHide();
   assert.ok(storage.get('ownpanel-read-cache-v1').includes('idle-only'));
   deferred.clearReadCache();
   for(const callback of idle)callback();
   assert.equal(storage.get('ownpanel-read-cache-v1'),'[]');
  }finally{delete global.window}
  console.log('PASS inventory cache: instant stale display, background callbacks, host isolation, reload persistence, sensitive file exclusion, invalidation races, forced refresh and uncached logs');
 }finally{Date.now=originalNow}
})().catch(e=>{console.error(e);process.exitCode=1});
