<script setup lang="ts">
import { operationMessage } from "./operation-message";
import PanelTable from "./PanelTable.vue";
import type { TableColumn } from "./table-layout";
import CategoryOrderDialog from './CategoryOrderDialog.vue';
import {readCategoryView,orderedCategoryTabs,saveCategoryView} from './category-view';
import {ref,computed,onMounted,onUnmounted,watch,nextTick} from 'vue';
const props=defineProps<{api:(url:string,method?:string,body?:unknown,onFresh?:(value:any)=>void)=>Promise<any>,writable:boolean,refreshSignal?:number;nodeId?:string;account?:string}>();
type Preference={category?:string,favorite?:boolean,membership?:string,group?:string};
const units=ref<any[]>([]),preferences=ref<Record<string,Preference>>({}),scope=ref(''),search=ref(''),type=ref('service'),state=ref('all'),group=ref('');
const selected=ref(''),detail=ref<any>(null),tab=ref('logs'),error=ref(''),busy=ref(false),loading=ref(true),configMode=ref('unit'),unitContent=ref(''),override=ref('');
const logText=ref(''),logSearch=ref(''),logRange=ref('all'),logLevel=ref('all'),logLines=ref(500),follow=ref(true),autoScroll=ref(true),logBusy=ref(false),logElement=ref<HTMLElement|null>(null);
const newModal=ref(false),createMode=ref('form'),newName=ref(''),newGroup=ref(''),newFavorite=ref(true),startAfterCreate=ref(true),enableAfterCreate=ref(false),rawContent=ref('');
const form=ref({description:'',command:'',directory:'',user:'root',restart:'on-failure',environment:''});
const pendingUnits=ref(new Map<string,string>()), formOperation=ref(''), batchOperation=ref('');
let inventoryVersion=0, lastInventory=0, activityFlight=false, supportsActivity=true;
let disposed=false,detailGeneration=0,logGeneration=0,timer:ReturnType<typeof setInterval>,ticks=0,inventoryFlight:Promise<void>|null=null;
const pref=(name:string)=>preferences.value[name]||{};
const categories=ref<string[]>([]);
const mine=(u:any)=>pref(u.unit).category==='我的服务';
const groups=computed(()=>categories.value);
const typeUnits=computed(()=>units.value.filter(u=>!type.value||u.unit.endsWith('.'+type.value)));
const viewKey='ownpanel-systemd-category-view-'+(props.nodeId||'local')+(props.account?':'+props.account:''),savedView=readCategoryView(viewKey),tabOrder=ref(savedView.order),sortModal=ref(false);
scope.value=savedView.selected;
watch(scope,value=>{if(!loading.value)saveCategoryView(viewKey,value,tabOrder.value)});
async function saveCategoryOrder(ids:string[]){await work(async()=>{const result=await props.api('/systemd/categories','POST',{action:'reorder',categories:ids.filter(id=>categories.value.includes(id))});applyCategoryChange(result);if(disposed)return;tabOrder.value=ids;saveCategoryView(viewKey,scope.value,ids);sortModal.value=false})}
const categoryTabs=computed(()=>orderedCategoryTabs([...categories.value.map(name=>({id:name,label:name})),{id:'__uncategorized',label:'未分类'},{id:'__favorites',label:'★ 收藏'}],tabOrder.value));
function inCategory(u:any,id:string){return id==='__favorites'?!!pref(u.unit).favorite:id==='__uncategorized'?!categories.value.includes(pref(u.unit).category||''):pref(u.unit).category===id}
const filtered=computed(()=>typeUnits.value.filter(u=>inCategory(u,scope.value)&&(state.value==='all'||(state.value==='enabled'?u.state==='enabled':u.active===state.value))&&(u.unit+' '+(u.description||'')).toLowerCase().includes(search.value.toLowerCase())).sort((a,b)=>a.unit.localeCompare(b.unit)));
function applyCategoryChange(result:{categories:string[];units:Record<string,Preference>}){if(disposed)return;categories.value=result.categories;preferences.value=result.units}
async function addCategory(){
 const name=prompt('分类名称')?.trim();if(!name)return;
 await work(async()=>{const result=await props.api('/systemd/categories','POST',{action:'create',name});applyCategoryChange(result);if(!disposed)scope.value=name});
}
async function deleteCategory(){
 const name=scope.value;if(!categories.value.includes(name)||!confirm('删除分类“'+name+'”？其中的服务将显示在未分类中。'))return;
 await work(async()=>{const result=await props.api('/systemd/categories','POST',{action:'delete',name});applyCategoryChange(result);if(!disposed)scope.value=result.categories[0]||'__uncategorized'});
}
async function renameCategory(){
 const old=scope.value;if(!categories.value.includes(old))return;
 const name=prompt('分类名称',old)?.trim();if(!name||name===old)return;
 await work(async()=>{const result=await props.api('/systemd/categories','POST',{action:'rename',name:old,target:name});applyCategoryChange(result);if(!disposed){tabOrder.value=tabOrder.value.map(id=>id===old?name:id);scope.value=name}});
}
const checkedUnits=ref<string[]>([]),batchCategory=ref(''),batchCategoryModal=ref(false),batchResults=ref<{unit:string;error?:string}[]>([]);
const allChecked=computed(()=>filtered.value.length>0&&filtered.value.every(u=>checkedUnits.value.includes(u.unit)));
function selectAll(){
 checkedUnits.value=allChecked.value?[]:filtered.value.map(u=>u.unit);
}
watch(filtered,list=>{const visible=new Set(list.map(u=>u.unit));checkedUnits.value=checkedUnits.value.filter(unit=>visible.has(unit))});
async function applyBatchCategory(){await batchAction('category');if(!error.value)batchCategoryModal.value=false}
async function batchAction(operation:string){
 if(busy.value||pendingUnits.value.size||!checkedUnits.value.length)return;
 const names=[...checkedUnits.value];
 if(operation!=='category'&&!props.writable)return;
 if(['stop','restart'].includes(operation)&&!confirm('确认'+(operation==='stop'?'停止':'重启')+'选中的 '+names.length+' 个服务？'))return;
 batchOperation.value=operation;
 batchResults.value=[];
 await work(async()=>{
  for(const unit of names){
   if(disposed)return;
   try{
    if(operation==='category')await preference(unit,{category:batchCategory.value});
    else await props.api('/systemd/action','POST',{unit,action:operation});
    batchResults.value.push({unit});
   }catch(e){batchResults.value.push({unit,error:(e as Error).message})}
  }
  if(operation!=='category')await loadUnits(true);
 });
 batchOperation.value='';
 if(disposed)return;
 if(!batchResults.value.some(result=>result.error)){
  checkedUnits.value=[];
  operationMessage('批量操作完成 · '+names.length+' 个服务');
  batchResults.value=[];
 }
}
const selectedUnit=computed(()=>units.value.find(u=>u.unit===selected.value));
const visibleLogs=computed(()=>logText.value.split('\n').filter(l=>l.toLowerCase().includes(logSearch.value.toLowerCase())).join('\n'));
const activeLabel=(value:string)=>({active:'运行中',inactive:'已停止',failed:'失败',activating:'启动中',deactivating:'停止中',reloading:'重载中'} as Record<string,string>)[value]||value||'未知';
const bootLabel=(value:string)=>({enabled:props.account?'已启用':'开机启动',disabled:'未启用',static:'静态单元',masked:'已屏蔽',alias:'别名',indirect:'间接启用',generated:'自动生成',transient:'临时单元'} as Record<string,string>)[value]||value||'—';
async function work(fn:()=>Promise<void>){busy.value=true;error.value='';try{await fn()}catch(e){if(!disposed)error.value=(e as Error).message}finally{busy.value=false}}
async function loadUnits(force=false){
 if(inventoryFlight && !force)return inventoryFlight;
 const version=++inventoryVersion;
 const apply=(r:any)=>{
  if(disposed || version!==inventoryVersion)return;
  units.value=r.units;
  lastInventory=Date.now();
 };
 const request=props.api('/systemd',force?'REFRESH':'GET',undefined,apply).then(apply);
 inventoryFlight=request;
 try{await request}finally{if(inventoryFlight===request)inventoryFlight=null}
}
async function pollActivity(){
 if(activityFlight || inventoryFlight || busy.value || pendingUnits.value.size)return;
 if(Date.now()-lastInventory>=120_000 || !supportsActivity){
  if(supportsActivity || Date.now()-lastInventory>=30_000)await loadUnits(true);
  return;
 }
 const version=inventoryVersion;
 activityFlight=true;
 try{
  const result=await props.api('/systemd?view=activity');
  if(disposed || version!==inventoryVersion || busy.value || pendingUnits.value.size)return;
  if(!Array.isArray(result.activity)){
   // Older Agents return the normal inventory for this URL.
   supportsActivity=false;
   if(Array.isArray(result.units)){units.value=result.units;lastInventory=Date.now()}
   return;
  }
  const states=new Map<string,{active:string;sub:string;load:string}>(result.activity.map((unit:any)=>[unit.unit,unit]));
  for(const row of units.value){
   const state=states.get(row.unit);
   row.active=state?.active || 'inactive';
   row.sub=state?.sub || 'dead';
   row.load=state?.load || 'not-loaded';
  }
 }finally{activityFlight=false}
}
async function loadAll(force=false){
 try{
  let [,,config]=await Promise.all([loadUnits(force),props.api('/systemd/preferences',force?'REFRESH':'GET').then(r=>{if(!disposed)preferences.value=r.units}),props.api('/systemd/categories')]);
  if(disposed)return;
  if(!config.initialized){
   const initial=['我的服务',props.account?'共享用户单元':'系统服务'];
   const assignments=Object.fromEntries(units.value.map(u=>[u.unit,pref(u.unit).membership==='my'||(pref(u.unit).membership!=='system'&&u.custom)?initial[0]:initial[1]]));
   await props.api('/systemd/categories','POST',{initial:true,categories:initial,units:assignments});
   config=await props.api('/systemd/categories');
   preferences.value=(await props.api('/systemd/preferences','REFRESH')).units;
  }
  if(disposed)return;
  categories.value=config.categories||[];
  if(!categoryTabs.value.some(tab=>tab.id===scope.value))scope.value=categoryTabs.value[0]?.id||'__uncategorized';
 }finally{loading.value=false}
}
async function preference(name:string,patch:Preference){const r=await props.api('/systemd/preferences','POST',{unit:name,...patch});if(!disposed)preferences.value={...preferences.value,[name]:r.preference}}
async function toggleFavorite(u:any){await work(()=>preference(u.unit,{favorite:!pref(u.unit).favorite}))}
async function refreshLogs(){const name=selected.value,turn=++logGeneration;if(!name)return;logBusy.value=true;try{const r=await props.api('/systemd/logs?'+new URLSearchParams({unit:name,lines:String(logLines.value),since:logRange.value,priority:logLevel.value}));if(disposed||turn!==logGeneration||selected.value!==name)return;logText.value=r.text;await nextTick();if(autoScroll.value&&logElement.value)logElement.value.scrollTop=logElement.value.scrollHeight}catch(e){if(!disposed&&turn===logGeneration)error.value=(e as Error).message}finally{if(turn===logGeneration)logBusy.value=false}}
function setDetail(r:any,reset=true){detail.value=r;if(reset){unitContent.value=r.source?.content||'';override.value=r.override}}
function detailDirty(){
 return !!detail.value&&(unitContent.value!==(detail.value.source?.content||'')||override.value!==detail.value.override);
}
function closeDetail(){
 if(busy.value||(detailDirty()&&!confirm('放弃尚未保存的服务配置？')))return;
 selected.value='';loadingDetailFor='';detailGeneration++;logGeneration++;logBusy.value=false}
let loadingDetailFor='';
async function loadDetail(){
 const name=selected.value;
 if(!name||detail.value||loadingDetailFor===name)return;
 const turn=++detailGeneration;
 loadingDetailFor=name;
 try{
  const r=await props.api('/systemd/unit?unit='+encodeURIComponent(name)+'&view=editor');
  if(disposed||turn!==detailGeneration||selected.value!==name)return;
  setDetail(r);
  configMode.value=mine(selectedUnit.value||{unit:name})&&r.source?.path&&!r.source?.error?'unit':'override';
 }catch(e){
  if(!disposed&&turn===detailGeneration)error.value=(e as Error).message;
 }finally{
  if(turn===detailGeneration)loadingDetailFor='';
 }
}
async function inspect(name:string,view='logs'){
 detailGeneration++;
 loadingDetailFor='';
 logGeneration++;
 logBusy.value=false;
 selected.value=name;
 detail.value=null;
 tab.value=view;
 error.value='';
 logText.value='';
 logSearch.value='';
 if(view==='logs')await refreshLogs();
 else await loadDetail();
}
const actionLabels:Record<string,string>={start:'启动',stop:'停止',restart:'重启',reload:'重载',enable:'设置开机启动',disable:'取消开机启动',mask:'屏蔽',unmask:'解除屏蔽','reset-failed':'清除失败状态','daemon-reload':'重新加载配置'};
async function refreshUnit(name:string,configChanged=false){
 const readEditor=configChanged && selected.value===name && !!detail.value;
 const result=await props.api('/systemd/unit?'+new URLSearchParams({unit:name,view:readEditor?'editor':'state'}));
 const {properties}=result;
 if(disposed)return;
 const row=units.value.find(u=>u.unit===name);
 if(row && properties) {
  if(properties.ActiveState)row.active=properties.ActiveState;
  if(properties.SubState)row.sub=properties.SubState;
  if(properties.UnitFileState!==undefined)row.state=properties.UnitFileState;
  if(properties.LoadState)row.load=properties.LoadState;
 }
 if(selected.value===name && detail.value){
  if(readEditor && !detailDirty())setDetail(result);
  else detail.value.properties=properties;
 }
}
function unitLocked(name:string){
 return busy.value || pendingUnits.value.has(name) || pendingUnits.value.has('');
}
async function action(operation:string,name=selected.value){
 if(!props.writable || unitLocked(name) || (!name && pendingUnits.value.size))return;
 if(['stop','restart','mask'].includes(operation)&&!confirm(actionLabels[operation]+' '+name+'？'))return;
 pendingUnits.value.set(name,operation);
 error.value='';
 try{
  await props.api('/systemd/action','POST',{action:operation,unit:name});
  if(disposed)return;
  ++inventoryVersion;
  operationMessage((name||'Systemd')+' · '+actionLabels[operation]+'完成');
  try{
   if(name){
    await refreshUnit(name,operation==='mask'||operation==='unmask');
    if(!disposed && selected.value===name && tab.value==='logs')void refreshLogs();
   }else await loadUnits(true);
  }catch(e){if(!disposed)operationMessage('操作已完成，状态刷新失败：'+(e as Error).message,'warning')}
 }catch(e){if(!disposed)operationMessage((name||'Systemd')+' · '+actionLabels[operation]+'失败：'+(e as Error).message,'error')}
 finally{pendingUnits.value.delete(name)}
}
async function saveConfig(restart=false){
 const name=selected.value,mode=configMode.value;
 if(unitLocked(name))return;
 formOperation.value=restart?'save-restart':'save';
 await work(async()=>{
  await props.api('/systemd/config','POST',{unit:name,action:mode,content:mode==='unit'?unitContent.value:override.value,revision:mode==='unit'?detail.value.source.revision:detail.value.revision,...(mode==='unit'?{path:detail.value.source.path}:{})});
  if(disposed)return;
  if(!restart)operationMessage(name+' · 配置已保存并加载');
  // Read the new revision even if a subsequent restart fails.
  const r=await props.api('/systemd/unit?unit='+encodeURIComponent(name)+'&view=editor');
  if(disposed)return;
  if(selected.value===name)setDetail(r);
  if(restart){
   try{await props.api('/systemd/action','POST',{unit:name,action:'restart'})}
   catch(e){throw Error('配置已保存，但重启失败：'+(e as Error).message)}
   if(disposed)return;
   operationMessage(name+' · 配置已保存，服务已重启');
   if(selected.value===name)tab.value='logs';
  }
  ++inventoryVersion;
  await refreshUnit(name);
 });
 formOperation.value='';
}
function downloadLogs(){const url=URL.createObjectURL(new Blob([visibleLogs.value],{type:'text/plain;charset=utf-8'}));const a=document.createElement('a');a.href=url;a.download=selected.value+'-'+new Date().toISOString().replace(/[:.]/g,'-')+'.log';a.click();URL.revokeObjectURL(url)}
function singleLine(value:string,label:string){if(/[\r\n\0]/.test(value))throw Error(label+'不能换行');return value.trim()}
function generatedConfig(){const f=form.value,command=singleLine(f.command,'启动命令');if(!command)throw Error('请输入启动命令');let text='[Unit]\nDescription='+singleLine(f.description||newName.value||'My service','描述')+(props.account?'':'\nAfter=network-online.target\nWants=network-online.target')+'\n\n[Service]\nType=simple\n';if(!props.account&&f.user.trim())text+='User='+singleLine(f.user,'运行用户')+'\n';if(f.directory.trim())text+='WorkingDirectory='+singleLine(f.directory,'工作目录')+'\n';text+='ExecStart='+command+'\nRestart='+f.restart+'\nRestartSec=3\n';for(const line of f.environment.split('\n')){if(!line.trim()||line.trim().startsWith('#'))continue;if(!/^[A-Za-z_][A-Za-z0-9_]*=/.test(line))throw Error('环境变量需每行填写 KEY=value');text+='Environment='+JSON.stringify(line.replace(/%/g,'%%'))+'\n'}return text+'\n[Install]\nWantedBy='+(props.account?'default.target':'multi-user.target')+'\n'}
let initialCreate='';
function createSnapshot(){
 return JSON.stringify([newName.value,newGroup.value,newFavorite.value,startAfterCreate.value,enableAfterCreate.value,form.value,rawContent.value]);
}
function createDirty(){return newModal.value&&createSnapshot()!==initialCreate}
function closeCreate(){
 if(busy.value||(createDirty()&&!confirm('放弃尚未保存的新服务？')))return;
 newModal.value=false;
}
function guardUnload(event:BeforeUnloadEvent){
 if(!createDirty()&&!(selected.value&&detailDirty()))return;
 event.preventDefault();
 event.returnValue='';
}
function openCreate(){newName.value='';newGroup.value=categories.value.includes(scope.value)?scope.value:'';newFavorite.value=true;startAfterCreate.value=true;enableAfterCreate.value=false;createMode.value='form';form.value={description:'',command:'',directory:'',user:'root',restart:'on-failure',environment:''};rawContent.value='';error.value='';initialCreate=createSnapshot();newModal.value=true}
function toRaw(){if(createMode.value==='raw')return;try{const saved=form.value.command;if(!saved)form.value.command='/usr/bin/sleep infinity';try{rawContent.value=generatedConfig()}finally{form.value.command=saved}createMode.value='raw';error.value=''}catch(e){error.value=(e as Error).message}}
async function create(){
 if(busy.value)return;
 formOperation.value='create';
 let created='',partialFailure='';
 await work(async()=>{
  let unit=singleLine(newName.value,'服务名称');
  if(!/\.(service|timer|socket|target|path|mount|automount|slice|scope|device|swap)$/.test(unit))unit+='.service';
  if(unit==='.service')throw Error('请输入服务名称');
  const content=createMode.value==='raw'?rawContent.value:generatedConfig();
  const category=categories.value.includes(newGroup.value)?newGroup.value:'';
  const favorite=newFavorite.value,enable=enableAfterCreate.value,start=startAfterCreate.value;
  await props.api('/systemd/config','POST',{unit,content,action:'create'});
  if(disposed)return;
  created=unit;
  try{
   await preference(unit,{category,favorite});
   if(enable&&!disposed)await props.api('/systemd/action','POST',{unit,action:'enable'});
   if(start&&!disposed)await props.api('/systemd/action','POST',{unit,action:'start'});
  }catch(e){partialFailure='服务已创建，后续操作未完成：'+(e as Error).message}
  if(disposed)return;
  newModal.value=false;
  scope.value=category||'__uncategorized';
  type.value=unit.slice(unit.lastIndexOf('.')+1);
  search.value='';group.value='';state.value='all';
  if(partialFailure)operationMessage(unit+' · '+partialFailure,'warning');
  else operationMessage(unit+' 已创建');
 });
 formOperation.value='';
 if(created&&!disposed){
  void loadUnits(true).catch(e=>{if(!disposed)operationMessage('服务列表刷新失败：'+e.message,'warning')});
  await inspect(created,'logs');
  if(partialFailure&&!disposed)error.value=partialFailure;
 }
}
watch(()=>props.refreshSignal,()=>{void work(()=>loadAll())});
watch(groups,names=>{if(group.value&&!names.includes(group.value))group.value=''});
watch([logRange,logLevel,logLines],()=>{if(selected.value&&tab.value==='logs')void refreshLogs()});watch(tab,value=>{if(!selected.value)return;if(value==='logs'){if(!logBusy.value)void refreshLogs()}else void loadDetail()});
onMounted(()=>{void work(loadAll);timer=setInterval(()=>{if(disposed||document.hidden)return;if(selected.value&&tab.value==='logs'&&follow.value&&!logBusy.value)void refreshLogs();ticks++;if(ticks%5===0)void pollActivity().catch(()=>{})},2000)});
onMounted(()=>window.addEventListener('beforeunload',guardUnload));
onUnmounted(()=>{window.removeEventListener('beforeunload',guardUnload);disposed=true;detailGeneration++;logGeneration++;clearInterval(timer)});
const tableColumnsSystemd: TableColumn[] = [
    {key:"selection",label:"",sortable:false,fixed:'left',width:52,className:'file-select'},
    {key:"favorite",label:"收藏",value:(u)=>!!pref(u.unit).favorite,width:90},
    {key:"unit",label:"服务",value:(u)=>u.unit,minWidth:210},
    {key:"description",label:"描述",value:(u)=>u.description || '',minWidth:210},
    {key:"status",label:"运行状态",value:(u)=>u.active,minWidth:130},
    {key:"boot",label:props.account?"自启动":"开机启动",value:(u)=>u.state,minWidth:130},
    {key:"actions",label:"操作",sortable:false,fixed:'right',minWidth:320}
];
</script>

<template><slot name="manager" :locked="busy||!!pendingUnits.size||!!selected||newModal||sortModal||batchCategoryModal" /><p v-if="error" class="banner warning">{{error}}</p>

 <ElTabs v-model="scope" class="service-scopes"><ElTabPane v-for="item in categoryTabs" :key="item.id" :name="item.id"><template #label>{{item.label}} · {{typeUnits.filter(u=>inCategory(u,item.id)).length}}</template></ElTabPane></ElTabs>
 <CategoryOrderDialog v-if="sortModal" :items="categoryTabs" :busy="busy" :error="error" @close="sortModal=false" @save="saveCategoryOrder"/>
 <div class="toolbar systemd-toolbar"><ElButton :disabled="busy||loading" @click="error='';sortModal=true">分类排序</ElButton><ElButton  :disabled="!writable||busy" @click="openCreate" native-type="button" type="primary">＋ 创建服务</ElButton><ElInput v-model="search" aria-label="搜索 Systemd 单元" placeholder="搜索服务名称或描述" /><ElButton :disabled="busy" @click="addCategory" native-type="button">添加分类</ElButton><ElButton v-if="categories.includes(scope)" :disabled="busy" @click="renameCategory" native-type="button">重命名分类</ElButton><ElButton v-if="categories.includes(scope)" :disabled="busy" @click="deleteCategory" native-type="button">删除分类</ElButton><ElSelect  v-model="type" aria-label="单元类型"><ElOption value="service" :label="'服务'" /><ElOption value="timer" :label="'定时器'" /><ElOption value="socket" :label="'Socket'" /><ElOption value="" :label="'全部类型'" /><ElOption v-for="t in ['target','mount','automount','path','slice','scope']" :key="t" :value="t" :label="(t)" /></ElSelect><ElSelect  v-model="state" aria-label="单元状态"><ElOption value="all" :label="'全部状态'" /><ElOption value="active" :label="'运行中'" /><ElOption value="inactive" :label="'已停止'" /><ElOption value="failed" :label="'失败'" /><ElOption value="enabled" :label="account?'已启用自启动':'开机启动'" /></ElSelect><ElButton  :disabled="busy" native-type="button" @click="work(()=>loadAll(true))">刷新</ElButton></div>
 <div v-if="checkedUnits.length" class="toolbar service-batch"><b>已选 {{checkedUnits.length}} 项</b>
 <ElButton  :disabled="busy||!!pendingUnits.size||!writable" :loading="batchOperation==='start'" @click="batchAction('start')" native-type="button">批量启动</ElButton>
 <ElButton  :disabled="busy||!!pendingUnits.size||!writable" :loading="batchOperation==='stop'" @click="batchAction('stop')" native-type="button">批量停止</ElButton>
 <ElButton  :disabled="busy||!!pendingUnits.size||!writable" :loading="batchOperation==='restart'" @click="batchAction('restart')" native-type="button">批量重启</ElButton>
 <ElButton  :disabled="busy" @click="batchCategory='';batchCategoryModal=true" native-type="button">批量改分类</ElButton>
 <ElButton  :disabled="busy" @click="checkedUnits=[]" native-type="button">取消选择</ElButton></div>
<div v-if="batchResults.some(result=>result.error)" class="batch-feedback" role="status"><div class="batch-feedback-heading"><span>{{batchResults.filter(r=>!r.error).length}} 项成功<span v-if="batchResults.some(r=>r.error)">，{{batchResults.filter(r=>r.error).length}} 项失败</span></span><ElButton aria-label="关闭执行结果" @click="batchResults=[]" native-type="button">×</ElButton></div>
 <div v-for="result in batchResults.filter(r=>r.error)" :key="result.unit" class="red">{{result.unit}} · {{result.error}}</div></div>
<section class="card table-card systemd-table"><PanelTable table-id="systemd" :rows="filtered" :columns="tableColumnsSystemd" row-key="unit" :row-class-name="(u)=>({'file-row-selected':checkedUnits.includes(u.unit)})"><template #header-selection><ElCheckbox  aria-label="全选当前服务" :disabled="busy" :model-value="allChecked" :indeterminate="checkedUnits.length>0&&!allChecked" @change="selectAll" /></template><template #selection="{row:u}"><ElCheckbox v-model="checkedUnits"  :value="u.unit" :aria-label="'选择服务 '+u.unit" :disabled="busy" /></template><template #favorite="{row:u}"><ElButton text  native-type="button" class="favorite-button" :class="{starred:pref(u.unit).favorite}" :aria-label="(pref(u.unit).favorite?'取消收藏 ':'收藏 ')+u.unit" :disabled="busy" @click="toggleFavorite(u)">{{pref(u.unit).favorite?'★':'☆'}}</ElButton></template><template #unit="{row:u}"><ElButton link native-type="button" class="unit-name" @click="inspect(u.unit)">{{u.unit}}</ElButton></template><template #description="{row:u}">{{u.description||'—'}}</template><template #status="{row:u}"><span class="tag" :class="{'state-active':u.active==='active','state-failed':u.active==='failed'}">{{activeLabel(u.active)}}</span><small>{{u.sub}}</small></template><template #boot="{row:u}">{{bootLabel(u.state)}}</template><template #actions="{row:u}"><ElSpace wrap class="actions"><ElButton  @click="inspect(u.unit,'logs')" native-type="button">日志</ElButton><ElButton  @click="inspect(u.unit,'config')" native-type="button">配置</ElButton><ElButton  :disabled="unitLocked(u.unit)||!writable" :loading="['start','stop'].includes(pendingUnits.get(u.unit)||'')" @click="action(u.active==='active'?'stop':'start',u.unit)" native-type="button">{{u.active==='active'?'停止':'启动'}}</ElButton><ElButton  :disabled="unitLocked(u.unit)||!writable" :loading="pendingUnits.get(u.unit)==='restart'" @click="action('restart',u.unit)" native-type="button">重启</ElButton></ElSpace></template><template #empty><template v-if="loading">正在读取服务…</template><template v-else>没有匹配的服务</template></template></PanelTable></section>


 <ElDialog v-if="batchCategoryModal" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(640px, 94vw)"><ElForm id="systemdmanager-form-1" label-position="top" class="modal component-dialog-body" @submit.prevent="applyBatchCategory">
   <ElFormItem :label="`将 ${checkedUnits.length} 个服务移至`"><ElSelect  v-model="batchCategory" aria-label="批量目标分类" :disabled="busy"><ElOption value="" :label="'未分类'" /><ElOption v-for="name in categories" :key="name" :value="name" :label="(name)" /></ElSelect></ElFormItem>
   </ElForm><template #header><ElRow justify="space-between" align="middle" ><span class="el-dialog__title">批量修改分类</span><ElButton text  native-type="button" class="icon-button" :disabled="busy" aria-label="关闭批量分类" @click="batchCategoryModal=false">×</ElButton></ElRow></template><template #footer><ElButton  :disabled="busy" @click="batchCategoryModal=false" native-type="button">取消</ElButton><ElButton form="systemdmanager-form-1"  :disabled="busy||!checkedUnits.length" native-type="submit" type="primary">确认修改</ElButton></template></ElDialog>
 <ElDialog v-if="selected" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(1120px, 96vw)"><section class="modal wide systemd-detail service-detail component-dialog-body"><div class="service-detail-actions"><span class="tag" :class="{'state-active':selectedUnit?.active==='active','state-failed':selectedUnit?.active==='failed'}">{{activeLabel(selectedUnit?.active)}}</span><div class="spacer"/><ElButton  :disabled="unitLocked(selected)||!writable" :loading="pendingUnits.get(selected)==='start'" @click="action('start')" native-type="button">启动</ElButton><ElButton  :disabled="unitLocked(selected)||!writable" :loading="pendingUnits.get(selected)==='stop'" @click="action('stop')" native-type="button">停止</ElButton><ElButton  :disabled="unitLocked(selected)||!writable" :loading="pendingUnits.get(selected)==='restart'" @click="action('restart')" native-type="button" type="primary">重启</ElButton></div><ElTabs v-model="tab"><ElTabPane v-for="t in [{id:'logs',label:'日志'},{id:'config',label:'配置'},{id:'status',label:'状态与更多操作'}]" :key="t.id" :label="t.label" :name="t.id" /></ElTabs><p v-if="error" class="form-error">{{error}}</p><div v-if="tab==='logs'" class="service-logs"><div class="toolbar log-controls"><ElSelect  v-model="logRange" aria-label="服务日志时间"><ElOption value="all" :label="'最近日志'" /><ElOption value="5m" :label="'最近 5 分钟'" /><ElOption value="1h" :label="'最近 1 小时'" /><ElOption value="24h" :label="'最近 24 小时'" /></ElSelect><ElSelect  v-model="logLevel" aria-label="服务日志级别"><ElOption value="all" :label="'全部级别'" /><ElOption value="err" :label="'错误及以上'" /><ElOption value="warning" :label="'警告及以上'" /><ElOption value="info" :label="'信息及以上'" /></ElSelect><ElSelect  v-model="logLines" aria-label="服务日志行数"><ElOption :value="200" :label="'200 行'" /><ElOption :value="500" :label="'500 行'" /><ElOption :value="2000" :label="'2000 行'" /></ElSelect><ElInput v-model="logSearch" aria-label="搜索服务日志" placeholder="筛选日志内容" /><ElButton  :loading="logBusy" @click="refreshLogs" native-type="button">刷新日志</ElButton><ElButton  @click="downloadLogs" native-type="button">下载日志</ElButton></div><ElSpace wrap class="log-follow"><ElCheckbox v-model="follow"  >实时刷新</ElCheckbox><ElCheckbox v-model="autoScroll"  >滚动到底部</ElCheckbox></ElSpace><pre ref="logElement" class="service-log" aria-label="服务日志内容">{{visibleLogs||(logBusy?'正在读取日志…':error?'日志读取失败':'暂无匹配日志')}}</pre></div>
 <template v-else-if="detail"><div v-if="tab==='config'" class="service-config"><div class="toolbar"><ElSelect  v-model="configMode" aria-label="服务配置类型"><ElOption value="unit" :label="'服务配置源文件'" /><ElOption value="override" :label="'覆盖配置'" /><ElOption value="effective" :label="'查看生效配置'" /></ElSelect></div><pre v-if="configMode==='effective'" class="service-log">{{detail.config||'无配置文件'}}</pre><template v-else><p class="config-path">{{configMode==='unit'?detail.source?.path:(detail.overridePath||(account?'':'/etc/systemd/system/'+selected+'.d/ownpanel.conf'))}}</p><p v-if="configMode==='unit'&&detail.source?.error" class="form-error">{{detail.source.error}}</p><ElInput type="textarea" v-if="configMode==='unit'" v-model="unitContent" aria-label="Systemd 服务配置" :disabled="busy||!writable||!!detail.source?.error" class="code-editor service-config-editor" spellcheck="false" /><ElInput type="textarea" v-else v-model="override" aria-label="Systemd 覆盖配置" :disabled="busy||!writable" class="code-editor service-config-editor" spellcheck="false" /></template></div>
 <div v-if="tab==='status'"><dl class="systemd-properties"><template v-for="(value,key) in detail.properties" :key="key"><dt>{{key}}</dt><dd>{{value||'—'}}</dd></template></dl><div class="toolbar"><ElButton v-for="a in ['reload','enable','disable','reset-failed','mask','unmask']" :key="a"  :disabled="unitLocked(selected)||!writable" :loading="pendingUnits.get(selected)===a" @click="action(a)" native-type="button">{{({reload:'重载服务',enable:account?'启用自启动':'开机启动',disable:'取消自启动','reset-failed':'清除失败',mask:'屏蔽',unmask:'取消屏蔽'} as Record<string,string>)[a]}}</ElButton><ElButton  :disabled="busy||!!pendingUnits.size||!writable" :loading="pendingUnits.has('')" @click="action('daemon-reload','')" native-type="button">重新加载全部配置</ElButton></div></div></template><p v-else-if="!error" class="muted">正在读取服务配置…</p></section><template #header><ElRow justify="space-between" align="middle" ><ElSpace direction="vertical" alignment="start" :size="4"><span class="el-dialog__title">{{selected}}</span><small>{{detail?.properties?.Description}}</small></ElSpace><ElButton text  native-type="button" class="icon-button" aria-label="关闭 Systemd 详情" @click="closeDetail">×</ElButton></ElRow></template><template #footer v-if="tab==='config' &amp;&amp; detail &amp;&amp; configMode!=='effective'"><ElButton  :disabled="unitLocked(selected)||!writable||(configMode==='unit'&&!!detail.source?.error)" :loading="formOperation==='save'" @click="saveConfig(false)" native-type="button">保存配置</ElButton><ElButton  :disabled="unitLocked(selected)||!writable||(configMode==='unit'&&!!detail.source?.error)" :loading="formOperation==='save-restart'" @click="saveConfig(true)" native-type="button" type="primary">保存并重启</ElButton></template></ElDialog>



 <ElDialog v-if="newModal" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(1120px, 96vw)"><ElForm id="systemdmanager-form-2" :disabled="busy" label-position="top" class="modal wide service-create component-dialog-body" @submit.prevent="create"><p v-if="error" class="form-error">{{error}}</p><ElRow :gutter="16" ><ElCol :xs="24" :sm="12" ><ElFormItem label="服务名称"><ElInput v-model="newName" aria-label="新单元名称" placeholder="my-app（自动补 .service）" required /></ElFormItem></ElCol><ElCol :xs="24" :sm="12" ><ElFormItem label="分类"><ElSelect  v-model="newGroup"><ElOption value="" :label="'未分类'" /><ElOption v-for="name in categories" :key="name" :value="name" :label="(name)" /></ElSelect></ElFormItem></ElCol></ElRow><ElSpace wrap class="segmented create-mode"><ElButton :type="(createMode==='form')?'primary':undefined" plain @click="createMode='form'" native-type="button">表单创建</ElButton><ElButton :type="(createMode==='raw')?'primary':undefined" plain @click="toRaw" native-type="button">原始配置</ElButton></ElSpace><template v-if="createMode==='form'"><ElFormItem label="服务描述"><ElInput v-model="form.description" aria-label="服务描述" placeholder="这个服务用来做什么" /></ElFormItem><ElFormItem label="启动命令"><ElInput v-model="form.command" aria-label="启动命令" placeholder="/usr/bin/node /srv/my-app/server.js" required /></ElFormItem><ElRow :gutter="16" ><ElCol :xs="24" :sm="12" ><ElFormItem label="工作目录"><ElInput v-model="form.directory" aria-label="工作目录" placeholder="/srv/my-app" /></ElFormItem></ElCol><ElCol v-if="!account" :xs="24" :sm="12" ><ElFormItem label="运行用户"><ElInput v-model="form.user" aria-label="运行用户" placeholder="root" /></ElFormItem></ElCol><ElCol :xs="24" :sm="12" ><ElFormItem label="自动重启"><ElSelect  v-model="form.restart" aria-label="自动重启策略"><ElOption value="on-failure" :label="'失败时重启'" /><ElOption value="always" :label="'始终重启'" /><ElOption value="no" :label="'不自动重启'" /></ElSelect></ElFormItem></ElCol></ElRow><ElFormItem label="环境变量"><ElInput type="textarea" v-model="form.environment" aria-label="服务环境变量" class="code-editor"  :rows="3" placeholder="NODE_ENV=production&#10;PORT=3000" /></ElFormItem></template><ElFormItem label="完整配置" v-else><ElInput type="textarea" v-model="rawContent" aria-label="新单元配置" class="code-editor service-config-editor" required spellcheck="false" /></ElFormItem><ElSpace wrap class="service-create-options"><ElCheckbox v-model="startAfterCreate"  >创建后立即启动</ElCheckbox><ElCheckbox v-model="enableAfterCreate"  >{{account?'启用自启动':'开机启动'}}</ElCheckbox><ElCheckbox v-model="newFavorite"  >加入收藏</ElCheckbox></ElSpace></ElForm><template #header><ElRow justify="space-between" align="middle" ><span class="el-dialog__title">创建自己的服务</span><ElButton text  native-type="button" class="icon-button" @click="closeCreate">×</ElButton></ElRow></template><template #footer><ElButton  @click="closeCreate" native-type="button">取消</ElButton><ElButton form="systemdmanager-form-2" :loading="formOperation==='create'" :disabled="busy" native-type="submit" type="primary">{{startAfterCreate?'创建并启动':'创建服务'}}</ElButton></template></ElDialog></template>
