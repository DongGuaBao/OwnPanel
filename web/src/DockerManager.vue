<script setup lang="ts">
import { operationMessage } from "./operation-message";
import { computed, onUnmounted, reactive, ref, watch } from 'vue';
import { ElAlert, ElTag } from 'element-plus';
import PanelTable from './PanelTable.vue';
import type { TableColumn } from './table-layout';

const props = defineProps<{nodeId: string; refreshSignal: number; api: (url: string, method?: string, body?: unknown) => Promise<any>}>();
const tabs = {containers: '容器', images: '镜像', networks: '网络', volumes: '数据卷'} as const;
type Resource = keyof typeof tabs;
type Row = Record<string, string>;
type Engine = {id: string; name: string};
const tab = ref<Resource>('containers'), engine = ref(''), engines = ref<Engine[]>([]);
const rows = ref<Row[]>([]), search = ref(''), project = ref(''), error = ref(''), loading = ref(false);
const modal = ref(''), saving = ref(false), modalError = ref(''), target = ref<Row>({}), logs = ref('');
const networks = ref<Row[]>([]), volumes = ref<Row[]>([]), images = ref<Row[]>([]);
const states: Record<string,string> = {running:'运行中', exited:'已退出', created:'已创建', paused:'已暂停', restarting:'重启中', dead:'异常', removing:'删除中'};
const actions: Record<string,string> = {start:'启动', stop:'停止', restart:'重启', remove:'删除'};
const form = reactive({name:'', image:'', network:'', restart:'no', ports:[] as {address:string; host:number; container:number; protocol:string}[], volumes:[] as {name:string; path:string; readOnly:boolean}[]});
const storageKey = 'ownpanel-docker-engine-'+props.nodeId;
let listVersion = 0, engineVersion = 0, dialogVersion = 0, disposed = false;
onUnmounted(()=>{disposed = true; ++listVersion; ++engineVersion; ++dialogVersion});
const endpoint = (path:string) => '/docker/'+path+'?engine='+encodeURIComponent(engine.value);
const engineName = computed(()=>engines.value.find(e=>e.id===engine.value)?.name);
const createLabel = computed(()=>({containers:'创建容器',images:'拉取镜像',networks:'创建网络',volumes:'创建数据卷'})[tab.value]);
const projects = computed(()=>[...new Set(rows.value.map(r=>r.project).filter(Boolean))].sort());
const filtered = computed(()=>rows.value.filter(row=>(!project.value || row.project===project.value) && Object.values(row).some(value=>value.toLowerCase().includes(search.value.trim().toLowerCase()))));
function imageRef(row:Row) {
    if (row.repository && row.repository!=='<none>') {
        if (row.tag && row.tag!=='<none>') return row.repository+':'+row.tag;
        if (row.digest && row.digest!=='<none>') return row.repository+'@'+row.digest;
    }
    return row.id;
}
function sizeBytes(value:string) {
    const m=/^([\d.]+)\s*([KMGTPE]?)B$/i.exec(value);
    if (!m) return null;
    const units = ['', 'K', 'M', 'G', 'T', 'P', 'E'];
    return Number(m[1]) * 1000 ** units.indexOf(m[2].toUpperCase());
}
const field = (key:string, label:string, minWidth:number):TableColumn=>({key,label,minWidth,value:r=>r[key],showOverflowTooltip:true});
const columns = computed<TableColumn[]>(()=>{
    const items:Record<Resource,TableColumn[]> = {
        containers:[field('name','容器名称',165),field('image','镜像',160),field('state','状态',95),field('ports','端口映射',145),field('project','编排项目',120)],
        images:[field('repository','仓库',200),field('tag','标签',95),field('id','镜像 ID',125),{...field('size','大小',95),value:r=>sizeBytes(r.size)}],
        networks:[field('name','网络名称',160),field('driver','驱动',100),field('scope','范围',100),field('id','网络 ID',125)],
        volumes:[field('name','数据卷名称',185),field('driver','驱动',100),field('scope','范围',100)]
    };
    return [...items[tab.value],{key:'actions',label:'操作',minWidth:tab.value==='containers'?235:150,sortable:false}];
});
function resetDialog() {++dialogVersion; modal.value=''; saving.value=false; modalError.value=''; logs.value=''}
async function loadRows() {
    const version=++listVersion;
    error.value='';
    if (!engine.value) {loading.value=false;return}
    loading.value=true;
    try {
        const data=await props.api(endpoint(tab.value));
        if (version===listVersion) {
            rows.value=data.rows;
            if (!projects.value.includes(project.value)) project.value='';
        }
    } catch(e) {if (version===listVersion) {error.value=(e as Error).message;rows.value=[]}}
    finally {if (version===listVersion) loading.value=false}
}
async function loadEngines() {
    const version=++engineVersion;
    try {
        const result=await props.api('/docker/engines');
        if (version!==engineVersion) return;
        engines.value=result.engines;
        const saved=engine.value || localStorage.getItem(storageKey);
        const next=engines.value.find(e=>e.id===saved)?.id || engines.value[0]?.id || '';
        if (next!==engine.value) engine.value=next;
        else await loadRows();
        if (!next) error.value='当前主机未发现 Docker 引擎，请检查 Docker CLI 和服务状态。';
    } catch(e) {
        if (version===engineVersion) {
            engine.value=''; engines.value=[]; rows.value=[];
            const message=(e as Error).message;
            error.value=/JSON|Unexpected|404/.test(message)?'当前主机未提供 Docker 管理接口，请更新 Agent。':message;
        }
    }
}
watch(()=>props.refreshSignal,loadEngines,{immediate:true});
watch([tab,engine],()=>{
    resetDialog(); rows.value=[]; search.value=''; project.value='';
    if(engine.value) {localStorage.setItem(storageKey,engine.value);void loadRows()}
    else {++listVersion;loading.value=false}
});
function begin(kind:string,row:Row={}) {
    resetDialog(); target.value=row; modal.value=kind;
    return dialogVersion;
}
async function createContainer(row?:Row) {
    const version=begin('container');
    Object.assign(form,{name:'',image:row?imageRef(row):'',network:'',restart:'no',ports:[],volumes:[]});
    images.value=[];networks.value=[];volumes.value=[];
    try {
        const [i,n,v]=await Promise.all(['images','networks','volumes'].map(resource=>props.api(endpoint(resource))));
        if(version===dialogVersion){images.value=i.rows;networks.value=n.rows;volumes.value=v.rows}
    } catch(e){if(version===dialogVersion) modalError.value=(e as Error).message}
}
function openCreate() {
    if(tab.value==='containers') {void createContainer();return}
    form.name='';begin('create');
}
function rowName(row:Row){return tab.value==='images'?imageRef(row):row.name}
function rowKey(row:Row){return tab.value==='images'?imageRef(row):row.id || row.name}
async function showLogs(row:Row) {
    const version=begin('logs',row);
    try {
        const r=await props.api(endpoint('containers/logs')+'&target='+encodeURIComponent(row.id));
        if(version===dialogVersion) logs.value=r.text || '暂无日志';
    } catch(e){if(version===dialogVersion)modalError.value=(e as Error).message}
}
async function save() {
    if(saving.value || disposed) return;
    const version=dialogVersion, kind=modal.value, resource=tab.value;
    let path=resource+'/'+kind, body:unknown;
    if(kind==='container'){
        path='containers/create';body={...form};
    } else if(kind==='create'){
        const pull=resource==='images';
        path=resource+'/'+(pull?'pull':'create');
        body=pull?{target:form.name.trim()}:{name:form.name.trim()};
    } else {
        const r=target.value;
        body={target:resource==='images'?imageRef(r):r.id || r.name};
    }
    saving.value=true;modalError.value='';
    try {
        const response=await props.api(endpoint(path),'POST',body);
        if(version!==dialogVersion) return;
        if(response.error) throw Error(response.error);
        const label=kind==='container'?form.name:kind==='create'?form.name:rowName(target.value);
        const result=kind==='container'?'已创建并启动':kind==='create'?(resource==='images'?'已拉取':'已创建'):'已'+actions[kind];
        operationMessage(label+' '+result);
        resetDialog();
        if(kind==='container' && tab.value!=='containers') tab.value='containers';
        else await loadRows();
    } catch(e){if(version===dialogVersion)modalError.value=(e as Error).message}
    finally{if(version===dialogVersion)saving.value=false}
}
const dialogTitle=computed(()=>modal.value==='logs'?'容器日志 · '+target.value.name:modal.value==='container'?'创建容器':modal.value==='create'?createLabel.value:(actions[modal.value]||'')+tabs[tab.value]);
</script>

<template>
    <ElTabs v-model="tab" aria-label="Docker 资源"><ElTabPane v-for="(label,id) in tabs" :key="id" :label="label" :name="id"/></ElTabs>
    <div class="toolbar">
        <ElSelect v-model="engine" aria-label="Docker 引擎" placeholder="选择引擎"><ElOption v-for="item in engines" :key="item.id" :value="item.id" :label="item.name"/></ElSelect>
        <ElInput v-model="search" clearable placeholder="搜索当前列表" aria-label="搜索当前列表"/>
        <ElSelect v-if="tab==='containers' && projects.length" v-model="project" clearable placeholder="全部编排项目" aria-label="编排项目"><ElOption v-for="name in projects" :key="name" :value="name" :label="name"/></ElSelect>
        <div class="spacer"/><ElButton type="primary" :disabled="!engine || !!error" @click="openCreate">{{createLabel}}</ElButton>
    </div>
    <ElAlert v-if="error" :title="error" type="error" :closable="false" show-icon/>
    <section class="table-card">
        <PanelTable :key="tab" :table-id="'docker-'+tab" :columns="columns" :rows="filtered" :row-key="rowKey">
            <template #state="{row}"><ElTag :type="row.state==='running'?'success':'info'" :title="row.status">{{states[row.state] || row.state}}</ElTag></template>
            <template #ports="{row}">{{row.ports || '—'}}</template>
            <template #project="{row}">{{row.project || '—'}}</template>
            <template #id="{row}"><span :title="row.id">{{row.id.replace(/^sha256:/,'').slice(0,12)}}</span></template>
            <template #size="{row}">{{row.size}}</template>
            <template #actions="{row}">
                <ElSpace wrap>
                    <template v-if="tab==='containers'">
                        <ElButton link type="primary" @click="showLogs(row)">日志</ElButton>
                        <ElButton v-if="['exited','created'].includes(row.state)" link type="primary" @click="begin('start',row)">启动</ElButton>
                        <template v-if="row.state==='running' || row.state==='restarting'">
                            <ElButton link @click="begin('stop',row)">停止</ElButton>
                            <ElButton link @click="begin('restart',row)">重启</ElButton>
                        </template>
                    </template>
                    <ElButton v-if="tab==='images'" link type="primary" @click="createContainer(row)">创建容器</ElButton>
                    <ElButton link type="danger" :disabled="tab==='containers' && !['dead','exited','created'].includes(row.state) || tab==='networks' && ['bridge','host','none'].includes(row.name)" @click="begin('remove',row)">删除</ElButton>
                </ElSpace>
            </template>
            <template #empty>{{loading?'正在读取…':search || project?'没有匹配项':'暂无'+tabs[tab]}}</template>
        </PanelTable>
    </section>
    <ElDialog class="docker-dialog" :model-value="!!modal" :title="dialogTitle" :width="modal==='container' || modal==='logs'?'min(780px, 94vw)':'min(460px, 94vw)'" append-to-body :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving" @update:model-value="!$event && resetDialog()">
        <ElAlert v-if="modalError" :title="modalError" type="error" :closable="false" show-icon/>
        <pre v-if="modal==='logs'" class="docker-logs">{{logs}}</pre>
        <ElForm v-else id="docker-action-form" :model="form" label-position="top" :disabled="saving" @submit.prevent="save">
            <template v-if="modal==='container'">
                <ElFormItem label="容器名称" required><ElInput v-model="form.name" required maxlength="255" placeholder="例如 web"/></ElFormItem>
                <ElFormItem label="本地镜像" required><ElSelect v-model="form.image" filterable placeholder="选择已拉取的镜像"><ElOption v-for="image in images" :key="imageRef(image)" :label="imageRef(image)" :value="imageRef(image)"/></ElSelect></ElFormItem>
                <ElRow :gutter="16"><ElCol :xs="24" :sm="12"><ElFormItem label="网络"><ElSelect v-model="form.network" clearable placeholder="默认网络"><ElOption v-for="n in networks" :key="n.id" :label="n.name" :value="n.name"/></ElSelect></ElFormItem></ElCol>
                    <ElCol :xs="24" :sm="12"><ElFormItem label="重启策略"><ElSelect v-model="form.restart"><ElOption v-for="(label,value) in {no:'不自动重启',always:'始终重启','unless-stopped':'手动停止后保持停止','on-failure':'失败时重启'}" :key="value" :value="value" :label="label"/></ElSelect></ElFormItem></ElCol>
                </ElRow>
                <ElFormItem label="端口映射"><ElButton @click="form.ports.push({address:'127.0.0.1',host:8080,container:80,protocol:'tcp'})">添加端口</ElButton></ElFormItem>
                <ElRow v-for="(port,i) in form.ports" :key="i" :gutter="8">
                    <ElCol :xs="12" :sm="6"><ElFormItem label="绑定地址"><ElInput v-model="port.address" required/></ElFormItem></ElCol>
                    <ElCol :xs="12" :sm="5"><ElFormItem label="主机端口"><ElInputNumber v-model="port.host" :min="1" :max="65535" :controls="false"/></ElFormItem></ElCol>
                    <ElCol :xs="12" :sm="5"><ElFormItem label="容器端口"><ElInputNumber v-model="port.container" :min="1" :max="65535" :controls="false"/></ElFormItem></ElCol>
                    <ElCol :xs="12" :sm="8"><ElFormItem label="协议"><ElSpace><ElSelect v-model="port.protocol" class="docker-protocol"><ElOption value="tcp"/><ElOption value="udp"/></ElSelect><ElButton link type="danger" @click="form.ports.splice(i,1)">移除</ElButton></ElSpace></ElFormItem></ElCol>
                </ElRow>
                <ElFormItem label="数据卷"><ElButton @click="form.volumes.push({name:'',path:'',readOnly:false})">挂载卷</ElButton></ElFormItem>
                <ElRow v-for="(v,i) in form.volumes" :key="i" :gutter="8">
                    <ElCol :xs="12" :sm="8"><ElFormItem label="卷名"><ElSelect v-model="v.name" filterable><ElOption v-for="vol in volumes" :key="vol.name" :value="vol.name" :label="vol.name"/></ElSelect></ElFormItem></ElCol>
                    <ElCol :xs="12" :sm="9"><ElFormItem label="容器内路径"><ElInput v-model="v.path" required placeholder="/data"/></ElFormItem></ElCol>
                    <ElCol :xs="24" :sm="7"><ElFormItem label="访问权限"><ElSpace><ElCheckbox v-model="v.readOnly">只读</ElCheckbox><ElButton link type="danger" @click="form.volumes.splice(i,1)">移除</ElButton></ElSpace></ElFormItem></ElCol>
                </ElRow>
            </template>
            <ElFormItem v-else-if="modal==='create'" :label="tab==='images'?'镜像名称':'名称'" required><ElInput v-model="form.name" required :placeholder="tab==='images'?'例如 nginx:stable':''"/></ElFormItem>
            <template v-else>
                <p>{{engineName}} · {{rowName(target)}}</p>
                <p v-if="modal==='remove' && tab==='volumes'">删除后，卷中的数据无法恢复。</p>
                <p v-else-if="modal==='remove' && tab==='containers'">删除后，容器可写层中的数据无法恢复。</p>
            </template>
        </ElForm>
        <template #footer>
            <ElButton :disabled="saving" @click="resetDialog">{{modal==='logs'?'关闭':'取消'}}</ElButton>
            <ElButton v-if="modal!=='logs'" form="docker-action-form" native-type="submit" :loading="saving" :disabled="modal==='container' && !form.image" :type="modal==='remove'?'danger':'primary'">{{modal==='container'?'创建并启动':modal==='create'?createLabel:actions[modal]}}</ElButton>
        </template>
    </ElDialog>
</template>
