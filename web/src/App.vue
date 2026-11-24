<script setup lang="ts">
import { operationMessage } from "./operation-message";
import PanelTable from "./PanelTable.vue";
import type { TableColumn } from "./table-layout";
import { defaultRoutes, renderRoutes, type RouteForm } from './nginx-routes';
import { ref, reactive, computed, onMounted, onUnmounted, watch, nextTick, defineAsyncComponent } from 'vue';
import { LayoutDashboard, Globe2, Box, Shield, FolderOpen, TerminalSquare, ScrollText, Activity, ChevronRight, RefreshCw, Plus, ArrowUp, Download, Upload, Settings2, LogOut, Circle, Server, X, Check, Trash2, FileText, Play, Square, Search } from 'lucide-vue-next';
const PersistentTerminal=defineAsyncComponent(()=>import('./PersistentTerminal.vue'));
const DockerManager=defineAsyncComponent(()=>import('./DockerManager.vue'));
const loadSystemdWorkspace=()=>import('./SystemdWorkspace.vue');
const SystemdManager=defineAsyncComponent(loadSystemdWorkspace);
import FirewallVisual from './FirewallVisual.vue';
import { ElButton, ElForm, ElFormItem } from 'element-plus';
import { useTabDrag, overTabBar, type DragPoint } from './tab-drag';
import UfwRules from './UfwRules.vue';
const FileExplorer=defineAsyncComponent(()=>import('./FileExplorer.vue'));
import {cachedRead,clearReadCache,expireReadCache} from './read-cache';
const ExistingNginx=defineAsyncComponent(()=>import('./ExistingNginx.vue'));
const SiteEditor=defineAsyncComponent(()=>import('./SiteEditor.vue'));
const SiteConfigEditor=defineAsyncComponent(()=>import('./SiteConfigEditor.vue'));
const CertificateFolder=defineAsyncComponent(()=>import('./CertificateFolder.vue'));
const nav=[{id:'hosts',name:'主机',icon:Server,sub:'多主机连接与能力管理'},{id:'overview',name:'概览',icon:LayoutDashboard,sub:'服务器运行状态'},{id:'nginx',name:'网站与反向代理',icon:Globe2,sub:'Nginx 站点与证书'},{id:'certificates',name:'证书夹',icon:FileText,sub:'证书文件与私钥管理'},{id:'firewall',name:'防火墙',icon:Shield,sub:'UFW 规则与访问控制'},{id:'files',name:'文件管理',icon:FolderOpen,sub:'目录、文件与权限'},{id:'terminal',name:'终端',icon:TerminalSquare,sub:'服务器交互会话'},{id:'docker',name:'Docker',icon:Box,sub:'容器、镜像与存储'},{id:'systemd',name:'Systemd',icon:Settings2,sub:'服务、启动项与单元配置'},{id:'logs',name:'日志',icon:ScrollText,sub:'SSH 登录与操作记录'}];
function readNavigation(){
 const [section,query]=location.hash.slice(1).split('?',2);
 const params=new URLSearchParams(query);
 const path=params.get('path');
 return {section:nav.some(item=>item.id===section)?section:'',node:params.get('node')||'',path:path?.startsWith('/')?path:'/'};
}
const initialNavigation=readNavigation();
const page=ref(initialNavigation.section||(nav.some(n=>n.id===sessionStorage.getItem('ownpanel-page'))?sessionStorage.getItem('ownpanel-page')!:'overview')),user=ref(false),csrf=ref(''),initializing=ref(true),busy=ref(0),error=ref('');
function selectCacheAccount(id:string){
 if(sessionStorage.getItem('ownpanel-cache-account')!==id){clearReadCache();sessionStorage.setItem('ownpanel-cache-account',id)}
}
const gatewayLogin=ref(false),accountName=ref('admin');
const login=reactive({username:'admin',password:''});
const cacheRefresh=ref(0);
const current=computed(()=>nav.find(n=>n.id===page.value)!);
const nodes=ref<any[]>([]),selectedNode=ref(initialNavigation.node||localStorage.getItem('ownpanel-node')||'local'),nodeModal=ref(false),enrollmentResult=ref<any>(null);
let selectionGeneration=0;
const activeNode=computed(()=>nodes.value.find(n=>n.id===selectedNode.value));
const nodeBase=()=>'/api/nodes/'+encodeURIComponent(selectedNode.value);
class StaleRequest extends Error{}
function invalidateWrite(target:string){
 if(/^\/api\/(login|logout|account)(\/|$)/.test(target)){clearReadCache();return}
 if(target==='/api/nodes'){clearReadCache('/api/nodes',true);return}
 const base=target.match(/^\/api\/nodes\/[^/?]+/)?.[0];if(!base)return;
 const endpoint=target.slice(base.length);
 if(!endpoint||/^\/(sudo|ssh-credentials|probe|enrollment)$/.test(endpoint)){clearReadCache(base);clearReadCache('/api/nodes',true);return}
 if(endpoint.startsWith('/nginx/categories')||endpoint.startsWith('/nginx/notes'))return;
 if(endpoint.startsWith('/systemd/preferences')||endpoint.startsWith('/systemd/categories'))clearReadCache(base+'/systemd/preferences');
 else if(endpoint.startsWith('/systemd')){clearReadCache(base+'/systemd');expireReadCache(base+'/nginx')}
 else if(endpoint.startsWith('/nginx'))clearReadCache(base+'/nginx');
 else if(endpoint.startsWith('/files')){clearReadCache(base+'/files');expireReadCache(base+'/nginx');expireReadCache(base+'/systemd')}
 else if(endpoint.startsWith('/firewall')||endpoint.startsWith('/iptables')){clearReadCache(base+'/firewall');clearReadCache(base+'/iptables')}
}
async function api(url:string,method='GET',body?:unknown,onFresh?:(value:any)=>void):Promise<any>{
 const force=method==='REFRESH';if(force)method='GET';
 const pathname=url.split('?',1)[0];
 const control=/^\/(login|session|logout|audit|account|nodes)(\/|$)/.test(pathname),generation=selectionGeneration,target=control?'/api'+url:nodeBase()+url;
 const fetcher=async()=>{const headers:Record<string,string>={};if(method!=='GET')headers['X-CSRF-Token']=csrf.value;if(body!==undefined)headers['Content-Type']='application/json';const r=await fetch(target,{method,headers,body:body===undefined?undefined:JSON.stringify(body)});const data=await r.json();if(!r.ok){if(r.status===401||r.status===403&&data.gateway===true){user.value=false;clearReadCache();if(typeof data.gateway==='boolean')gatewayLogin.value=data.gateway}if(!control&&generation!==selectionGeneration)throw new StaleRequest();throw Error(data.error||r.statusText)}if(method!=='GET')invalidateWrite(target);if(!control&&generation!==selectionGeneration)throw new StaleRequest();return data};
 return method==='GET'?cachedRead(target,fetcher,onFresh?value=>{if(control||generation===selectionGeneration)onFresh(value)}:undefined,force):fetcher()
}
const nodeForm=reactive({name:'',mode:'ssh',host:'',port:22,user:'root',fingerprint:'',fileRoot:'/',sudo:false,sudoMode:'none',secret:{password:'',privateKey:'',passphrase:'',sudoPassword:''}});
const authMode=ref('key');
const can=(key:string)=>activeNode.value?.mode==='local'||!!(status.value?.capabilities?.[key]??activeNode.value?.capabilities?.[key])?.supported;
async function loadNodes(){const apply=(r:any)=>{nodes.value=r.nodes;if(!nodes.value.some(n=>n.id===selectedNode.value)){selectedNode.value=nodes.value[0]?.id||'';if(!selectedNode.value)page.value='hosts'}};apply(await api('/nodes','GET',undefined,apply))}
const credentialNode=ref<any>(null);
const credentialForm=reactive({password:'',privateKey:'',passphrase:'',sudoPassword:''});
const credentialType=ref('key');
function closeCredentials(){credentialNode.value=null;Object.assign(credentialForm,{password:'',privateKey:'',passphrase:'',sudoPassword:''})}
function editCredentials(node:any){closeCredentials();error.value='';credentialType.value='key';credentialNode.value=node}
async function saveCredentials(){
 await work(async()=>{
  const key=credentialType.value==='key';
  await api('/nodes/'+credentialNode.value.id+'/ssh-credentials','POST',key?{privateKey:credentialForm.privateKey,passphrase:credentialForm.passphrase,sudoPassword:credentialForm.sudoPassword}:{password:credentialForm.password,sudoPassword:credentialForm.sudoPassword});
  closeCredentials();
  operationMessage('SSH 凭证已更新');
 });
}
const sudoNode=ref<any>(null),sudoForm=reactive({mode:'none',password:''});
function closeSudo(){sudoNode.value=null;sudoForm.password=''}
function editSudo(node:any){sudoNode.value=node;sudoForm.mode=node.sudoMode||(node.sudo?'nopasswd':'none');sudoForm.password='';error.value=''}
async function saveSudo(){const node=sudoNode.value;if(!node)return;await work(async()=>{await api('/nodes/'+node.id+'/sudo','POST',{...sudoForm});if(sudoNode.value===node)closeSudo();await loadNodes();if(selectedNode.value===node.id)status.value=await api('/status');operationMessage('提权设置已验证并保存')})}
function addNode(){Object.assign(nodeForm,{name:'',mode:'ssh',host:'',port:22,user:'root',fingerprint:'',fileRoot:'/',sudo:false,sudoMode:'none',secret:{password:'',privateKey:'',passphrase:'',sudoPassword:''}});nodeModal.value=true}
async function fingerprint(){await work(async()=>{const r=await api('/nodes/probe-ssh','POST',{host:nodeForm.host,port:nodeForm.port});nodeForm.fingerprint=r.fingerprint;operationMessage('已读取指纹，请通过可信渠道核实目标身份后保存')})}
async function saveNode(){await work(async()=>{const body={...nodeForm,sudo:nodeForm.sudoMode!=='none',secret:{password:authMode.value==='password'?nodeForm.secret.password:'',privateKey:authMode.value==='key'?nodeForm.secret.privateKey:'',passphrase:authMode.value==='key'?nodeForm.secret.passphrase:'',sudoPassword:nodeForm.secret.sudoPassword}};const r=await api('/nodes','POST',body);nodeForm.secret={password:'',privateKey:'',passphrase:'',sudoPassword:''};nodeModal.value=false;await loadNodes();if(r.enrollment)enrollmentResult.value=r.enrollment;else{const n=await api('/nodes/'+r.node.id+'/probe','POST');await loadNodes();operationMessage(n.online?'SSH 主机已连接':'主机已保存，连接失败：'+n.lastError,n.online?'success':'warning')}})}
async function probeNode(id:string){await work(async()=>{const n=await api('/nodes/'+id+'/probe','POST');await loadNodes();operationMessage(n.online?'连接和能力检查通过':n.lastError,n.online?'success':'error')})}
async function deleteNode(n:any){if(!confirm('删除主机 '+n.name+'？现有连接将关闭，远端软件和文件不会卸载。'))return;await work(async()=>{await api('/nodes/'+n.id,'DELETE');await loadNodes()})}
async function enrollNode(id:string){await work(async()=>{enrollmentResult.value=await api('/nodes/'+id+'/enrollment','POST')})}
const installSSH=ref(''),installAgentBusy=ref(false),installAgentMessage=ref('');
async function installEnrollmentSSH(){
 if(!enrollmentResult.value||!installSSH.value||installAgentBusy.value)return;
 installAgentBusy.value=true;installAgentMessage.value='正在通过 SSH 上传并安装 Agent…';
 try{
  const result=await api('/nodes/'+encodeURIComponent(installSSH.value)+'/install-agent','POST',{target:enrollmentResult.value.nodeId});
  installAgentMessage.value=result.message;
  await loadNodes();
 }catch(err){installAgentMessage.value=(err as Error).message}
 finally{installAgentBusy.value=false}
}
function downloadEnrollment(){const blob=new Blob([JSON.stringify(enrollmentResult.value,null,2)],{type:'application/json'});const u=URL.createObjectURL(blob);const a=document.createElement('a');a.href=u;a.download='ownpanel-enrollment.json';a.click();URL.revokeObjectURL(u)}
async function work(fn:()=>Promise<void>){busy.value++;error.value='';try{await fn()}catch(e){if(!(e instanceof StaleRequest))error.value=String((e as Error).message)}finally{busy.value--}}
async function signIn(){await work(async()=>{const s=await api('/login','POST',login);csrf.value=s.csrf;login.password='';user.value=true;await loadNodes();await refresh()})}
const passwordModal=ref(false);
const passwordForm=reactive({current:'',new:''});
function closePasswordModal(){passwordModal.value=false;passwordForm.current='';passwordForm.new=''}
async function changePassword(){await work(async()=>{await api('/account/password','POST',passwordForm);closePasswordModal();user.value=false})}
async function logout(){await work(async()=>{await api('/logout','POST');user.value=false;})}
const metrics=ref<any>(null),status=ref<any>(null),history=ref<{cpu:number;memory:number;rx:number;tx:number;time:number}[]>([]);
let prior:any=null;
const bytes=(n:number=0)=>{if(!Number.isFinite(n))return '—';if(n<1024)return `${n.toFixed(0)} B`;const units=['KiB','MiB','GiB','TiB'];let i=-1;do{n/=1024;i++}while(n>=1024&&i<3);return `${n.toFixed(n>=100?0:1)} ${units[i]}`};
const date=(s:string)=>new Date(s).toLocaleString('zh-CN',{hour12:false});
const uptime=(n:number)=>`${Math.floor(n/86400)} 天 ${Math.floor(n%86400/3600)} 小时`;
const last=computed(()=>history.value.at(-1));
const metricRange=ref('recent');
const sampledHistory=ref<typeof history.value>([]);
const displayHistory=computed(()=>metricRange.value==='recent'?history.value:sampledHistory.value);
async function loadMetricRange(){
 const range=metricRange.value;
 sampledHistory.value=[];
 if(range==='recent')return;
 await work(async()=>{
  const result=await api('/metrics/history?range='+range);
  if(metricRange.value!==range)return;
  let previous:any=null;
  sampledHistory.value=result.samples.map((m:any)=>{
   const next=metricPoint(m,previous);
   previous=next.counters;
   return next.point;
  });
 });
}
function graph(key:'cpu'|'memory'|'rx'|'tx',max=100){
 const points=displayHistory.value;
 const scale=max||Math.max(...points.map(v=>v[key]),1);
 const first=points[0]?.time||0;
 const span=Math.max((points.at(-1)?.time||first)-first,1);
 return points.map(v=>`${(v.time-first)/span*700},${145-v[key]/scale*125}`).join(' ');
}
async function poll(){addMetric(await api('/metrics'))}
function metricPoint(m:any,previous:any){
 const nets=(m.network||[]).filter((n:any)=>n.name!=='lo');
 const rx=nets.reduce((v:number,n:any)=>v+n.bytesRecv,0),tx=nets.reduce((v:number,n:any)=>v+n.bytesSent,0);
 const seconds=previous?(m.time-previous.time)/1000:0;
 return {point:{cpu:m.cpu,memory:m.memory?.usedPercent||0,rx:seconds>0?Math.max(0,rx-previous.rx)/seconds:0,tx:seconds>0?Math.max(0,tx-previous.tx)/seconds:0,time:m.time},counters:{rx,tx,time:m.time}};
}
function addMetric(m:any){
 if(prior&&m.time<=prior.time)return;
 metrics.value=m;
 const next=metricPoint(m,prior);
 history.value.push(next.point);
 history.value=history.value.slice(-90);
 prior=next.counters;
}
const managedSitePrefix=ref('');
const sites=ref<any[]>([]),nginxRunning=ref(false),siteModal=ref(false),fileTab=ref('server.conf'),configView=ref<any>(null);
const routeForm=ref<RouteForm>(defaultRoutes()),advancedRouting=ref(false),originalRoutingOnly=ref(false);
const emptySite=()=>({id:'',domains:'',port:9088,httpEnabled:true,httpsEnabled:false,tlsPort:null as number|null,quic:false,quicPort:null as number|null,certificateInfo:null as any,kind:'locations',routing:'root /var/www/html;\nindex index.html;\nlocation / { try_files $uri $uri/ =404; }',root:'/var/www/html',upstream:'http://127.0.0.1:3000',spa:false,tls:false,custom:'',certificate:'',privateKey:'',bindings:[] as any[]});
function siteListeners(site:any){const result=[];if(site.httpEnabled??!site.tls)result.push('HTTP :'+site.port);if(site.httpsEnabled??site.tls)result.push('HTTPS :'+(site.tlsPort||site.port));if(site.quic)result.push('QUIC :'+site.quicPort);return result.join(' · ')}
const siteForm=ref(emptySite()),revision=ref(''),multipleCertificates=ref(false);
function addCertificateBinding(){const first=siteForm.value.bindings.length===0;siteForm.value.bindings.push({id:first?'default':'cert-'+Array.from(crypto.getRandomValues(new Uint8Array(8)),n=>n.toString(16).padStart(2,'0')).join(''),domains:first?siteForm.value.domains:'',certificate:first?siteForm.value.certificate:'',privateKey:first?siteForm.value.privateKey:'',certificateInfo:first?siteForm.value.certificateInfo:null})}
async function loadSites(){const apply=(s:any)=>{sites.value=s.sites;managedSitePrefix.value=s.prefix||'';nginxRunning.value=s.running};apply(await api('/nginx/sites','GET',undefined,apply))}
const batchCertificate=ref(false),batchRunning=ref(false);
const batchPEM=reactive({certificate:'',privateKey:''});
const batchSelected=ref<string[]>([]);
const batchResults=ref<{label:string;error?:string}[]>([]);
const batchTargets=computed(()=>sites.value.filter(item=>!item.site.manual).flatMap(item=>{
 const site=item.site;
 const bindings=site.bindings?.length?site.bindings:[{id:'',domains:site.domains}];
 return bindings.map((binding:any)=>({
  key:JSON.stringify([site.id,binding.id]),siteID:site.id,bindingID:binding.id,
  label:site.id+' · '+binding.domains
 }));
}));
function closeBatchCertificate(){
 batchCertificate.value=false;
 batchPEM.certificate='';
 batchPEM.privateKey='';
 batchSelected.value=[];
 batchResults.value=[];
}
async function deployBatchCertificate(){
 if(batchRunning.value||!can('nginx.write'))return;
 const generation=selectionGeneration;
 const targets=batchTargets.value.filter(target=>batchSelected.value.includes(target.key));
 batchResults.value=[];
 error.value='';
 batchRunning.value=true;
 try{
  const inventory=await api('/nginx/sites','REFRESH');
  const pem={certificate:batchPEM.certificate.trim(),privateKey:batchPEM.privateKey.trim()};
  if(!pem.certificate||!pem.privateKey)throw Error('请输入证书链及私钥');
  for(const id of new Set(targets.map(target=>target.siteID))){
   const group=targets.filter(target=>target.siteID===id);
   const label=group.map(target=>target.label).join('、');
   const item=inventory.sites.find((entry:any)=>entry.site.id===id);
   try{
    if(generation!==selectionGeneration||!batchCertificate.value)return;
    if(!item?.revision||item.site.manual)throw Error('站点已变更，请刷新后重试');
    const site={...item.site,tls:true};
    if(site.bindings?.length){
     if(group.some(target=>!site.bindings.some((binding:any)=>binding.id===target.bindingID&&id+' · '+binding.domains===target.label)))throw Error('证书绑定已变更，请刷新后重试');
     site.bindings=site.bindings.map((binding:any)=>group.some(target=>target.bindingID===binding.id)?{...binding,...pem}:binding);
    }else{
     if(group.some(target=>target.bindingID||target.label!==id+' · '+site.domains))throw Error('站点域名已变更，请刷新后重试');
     Object.assign(site,pem);
    }
    await api('/nginx/sites','POST',{site,revision:item.revision});
    batchSelected.value=batchSelected.value.filter(key=>!group.some(target=>target.key===key));
    batchResults.value.push({label});
   }catch(err){
    if(generation!==selectionGeneration||!batchCertificate.value)return;
    batchResults.value.push({label,error:err instanceof Error?err.message:String(err)});
   }
  }
  if(!batchSelected.value.length){batchPEM.certificate='';batchPEM.privateKey=''}
  await loadSites();
 }catch(err){
  if(generation===selectionGeneration)error.value=err instanceof Error?err.message:String(err);
 }finally{
  batchRunning.value=false;
 }
}
async function openSiteConfig(s:any){await work(async()=>{if(!s.files){const result=await api('/nginx/sites','REFRESH');s=result.sites.find((r:any)=>r.site.id===s.site.id);if(!s)throw Error('站点已不存在')};configView.value=JSON.parse(JSON.stringify(s));fileTab.value='server.conf'})}
const newSiteCategory=ref<string|null>(null);
async function editSite(item?:any,category=''){
 await work(async()=>{
  if(item&&item.site.kind!=='locations'&&!item.files){
   const current=await api('/nginx/sites','REFRESH');
   const id=item.site.id;
   item=current.sites.find((entry:any)=>entry.site.id===id);
   if(!item)throw Error('站点已不存在');
  }
  const form=item?JSON.parse(JSON.stringify({...emptySite(),...item.site})):emptySite();
  if(item&&item.site.kind!=='locations')form.routing=item.files['routing.conf'];
  if(item){form.httpEnabled=item.site.httpEnabled??!item.site.tls;form.httpsEnabled=item.site.httpsEnabled??!!item.site.tls;form.tlsPort=item.site.tlsPort||(item.site.httpsEnabled==null&&item.site.tls?item.site.port:null);form.quic=!!item.site.quic;form.quicPort=item.site.quicPort||null;form.certificateExpiry=item.certificateExpiry||''}
  form.kind='locations';
  routeForm.value=item?.site.routeForm||defaultRoutes();
  const defaultLegacy=emptySite().routing.trim();
  originalRoutingOnly.value=!!item&&(!item.site.routeForm&&form.routing.trim()!==defaultLegacy);
  advancedRouting.value=originalRoutingOnly.value;
  if(!item)form.id='site-'+Array.from(crypto.getRandomValues(new Uint8Array(12)),n=>n.toString(16).padStart(2,'0')).join('');
  siteForm.value=form;
  newSiteCategory.value=item?null:category;
  revision.value=item?.revision||'';
  multipleCertificates.value=!!siteForm.value.bindings.length;
  siteModal.value=true;
 });
}
const certFolder=ref(false),certTarget=ref<any>(null);
function openCertificateFolder(target:any){certTarget.value=target;certFolder.value=true}
function chooseCertificate(pair:{certificate:string;privateKey:string;certificateInfo?:any}){if(certTarget.value)Object.assign(certTarget.value,pair);certFolder.value=false;certTarget.value=null}
async function saveConfig(){
 const view=configView.value;
 if(!view)return;
 await work(async()=>{
  const id=view.site.id;
  await api('/nginx/sites/'+encodeURIComponent(id)+'/config','POST',{file:fileTab.value,content:view.files[fileTab.value],revision:view.revision});
  await loadSites();
  if(configView.value===view){
   const updated=sites.value.find(s=>s.site.id===id);
   configView.value=updated?JSON.parse(JSON.stringify(updated)):null;
  }
  operationMessage('原始配置已检查并应用');
 });
}
function openRawRouting(){
 try{
  siteForm.value.routing=renderRoutes(routeForm.value);
  advancedRouting.value=true;
  error.value='';
 }catch(err){error.value=(err as Error).message}
}
const siteSaveStage=ref(''), siteCategoryRefresh=ref(0), nginxOperation=ref('');
async function saveSite(){
 if(siteSaveStage.value)return;
 const generation=selectionGeneration, category=newSiteCategory.value, creating=!revision.value;
 siteSaveStage.value='正在检查并保存';
 await work(async()=>{
  try{
   const routing=advancedRouting.value?siteForm.value.routing:renderRoutes(routeForm.value);
   const {certificateExpiry:_displayExpiry,...savedSite}=siteForm.value as typeof siteForm.value & {certificateExpiry?:string};
   const label=savedSite.domains || savedSite.bindings[0]?.domains || savedSite.id;
   await api('/nginx/sites','POST',{patchOnly:true,site:{...savedSite,routing,routeForm:advancedRouting.value?undefined:routeForm.value,bindings:multipleCertificates.value?savedSite.bindings:[]},revision:revision.value});
   try{
    if(category){
     siteSaveStage.value='正在设置分类';
     await api('/nginx/categories','POST',{action:'assign',keys:['managed:'+savedSite.id],target:category});
     siteCategoryRefresh.value++;
    }
    operationMessage(label+(creating?' 已创建':' 已保存'));
   }catch(e){
    if(generation===selectionGeneration)operationMessage(label+' 已创建，但分类保存失败：'+(e as Error).message,'warning');
   }finally{
    if(generation===selectionGeneration){
     siteModal.value=false;newSiteCategory.value=null;
     void loadSites().catch(e=>{if(generation===selectionGeneration)operationMessage('站点已保存，列表刷新失败：'+e.message,'warning')});
    }
   }
  }finally{if(generation===selectionGeneration)siteSaveStage.value=''}
 });
}
async function showCertificateStatus(id:string){
 await work(async()=>{
  const status=await api('/nginx/sites/'+encodeURIComponent(id)+'/acme');
  const labels:Record<string,string>={idle:'尚未提交',running:'申请中',succeeded:'证书已部署',failed:'申请失败',interrupted:'申请中断'};
  operationMessage('证书任务 · '+(labels[status.state]||status.state)+(status.error?' · '+status.error:''),status.error?'error':'info');
  if(status.state==='succeeded')await loadSites();
 });
}
const siteVersions=ref<{site:string;items:{id:string;time:string}[]}|null>(null);
async function showSiteVersions(id:string){
 await work(async()=>{
  const result=await api('/nginx/sites/'+encodeURIComponent(id)+'/versions');
  siteVersions.value={site:id,items:result.versions};
 });
}
async function restoreVersion(version:string){
 const state=siteVersions.value;
 if(!state||!confirm('恢复选中的站点配置版本？'))return;
 await work(async()=>{
  await api('/nginx/sites/'+encodeURIComponent(state.site)+'/versions/'+encodeURIComponent(version)+'/restore','POST');
  siteVersions.value=null;
  await loadSites();
  operationMessage('站点配置已恢复');
 });
}
async function requestCertificate(id:string){
 const email=prompt('输入 ACME 联系邮箱');
 if(!email)return;
 const tlsPort=Number(prompt('签发后使用的 SSL / HTTPS 端口（1–65535）',''));
 if(!Number.isInteger(tlsPort)||tlsPort<1||tlsPort>65535){error.value='请输入有效的 SSL 端口';return}
 if(!confirm('确认域名已指向该主机，HTTP-01 验证可访问 80 端口，并同意 Let’s Encrypt 服务条款？'))return;
 await work(async()=>{
  await api('/nginx/sites/'+encodeURIComponent(id)+'/acme','POST',{email,tlsPort});
  operationMessage('证书申请已提交。可刷新站点查看 HTTPS 状态，失败详情见 Agent 日志。');
 });
}
async function control(action:string){if(nginxOperation.value)return;if(action==='stop'&&!confirm(activeNode.value?.mode==='ssh'?'停止目标系统 Nginx？该实例的已有站点和面板站点都会暂时不可用。':'停止面板创建站点的独立 Nginx 实例？该实例中的站点会暂时不可用。'))return;
 const generation=selectionGeneration;
 nginxOperation.value=action;
 await work(async()=>{try{
  await api('/nginx/control','POST',{action});
  operationMessage('Nginx '+({start:'已启动',stop:'已停止',reload:'已重载'}[action]||'操作完成'));
  await loadSites();
 }finally{if(generation===selectionGeneration)nginxOperation.value=''}});
}
async function deleteSite(id:string){if(!confirm(`删除站点 ${id}？配置将保留在备份目录。`))return;await work(async()=>{await api('/nginx/sites/'+encodeURIComponent(id),'DELETE');await loadSites()})}
const floatingTerminalView=ref<any>(null),floatingSession=ref(''),floatingStarted=ref(false);
const terminalView=ref<any>(null),floatingTerminal=ref(false),terminalMinimized=ref(false);
const terminalPosition=reactive({x:80,y:100}), terminalDockHover=ref(false);
const beginTerminalDrag=useTabDrag();
let terminalTransfer=0;
const terminalTabBar=()=>document.querySelector('main .terminal-session-bar');
async function dockTerminal(id:string){
 terminalTransfer++;
 floatingTerminalView.value?.detach();
 floatingTerminal.value=false;floatingSession.value='';terminalDockHover.value=false;
 await nextTick();
 await terminalView.value?.open(id);
}
function positionTerminal(point:DragPoint){terminalPosition.x=Math.max(8,Math.min(innerWidth-120,point.x-40));terminalPosition.y=Math.max(8,Math.min(innerHeight-48,point.y-20));fitFloatingPosition()}
function dragTerminalTab(id:string,event:PointerEvent,fromFloating=false){
 let popped=fromFloating,attached=false;
 beginTerminalDrag(event,{
  move(point){
   if(fromFloating&&!attached){attached=true;void popoutTerminal(id)}
   if(!popped&&!overTabBar(terminalTabBar(),point)){popped=true;void popoutTerminal(id)}
   if(popped){positionTerminal(point);terminalDockHover.value=overTabBar(terminalTabBar(),point)}
  },
  end(point){if(popped&&overTabBar(terminalTabBar(),point))void dockTerminal(id);terminalDockHover.value=false;fitFloatingPosition()},
  cancel(){terminalDockHover.value=false;fitFloatingPosition()}
 });
}
function fitFloatingPosition(){const box=document.querySelector('.floating-terminal')?.getBoundingClientRect();terminalPosition.x=Math.max(8,Math.min(terminalPosition.x,innerWidth-(box?.width||Math.min(900,innerWidth*.92))-8));terminalPosition.y=Math.max(8,Math.min(terminalPosition.y,innerHeight-(box?.height||Math.min(540,innerHeight*.9))-8))}
onMounted(()=>window.addEventListener('resize',fitFloatingPosition));
onUnmounted(()=>window.removeEventListener('resize',fitFloatingPosition));
async function fileTerminal(requestedPath?:string){
 const path=requestedPath||directory.value;
 floatingStarted.value=true;floatingTerminal.value=true;terminalMinimized.value=false;fitFloatingPosition();
 await nextTick();
 for(let attempt=0;attempt<100&&!floatingTerminalView.value;attempt++)await new Promise(resolve=>setTimeout(resolve,50));
 await floatingTerminalView.value?.create(path);
}
async function popoutTerminal(id:string){const transfer=++terminalTransfer,node=selectedNode.value;floatingStarted.value=true;floatingTerminal.value=true;terminalMinimized.value=false;fitFloatingPosition();await nextTick();for(let attempt=0;attempt<100&&!floatingTerminalView.value&&transfer===terminalTransfer;attempt++)await new Promise(resolve=>setTimeout(resolve,50));if(transfer===terminalTransfer&&node===selectedNode.value&&floatingTerminal.value)await floatingTerminalView.value?.open(id)}
function allowFloatingFocus(event:FocusEvent){if((event.relatedTarget as Element|null)?.closest?.('.floating-terminal'))event.stopPropagation()}
onMounted(()=>document.addEventListener('focusout',allowFloatingFocus,true));
onUnmounted(()=>document.removeEventListener('focusout',allowFloatingFocus,true));
function dragTerminal(event:PointerEvent){
 const startX=terminalPosition.x,startY=terminalPosition.y;
 beginTerminalDrag(event,{
  move(point,delta){terminalPosition.x=Math.max(8,Math.min(innerWidth-120,startX+delta.x));terminalPosition.y=Math.max(8,Math.min(innerHeight-48,startY+delta.y));fitFloatingPosition();terminalDockHover.value=overTabBar(terminalTabBar(),point)},
  end(point){if(floatingSession.value&&overTabBar(terminalTabBar(),point))void dockTerminal(floatingSession.value);terminalDockHover.value=false;fitFloatingPosition()},
  cancel(){terminalDockHover.value=false;fitFloatingPosition()}
 });
}
const fileExplorerRefresh=ref(0);
const fileExplorerApi=computed(()=>{const node=selectedNode.value,generation=selectionGeneration;return (url:string,method?:string,body?:unknown,onFresh?:(value:any)=>void)=>{if(node!==selectedNode.value||generation!==selectionGeneration)return Promise.reject(new StaleRequest());return api(url,method,body,onFresh)}});
async function explorerUpload(path:string,file:File){const generation=selectionGeneration,target=nodeBase()+'/files/upload',body=new FormData();body.append('path',path);body.append('file',file);const response=await fetch(target,{method:'POST',headers:{'X-CSRF-Token':csrf.value},body});const data=await response.json();if(response.ok)invalidateWrite(target);if(generation!==selectionGeneration)throw new StaleRequest();if(!response.ok)throw Error(data.error)}
const fileExplorerUpload=computed(()=>{const node=selectedNode.value,generation=selectionGeneration;return (path:string,file:File)=>{if(node!==selectedNode.value||generation!==selectionGeneration)return Promise.reject(new StaleRequest());return explorerUpload(path,file)}});
async function explorerScan(path:string){await work(async()=>{await api('/scan-jobs','POST',{path});await loadScanJobs()})}
const directory=ref(initialNavigation.path),scan=ref<any>(null);
const scanJobs=ref<any[]|null>(null);
async function loadScanJobs(){scanJobs.value=(await api('/scan-jobs')).jobs}
const firewallMode=ref('ufw'),iptablesState=ref<any>(null),iptablesProgram=ref('iptables'),iptablesRules=ref('');
const firewall=ref<any>(null),rule=reactive({port:'',protocol:'tcp',source:'any',policy:'allow'}),now=ref(Date.now());
const remaining=computed(()=>Math.max(0,Math.ceil((Date.parse((firewallMode.value==='ufw'?firewall.value:iptablesState.value)?.pending?.expires||'')-now.value)/1000)||0));
async function loadIPTables(reset=true,force=false){const program=iptablesProgram.value;const apply=(result:any,background=false)=>{if(program!==iptablesProgram.value||firewallMode.value!=='iptables')return;const saved=iptablesState.value?.rules;if(reset&&(!background||iptablesRules.value===saved))iptablesRules.value=result.rules;iptablesState.value=result};apply(await api('/iptables?program='+encodeURIComponent(program),force?'REFRESH':'GET',undefined,(result:any)=>apply(result,true)))}
async function loadFirewall(){if(firewallMode.value==='iptables'){await loadIPTables();return};const apply=(r:any)=>{firewall.value=r};apply(await api('/firewall','GET',undefined,apply))}
async function applyIPTables(){if(!confirm('应用当前规则草稿？未确认将在 60 秒后恢复。'))return;await runFirewall('apply',async()=>{await api('/iptables','POST',{program:iptablesProgram.value,rules:iptablesRules.value});await loadIPTables();operationMessage('原始规则已应用，请使用新的连接验证后确认保留')})}
function exportIPTables(){const blob=new Blob([iptablesRules.value],{type:'text/plain'});const url=URL.createObjectURL(blob);const a=document.createElement('a');a.href=url;a.download=iptablesProgram.value+'-rules.txt';a.click();URL.revokeObjectURL(url)}
watch(firewallMode,()=>{if(user.value&&page.value==='firewall')void work(loadFirewall)});
watch(iptablesProgram,()=>{if(user.value&&page.value==='firewall'&&firewallMode.value==='iptables')void work(()=>loadIPTables())});
const firewallRuleModal=ref(false), firewallPending=ref('');
async function runFirewall(operation:string, task:()=>Promise<void>){
 if(firewallPending.value)return;
 const generation=selectionGeneration;
 firewallPending.value=operation;
 try{await work(task)}finally{if(generation===selectionGeneration)firewallPending.value=''}
}
watch(selectedNode,()=>{firewallRuleModal.value=false;firewallPending.value=''});
async function addFirewallRule(){await firewallAction('add');if(!error.value)firewallRuleModal.value=false}
async function firewallAction(action:string){let payload:any={action,...rule};if(['enable','disable'].includes(action)&&!confirm(`${action==='enable'?'启用':'关闭'}系统 UFW？未确认的变更将在 60 秒后恢复。`))return;await runFirewall(action,async()=>{await api('/firewall','POST',payload);await loadFirewall();operationMessage('变更已应用，请使用新的连接验证访问后确认保留')})}
async function confirmFirewall(){await runFirewall('confirm',async()=>{await api((firewallMode.value==='ufw'?'/firewall':'/iptables')+'/confirm','POST',{id:(firewallMode.value==='ufw'?firewall.value:iptablesState.value).pending.id});await loadFirewall();operationMessage('已保留防火墙变更')})}
const logKind=ref('ssh'),logText=ref(''),logFilter=ref(''),follow=ref(true),logSite=ref(''),logNginxKind=ref('access');
const filteredLog=computed(()=>logText.value.split('\n').filter(l=>l.toLowerCase().includes(logFilter.value.toLowerCase())).join('\n'));
async function loadLogs(){if(logKind.value==='audit'){const r=await api('/audit?nodeId='+encodeURIComponent(selectedNode.value));logText.value=r.entries.map((v:any)=>JSON.stringify(v)).join('\n');return};let url='/logs/'+logKind.value;if(logKind.value==='nginx'){if(!logSite.value){logText.value='请选择站点';return};url='/nginx/logs?id='+encodeURIComponent(logSite.value)+'&kind='+logNginxKind.value}const r=await api(url);logText.value=r.text}
async function refresh(){
 if(!user.value)return;
 await work(async()=>{
  if(page.value==='hosts'){expireReadCache('/api/nodes',true);await loadNodes();return}
  if(!selectedNode.value)return;
  const tasks:Promise<unknown>[]=[api('/status','GET',undefined,(value:any)=>{status.value=value}).then(value=>{status.value=value})];
  if(page.value==='overview')tasks.push((async()=>{if(!history.value.length){const result=await api('/metrics/history');for(const sample of result.samples)addMetric(sample)}await poll()})());
  if(page.value==='nginx')tasks.push(loadSites());
  if(page.value==='files')fileExplorerRefresh.value++;
  if(page.value==='firewall')tasks.push(loadFirewall());
  if(page.value==='logs')tasks.push(loadLogs(),logKind.value==='nginx'?loadSites():Promise.resolve());
  const results=await Promise.allSettled(tasks);
  const failure=results.find(result=>result.status==='rejected') as PromiseRejectedResult|undefined;
  if(failure)throw failure.reason;
 });
}
watch(selectedNode,()=>{terminalTransfer++;beginTerminalDrag.cancel();selectionGeneration++;nginxOperation.value='';siteSaveStage.value='';floatingTerminal.value=false;floatingStarted.value=false;floatingSession.value='';closeBatchCertificate();certFolder.value=false;certTarget.value=null;localStorage.setItem('ownpanel-node',selectedNode.value);closeSudo();siteModal.value=false;configView.value=null;siteVersions.value=null;scan.value=null;scanJobs.value=null;logKind.value='ssh';logSite.value='';siteForm.value=emptySite();directory.value='/';metrics.value=null;status.value=null;history.value=[];sampledHistory.value=[];metricRange.value='recent';prior=null;sites.value=[];managedSitePrefix.value='';firewall.value=null;iptablesState.value=null;iptablesRules.value='';logText.value='';error.value='';void refresh()},{flush:'sync'});
watch([user,()=>activeNode.value?.id,()=>can('systemd.read')],([authenticated,id,supported])=>{
 if(!authenticated||!id||!supported)return;
 const generation=selectionGeneration;
 void loadSystemdWorkspace().catch(()=>{});
 void api('/systemd').catch(()=>{});
 void api('/systemd/users').then(({users})=>{
  if(generation!==selectionGeneration||!user.value||!Array.isArray(users)||users.length!==1)return;
  return api('/systemd/user/'+encodeURIComponent(users[0]));
 }).catch(()=>{});
});
watch(page,()=>{sessionStorage.setItem('ownpanel-page',page.value);error.value='';void refresh()});
watch([logKind,logSite,logNginxKind],()=>{if(page.value==='logs'&&user.value)void work(loadLogs)});
let restoringNavigation=false;
function writeNavigation(replace=false){
 const params=new URLSearchParams();
 if(selectedNode.value)params.set('node',selectedNode.value);
 if(page.value==='files'&&directory.value!=='/')params.set('path',directory.value);
 const hash='#'+page.value+(params.size?'?'+params.toString():'');
 if(location.hash===hash)return;
 if(replace)window.history.replaceState(null,'',hash);
 else window.history.pushState(null,'',hash);
}
async function restoreNavigation(){
 const route=readNavigation();
 if(!route.section)return;
 restoringNavigation=true;
 const oldPage=page.value,oldNode=selectedNode.value,oldPath=directory.value;
 if(route.node&&(!user.value||nodes.value.some(node=>node.id===route.node)))selectedNode.value=route.node;
 page.value=route.section;
 if(page.value==='files'){
  directory.value=route.path;
  if(oldPage===page.value&&oldNode===selectedNode.value&&oldPath!==route.path){

   if(user.value)fileExplorerRefresh.value++;
  }
 }
 await nextTick();
 restoringNavigation=false;
}
watch([page,selectedNode,directory],()=>{
 if(!initializing.value&&!restoringNavigation)writeNavigation();
});
let timer:ReturnType<typeof setInterval>,clock:ReturnType<typeof setInterval>,polling=false;
let cacheErrorURL='';
function cacheStatus(event:Event){const data=(event as CustomEvent).detail;const section=({nginx:'/nginx',systemd:'/systemd',files:'/files',firewall:'/firewall'} as Record<string,string>)[page.value];if(!data.url.startsWith(nodeBase()+'/status')&&!(section&&data.url.startsWith(nodeBase()+section))&&!(page.value==='firewall'&&data.url.startsWith(nodeBase()+'/iptables'))&&!(page.value==='hosts'&&data.url==='/api/nodes'))return;if(data.error){cacheErrorURL=data.url;error.value='当前显示缓存，后台刷新失败：'+data.error}else if(cacheErrorURL===data.url&&error.value.startsWith('当前显示缓存')){error.value='';cacheErrorURL=''}}
onMounted(async()=>{window.addEventListener('popstate',restoreNavigation);window.addEventListener('ownpanel-cache-status',cacheStatus);try{const s=await api('/session');gatewayLogin.value=!!s.gateway;accountName.value=s.username;csrf.value=s.csrf;selectCacheAccount(s.gateway?'gateway:'+s.userId:'local');user.value=true;await loadNodes();void refresh()}catch{}finally{initializing.value=false;writeNavigation(true)}timer=setInterval(async()=>{if(!user.value||polling)return;polling=true;try{if(page.value==='hosts')await loadNodes();if(page.value==='overview')await poll();if(page.value==='logs'&&follow.value)await loadLogs();if(page.value==='firewall'){if(firewallMode.value==='ufw'&&firewall.value?.pending)await loadFirewall();if(firewallMode.value==='iptables'&&iptablesState.value?.pending)await loadIPTables(false)}}catch(e){if(!(e instanceof StaleRequest))error.value=(e as Error).message}finally{polling=false}},2500);clock=setInterval(()=>now.value=Date.now(),1000)});
onUnmounted(()=>{window.removeEventListener('popstate',restoreNavigation);window.removeEventListener('ownpanel-cache-status',cacheStatus);clearInterval(timer);clearInterval(clock);});
const tableColumnsHosts: TableColumn[] = [
    {key:"host",label:"主机",value:(n)=>n.name,minWidth:200},
    {key:"mode",label:"连接方式",value:(n)=>n.mode,minWidth:140},
    {key:"status",label:"状态",value:(n)=>n.online,minWidth:130},
    {key:"capabilities",label:"权限与能力",value:(n)=>!!n.capabilities?.['terminal.root']?.supported,minWidth:130},
    {key:"actions",label:"操作",sortable:false,fixed:'right',minWidth:320}
];
const tableColumnsScanJobs: TableColumn[] = [
    {key:"path",label:"路径",value:(job)=>job.path,minWidth:300},
    {key:"status",label:"状态",value:(job)=>job.status,minWidth:130},
    {key:"actions",label:"操作",sortable:false,fixed:'right',minWidth:180}
];
const tableColumnsScanResults: TableColumn[] = [
    {key:"path",label:"最大文件",value:(f)=>f.path,minWidth:300},
    {key:"size",label:"大小",value:(f)=>f.size,minWidth:130}
];
const tableColumnsSiteVersions: TableColumn[] = [
    {key:"version",label:"版本",value:(version)=>version.id,minWidth:210},
    {key:"time",label:"时间",value:(version)=>Date.parse(version.time),minWidth:190},
    {key:"actions",label:"操作",sortable:false,fixed:'right',minWidth:180}
];
</script>

<template><div v-if="initializing" class="loading">正在连接 OwnPanel…</div>
 <div v-else-if="!user" class="login-page"><div class="login-card"><div class="brand-mark"><Server :size="26"/></div><h1>OwnPanel</h1><ElButton v-if="gatewayLogin" tag="a" href="/auth/start" type="primary">通过网关登录</ElButton><ElForm v-else label-position="top" @submit.prevent="signIn"><ElFormItem label="用户名"><ElInput v-model="login.username" autocomplete="username" required /></ElFormItem><ElFormItem label="密码"><ElInput v-model="login.password" type="password" autocomplete="current-password" required /></ElFormItem><p v-if="error" class="form-error">{{error}}</p><ElButton  :disabled="!!busy" native-type="submit" type="primary" :loading="!!busy">{{busy?'正在登录…':'登录控制台'}}<ChevronRight :size="17"/></ElButton></ElForm><p v-if="!gatewayLogin" class="muted">首次启动生成的密码可在服务器的 initial-password 文件中读取</p></div></div>
 <div v-else class="app-shell"><aside><a class="brand" href="#" @click.prevent="page='overview'"><span class="brand-mark"><Server :size="23"/></span><b>OwnPanel<small>SERVER CONSOLE</small></b></a><div class="nav-caption">工作空间</div><nav><div v-for="n in nav" :key="n.id"><ElButton text class="nav-item" :type="page===n.id?'primary':undefined" :aria-label="n.name" :aria-current="page===n.id?'page':undefined" @click="page=n.id"><component :is="n.icon" :size="18"/><span class="nav-label">{{n.name}}</span></ElButton></div></nav></aside>
  <div class="workspace"><header><div class="header-right"><ElButton circle aria-label="刷新当前页面" title="刷新" :disabled="!!busy" @click="expireReadCache('/api/nodes',true);expireReadCache(nodeBase());cacheRefresh++;refresh()"><RefreshCw :size="16" :class="{spinning:busy}"/></ElButton><ElSelect  v-if="nodes.length" class="host-switch" aria-label="当前主机" v-model="selectedNode"><ElOption v-for="n in nodes" :value="n.id" :label="(n.name)+' · '+(n.mode==='ssh'?'SSH':'Agent')" /></ElSelect><span class="local-badge"><span class="dot"/>{{activeNode?.name||'未选择主机'}}</span><div class="avatar">A</div><span>{{accountName}}</span><ElButton v-if="!gatewayLogin" text  native-type="button" class="icon-button" title="修改密码" @click="passwordModal=true"><Settings2 :size="17"/></ElButton><ElButton text  native-type="button" class="icon-button" title="退出登录" @click="logout"><LogOut :size="17"/></ElButton></div></header>
   <main :aria-label="current.name" :class="{'terminal-page':page==='terminal','table-page':['hosts','files','nginx','systemd','docker','certificates','firewall'].includes(page)}"><div v-if="error" class="banner danger"><span>{{error}}</span><ElButton @click="error=''" native-type="button">×</ElButton></div>
   <template v-if="page==='hosts'"><div class="toolbar"><span class="muted">{{nodes.length}} 台主机</span><div class="spacer"/><ElButton  @click="addNode" native-type="button" type="primary"><Plus :size="16"/>添加主机</ElButton></div>
    <section class="card table-card"><PanelTable table-id="hosts" :rows="nodes" :columns="tableColumnsHosts" row-key="id"><template #host="{row:n}"><b>{{n.name}}</b><small class="mono">{{n.mode==='ssh'?n.user+'@'+n.host+':'+n.port:n.mode==='local'?'本机 Unix Socket':n.id}}</small></template><template #mode="{row:n}"><span class="tag">{{n.mode==='ssh'?'SSH · 免安装':n.mode==='local'?'Agent · 本机':'Agent · 出站连接'}}</span></template><template #status="{row:n}"><span class="status-pill" :class="{off:!n.online}"><span class="dot"/>{{n.online?'在线':'未连接'}}</span><small v-if="n.lastError" class="node-error">{{n.lastError}}</small><small v-else-if="n.lastSeen">最近在线 {{date(n.lastSeen)}}</small></template><template #capabilities="{row:n}"><span v-if="n.capabilities?.['terminal.root']?.supported" class="tag green">root 可用</span><span v-else class="tag">普通账号 / 未探测</span><small v-if="n.mode==='ssh'">{{n.capabilities?.['firewall.write']?.supported?'防火墙可管理':'防火墙只读'}} · SFTP 使用登录账号权限</small><small v-if="n.mode==='agent'">双向 TLS · 自动重连</small></template><template #actions="{row:n}"><ElSpace wrap class="row-actions"><ElButton @click="selectedNode=n.id;page='overview'" native-type="button">管理</ElButton><ElButton @click="probeNode(n.id)" native-type="button">探测</ElButton><ElButton v-if="n.mode==='ssh'" @click="editCredentials(n)" native-type="button">更新凭证</ElButton><ElButton v-if="n.mode==='ssh'" @click="editSudo(n)" native-type="button">提权设置</ElButton><ElButton v-if="n.mode==='agent'" @click="enrollNode(n.id)" native-type="button">注册配置</ElButton><ElButton v-if="n.mode!=='local'"  @click="deleteNode(n)" native-type="button" type="danger" plain>删除</ElButton></ElSpace></template><template #empty>添加 Agent 或 SSH 主机开始管理。</template></PanelTable></section></template>
   <template v-if="page==='overview'"><div class="stat-grid"><article class="stat-card"><div class="stat-top">CPU 使用率<Activity :size="18"/></div><div class="stat-value">{{metrics?.cpu?.toFixed(1)||'0.0'}}<small>%</small></div><div class="meter"><i :style="{width:(metrics?.cpu||0)+'%'}"/></div><p>{{metrics?.host?.kernelArch||'Linux'}}<span>负载 {{metrics?.load?.load1?.toFixed(2)||'—'}}</span></p></article><article class="stat-card"><div class="stat-top">内存使用<Server :size="18"/></div><div class="stat-value">{{bytes(metrics?.memory?.used).split(' ')[0]}}<small>{{bytes(metrics?.memory?.used).split(' ')[1]}}</small></div><div class="meter blue"><i :style="{width:(metrics?.memory?.usedPercent||0)+'%'}"/></div><p>共 {{bytes(metrics?.memory?.total)}}<span>{{metrics?.memory?.usedPercent?.toFixed(1)||0}}%</span></p></article><article class="stat-card"><div class="stat-top">磁盘空间<FolderOpen :size="18"/></div><div class="stat-value">{{metrics?.disk?.usedPercent?.toFixed(1)||'0.0'}}<small>%</small></div><div class="meter amber"><i :style="{width:(metrics?.disk?.usedPercent||0)+'%'}"/></div><p>{{bytes(metrics?.disk?.used)}} / {{bytes(metrics?.disk?.total)}}<span>已使用</span></p></article><article class="stat-card"><div class="stat-top">网络吞吐<Globe2 :size="18"/></div><div class="network-value"><span>↓</span>{{bytes(last?.rx)}}<small>/s</small></div><div class="network-value secondary"><span>↑</span>{{bytes(last?.tx)}}<small>/s</small></div><p>所有非回环网卡<span>实时</span></p></article></div>
    <div class="chart-grid"><section class="card"><div class="card-heading"><h2>资源趋势</h2><ElSelect aria-label="监控时间范围" v-model="metricRange" @change="loadMetricRange"><ElOption value="recent">最近采样</ElOption><ElOption value="day">过去 24 小时</ElOption><ElOption value="week">过去 7 天</ElOption></ElSelect><div class="legend"><span><i/>CPU</span><span><i class="blue-dot"/>内存</span></div></div><div class="chart"><div class="chart-axis"><span>100%</span><span>50%</span><span>0%</span></div><svg viewBox="0 0 700 160" preserveAspectRatio="none"><path d="M0 20H700 M0 82H700 M0 145H700" class="grid-line"/><polyline :points="graph('memory')" class="line blue-line"/><polyline :points="graph('cpu')" class="line"/></svg></div><div class="chart-footer"><span>{{displayHistory.length}} 个采样点</span><span>现在</span></div></section><section class="card"><div class="card-heading"><h2>网络流量</h2><div class="legend"><span><i/>接收</span><span><i class="blue-dot"/>发送</span></div></div><div class="chart"><svg viewBox="0 0 700 160" preserveAspectRatio="none"><path d="M0 20H700 M0 82H700 M0 145H700" class="grid-line"/><polyline :points="graph('tx',0)" class="line blue-line"/><polyline :points="graph('rx',0)" class="line"/></svg></div><div class="chart-footer"><span>各曲线按峰值缩放</span><span>现在</span></div></section></div>
    <div class="bottom-grid"><section class="card"><div class="card-heading"><h2>服务器信息</h2><span class="tag">Linux</span></div><dl class="info-grid"><div><dt>主机名</dt><dd>{{metrics?.host?.hostname||'—'}}</dd></div><div><dt>操作系统</dt><dd>{{metrics?.host?.platform}} {{metrics?.host?.platformVersion}}</dd></div><div><dt>内核版本</dt><dd>{{metrics?.host?.kernelVersion||'—'}}</dd></div><div><dt>运行时间</dt><dd>{{uptime(metrics?.host?.uptime||0)}}</dd></div><div><dt>文件管理目录</dt><dd class="mono">{{status?.fileRoot}}</dd></div><div><dt>进程数量</dt><dd>{{metrics?.host?.procs||'—'}}</dd></div></dl></section><section class="card"><div class="card-heading"><h2>快捷操作</h2><span class="muted">常用管理</span></div><div class="quick-grid"><ElButton @click="page='nginx'" native-type="button"><Globe2/><b>管理网站</b><span>站点与反向代理</span></ElButton><ElButton @click="page='files'" native-type="button"><FolderOpen/><b>浏览文件</b><span>文件与目录管理</span></ElButton><ElButton @click="page='terminal'" native-type="button"><TerminalSquare/><b>打开终端</b><span>root 交互会话</span></ElButton><ElButton @click="page='logs'" native-type="button"><ScrollText/><b>登录日志</b><span>查看 SSH 活动</span></ElButton></div></section></div></template>
   <template v-if="page==='nginx'"><ExistingNginx :category-refresh="siteCategoryRefresh" :node-id="selectedNode" :refresh-signal="cacheRefresh" :key="selectedNode" :api="api" :sites="sites" :managed-prefix="managedSitePrefix" :writable="can('nginx.write')"><template #toolbar="{category}"><div class="site-toolbar"><span class="status-pill" :class="{off:!nginxRunning}"><span class="dot"/>Nginx {{nginxRunning?'运行中':'已停止'}}</span><div class="spacer"/><ElButton :disabled="!!nginxOperation" :loading="nginxOperation==='start'||nginxOperation==='reload'" @click="control(nginxRunning?'reload':'start')" native-type="button"><Play :size="14"/>{{nginxRunning?'重载':'启动'}}</ElButton><ElButton v-if="nginxRunning" :disabled="!!nginxOperation" :loading="nginxOperation==='stop'" @click="control('stop')" native-type="button"><Square :size="13"/>停止</ElButton><ElButton  :disabled="!can('nginx.write')||!batchTargets.length||batchRunning" @click="error='';batchCertificate=true" native-type="button">批量部署证书</ElButton><ElButton type="primary" :disabled="!can('nginx.write')" @click="editSite(undefined,category)"><Plus :size="16"/>创建站点</ElButton></div></template><template #managed-cell="{column,site:s,notes,editNote,categoryFor,editCategory,checkedSites,toggleSite,categoryBusy}"><template v-if="column==='selection'"><ElCheckbox :aria-label="'选择站点 '+s.site.domains" :model-value="checkedSites.includes('managed:'+s.site.id)" :disabled="categoryBusy" @change="toggleSite('managed:'+s.site.id)" /></template><template v-else-if="column==='domain'"><b :data-site-id="s.site.id">{{s.site.domains}}</b></template><template v-else-if="column==='note'"><ElButton text  native-type="button" class="site-note" :title="notes['managed:'+s.site.id]" @click="editNote('managed:'+s.site.id,s.site.domains)">{{notes['managed:'+s.site.id]||'添加备注'}}</ElButton></template><template v-else-if="column==='listen'">{{siteListeners(s.site)}}</template><template v-else-if="column==='tls'"><span :class="s.site.tls?'green':'muted'">{{s.site.tls?'已启用':'未启用'}}{{s.site.bindings?.length?' · '+s.site.bindings.length+' 份证书':''}}</span><small v-if="s.certificateExpiry">到期 {{date(s.certificateExpiry)}}</small></template><template v-else-if="column==='actions'"><ElSpace wrap class="row-actions"><ElButton @click="editSite(s)" native-type="button">编辑</ElButton><ElButton v-if="activeNode?.mode!=='ssh'&&!s.site.tls&&!s.site.manual&&s.site.port===80" @click="requestCertificate(s.site.id)" native-type="button">申请证书</ElButton><ElButton v-if="activeNode?.mode!=='ssh'" @click="showCertificateStatus(s.site.id)" native-type="button">证书任务</ElButton><ElButton @click="openSiteConfig(s)" native-type="button">配置</ElButton><ElButton @click="showSiteVersions(s.site.id)" native-type="button">历史版本</ElButton><ElButton @click="logKind='nginx';logSite=s.site.id;page='logs'" native-type="button">日志</ElButton><ElButton  @click="deleteSite(s.site.id)" native-type="button" type="danger" plain>删除</ElButton></ElSpace></template></template></ExistingNginx></template>
   <FileExplorer @activate="page='files'" v-if="user" :key="selectedNode" :active="page==='files'" :node-id="selectedNode" :initial-path="directory" :api="fileExplorerApi" :base="nodeBase()" :upload="fileExplorerUpload" :refresh-signal="fileExplorerRefresh" @navigate="directory=$event" @terminal="fileTerminal" @scan="explorerScan" @scan-jobs="work(loadScanJobs)"/>
   <template v-if="page==='firewall'"><ElSpace wrap class="segmented firewall-tabs"><ElButton :type="(firewallMode==='ufw')?'primary':undefined" plain @click="firewallMode='ufw'" native-type="button">UFW 简单管理</ElButton><ElButton :type="(firewallMode==='iptables')?'primary':undefined" plain @click="firewallMode='iptables'" native-type="button">iptables 全规则</ElButton></ElSpace><template v-if="firewallMode==='ufw'"><div v-if="firewall?.recoveryError||(firewall?.pending&&!remaining)" class="banner warning">{{firewall.recoveryError||'规则尚待恢复。Agent 会自动重试，请查看 Agent 日志了解详情。'}}</div><div v-if="firewall?.pending&&remaining" class="banner warning"><div><b>变更等待确认 · {{remaining}} 秒</b><p>请使用新连接验证 SSH 和面板可访问。未确认将自动恢复此前配置。</p></div><ElButton  :loading="firewallPending==='confirm'" @click="confirmFirewall" native-type="button" type="primary"><Check :size="15"/>确认保留</ElButton></div><UfwRules :added="firewall?.added||''" :writable="can('firewall.write') && !firewall?.pending" :api="api" @changed="work(loadFirewall)"><template #toolbar><div class="ufw-toolbar"><span class="status-pill" :class="{off:!firewall?.text?.includes('Status: active')}"><span class="dot"/>{{firewall?.text?.includes('Status: active')?'UFW 已启用':'UFW 未启用'}}</span><div class="spacer"/><ElButton :disabled="!!firewallPending||!can('firewall.write')"  :loading="firewallPending==='enable'" @click="firewallAction('enable')" native-type="button">启用 UFW</ElButton><ElButton :disabled="!!firewallPending||!can('firewall.write')"  :loading="firewallPending==='disable'" @click="firewallAction('disable')" native-type="button">关闭 UFW</ElButton><ElButton type="primary" :disabled="!!busy||!!remaining||!can('firewall.write')" @click="error='';firewallRuleModal=true"><Plus :size="15"/>添加规则</ElButton></div></template></UfwRules><ElDialog v-model="firewallRuleModal" title="添加 UFW 规则" width="min(560px, 94vw)" append-to-body :close-on-click-modal="false" :close-on-press-escape="!busy" :show-close="!busy"><ElForm id="firewall-add-form" :model="rule" label-position="top" @submit.prevent="addFirewallRule"><ElFormItem label="端口 / 范围"><ElInput v-model="rule.port" placeholder="443 或 8000:8100" required /></ElFormItem><ElFormItem label="协议"><ElSelect v-model="rule.protocol"><ElOption value="tcp" label="tcp" /><ElOption value="udp" label="udp" /></ElSelect></ElFormItem><ElFormItem label="来源 IP / CIDR"><ElInput v-model="rule.source" placeholder="any" /></ElFormItem><ElFormItem label="策略"><ElSelect v-model="rule.policy"><ElOption value="allow" label="允许" /><ElOption value="deny" label="拒绝" /></ElSelect></ElFormItem><p v-if="error" class="form-error">{{error}}</p></ElForm><template #footer><ElButton :disabled="!!busy" @click="firewallRuleModal=false">取消</ElButton><ElButton form="firewall-add-form" :loading="firewallPending==='add'" type="primary" native-type="submit" :disabled="!!busy||!!remaining||!can('firewall.write')"><Plus :size="15"/>添加规则</ElButton></template></ElDialog></template><template v-else><div v-if="iptablesState?.pending" class="banner warning"><div><b>原始规则等待确认 · {{remaining}} 秒</b><p>未确认会由主机上的独立任务恢复原始规则。</p></div><ElButton  :disabled="!remaining" :loading="firewallPending==='confirm'" @click="confirmFirewall" native-type="button" type="primary">确认保留</ElButton></div><ElForm inline label-position="top" class="toolbar"><ElFormItem label="规则后端"><ElSelect  aria-label="iptables 后端" :disabled="!!busy" v-model="iptablesProgram"><ElOption v-for="program in (iptablesState?.programs||['iptables','ip6tables','iptables-nft','ip6tables-nft','iptables-legacy','ip6tables-legacy'])" :value="program" :label="(program)" /></ElSelect></ElFormItem><span class="muted">{{iptablesState?.version}}</span><div class="spacer"/><ElButton  :disabled="!!busy" native-type="button" @click="work(()=>loadIPTables(true,true))">重新读取规则</ElButton><ElButton  @click="exportIPTables" native-type="button">导出规则</ElButton><ElButton  :disabled="!!busy||!!iptablesState?.pending||!can('iptables.write')" :loading="firewallPending==='apply'" @click="applyIPTables" native-type="button" type="primary">校验并应用规则</ElButton></ElForm><FirewallVisual v-model="iptablesRules" :writable="can('iptables.write') && !iptablesState?.pending"/><details class="card iptables-card raw-rules"><summary>高级：原始规则编辑</summary><ElInput type="textarea" aria-label="iptables 原始规则" v-model="iptablesRules" class="code-editor full-editor" spellcheck="false" /></details></template></template>
<div class="terminal-workspace" v-show="page==='terminal'"><div class="terminal-content"><PersistentTerminal v-if="user" ref="terminalView" :key="selectedNode" :api="api" :base="nodeBase()" :node-id="selectedNode" :identity="activeNode?.mode==='ssh'&&!can('terminal.root')?activeNode.user:'root'" :excluded-id="floatingTerminal?floatingSession:''" :dock-target="terminalDockHover" @tab-drag="dragTerminalTab"/></div></div>
<Teleport to="body"><div v-if="user&&floatingStarted" v-show="floatingTerminal" class="terminal-workspace floating-terminal" :class="{'terminal-minimized':terminalMinimized}" :style="{left:terminalPosition.x+'px',top:terminalPosition.y+'px'}"><div class="floating-terminal-bar" @pointerdown="dragTerminal"><b>终端 · {{activeNode?.name}}</b><div><ElButton @click="terminalMinimized=!terminalMinimized">{{terminalMinimized?'展开':'最小化'}}</ElButton><ElButton @click="page='terminal'">终端页</ElButton><ElButton aria-label="隐藏浮动终端" @click="floatingTerminal=false">×</ElButton></div></div><div class="terminal-content" v-show="!terminalMinimized"><PersistentTerminal ref="floatingTerminalView" :key="selectedNode" :api="api" :base="nodeBase()" :node-id="selectedNode" :identity="activeNode?.mode==='ssh'&&!can('terminal.root')?activeNode.user:'root'" storage-scope="floating" @tab-drag="(id,event)=>dragTerminalTab(id,event,true)" @selected="floatingSession=$event"/></div></div></Teleport>
   <CertificateFolder v-if="page==='certificates'" :refresh-signal="cacheRefresh" :key="selectedNode" :api="api" :node-id="selectedNode" :writable="true"/>
   <DockerManager v-if="page==='docker'" :key="selectedNode" :node-id="selectedNode" :refresh-signal="cacheRefresh" :api="api"/>
   <SystemdManager :ssh-user="activeNode?.mode==='ssh'?activeNode.user:undefined" :node-id="selectedNode" :refresh-signal="cacheRefresh" v-if="page==='systemd'" :key="selectedNode" :api="api" :writable="can('systemd.write')"/>
   <template v-if="page==='logs'"><div class="toolbar"><ElSpace wrap class="segmented"><ElButton :type="(logKind==='ssh')?'primary':undefined" plain @click="logKind='ssh'" native-type="button">SSH 登录</ElButton><ElButton :type="(logKind==='nginx')?'primary':undefined" plain @click="logKind='nginx'" native-type="button">Nginx</ElButton><ElButton :type="(logKind==='audit')?'primary':undefined" plain @click="logKind='audit'" native-type="button">中心操作记录</ElButton></ElSpace><ElSelect  v-if="logKind==='nginx'" v-model="logSite"><ElOption value="" :label="'选择站点'" /><ElOption v-for="s in sites" :value="s.site.id" :label="(s.site.id)" /></ElSelect><ElSelect  v-if="logKind==='nginx'" v-model="logNginxKind"><ElOption value="access" :label="'访问日志'" /><ElOption value="error" :label="'错误日志'" /></ElSelect><div class="spacer"/><ElCheckbox class="check" v-model="follow"  >实时跟随</ElCheckbox></div><div class="search-box"><Search :size="16"/><ElInput v-model="logFilter" placeholder="筛选 IP、用户名或关键词" /></div><section class="card log-card"><div class="card-heading"><h2>{{logKind==='ssh'?'SSH 服务日志':logKind==='nginx'?'Nginx 站点日志':'特权 中心操作记录'}}</h2><span class="muted">最近 300 行 · 最多 256 KiB</span></div><pre>{{filteredLog||'暂无匹配日志'}}</pre></section></template></main></div></div>
 <ElDialog v-if="batchCertificate" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(640px, 94vw)"><section class="modal component-dialog-body">

   <ElForm id="app-form-2" :disabled="batchRunning" label-position="top" @submit.prevent="deployBatchCertificate"><ElSpace direction="vertical" fill style="width: 100%"><div class="wide"><ElButton  @click="openCertificateFolder(batchPEM)" native-type="button">从证书夹选择</ElButton></div>
     <div class="wide" style="max-height: 12rem; overflow: auto"><ElCheckbox v-for="target in batchTargets" :key="target.key" class="check" v-model="batchSelected"  :value="target.key" >{{target.label}}</ElCheckbox></div>
     <ElFormItem label="PEM 证书链" class="wide"><ElInput type="textarea" v-model="batchPEM.certificate" required  :rows="4" spellcheck="false" /></ElFormItem>
     <ElFormItem label="PEM 私钥" class="wide"><ElInput type="textarea" v-model="batchPEM.privateKey" required  :rows="4" spellcheck="false" autocomplete="off" /></ElFormItem></ElSpace>
    <p v-if="error" class="form-error">{{error}}</p>
    <ul v-if="batchResults.length" aria-live="polite"><li v-for="result in batchResults" :key="result.label" :class="result.error?'red':'green'">{{result.label}} — {{result.error||'部署成功'}}</li></ul>
    </ElForm></section><template #header><ElRow justify="space-between" align="middle" ><span class="el-dialog__title">批量部署证书</span><ElButton text  native-type="button" class="icon-button" :disabled="batchRunning" @click="closeBatchCertificate"><X/></ElButton></ElRow></template><template #footer><ElButton  :disabled="batchRunning" @click="closeBatchCertificate" native-type="button">关闭</ElButton>
     <ElButton form="app-form-2" :loading="batchRunning" :disabled="batchRunning||!batchSelected.length" native-type="submit" type="primary">{{batchRunning?'部署中…':'部署到所选域名'}}</ElButton></template></ElDialog>

 <SiteEditor v-if="siteModal" v-model:site="siteForm" v-model:routes="routeForm" v-model:advanced="advancedRouting" v-model:multiple="multipleCertificates" :revision="revision" :busy="!!siteSaveStage" :save-stage="siteSaveStage" :error="error" :original-routing-only="originalRoutingOnly" @close="siteModal=false" @save="saveSite" @certificate="openCertificateFolder" @raw-routing="openRawRouting" @add-binding="addCertificateBinding"/>
 <ElDialog v-if="certFolder" class="certificate-picker-dialog" :model-value="true" title="选择证书" width="min(900px, calc(100vw - 24px))" top="6vh" append-to-body :close-on-click-modal="false" @close="certFolder=false;certTarget=null"><CertificateFolder :key="selectedNode" :api="api" :node-id="selectedNode" :writable="true" picker @select="chooseCertificate"/></ElDialog>
 <SiteConfigEditor v-if="configView" v-model:tab="fileTab" :site="configView.site.id" :files="configView.files" :busy="!!busy" :error="error" @close="configView=null" @save="saveConfig"/>

 <ElDialog v-if="scanJobs" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(640px, 94vw)"><section class="modal component-dialog-body"><PanelTable table-id="scan-jobs" :rows="scanJobs" :columns="tableColumnsScanJobs" row-key="id"><template #path="{row:job}">{{job.path}}</template><template #status="{row:job}">{{({queued:'排队中',running:'扫描中',done:'完成',failed:'失败'} as Record<string,string>)[job.status]}}<small v-if="job.error">{{job.error}}</small></template><template #actions="{row:job}"><ElButton v-if="job.result"  @click="scan=job.result;scanJobs=null" native-type="button">查看结果</ElButton></template><template #empty>暂无扫描记录</template></PanelTable></section><template #header><ElRow justify="space-between" align="middle" ><ElSpace direction="vertical" alignment="start" :size="4"><span class="el-dialog__title">空间扫描记录</span></ElSpace><ElButton text  native-type="button" class="icon-button" aria-label="关闭" @click="scanJobs=null"><X/></ElButton></ElRow></template><template #footer><ElButton  @click="work(loadScanJobs)" native-type="button">刷新记录</ElButton></template></ElDialog>
 <ElDialog v-if="scan" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(640px, 94vw)"><section class="modal component-dialog-body"><PanelTable table-id="scan-results" :rows="scan.largest" :columns="tableColumnsScanResults" row-key="path"><template #path="{row:f}">{{f.path}}</template><template #size="{row:f}">{{bytes(f.size)}}</template></PanelTable></section><template #header><ElRow justify="space-between" align="middle" ><ElSpace direction="vertical" alignment="start" :size="4"><span class="el-dialog__title">空间扫描</span><p>{{scan.files}} 个文件 · {{bytes(scan.bytes)}} 逻辑大小{{scan.truncated?' · 已达扫描上限':''}}{{scan.skipped?' · '+scan.skipped+' 处无法读取，结果不完整':''}}</p></ElSpace><ElButton text  native-type="button" class="icon-button" @click="scan=null"><X/></ElButton></ElRow></template></ElDialog>

 <ElDialog v-if="nodeModal" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(640px, 94vw)"><section class="modal component-dialog-body"><ElForm id="app-form-3" label-position="top" @submit.prevent="saveNode"><ElRow :gutter="16" ><ElCol :xs="24" :sm="12" ><ElFormItem label="主机名称"><ElInput v-model="nodeForm.name" required placeholder="生产服务器 / 家庭主机" /></ElFormItem></ElCol><ElCol :xs="24" :sm="12" ><ElFormItem label="连接方式"><ElSelect  v-model="nodeForm.mode"><ElOption value="ssh" :label="'SSH · 不安装 Agent'" /><ElOption value="agent" :label="'Agent · 主动连接中心'" /></ElSelect></ElFormItem></ElCol><template v-if="nodeForm.mode==='ssh'"><ElCol :xs="24" :sm="12" ><ElFormItem label="主机地址"><ElInput v-model="nodeForm.host" required placeholder="192.168.1.10" /></ElFormItem></ElCol><ElCol :xs="24" :sm="12" ><ElFormItem label="SSH 端口"><ElInputNumber v-model="nodeForm.port"  :min="1" :max="65535" required /></ElFormItem></ElCol><ElCol :xs="24" :sm="12" ><ElFormItem label="SSH 用户"><ElInput v-model="nodeForm.user" required autocomplete="off" /></ElFormItem></ElCol><ElCol :xs="24" :sm="12" ><ElFormItem label="认证方式"><ElSelect  v-model="authMode"><ElOption value="key" :label="'SSH 私钥'" /><ElOption value="password" :label="'密码'" /></ElSelect></ElFormItem></ElCol><ElCol :xs="24" :sm="12" ><ElFormItem label="提权方式"><ElSelect  v-model="nodeForm.sudoMode"><ElOption value="none" :label="'不提权 / 直接 root 登录'" /><ElOption value="password" :label="'使用密码 sudo（无需免密配置）'" /><ElOption value="nopasswd" :label="'免密 sudo -n（已配置时使用）'" /></ElSelect></ElFormItem></ElCol><ElCol :xs="24" :sm="24" v-if="nodeForm.sudoMode==='password'"><ElFormItem label="sudo 密码（留空使用 SSH 登录密码）"  class="wide"><ElInput v-model="nodeForm.secret.sudoPassword" type="password" autocomplete="new-password" /></ElFormItem></ElCol><ElCol :xs="24" :sm="24" v-if="authMode==='password'"><ElFormItem label="SSH 密码"  class="wide"><ElInput v-model="nodeForm.secret.password" type="password" autocomplete="new-password" required /></ElFormItem></ElCol><template v-else><ElCol :xs="24" :sm="24" ><ElFormItem label="SSH 私钥" class="wide"><ElInput type="textarea" v-model="nodeForm.secret.privateKey"  :rows="5" class="code-editor" required spellcheck="false" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" /></ElFormItem></ElCol><ElCol :xs="24" :sm="24" ><ElFormItem label="私钥口令（可选）" class="wide"><ElInput v-model="nodeForm.secret.passphrase" type="password" autocomplete="new-password" /></ElFormItem></ElCol></template><ElCol :xs="24" :sm="24" ><ElFormItem label="SSH 主机指纹" class="wide"><ElSpace fill><ElInput v-model="nodeForm.fingerprint" class="mono" required placeholder="SHA256:…" /><ElButton  :disabled="!!busy" @click="fingerprint" native-type="button">获取指纹</ElButton></ElSpace><span class="muted">请与目标服务器的可信指纹核对；后续指纹变化将阻止连接。</span></ElFormItem></ElCol></template><ElCol :xs="24" :sm="24" v-else><div  class="wide enrollment-help">注册配置有效期为 10 分钟，仅可使用一次。</div></ElCol></ElRow><p v-if="error" class="form-error">{{error}}</p></ElForm></section><template #header><ElRow justify="space-between" align="middle" ><ElSpace direction="vertical" alignment="start" :size="4"><span class="el-dialog__title">添加主机</span></ElSpace><ElButton text  native-type="button" class="icon-button" @click="nodeModal=false"><X/></ElButton></ElRow></template><template #footer><ElButton  @click="nodeModal=false" native-type="button">取消</ElButton><ElButton form="app-form-3"  :disabled="!!busy" native-type="submit" type="primary">{{busy?'正在保存…':nodeForm.mode==='agent'?'创建并生成注册配置':'保存并探测连接'}}</ElButton></template></ElDialog>
 <ElDialog v-if="enrollmentResult" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(640px, 94vw)"><section class="modal component-dialog-body"><ElRow :gutter="16" ><ElCol :xs="24" :sm="24" ><ElFormItem label="通过已有 SSH 主机安装" class="wide"><ElSelect v-model="installSSH" placeholder="选择已连接的 SSH 主机"><ElOption v-for="node in nodes.filter(n=>n.mode==='ssh')" :key="node.id" :value="node.id" :label="node.name"/></ElSelect></ElFormItem></ElCol>
 <ElCol :xs="24" :sm="12" ><ElButton type="primary" :disabled="!installSSH" :loading="installAgentBusy" @click="installEnrollmentSSH">通过 SSH 安装 Agent</ElButton></ElCol>
 <ElCol :xs="24" :sm="24" v-if="installAgentMessage"><p  class="wide" role="status">{{installAgentMessage}}</p></ElCol></ElRow><p class="enrollment-help">在目标主机运行下方命令。注册文件包含凭证，请勿外传。</p><pre class="config-preview">sudo ./ownpanel-agent \
  -data /var/lib/ownpanel-agent \
  -file-root /srv/ownpanel \
  -nginx-prefix /etc/nginx/ownpanel-agent \
  -enrollment-file ./ownpanel-enrollment.json</pre><p class="muted">中心接入地址：{{enrollmentResult.url}}</p><p class="footnote">远程 Agent 需能访问上方中心接入地址。</p></section><template #header><ElRow justify="space-between" align="middle" ><ElSpace direction="vertical" alignment="start" :size="4"><span class="el-dialog__title">Agent 注册配置</span><p>一次性凭证 · 有效期至 {{date(enrollmentResult.expires)}}</p></ElSpace><ElButton text  native-type="button" class="icon-button" @click="enrollmentResult=null"><X/></ElButton></ElRow></template><template #footer><ElButton  @click="enrollmentResult=null" native-type="button">关闭</ElButton><ElButton  @click="downloadEnrollment" native-type="button" type="primary"><Download :size="16"/>下载注册配置</ElButton></template></ElDialog>
 <ElDialog v-if="passwordModal&&user" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(1120px, 96vw)"><section class="modal component-dialog-body"><ElForm id="app-form-4" label-position="top" @submit.prevent="changePassword"><ElRow :gutter="16" ><ElCol :xs="24" :sm="24" ><ElFormItem label="当前密码" class="wide"><ElInput type="password" autocomplete="current-password" v-model="passwordForm.current" required /></ElFormItem></ElCol><ElCol :xs="24" :sm="24" ><ElFormItem label="新密码" class="wide"><ElInput type="password" autocomplete="new-password" v-model="passwordForm.new" minlength="12" required /></ElFormItem></ElCol></ElRow><p v-if="error" class="form-error">{{error}}</p></ElForm></section><template #header><ElRow justify="space-between" align="middle" ><span class="el-dialog__title">修改密码</span><ElButton text  native-type="button" class="icon-button" aria-label="关闭" @click="closePasswordModal"><X/></ElButton></ElRow></template><template #footer><ElButton form="app-form-4"  :disabled="!!busy" native-type="submit" type="primary">保存并退出</ElButton></template></ElDialog>
 <ElDialog v-if="credentialNode&&user" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(1120px, 96vw)"><section class="modal component-dialog-body"><ElForm id="app-form-5" label-position="top" @submit.prevent="saveCredentials"><ElRow :gutter="16" ><ElCol :xs="24" :sm="24" ><ElFormItem label="认证方式" class="wide"><ElSelect  v-model="credentialType"><ElOption value="key" :label="'SSH 私钥'" /><ElOption value="password" :label="'密码'" /></ElSelect></ElFormItem></ElCol><ElCol :xs="24" :sm="24" v-if="credentialType==='password'"><ElFormItem label="SSH 密码"  class="wide"><ElInput type="password" v-model="credentialForm.password" autocomplete="new-password" required /></ElFormItem></ElCol><template v-else><ElCol :xs="24" :sm="24" ><ElFormItem label="SSH 私钥" class="wide"><ElInput type="textarea" v-model="credentialForm.privateKey"  :rows="5" required spellcheck="false" /></ElFormItem></ElCol><ElCol :xs="24" :sm="24" ><ElFormItem label="私钥口令（可选）" class="wide"><ElInput type="password" v-model="credentialForm.passphrase" autocomplete="new-password" /></ElFormItem></ElCol></template></ElRow><ElFormItem label="sudo 密码（留空保留）" v-if="credentialNode.sudoMode==='password'"><ElInput v-model="credentialForm.sudoPassword" type="password" autocomplete="new-password" /></ElFormItem><p>连接验证成功后替换凭证，现有 SSH 会话将断开。</p><p v-if="error" class="form-error">{{error}}</p></ElForm></section><template #header><ElRow justify="space-between" align="middle" ><span class="el-dialog__title">更新 SSH 凭证 · {{credentialNode.name}}</span><ElButton text  native-type="button" class="icon-button" aria-label="关闭" @click="closeCredentials"><X/></ElButton></ElRow></template><template #footer><ElButton form="app-form-5"  :disabled="!!busy" native-type="submit" type="primary">验证并更新</ElButton></template></ElDialog>
 <ElDialog v-if="siteVersions&&user" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(640px, 94vw)"><section class="modal component-dialog-body"><p v-if="!siteVersions.items.length">暂无完整站点快照。</p><PanelTable table-id="site-versions" :rows="siteVersions.items" :columns="tableColumnsSiteVersions" row-key="id" v-else><template #version="{row:version}"><code>{{version.id}}</code></template><template #time="{row:version}">{{date(version.time)}}</template><template #actions="{row:version}"><ElButton  :disabled="!!busy" @click="restoreVersion(version.id)" native-type="button">恢复</ElButton></template></PanelTable><p v-if="error" class="form-error">{{error}}</p></section><template #header><ElRow justify="space-between" align="middle" ><span class="el-dialog__title">历史版本 · {{siteVersions.site}}</span><ElButton text  native-type="button" class="icon-button" aria-label="关闭" @click="siteVersions=null"><X/></ElButton></ElRow></template></ElDialog>
 <ElDialog v-if="sudoNode&&user" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(640px, 94vw)"><section class="modal component-dialog-body"><ElForm id="app-form-6" label-position="top" @submit.prevent="saveSudo"><ElFormItem label="提权方式"><ElSelect  v-model="sudoForm.mode"><ElOption value="none" :label="'不提权 / 直接 root 登录'" /><ElOption value="password" :label="'使用密码 sudo（无需免密配置）'" /><ElOption value="nopasswd" :label="'免密 sudo -n（已配置时使用）'" /></ElSelect></ElFormItem><ElFormItem label="sudo 密码" v-if="sudoForm.mode==='password'"><ElInput v-model="sudoForm.password" type="password" autocomplete="new-password" placeholder="留空保留已有密码，未单独设置时使用 SSH 密码" /></ElFormItem><p>账号需有 sudo 权限；保存后现有 SSH 连接将断开。</p><p v-if="error" class="form-error">{{error}}</p></ElForm></section><template #header><ElRow justify="space-between" align="middle" ><span class="el-dialog__title">提权设置 · {{sudoNode.name}}</span><ElButton text  native-type="button" class="icon-button" aria-label="关闭" @click="closeSudo"><X/></ElButton></ElRow></template><template #footer><ElButton  @click="closeSudo" native-type="button">取消</ElButton><ElButton form="app-form-6"  :disabled="!!busy" native-type="submit" type="primary">验证并保存</ElButton></template></ElDialog></template>
