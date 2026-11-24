<script setup lang="ts">
import { operationMessage } from "./operation-message";
import PanelTable from "./PanelTable.vue";
import type { TableColumn } from "./table-layout";
    import CategoryOrderDialog from "./CategoryOrderDialog.vue";
    import { readCategoryView, orderedCategoryTabs, saveCategoryView } from "./category-view";
    import { ElButton, ElInput, ElTag } from "element-plus";
    import { ref, computed, onMounted, onUnmounted, watch } from "vue";
    const props = defineProps<{
        api: (url: string, method?: string, body?: unknown, onFresh?: (value: any) => void) => Promise<any>;
        writable: boolean;
        refreshSignal?: number;
        categoryRefresh?: number;
        sites: any[];
        managedPrefix?: string;
        nodeId?: string;
    }>();
    const snapshot = ref<any>(null),
        loading = ref(true),
        busy = ref(false),
        error = ref(""),
        search = ref(""),
        instanceFilter = ref("");
    const instanceModal = ref(false);
    const fileModal = ref(false),
        instanceID = ref(""),
        filePath = ref(""),
        fileSearch = ref(""),
        content = ref(""),
        original = ref(""),
        revision = ref(""),
        versions = ref<any[]>([]);
    const notes = ref<Record<string, string>>({});
    type CategoryState = { categories: string[]; assignments: Record<string, string> };
    const categoryState = ref<CategoryState>({ categories: [], assignments: {} }),
        categoryScope = ref("uncategorized"),
        categoriesReady = ref(false);
    const viewKey = "ownpanel-nginx-category-view-" + (props.nodeId || "local"),
        savedView = readCategoryView(viewKey),
        tabOrder = ref(savedView.order),
        sortModal = ref(false);
    let categoryViewReady = false;
    const categoryTabs = computed(() =>
        orderedCategoryTabs(
            [...categoryState.value.categories.map((name) => ({ id: "category:" + name, label: name })), { id: "uncategorized", label: "未分类" }, { id: "all", label: "全部" }],
            tabOrder.value,
        ),
    );
    watch(categoryScope, (value) => {
        if (categoriesReady.value && value !== "all") saveCategoryView(viewKey, value, tabOrder.value);
    });
    async function saveCategoryOrder(ids: string[]) {
        await work(async () => {
            await mutateCategory({ action: "reorder", keys: ids.filter((id) => id.startsWith("category:")).map((id) => id.slice(9)) });
            if (disposed) return;
            tabOrder.value = ids;
            saveCategoryView(viewKey, categoryScope.value === "all" ? "" : categoryScope.value, ids);
            sortModal.value = false;
        });
    }
    const checkedSites = ref<string[]>([]),
        categoryTarget = ref("");
    const categoryDraft = ref<{ original: string; name: string } | null>(null),
        assignmentDraft = ref<{ keys: string[]; label: string } | null>(null);
    let categoryWriteVersion = 0;
    const categoryName = computed(() => (categoryScope.value.startsWith("category:") ? categoryScope.value.slice(9) : ""));
    function categoryFor(key: string) {
        return categoryState.value.assignments[key] || "";
    }
    function inCategory(key: string) {
        return categoriesReady.value && (categoryScope.value === "all" || (categoryScope.value === "uncategorized" ? !categoryFor(key) : categoryFor(key) === categoryName.value));
    }
    // Counts and category tabs use the same search/instance scope as the table.
    const allSiteKeys = computed(() => Array.from(new Set([...scopedManagedRows.value.map((s) => "managed:" + s.site.id), ...scopedRows.value.map(externalNoteKey)])) as string[]);
    function categoryCount(name: string) {
        return allSiteKeys.value.filter((key) => categoryFor(key) === name).length;
    }
    const visibleKeys = computed(() => Array.from(new Set([...managedRows.value.map((s) => "managed:" + s.site.id), ...rows.value.map(externalNoteKey)])) as string[]);
    const allChecked = computed(() => visibleKeys.value.length > 0 && visibleKeys.value.every((key) => checkedSites.value.includes(key)));
    function toggleSite(key: string) {
        checkedSites.value = checkedSites.value.includes(key) ? checkedSites.value.filter((item) => item !== key) : [...checkedSites.value, key];
    }
    function selectAll() {
        checkedSites.value = allChecked.value ? [] : [...visibleKeys.value];
    }
    function editCategory(key: string, label: string) {
        categoryTarget.value = categoryFor(key);
        assignmentDraft.value = { keys: [key], label };
        error.value = "";
    }
    function batchCategory() {
        categoryTarget.value = "";
        assignmentDraft.value = { keys: [...checkedSites.value], label: "已选 " + checkedSites.value.length + " 个站点" };
        error.value = "";
    }
    async function loadCategories() {
        const version = categoryWriteVersion;
        try {
            const state = await props.api("/nginx/categories");
            if (disposed || version !== categoryWriteVersion) return;
            categoryState.value = state;
            categoriesReady.value = true;
            if (!categoryViewReady) {
                categoryScope.value =
                    categoryTabs.value.some((tab) => tab.id === savedView.selected) && savedView.selected !== "all" ? savedView.selected : categoryTabs.value.find((tab) => tab.id !== "all")!.id;
                categoryViewReady = true;
            } else if (categoryName.value && !state.categories.includes(categoryName.value)) categoryScope.value = categoryTabs.value.find((tab) => tab.id !== "all")!.id;
        } catch (e) {
            if (!disposed) error.value = "分类读取失败：" + (e as Error).message;
        }
    }
    async function mutateCategory(body: unknown) {
        categoryWriteVersion++;
        const state = await props.api("/nginx/categories", "POST", body);
        if (!disposed) {
            categoryState.value = state;
            categoriesReady.value = true;
        }
    }
    async function saveCategoryName() {
        const draft = categoryDraft.value;
        if (!draft) return;
        await work(async () => {
            const name = draft.name.trim();
            if (!name) throw Error("请输入分类名称");
            await mutateCategory(draft.original ? { action: "rename", name: draft.original, target: name } : { action: "create", name });
            if (disposed) return;
            if (draft.original) tabOrder.value = tabOrder.value.map((id) => (id === "category:" + draft.original ? "category:" + name : id));
            categoryScope.value = "category:" + name;
            categoryDraft.value = null;
        });
    }
    async function deleteCategory() {
        const name = categoryName.value;
        if (!name || !confirm("删除分类“" + name + "”？其中站点将移至未分类，网站配置保持不变。")) return;
        await work(async () => {
            await mutateCategory({ action: "delete", name });
            if (!disposed) categoryScope.value = "uncategorized";
        });
    }
    async function saveAssignment() {
        const draft = assignmentDraft.value;
        if (!draft) return;
        await work(async () => {
            await mutateCategory({ action: "assign", keys: draft.keys, target: categoryTarget.value });
            if (!disposed) {
                assignmentDraft.value = null;
                checkedSites.value = [];
            }
        });
    }
    const noteDraft = ref<{ key: string; label: string; text: string; original: string } | null>(null);
    let noteWriteVersion = 0;
    function externalNoteKey(site: any) {
        return "external:" + JSON.stringify([site.instance, site.file, site.domains, site.listen]);
    }
    async function loadNotes() {
        const version = noteWriteVersion;
        try {
            const result = await props.api("/nginx/notes");
            if (!disposed && version === noteWriteVersion) notes.value = result;
        } catch (e) {
            if (!disposed) error.value = (e as Error).message;
        }
    }
    function editNote(key: string, label: string) {
        const text = notes.value[key] || "";
        noteDraft.value = { key, label, text, original: text };
        error.value = "";
    }
    function closeNote() {
        if (busy.value) return;
        if (noteDraft.value && noteDraft.value.text !== noteDraft.value.original && !confirm("放弃未保存的站点备注？")) return;
        noteDraft.value = null;
    }
    async function saveNote() {
        const draft = noteDraft.value;
        if (!draft || busy.value) return;
        await work(async () => {
            await props.api("/nginx/notes", "POST", { key: draft.key, note: draft.text });
            noteWriteVersion++;
            notes.value[draft.key] = draft.text;
            noteDraft.value = null;
        });
    }
    let disposed = false,
        fileGeneration = 0;
    const scopedRows = computed(() =>
        (snapshot.value?.sites || []).filter(
            (s: any) =>
                (!instanceFilter.value || s.instance === instanceFilter.value) &&
                [...s.domains, s.file, s.root, ...s.upstreams, notes.value[externalNoteKey(s)] || ""].join(" ").toLowerCase().includes(search.value.trim().toLowerCase()),
        ),
    );
    function managedInInstance(site: any) {
        if (!instanceFilter.value) return true;
        const selected = snapshot.value?.instances.find((i: any) => i.id === instanceFilter.value);
        const prefix = props.managedPrefix?.replace(/\/+$/, "");
        if (!selected || !prefix) return false;
        // Local Agent owns a dedicated main config; SSH sites are included by the
        // system Nginx, whose main config lives outside the managed prefix.
        return selected.config === prefix + "/nginx.conf" || selected.files?.some((file: any) => file.path === prefix + "/sites/" + site.site.id + "/server.conf");
    }
    const scopedManagedRows = computed(() =>
        props.sites.filter(
            (s) =>
                managedInInstance(s) &&
                [s.site.domains, props.managedPrefix ? props.managedPrefix + "/sites/" + s.site.id + "/server.conf" : "", notes.value["managed:" + s.site.id] || ""]
                    .join(" ")
                    .toLowerCase()
                    .includes(search.value.trim().toLowerCase()),
        ),
    );
    const rows = computed(() => scopedRows.value.filter((s: any) => inCategory(externalNoteKey(s))));
    const managedRows = computed(() => scopedManagedRows.value.filter((s) => inCategory("managed:" + s.site.id)));
    function clearFilters() {
        categoryScope.value = "all";
        instanceFilter.value = "";
        search.value = "";
    }
    watch(visibleKeys, (keys) => {
        const visible = new Set(keys);
        checkedSites.value = checkedSites.value.filter((key) => visible.has(key));
    });
    const instance = computed(() => snapshot.value?.instances.find((i: any) => i.id === instanceID.value));
    const files = computed(() => (instance.value?.files || []).filter((f: any) => f.path.toLowerCase().includes(fileSearch.value.toLowerCase())));

    function apply(value: any) {
        if (disposed) return;
        snapshot.value = value;
        if (instanceFilter.value && !value.instances.some((i: any) => i.id === instanceFilter.value)) instanceFilter.value = "";
        loading.value = false;
    }
    async function load(force = false) {
        try {
            apply(await props.api("/nginx/existing", force ? "REFRESH" : "GET", undefined, apply));
        } catch (e) {
            if (!disposed) {
                error.value = (e as Error).message;
                loading.value = false;
            }
        }
    }
    async function work(fn: () => Promise<void>) {
        busy.value = true;
        error.value = "";
        try {
            await fn();
        } catch (e) {
            if (!disposed) error.value = (e as Error).message;
        } finally {
            busy.value = false;
        }
    }
    async function readFile(path: string, check = true) {
        if (check && content.value !== original.value && !confirm("放弃当前文件尚未保存的修改？")) return;
        const turn = ++fileGeneration,
            id = instanceID.value;
        await work(async () => {
            const r = await props.api("/nginx/existing/file?" + new URLSearchParams({ instance: id, path }));
            if (disposed || turn !== fileGeneration) return;
            filePath.value = r.path;
            content.value = r.content;
            original.value = r.content;
            revision.value = r.revision;
            versions.value = r.versions;
        });
    }
    async function openFiles(site?: any) {
        instanceID.value = site?.instance || instanceFilter.value || snapshot.value?.instances[0]?.id || "";
        if (!instanceID.value) return;
        fileSearch.value = "";
        filePath.value = "";
        content.value = "";
        original.value = "";
        fileModal.value = true;
        void load();
        await readFile(site?.file || instance.value.config, false);
    }
    async function changeInstance() {
        filePath.value = "";
        content.value = "";
        original.value = "";
        await readFile(instance.value.config, false);
    }
    function closeFiles() {
        if (content.value !== original.value && !confirm("放弃当前文件尚未保存的修改？")) return;
        fileModal.value = false;
        fileGeneration++;
    }
    async function save() {
        const path = filePath.value,
            id = instanceID.value;
        await work(async () => {
            const r = await props.api("/nginx/existing/file", "POST", { instance: id, path, content: content.value, revision: revision.value });
            operationMessage("配置已保存，已重载 " + r.reloaded + " 个运行实例");
            original.value = content.value;
            revision.value = r.revision;
            await load();
            const saved = await props.api("/nginx/existing/file?" + new URLSearchParams({ instance: id, path }));
            if (filePath.value === path && instanceID.value === id) versions.value = saved.versions;
        });
    }
    async function restore(version: string) {
        if (!confirm("恢复这个版本保存前的文件内容？将检查配置并重载相关实例。")) return;
        await work(async () => {
            await props.api("/nginx/existing/restore", "POST", { instance: instanceID.value, path: filePath.value, revision: revision.value, version });
            const r = await props.api("/nginx/existing/file?" + new URLSearchParams({ instance: instanceID.value, path: filePath.value }));
            content.value = original.value = r.content;
            revision.value = r.revision;
            versions.value = r.versions;
            await load();
            operationMessage("历史配置已恢复并重新检查");
        });
    }
    const instanceAction = ref("");
    async function control(i: any, operation: string) {
        if (busy.value) return;
        instanceAction.value = i.id + operation;
        await work(async () => {
            await props.api("/nginx/existing/control", "POST", { instance: i.id, operation });
            operationMessage(i.label + (operation === "test" ? " 配置检查通过" : " 已重载"));
            if (operation !== "test") await load();
        });
        instanceAction.value = "";
    }
    watch(
        () => props.refreshSignal,
        () => {
            void load();
            if (!busy.value) void loadCategories();
        },
    );
    watch(() => props.categoryRefresh, loadCategories);
    onMounted(() => {
        void load();
        void loadNotes();
        void loadCategories();
    });
    onUnmounted(() => {
        disposed = true;
        fileGeneration++;
    });

const tableRows = computed(()=>[
 ...managedRows.value.map(item=>({key:'managed:'+item.site.id,managed:true,item})),
 ...rows.value.map((item:any)=>({key:externalNoteKey(item),managed:false,item})),
]);
const tableColumnsSites:TableColumn[]=[
 {key:'selection',label:'',width:52,fixed:'left',sortable:false,className:'file-select'},
 {key:'domain',label:'站点域名',minWidth:220,value:r=>r.managed?r.item.site.domains:r.item.domains.join(' ')},
 {key:'note',label:'备注',minWidth:140,value:r=>notes.value[r.key]||''},
 {key:'listen',label:'监听',minWidth:160,value:r=>r.managed?(r.item.site.httpEnabled!==false?r.item.site.port:(r.item.site.tlsPort||r.item.site.port)):r.item.listen.join(', ')},
 {key:'tls',label:'HTTPS',minWidth:150,value:r=>r.managed?!!r.item.site.tls:!!r.item.tls},
 {key:'actions',label:'操作',minWidth:440,fixed:'right',sortable:false},
];
</script>
<template><section class="card table-card existing-nginx"><div class="card-heading site-list-heading"><slot name="toolbar" :category="categoryName" />
            <div class="site-list-controls"><ElInput v-model="search" aria-label="搜索站点" placeholder="搜索域名或配置路径" clearable />
                <ElSelect v-if="snapshot?.instances.length > 1" v-model="instanceFilter" aria-label="Nginx 实例筛选" placeholder="全部实例"><ElOption value="" :label="'全部实例'" />
                    <ElOption v-for="i in snapshot?.instances || []" :key="i.id" :value="i.id" :label="i.label + ' · ' + i.config" /></ElSelect>
                <ElButton :disabled="busy || !snapshot?.instances.length" @click="instanceModal = true; load()">实例管理</ElButton>
                <ElButton :disabled="busy || !snapshot?.instances.length" @click="openFiles()">配置文件</ElButton>
                <ElButton
                    :disabled="busy"
                    @click="
                        work(async () => {
                            await load(true);
                            await loadCategories();
                        })
                    ">
                    重新扫描
                </ElButton></div></div>
        <div class="site-category-bar"><ElSpace wrap class="site-category-tabs" role="group" aria-label="网站分类筛选"><ElButton v-for="item in categoryTabs" :key="item.id" :type="(categoryScope === item.id )?'primary':undefined" plain :aria-pressed="categoryScope === item.id" @click="categoryScope = item.id">{{ item.label }}
                    <span>{{ item.id === "all" ? allSiteKeys.length : categoryCount(item.id === "uncategorized" ? "" : item.label) }}</span></ElButton></ElSpace>
            <ElSpace wrap class="site-category-actions"><ElButton
                    :disabled="busy || !categoriesReady"
                    @click="
                        error = '';
                        sortModal = true;
                    ">
                    分类排序
                </ElButton>
                <ElButton
                    :disabled="busy || !categoriesReady"
                    @click="
                        error = '';
                        categoryDraft = { original: '', name: '' };
                    ">
                    新增分类
                </ElButton>
                <ElButton
                    v-if="categoryName"
                    :disabled="busy"
                    @click="
                        error = '';
                        categoryDraft = { original: categoryName, name: categoryName };
                    ">
                    重命名
                </ElButton>
                <ElButton v-if="categoryName" :disabled="busy" @click="deleteCategory">删除分类</ElButton></ElSpace></div>
        <div v-if="checkedSites.length" class="site-category-batch"><b>已选 {{ checkedSites.length }} 项</b>
            <ElButton :disabled="busy || !categoriesReady" @click="batchCategory">批量改分类</ElButton>
            <ElButton :disabled="busy" @click="checkedSites = []">取消选择</ElButton></div>
        <p v-if="error && !fileModal && !instanceModal" class="banner warning">{{ error }}</p>
        <div class="rule-table-scroll"><PanelTable table-id="sites" :rows="tableRows" :columns="tableColumnsSites" row-key="key"><template #header-selection><ElCheckbox
                                aria-label="全选当前站点"
                                :model-value="allChecked"
                                :indeterminate="checkedSites.length > 0 && !allChecked"
                                :disabled="busy || !categoriesReady"
                                @change="selectAll" /></template><template #selection="{row:{item:s,managed}}"><slot v-if="managed" name="managed-cell" column="selection" :site="s" :notes="notes" :edit-note="editNote" :category-for="categoryFor" :edit-category="editCategory" :checked-sites="checkedSites" :toggle-site="toggleSite" :category-busy="busy || !categoriesReady"/><template v-else><ElCheckbox
                                :aria-label="'选择站点 ' + s.domains.join(' ')"
                                :model-value="checkedSites.includes(externalNoteKey(s))"
                                :disabled="busy || !categoriesReady"
                                @change="toggleSite(externalNoteKey(s))" /></template></template><template #domain="{row:{item:s,managed}}"><slot v-if="managed" name="managed-cell" column="domain" :site="s" :notes="notes" :edit-note="editNote" :category-for="categoryFor" :edit-category="editCategory" :checked-sites="checkedSites" :toggle-site="toggleSite" :category-busy="busy || !categoriesReady"/><template v-else><b data-external-site>{{ s.domains.join(" ") }}</b>
                            <small class="mono existing-path">{{ s.file }}:{{ s.line }}</small></template></template><template #note="{row:{item:s,managed}}"><slot v-if="managed" name="managed-cell" column="note" :site="s" :notes="notes" :edit-note="editNote" :category-for="categoryFor" :edit-category="editCategory" :checked-sites="checkedSites" :toggle-site="toggleSite" :category-busy="busy || !categoriesReady"/><template v-else><ElButton text  native-type="button" class="site-note" :title="notes[externalNoteKey(s)]" @click="editNote(externalNoteKey(s), s.domains.join(' '))">{{ notes[externalNoteKey(s)] || "添加备注" }}</ElButton></template></template><template #listen="{row:{item:s,managed}}"><slot v-if="managed" name="managed-cell" column="listen" :site="s" :notes="notes" :edit-note="editNote" :category-for="categoryFor" :edit-category="editCategory" :checked-sites="checkedSites" :toggle-site="toggleSite" :category-busy="busy || !categoriesReady"/><template v-else>{{ s.listen.join(", ") }}</template></template><template #tls="{row:{item:s,managed}}"><slot v-if="managed" name="managed-cell" column="tls" :site="s" :notes="notes" :edit-note="editNote" :category-for="categoryFor" :edit-category="editCategory" :checked-sites="checkedSites" :toggle-site="toggleSite" :category-busy="busy || !categoriesReady"/><template v-else>{{ s.tls ? "已启用" : "未启用" }}</template></template><template #actions="{row:{item:s,managed}}"><slot v-if="managed" name="managed-cell" column="actions" :site="s" :notes="notes" :edit-note="editNote" :category-for="categoryFor" :edit-category="editCategory" :checked-sites="checkedSites" :toggle-site="toggleSite" :category-busy="busy || !categoriesReady"/><template v-else><ElButton text type="primary" @click="openFiles(s)">编辑配置</ElButton></template></template><template #empty>{{ loading || !categoriesReady ? "正在读取站点…" : search || instanceFilter || categoryScope !== "all" ? "没有匹配的站点" : "暂无站点" }}
                            <ElButton v-if="!loading && (search || instanceFilter || categoryScope !== 'all')" text type="primary" @click="clearFilters">清除筛选</ElButton></template></PanelTable></div>
        <details v-if="snapshot?.warnings.length" class="existing-warnings"><summary>扫描提示（{{ snapshot.warnings.length }}）</summary>
            <pre>{{ snapshot.warnings.join("\n") }}</pre></details></section>
    <CategoryOrderDialog v-if="sortModal" :items="categoryTabs" :busy="busy" :error="error" @close="sortModal = false" @save="saveCategoryOrder" />
    <ElDialog v-model="instanceModal" :close-on-click-modal="false" :close-on-press-escape="!busy" :show-close="!busy" title="Nginx 实例管理" width="min(900px, 94vw)" append-to-body><p v-if="error" class="banner warning">{{ error }}</p>
        <div class="nginx-instance-list"><div v-for="i in snapshot?.instances || []" :key="i.id" class="toolbar"><div><b>{{ i.label }} · {{ i.running ? "运行中" : "未运行" }}</b>
                    <small class="mono existing-path">{{ i.config }}</small>
                    <small class="muted">{{ i.files.length }} 个配置文件 · {{ i.binary }}</small></div>
                <div class="spacer" />
                <ElButton :disabled="busy || !writable" :loading="instanceAction===i.id+'test'" @click="control(i, 'test')" native-type="button">检查此实例</ElButton>
                <ElButton :disabled="busy || !writable || !i.running" :loading="instanceAction===i.id+'reload'" @click="control(i, 'reload')" native-type="button">重载此实例</ElButton></div></div></ElDialog>
    <ElDialog v-if="categoryDraft" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(480px, 94vw)"><ElForm id="existingnginx-form-1" label-position="top" class="modal component-dialog-body" @submit.prevent="saveCategoryName">
            <ElFormItem label="分类名称"><ElInput v-model="categoryDraft.name" aria-label="网站分类名称" maxlength="64" :disabled="busy" placeholder="例如：业务网站、内部工具" autofocus /></ElFormItem>
            <p v-if="error" class="form-error">{{ error }}</p>
            </ElForm><template #header><ElRow justify="space-between" align="middle" ><span class="el-dialog__title">{{ categoryDraft.original ? "重命名分类" : "新增网站分类" }}</span>
                <ElButton text  native-type="button" class="icon-button" :disabled="busy" aria-label="关闭分类名称" @click="categoryDraft = null">×</ElButton></ElRow></template><template #footer><ElButton :disabled="busy" @click="categoryDraft = null">取消</ElButton>
                <ElButton form="existingnginx-form-1" type="primary" native-type="submit" :loading="busy">保存分类</ElButton></template></ElDialog>
    <ElDialog v-if="assignmentDraft" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(480px, 94vw)"><ElForm id="existingnginx-form-2" label-position="top" class="modal component-dialog-body" @submit.prevent="saveAssignment">
            <p>{{ assignmentDraft.label }}</p>
            <ElFormItem label="目标分类"><ElSelect v-model="categoryTarget" aria-label="网站目标分类" :disabled="busy"><ElOption value="" label="未分类" />
                    <ElOption v-for="name in categoryState.categories" :key="name" :value="name" :label="name" /></ElSelect></ElFormItem>
            <p v-if="error" class="form-error">{{ error }}</p>
            </ElForm><template #header><ElRow justify="space-between" align="middle" ><span class="el-dialog__title">修改网站分类</span>
                <ElButton text  native-type="button" class="icon-button" :disabled="busy" aria-label="关闭网站分类" @click="assignmentDraft = null">×</ElButton></ElRow></template><template #footer><ElButton :disabled="busy" @click="assignmentDraft = null">取消</ElButton>
                <ElButton form="existingnginx-form-2" type="primary" native-type="submit" :loading="busy">确认分类</ElButton></template></ElDialog>
    <ElDialog v-if="noteDraft" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(640px, 94vw)"><ElForm id="existingnginx-form-3" label-position="top" class="modal component-dialog-body" @submit.prevent="saveNote">
            <ElFormItem label="备注"><ElInput type="textarea" v-model="noteDraft.text" :rows="5" :disabled="busy" placeholder="例如：项目用途、负责人或维护提醒" /></ElFormItem>
            <p v-if="error" class="form-error">{{ error }}</p>
            </ElForm><template #header><ElRow justify="space-between" align="middle" ><ElSpace direction="vertical" alignment="start" :size="4"><span class="el-dialog__title">编辑站点备注</span>
                    <small>{{ noteDraft.label }}</small></ElSpace>
                <ElButton text  class="icon-button" native-type="button" aria-label="关闭备注" @click="closeNote">×</ElButton></ElRow></template><template #footer><ElButton :disabled="busy" @click="closeNote">取消</ElButton>
                <ElButton form="existingnginx-form-3" type="primary" native-type="submit" :loading="busy">保存备注</ElButton></template></ElDialog>
    <ElDialog v-if="fileModal" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(1120px, 96vw)"><section class="modal large existing-editor component-dialog-body">
            <p v-if="error" class="form-error">{{ error }}</p>
            <ElSelect v-model="instanceID" aria-label="配置所属实例" :disabled="busy || content !== original" @change="changeInstance"><ElOption v-for="i in snapshot?.instances || []" :value="i.id" :key="i.id" :label="i.label + ' · ' + i.config" /></ElSelect>
            <div class="existing-config-layout"><div class="existing-file-tree"><ElInput v-model="fileSearch" aria-label="筛选 Nginx 配置文件" placeholder="筛选完整路径" />
                    <ElButton v-for="f in files" :key="f.path" :title="f.path" :type="(filePath === f.path )?'primary':undefined" plain @click="readFile(f.path)" native-type="button">{{ f.path }}</ElButton></div>
                <div class="existing-file-content"><p class="config-path">{{ filePath || "选择配置文件" }}
                        <span v-if="content !== original" class="tag">未保存</span></p>
                    <ElInput type="textarea" v-model="content" aria-label="现有 Nginx 配置内容" class="code-editor" :disabled="busy || !filePath || !writable" spellcheck="false" />
                    </div></div>
            <details v-if="versions.length" class="existing-history"><summary>此文件的历史版本（{{ versions.length }}）</summary>
                <div v-for="v in versions" :key="v.id" class="toolbar"><span>{{ new Date(v.time).toLocaleString() }} · 保存前版本</span>
                    <div class="spacer" />
                    <ElButton :disabled="busy || !writable" @click="restore(v.id)" native-type="button">恢复</ElButton></div></details></section><template #header><ElRow justify="space-between" align="middle" ><ElSpace direction="vertical" alignment="start" :size="4"><span class="el-dialog__title">现有 Nginx 配置文件</span></ElSpace>
                <ElButton text  native-type="button" class="icon-button" aria-label="关闭现有配置" @click="closeFiles">×</ElButton></ElRow></template><template #footer><ElButton :disabled="busy" @click="closeFiles" native-type="button">关闭</ElButton>
                        <ElButton :disabled="busy || !writable || !revision || content === original" :loading="busy" @click="save" native-type="button" type="primary">检查、保存并重载</ElButton></template></ElDialog></template>
