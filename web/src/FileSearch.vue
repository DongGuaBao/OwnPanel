<script setup lang="ts">
import { ref, onUnmounted } from 'vue';
import { ElDialog, ElForm, ElFormItem, ElInput, ElSelect, ElOption, ElRow, ElCol, ElButton, ElAlert, ElSpace } from 'element-plus';
import PanelTable from './PanelTable.vue';
import type { TableColumn } from './table-layout';
const props = defineProps<{ path: string; api: (url: string) => Promise<any> }>();
const emit = defineEmits<{ close: []; locate: [path: string, directory: boolean]; newTab: [path: string] }>();
type Result = { path: string; name: string; directory: boolean; symlink: boolean; bytes: number | null; modifiedAt: number };
const query = ref(''), scope = ref('current'), rows = ref<Result[]>([]), busy = ref(false), searched = ref(false), error = ref(''), status = ref('');
let generation = 0, disposed = false;
onUnmounted(() => { disposed = true; generation++; });
const columns: TableColumn[] = [
    { key: 'name', label: '名称', value: row => row.name, minWidth: 180, showOverflowTooltip: true },
    { key: 'path', label: '路径', value: row => row.path, minWidth: 320, showOverflowTooltip: true },
    { key: 'type', label: '类型', value: row => row.directory ? '目录' : '文件', width: 90 },
    { key: 'size', label: '大小', value: row => row.bytes, width: 120 },
    { key: 'actions', label: '操作', sortable: false, width: 190 },
];
function formatSize(value: number | null) {
    if (value == null) return '—';
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++; }
    return (unit ? value.toFixed(1) : String(value)) + ' ' + units[unit];
}
async function search() {
    if (!query.value.trim()) return;
    const turn = ++generation;
    busy.value = true; error.value = ''; status.value = ''; searched.value = true; rows.value = [];
    try {
        const result = await props.api('/files/search?q=' + encodeURIComponent(query.value.trim()) + '&path=' + encodeURIComponent(scope.value === 'all' ? '/' : props.path));
        if (disposed || turn !== generation) return;
        if (!result.available) { error.value = '此主机的文件索引暂不可用'; return; }
        rows.value = result.results || [];
        status.value = [result.initializing ? '索引正在初始化，结果可能不完整' : result.stale ? '索引正在校验，结果可能已变化' : '', result.partial ? '搜索达到时间限制，结果不完整' : '', result.limited ? '仅显示前 200 条，请缩小搜索范围' : ''].filter(Boolean).join('；');
    } catch (e) { if (!disposed && turn === generation) error.value = (e as Error).message; }
    finally { if (!disposed && turn === generation) busy.value = false; }
}
function parent(path: string) { return path.slice(0, path.lastIndexOf('/')) || '/'; }
</script>
<template>
    <ElDialog :model-value="true" title="文件搜索" width="min(1120px, 96vw)" top="5vh" class="file-search-dialog" append-to-body @close="emit('close')">
        <ElForm label-position="top" @submit.prevent="search">
            <ElRow :gutter="12" align="bottom">
                <ElCol :xs="24" :sm="14"><ElFormItem label="文件名"><ElInput v-model="query" aria-label="搜索文件名" placeholder="文件名关键词" clearable :maxlength="256" autofocus /></ElFormItem></ElCol>
                <ElCol :xs="16" :sm="6"><ElFormItem label="范围"><ElSelect v-model="scope" aria-label="搜索范围"><ElOption label="当前目录及子目录" value="current" /><ElOption label="全盘" value="all" /></ElSelect></ElFormItem></ElCol>
                <ElCol :xs="8" :sm="4"><ElFormItem><ElButton native-type="submit" type="primary" :loading="busy" :disabled="!query.trim()">搜索</ElButton></ElFormItem></ElCol>
            </ElRow>
        </ElForm>
        <ElAlert v-if="error || status" :title="error || status" :type="error ? 'warning' : 'info'" :closable="false" />
        <div class="table-card file-search-results"><PanelTable table-id="file-search" :rows="rows" :columns="columns" row-key="path" @row-dblclick="row => emit('locate', row.path, row.directory)">
            <template #type="{row}">{{ row.directory ? '目录' : '文件' }}{{ row.symlink ? '链接' : '' }}</template>
            <template #size="{row}">{{ formatSize(row.bytes) }}</template>
            <template #actions="{row}"><ElSpace><ElButton link type="primary" @click="emit('locate', row.path, row.directory)">{{ row.directory ? '打开' : '定位' }}</ElButton><ElButton link @click="emit('newTab', row.directory ? row.path : parent(row.path))">新标签页</ElButton></ElSpace></template>
            <template #empty>{{ busy ? '正在搜索…' : searched ? '没有匹配结果' : '输入文件名开始搜索' }}</template>
        </PanelTable></div>
        <template #footer><ElSpace><span>{{ rows.length }} 项</span><ElButton @click="emit('close')">关闭</ElButton></ElSpace></template>
    </ElDialog>
</template>
