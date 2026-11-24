<script setup lang="ts">
import { operationMessage } from "./operation-message";
import PanelTable from "./PanelTable.vue";
import FileSearch from "./FileSearch.vue";
import type { TableColumn } from "./table-layout";
    import { ref, computed, onMounted, onUnmounted, watch, nextTick } from "vue";
    import { ElButton, ElInput, ElCheckbox, ElDialog, ElMenu, ElMenuItem, ElMessageBox, ElAlert, ElTooltip } from "element-plus";
    import { ArrowLeft, ArrowRight, ArrowUp, RefreshCw, FolderOpen, FileText, Upload, Plus } from "lucide-vue-next";
    import FilePermissions from "./FilePermissions.vue";
    import { cleanPath, fileSelection as applySelection, type FileTab, type FileEntry, type FileClipboard } from "./file-browser-state";
    const props = defineProps<{
        tab: FileTab;
        active: boolean;
        api: (url: string, method?: string, body?: unknown, onFresh?: (result: any) => void) => Promise<any>;
        base: string;
        upload: (path: string, file: File) => Promise<void>;
        refreshSignal: number;
    }>();
    const clipboard = defineModel<FileClipboard>("clipboard", { required: true });
    const emit = defineEmits<{ changed: []; navigate: [path: string]; newTab: [path: string]; terminal: [path: string]; scan: [path: string]; scanJobs: [] }>();
    const root = ref<HTMLElement | null>(null),
        tableArea = ref<HTMLElement | null>(null),
        uploadInput = ref<HTMLInputElement | null>(null),
        error = ref("");
    const searchOpen = ref(false), uploadProgress = ref(""), directoryReading = ref(false);
    async function locateSearchResult(path: string, directory: boolean) {
        searchOpen.value = false;
        await navigate(directory ? path : cleanPath(path + "/.."));
        if (!directory) {
            const name = path.slice(path.lastIndexOf("/") + 1);
            if (props.tab.entries.some(entry => entry.name === name)) { props.tab.selected = [name]; props.tab.focused = name; props.tab.anchor = name; }
            await nextTick();
            root.value?.querySelector(".file-row-selected")?.scrollIntoView({ block: "nearest" });
        }
    }
    const menu = ref<{ x: number; y: number; path: string } | null>(null),
        permission = ref<{ entry: FileEntry; path: string } | null>(null),
        editor = ref<{ path: string; content: string; original: string; isNew?: boolean } | null>(null);
    let disposed = false,
        readGeneration = 0;
    const selected = computed(() => props.tab.entries.filter((entry) => props.tab.selected.includes(entry.name))),
        single = computed(() => (selected.value.length === 1 ? selected.value[0] : null)),
        allChecked = computed(() => props.tab.entries.length > 0 && selected.value.length === props.tab.entries.length);
    const viewEntries = ref<FileEntry[]>([]);
    function fileSelection(tab: FileTab, name: string, ctrl: boolean, shift: boolean) {
        applySelection(tab, name, ctrl, shift, viewEntries.value.map((entry) => entry.name));
    }
    const scrollArea = () => tableArea.value?.querySelector<HTMLElement>('.el-table__body-wrapper .el-scrollbar__wrap') || tableArea.value;
    function saveScroll(event: Event) {
        const target = event.target as HTMLElement;
        if (target.closest('.el-table__body-wrapper')) props.tab.scroll = target.scrollTop;
    }
    const ownerUsers = ref(new Map<number, string>()), ownerGroups = ref(new Map<number, string>());
    let identitiesReady = false, identitiesLoading = false;
    function ownerText(entry: FileEntry) {
        return (ownerUsers.value.get(entry.uid) ?? String(entry.uid)) + ' / ' + (ownerGroups.value.get(entry.gid) ?? String(entry.gid));
    }
    async function loadIdentities(force = false) {
        if (identitiesLoading || (identitiesReady && !force)) return;
        identitiesLoading = true;
        try {
            const result = await props.api('/files/identities', force ? 'REFRESH' : 'GET');
            if (disposed) return;
            ownerUsers.value = new Map((result.users || []).map((user: {uid: number; name: string}) => [user.uid, user.name]));
            ownerGroups.value = new Map((result.groups || []).map((group: {gid: number; name: string}) => [group.gid, group.name]));
            identitiesReady = true;
        } catch { /* A missing account directory must not prevent browsing files. */ }
        finally { identitiesLoading = false; }
    }
    const join = (name: string) => cleanPath(props.tab.path + "/" + name);
    const size = (bytes: number) => {
        const units = ['B','KiB','MiB','GiB','TiB','PiB'];
        let value = bytes, unit = 0;
        while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++; }
        return (unit ? value.toFixed(1) : String(value)) + ' ' + units[unit];
    };
    type DirectorySize = { bytes: number | null; state: string; complete: boolean; updatedAt?: number };
    const directorySizes = ref<Record<string, DirectorySize>>({}), sizesAvailable = ref<boolean | null>(null);
    let sizeGeneration = 0, sizeLoading = false, sizePath = '', sizePoll: ReturnType<typeof setInterval>;
    function directorySizeValue(entry: FileEntry) {
        if (!entry.directory) return entry.size;
        const result = directorySizes.value[entry.name];
        return entry.symlink || !result || result.bytes == null ? null : result.bytes;
    }
    function directorySizeText(entry: FileEntry) {
        if (!entry.directory) return size(entry.size);
        if (entry.symlink) return '—';
        const result = directorySizes.value[entry.name];
        if (result?.bytes != null) return (!result.complete ? '≥ ' : result.state === 'ready' ? '' : '≈ ') + size(result.bytes);
        return result?.state === 'calculating' || sizesAvailable.value === null ? '计算中' : '—';
    }
    function directorySizeHint(entry: FileEntry) {
        if (entry.symlink) return '目录链接不递归统计';
        const result = directorySizes.value[entry.name];
        const states: Record<string,string> = { ready:'索引范围内的逻辑大小', stale:'缓存大小，正在校验', updating:'大小正在更新', calculating:'后台计算中', partial:'统计不完整', excluded:'不在索引范围', unavailable:'目录索引未启用或暂不可用' };
        return (states[result?.state || (sizesAvailable.value === null ? 'calculating' : 'unavailable')] || '后台计算中') +
            (result?.updatedAt ? ' · 更新于 ' + new Date(result.updatedAt).toLocaleString('zh-CN',{hour12:false}) : '');
    }
    async function loadDirectorySizes() {
        if (disposed || !props.active || sizeLoading || !props.tab.entries.some(entry => entry.directory && !entry.symlink)) return;
        const path = props.tab.path, turn = ++sizeGeneration;
        sizeLoading = true;
        if (sizePath !== path) { sizePath = path; directorySizes.value = {}; sizesAvailable.value = null; }
        try {
            let cursor = '';
            do {
                const result = await props.api('/files/directory-sizes?path=' + encodeURIComponent(path) + (cursor ? '&cursor=' + encodeURIComponent(cursor) : ''));
                if (disposed || turn !== sizeGeneration || props.tab.path !== path || !props.active) return;
                sizesAvailable.value = !!result.available;
                if (!result.available) {
                    directorySizes.value = Object.fromEntries(Object.entries(directorySizes.value).map(([name, value])=>[name,{...value,state:'stale'}]));
                    break;
                }
                directorySizes.value = {...directorySizes.value,...result.entries};
                const next = result.next || '';
                if (next === cursor) break;
                cursor = next;
            } while (cursor);
        } catch {
            if (!disposed && turn === sizeGeneration && props.tab.path === path) {
                sizesAvailable.value = false;
                directorySizes.value = Object.fromEntries(Object.entries(directorySizes.value).map(([name, value])=>[name,{...value,state:'stale'}]));
            }
        } finally { if (turn === sizeGeneration) sizeLoading = false; }
    }
    function selection(entry: FileEntry, event: MouseEvent) {
        fileSelection(props.tab, entry.name, event.ctrlKey || event.metaKey, event.shiftKey);
        root.value?.focus({ preventScroll: true });
    }
    function backgroundClick(event: MouseEvent) {
        const row = (event.target as Element).closest("tr");
        if ((event.target as Element).closest(".el-table__header-wrapper")) return;
        if (!row || row.querySelector(".empty")) props.tab.selected = [];
    }
    function checkbox(entry: FileEntry) {
        fileSelection(props.tab, entry.name, true, false);
    }
    async function load(force = false) {
        void loadIdentities(force);
        const path = props.tab.path,
            turn = ++readGeneration;
        directoryReading.value = true;
        const apply = (result: any) => {
            if (disposed || turn !== readGeneration || path !== props.tab.path) return;
            props.tab.entries = result.entries;
            props.tab.loaded = true;
            void loadDirectorySizes();
            props.tab.selected = props.tab.selected.filter((name) => result.entries.some((entry: FileEntry) => entry.name === name));
        };
        try {
            apply(await props.api("/files?path=" + encodeURIComponent(path), force ? "REFRESH" : "GET", undefined, apply));
            await nextTick();
            if (props.active && scrollArea()) scrollArea()!.scrollTop = props.tab.scroll;
        } catch (e) {
            if (!disposed && turn === readGeneration) error.value = (e as Error).message;
        } finally {
            if (turn === readGeneration) directoryReading.value = false;
        }
    }
    async function navigate(path: string, historyIndex?: number) {
        if (props.tab.busy) return;
        path = cleanPath(path);
        sizeGeneration++; sizeLoading = false; directorySizes.value = {}; sizesAvailable.value = null;
        menu.value = null;
        props.tab.path = path;
        props.tab.input = path;
        props.tab.selected = [];
        props.tab.anchor = "";
        props.tab.focused = "";
        props.tab.scroll = 0;
        props.tab.loaded = false;
        props.tab.entries = [];
        error.value = "";
        if (historyIndex === undefined) {
            props.tab.history = props.tab.history.slice(0, props.tab.historyIndex + 1);
            if (props.tab.history.at(-1) !== path) props.tab.history.push(path);
            props.tab.historyIndex = props.tab.history.length - 1;
        } else props.tab.historyIndex = historyIndex;
        emit("navigate", path);
        await load();
    }
    async function work(fn: () => Promise<void>) {
        if (props.tab.busy) return;
        props.tab.busy = true;
        error.value = "";
        try {
            await fn();
        } catch (e) {
            if (!disposed && e !== "cancel" && e !== "close") error.value = e instanceof Error ? e.message : String(e);
        } finally {
            if (!disposed) props.tab.busy = false;
        }
    }
    async function open(entry: FileEntry) {
        if (entry.directory) {
            await navigate(join(entry.name));
            return;
        }
        await work(async () => {
            const path = join(entry.name),
                result = await props.api("/files/content?path=" + encodeURIComponent(path));
            if (!disposed) editor.value = { path, content: result.content, original: result.content };
        });
    }
    async function save() {
        const draft = editor.value;
        if (!draft) return;
        await work(async () => {
            await props.api("/files/action", "POST", { action: "write", path: draft.path, content: draft.content });
            if (disposed) return;
            operationMessage(draft.path + " 已保存");
            emit("changed");
            editor.value = null;
            props.tab.dirty = false;
        });
    }
    async function closeEditor(done: () => void) {
        if (props.tab.busy) return;
        if (props.tab.dirty) {
            try {
                await ElMessageBox.confirm("放弃未保存的文件修改？", "关闭编辑器", { confirmButtonText: "放弃修改", cancelButtonText: "继续编辑", type: "warning" });
            } catch {
                return;
            }
        }
        editor.value = null;
        props.tab.dirty = false;
        done();
    }
    watch(
        () => editor.value?.content,
        () => {
            props.tab.dirty = !!editor.value && (!!editor.value.isNew || editor.value.content !== editor.value.original);
        },
    );
    function showMenu(event: MouseEvent, entry?: FileEntry) {
        if ((event.target as Element).closest(".el-table__header-wrapper")) return;
        event.preventDefault();
        event.stopPropagation();
        if (entry && !props.tab.selected.includes(entry.name)) fileSelection(props.tab, entry.name, false, false);
        if (!entry) props.tab.selected = [];
        menu.value = {
            x: Math.max(4, Math.min(event.clientX, innerWidth - 228)),
            y: Math.max(4, Math.min(event.clientY, innerHeight - 480)),
            path: entry?.directory ? join(entry.name) : props.tab.path,
        };
    }
    function dismiss(event: PointerEvent) {
        if (!(event.target as Element)?.closest(".file-action-menu")) menu.value = null;
    }
    async function ask(title: string, value = "") {
        const result = await ElMessageBox.prompt("", title, { inputValue: value, confirmButtonText: "确定", cancelButtonText: "取消", inputValidator: (text) => !!text?.trim() || "请输入名称" });
        return result.value.trim();
    }
    function stage(action: "copy" | "move") {
        if (!selected.value.length) return;
        clipboard.value = { action, items: selected.value.map((entry) => ({ name: entry.name, path: join(entry.name) })) };
    }
    async function paste() {
        const value = clipboard.value;
        if (!value) return;
        await work(async () => {
            const destination = props.tab.path,
                failed: typeof value.items = [];
            for (const entry of value.items) {
                if (disposed) return;
                try {
                    await props.api("/files/action", "POST", { action: value.action, path: entry.path, target: cleanPath(destination + "/" + entry.name) });
                } catch (e) {
                    failed.push(entry);
                    error.value += (error.value ? "\n" : "") + entry.name + "：" + (e as Error).message;
                }
            }
            if (disposed) return;
            if (value.action === "move" && clipboard.value === value) clipboard.value = failed.length ? { ...value, items: failed } : null;
            if (!failed.length) operationMessage((value.action==="move"?"已移动 ":"已复制 ")+value.items.length+" 项");
            emit("changed");
        });
    }
    async function command(action: string) {
        const context = menu.value;
        menu.value = null;
        if (props.tab.busy) return;
        if (action === "copy" || action === "move") {
            stage(action);
            return;
        }
        if (action === "paste") {
            await paste();
            return;
        }
        if (action === "new-tab") {
            emit("newTab", context?.path || props.tab.path);
            return;
        }
        if (action === "terminal") {
            emit("terminal", context?.path || props.tab.path);
            return;
        }
        if (action === "scan") {
            emit("scan", context?.path || props.tab.path);
            return;
        }
        if (action === "scan-jobs") {
            emit("scanJobs");
            return;
        }
        if (action === "permissions" && single.value) {
            permission.value = { entry: { ...single.value }, path: join(single.value.name) };
            return;
        }
        if (action === "download" && single.value && !single.value.directory) {
            window.open(props.base + "/files/content?download=1&path=" + encodeURIComponent(join(single.value.name)), "_blank", "noopener");
            return;
        }
        await work(async () => {
            if (action === "new") {
                const name = await ask("新文件名称");
                if (!disposed) editor.value = { path: join(name), content: "", original: "", isNew: true };
                return;
            }
            if (action === "mkdir") {
                const name = await ask("新文件夹名称");
                if (disposed) return;
                await props.api("/files/action", "POST", { action: "mkdir", path: join(name) });
            } else if (action === "rename" && single.value) {
                const entry = single.value,
                    target = await ask("重命名", entry.name);
                if (disposed) return;
                await props.api("/files/action", "POST", { action: "rename", path: join(entry.name), target: target.startsWith("/") ? cleanPath(target) : join(target) });
            } else if (action === "extract" && single.value) {
                const entry = single.value,
                    target = await ask("解压到目录", entry.name.replace(/\.zip$/i, ""));
                if (disposed) return;
                await props.api("/files/action", "POST", { action: "extract", path: join(entry.name), target: join(target) });
            } else if (["delete", "remove", "compress"].includes(action) && selected.value.length) {
                const items = [...selected.value];
                if (action !== "compress")
                    await ElMessageBox.confirm(
                        action === "remove" ? "永久删除选中的 " + items.length + " 项？无法从回收站恢复。" : "将选中的 " + items.length + " 项移入回收站？",
                        action === "remove" ? "永久删除" : "删除",
                        { confirmButtonText: "删除", cancelButtonText: "取消", type: "warning" },
                    );
                const failures: string[] = [];
                for (const entry of items) {
                    if (disposed) return;
                    try {
                        await props.api("/files/action", "POST", { action, path: join(entry.name) });
                    } catch (e) {
                        failures.push(entry.name + "：" + (e as Error).message);
                    }
                }
                if (failures.length) error.value = failures.join("\n");
            } else return;
            if (disposed) return;
            emit("changed");
        });
    }
    async function uploadChanged(event: Event) {
        const input = event.target as HTMLInputElement;
        const files = Array.from(input.files || []),
            directory = props.tab.path;
        await work(async () => {
            let uploaded = 0;
            try {
                for (const file of files) {
                    if (disposed) return;
                    uploadProgress.value = "上传 " + (uploaded+1) + "/" + files.length;
                    await props.upload(directory, file);
                    uploaded++;
                }
            } finally {
                uploadProgress.value = "";
                if (uploaded && !disposed) emit("changed");
            }
            if (disposed) return;
            if (files.length) operationMessage("已上传 "+files.length+" 个文件");
        });
        input.value = "";
    }
    function keydown(event: KeyboardEvent) {
        if (!props.active || props.tab.busy || (event.target as Element).closest("input,textarea,[contenteditable=true]")) return;
        const ctrl = event.ctrlKey || event.metaKey,
            key = event.key.toLowerCase();
        if (event.key === "Escape") {
            menu.value = null;
            return;
        }
        if (ctrl && ["a", "c", "x", "v"].includes(key)) {
            event.preventDefault();
            if (key === "a") props.tab.selected = props.tab.entries.map((entry) => entry.name);
            else if (key === "c") stage("copy");
            else if (key === "x") stage("move");
            else void paste();
            return;
        }
        if (event.key === "Delete") {
            event.preventDefault();
            void command(event.shiftKey ? "remove" : "delete");
            return;
        }
        if (event.key === "F2") {
            event.preventDefault();
            void command("rename");
            return;
        }
        if (event.key === "Enter") {
            const entry = props.tab.entries.find((entry) => entry.name === props.tab.focused) || single.value;
            if (entry) {
                event.preventDefault();
                void open(entry);
            }
            return;
        }
        if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
            event.preventDefault();
            const entries = viewEntries.value;
            if (!entries.length) return;
            const current = entries.findIndex((entry) => entry.name === props.tab.focused),
                index = event.key === "Home" ? 0 : event.key === "End" ? entries.length - 1 : Math.max(0, Math.min(entries.length - 1, current + (event.key === "ArrowDown" ? 1 : -1))),
                entry = entries[index];
            if (ctrl && !event.shiftKey) props.tab.focused = entry.name;
            else fileSelection(props.tab, entry.name, ctrl, event.shiftKey);
            void nextTick(() => tableArea.value?.querySelector(".file-row-focused")?.scrollIntoView({ block: "nearest" }));
        }
    }
    watch(() => props.tab.path, () => {
        sizeGeneration++; sizeLoading = false; directorySizes.value = {}; sizesAvailable.value = null; sizePath = '';
    }, { flush: 'sync' });
    watch(
        () => props.active,
        (value) => {
            if (value) void load();
            else { menu.value = null; sizeGeneration++; sizeLoading = false; }
        },
    );
    watch(
        () => props.refreshSignal,
        () => {
            if (props.active) void load();
        },
    );
    onMounted(() => {
        document.addEventListener("pointerdown", dismiss, true);
        if (props.active) void load();
        sizePoll = setInterval(() => { if (document.visibilityState !== 'hidden') void loadDirectorySizes(); }, 4000);
    });
    onUnmounted(() => {
        disposed = true;
        readGeneration++; sizeGeneration++;
        clearInterval(sizePoll);
        document.removeEventListener("pointerdown", dismiss, true);
    });
const tableColumnsFiles: TableColumn[] = [
    {key:"selection",label:"",sortable:false,fixed:'left',width:52,className:'file-select'},
    {key:"name",label:"名称",value:(entry)=>entry.name,minWidth:220},
    {key:"size",label:"大小",value:(entry)=>directorySizeValue(entry),minWidth:150},
    {key:"mode",label:"权限",value:(entry)=>parseInt(entry.mode,8),minWidth:140},
    {key:"owner",label:"所有者 / 用户组",value:(entry)=>ownerText(entry),minWidth:190},
    {key:"modified",label:"修改时间",value:(entry)=>Date.parse(entry.modified),minWidth:190}
];
</script>
<template><section v-show="active" ref="root" class="file-browser" tabindex="0" @keydown="keydown" @contextmenu="showMenu($event)"><ElAlert v-if="error" :title="error" type="error" :closable="true" @close="error = ''" />
        <div class="file-browser-toolbar"><ElButton aria-label="后退" title="后退" :disabled="tab.historyIndex === 0 || tab.busy" @click="navigate(tab.history[tab.historyIndex - 1], tab.historyIndex - 1)"><ArrowLeft :size="16" /></ElButton>
            <ElButton aria-label="前进" title="前进" :disabled="tab.historyIndex === tab.history.length - 1 || tab.busy" @click="navigate(tab.history[tab.historyIndex + 1], tab.historyIndex + 1)"><ArrowRight :size="16" /></ElButton>
            <ElButton aria-label="上级目录" title="上级目录" :disabled="tab.path === '/' || tab.busy" @click="navigate(cleanPath(tab.path + '/..'))"><ArrowUp :size="16" /></ElButton>
            <ElInput v-model="tab.input" aria-label="文件路径" :disabled="tab.busy" @keydown.enter="navigate(tab.input)"><template #prefix><FolderOpen :size="15" /></template></ElInput>
            <ElButton @click="searchOpen = true">搜索文件</ElButton>
            <ElButton aria-label="刷新目录" title="刷新目录" :loading="directoryReading" :disabled="tab.busy" @click="load(true)"><RefreshCw :size="16" /></ElButton>
            <ElButton :disabled="tab.busy" @click="command('mkdir')"><Plus :size="15" />
                文件夹
            </ElButton>
            <ElButton type="primary" :loading="!!uploadProgress" :disabled="tab.busy" @click="uploadInput?.click()"><Upload :size="15" />
                {{ uploadProgress || "上传" }}
            </ElButton>
            <input ref="uploadInput" type="file" hidden multiple @change="uploadChanged" /></div>
        <div ref="tableArea" class="card table-card file-browser-table" @scroll.capture="saveScroll" @click="backgroundClick"><PanelTable table-id="files" :rows="tab.entries" :columns="tableColumnsFiles" row-key="name" @row-click="(entry,_column,$event)=>selection(entry, $event)" @row-dblclick="(entry,_column,$event)=>open(entry)" @row-contextmenu="(entry,_column,$event)=>showMenu($event, entry)" :group-by="(entry)=>entry.directory?0:1" :row-class-name="(entry)=>({'file-row-selected':tab.selected.includes(entry.name),'file-row-focused':tab.focused===entry.name})" @view-order-change="viewEntries=$event"><template #header-selection><ElCheckbox
                                aria-label="全选文件"
                                :disabled="tab.busy"
                                :model-value="allChecked"
                                :indeterminate="selected.length > 0 && !allChecked"
                                @change="tab.selected = allChecked ? [] : tab.entries.map((entry) => entry.name)" /></template><template #selection="{row:entry}"><ElCheckbox :aria-label="'选择 ' + entry.name" :model-value="tab.selected.includes(entry.name)" :disabled="tab.busy" @click.stop @dblclick.stop @change="checkbox(entry)" /></template><template #name="{row:entry}"><span class="file-entry-name"><FolderOpen v-if="entry.directory" :size="18" class="folder-icon" />
                                <FileText v-else :size="18" />
                                {{ entry.name }}
                                <small v-if="entry.symlink">链接</small></span></template><template #size="{row:entry}"><ElTooltip v-if="entry.directory" :enterable="false" :content="directorySizeHint(entry)" placement="top"><span>{{ directorySizeText(entry) }}</span></ElTooltip><template v-else>{{ size(entry.size) }}</template></template><template #mode="{row:entry}">{{ entry.mode }}</template><template #owner="{row:entry}"><ElTooltip :enterable="false" :content="'UID: '+entry.uid+' · GID: '+entry.gid" placement="top"><span>{{ ownerText(entry) }}</span></ElTooltip></template><template #modified="{row:entry}">{{ new Date(entry.modified).toLocaleString("zh-CN", { hour12: false }) }}</template><template #empty>{{ tab.loaded ? "此目录为空" : error ? "无法读取目录" : "正在读取目录…" }}</template></PanelTable></div>
        <div class="file-browser-status">{{ tab.entries.length }} 项
            <span v-if="selected.length">· 已选 {{ selected.length }} 项</span></div></section>
    <Teleport to="body"><div v-if="menu && active" class="file-action-menu" :style="{ left: menu.x + 'px', top: menu.y + 'px', maxHeight: 'calc(100dvh - ' + (menu.y + 4) + 'px)' }" @contextmenu.prevent><ElMenu @select="command"><ElMenuItem index="new-tab">在新标签页打开</ElMenuItem>
                <ElMenuItem index="terminal">在此处打开终端</ElMenuItem>
                <ElMenuItem index="copy" :aria-disabled="!selected.length || tab.busy" :disabled="!selected.length || tab.busy">复制</ElMenuItem>
                <ElMenuItem index="move" :aria-disabled="!selected.length || tab.busy" :disabled="!selected.length || tab.busy">剪切</ElMenuItem>
                <ElMenuItem index="paste" :aria-disabled="!clipboard || tab.busy" :disabled="!clipboard || tab.busy">粘贴</ElMenuItem>
                <ElMenuItem index="rename" :aria-disabled="!single || tab.busy" :disabled="!single || tab.busy">重命名</ElMenuItem>
                <ElMenuItem index="permissions" :aria-disabled="!single || tab.busy" :disabled="!single || tab.busy">权限</ElMenuItem>
                <ElMenuItem index="compress" :aria-disabled="!selected.length || tab.busy" :disabled="!selected.length || tab.busy">压缩 ZIP</ElMenuItem>
                <ElMenuItem index="extract" :aria-disabled="!single || tab.busy" :disabled="!single || tab.busy">解压 ZIP</ElMenuItem>
                <ElMenuItem index="download" :aria-disabled="!single || single.directory" :disabled="!single || single.directory">下载</ElMenuItem>
                <ElMenuItem index="delete" :aria-disabled="!selected.length || tab.busy" :disabled="!selected.length || tab.busy">删除</ElMenuItem>
                <ElMenuItem index="remove" :aria-disabled="!selected.length || tab.busy" :disabled="!selected.length || tab.busy">永久删除</ElMenuItem>
                <ElMenuItem index="new">新文件</ElMenuItem>
                <ElMenuItem index="mkdir">新建文件夹</ElMenuItem>
                <ElMenuItem index="scan">空间扫描</ElMenuItem>
                <ElMenuItem index="scan-jobs">扫描记录</ElMenuItem></ElMenu></div></Teleport>
    <FileSearch v-if="searchOpen" :path="tab.path" :api="api" @close="searchOpen = false" @locate="locateSearchResult" @new-tab="path => { searchOpen = false; emit('newTab', path); }" />
    <FilePermissions
        v-if="permission"
        :entry="permission.entry"
        :path="permission.path"
        :api="api"
        @close="permission = null"
        @saved="
            permission = null;
            emit('changed');
            load();
        " />
    <ElDialog v-if="editor" :model-value="true" :title="editor.path" width="min(1120px, 96vw)" class="file-content-dialog" append-to-body :close-on-click-modal="false" :before-close="closeEditor"><ElInput v-model="editor.content" type="textarea" class="full-editor code-editor" aria-label="文件内容" spellcheck="false" :disabled="tab.busy" />
        <template #footer><ElButton :disabled="tab.busy" @click="closeEditor(() => {})">关闭</ElButton>
            <ElButton type="primary" :loading="tab.busy" @click="save">保存文件</ElButton></template></ElDialog></template>
