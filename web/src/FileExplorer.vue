<script setup lang="ts">
    import { ref, onMounted, onUnmounted, watch, nextTick } from "vue";
    import { type TabsInstance, ElTabs, ElTabPane, ElButton, ElMessageBox } from "element-plus";
    import { Plus, X, Minimize2 } from "lucide-vue-next";
    import FileBrowser from "./FileBrowser.vue";
    import { useTabDrag, useActiveTabVisibility, overTabBar, type DragPoint } from "./tab-drag";
    import { newFileTab, cleanPath, type FileTab, type FileClipboard } from "./file-browser-state";
    const props = defineProps<{
        active: boolean;
        nodeId: string;
        initialPath: string;
        api: (url: string, method?: string, body?: unknown, onFresh?: (result: any) => void) => Promise<any>;
        base: string;
        upload: (path: string, file: File) => Promise<void>;
        refreshSignal: number;
    }>();
    const emit = defineEmits<{ navigate: [path: string]; terminal: [path: string]; scan: [path: string]; scanJobs: []; activate: [] }>();
    let serial = 0;
    const id = () => `file-tab-${++serial}`,
        tabs = ref<FileTab[]>([newFileTab(id(), props.initialPath)]),
        current = ref(tabs.value[0].id),
        clipboard = ref<FileClipboard>(null),
        refresh = ref(0);
    type Floating = { tab: FileTab; x: number; y: number; minimized: boolean };
    const floating = ref<Floating[]>([]);
    const tabBar = ref<HTMLElement | null>(null), dockHover = ref(false);
    const beginDrag = useTabDrag();
    const libraryTabs = ref<TabsInstance>();
    useActiveTabVisibility(libraryTabs);
    const label = (tab: FileTab) => (tab.path === "/" ? "/" : tab.path.split("/").at(-1) || "/");
    function add(path?: string) {
        const tab = newFileTab(id(), path || tabs.value.find((tab) => tab.id === current.value)?.path || "/");
        tabs.value.push(tab);
        current.value = tab.id;
        emit("activate");
    }
    async function mayClose(tab: FileTab) {
        if (tab.busy) return false;
        if (tab.dirty) {
            try {
                await ElMessageBox.confirm("放弃此标签中未保存的文件修改？", "关闭标签", { confirmButtonText: "放弃修改", cancelButtonText: "取消", type: "warning" });
            } catch {
                return false;
            }
        }
        return true;
    }
    async function close(id: string) {
        const index = tabs.value.findIndex((tab) => tab.id === id),
            tab = tabs.value[index];
        if (!tab || !(await mayClose(tab))) return;
        tabs.value.splice(index, 1);
        if (!tabs.value.length) add("/");
        else if (current.value === id) current.value = tabs.value[Math.min(index, tabs.value.length - 1)].id;
    }
    function popout(id: string) {
        const index = tabs.value.findIndex((tab) => tab.id === id),
            tab = tabs.value[index];
        if (!tab || tab.busy || tab.dirty) return;
        tabs.value.splice(index, 1);
        floating.value.push({
            tab,
            x: Math.max(8, Math.min(100 + floating.value.length * 24, innerWidth - Math.min(1000, innerWidth * 0.94) - 8)),
            y: 80 + floating.value.length * 40,
            minimized: false,
        });
        if (!tabs.value.length) add("/");
        else if (current.value === id) current.value = tabs.value[Math.min(index, tabs.value.length - 1)].id;
        void nextTick(fit);
        return floating.value.at(-1);
    }
    async function closeFloating(window: Floating) {
        if (await mayClose(window.tab)) floating.value = floating.value.filter((item) => item !== window);
    }
    function dock(window: Floating) {
        if (window.tab.busy || window.tab.dirty) return;
        floating.value = floating.value.filter((item) => item !== window);
        tabs.value.push(window.tab);
        current.value = window.tab.id;
        emit("activate");
    }
    function front(window: Floating) {
        const index = floating.value.indexOf(window);
        if (index >= 0 && index !== floating.value.length - 1) {
            floating.value.splice(index, 1);
            floating.value.push(window);
        }
    }
    function position(window: Floating, point: DragPoint) {
        window.x = Math.max(8, Math.min(innerWidth - 120, point.x - 40));
        window.y = Math.max(8, Math.min(innerHeight - 48, point.y - 20));
    }
    function dragTab(event: PointerEvent, id: string) {
        const tab = tabs.value.find((item) => item.id === id);
        if (!tab || tab.busy || tab.dirty) return;
        let window: Floating | undefined;
        beginDrag(event, {
            move(point) {
                if (!window && !overTabBar(tabBar.value, point)) window = popout(id);
                if (window) { position(window, point); dockHover.value = overTabBar(tabBar.value, point); }
            },
            end(point) {
                if (window && overTabBar(tabBar.value, point)) dock(window);
                dockHover.value = false;
                fit();
            },
            cancel() { dockHover.value = false; fit(); },
        });
    }
    function drag(event: PointerEvent, window: Floating) {
        const left = window.x, top = window.y;
        beginDrag(event, {
            move(point, delta) {
                window.x = Math.max(8, Math.min(innerWidth - 120, left + delta.x));
                window.y = Math.max(8, Math.min(innerHeight - 48, top + delta.y));
                dockHover.value = !window.tab.busy && !window.tab.dirty && overTabBar(tabBar.value, point);
            },
            end(point) {
                if (overTabBar(tabBar.value, point)) dock(window);
                dockHover.value = false;
                fit();
            },
            cancel() { dockHover.value = false; fit(); },
        });
    }
    function fit() {
        for (const window of floating.value) {
            const box = document.querySelector('[data-file-window-id="' + window.tab.id + '"]')?.getBoundingClientRect();
            window.x = Math.max(8, Math.min(window.x, innerWidth - (box?.width || Math.min(1000, innerWidth * 0.94)) - 8));
            window.y = Math.max(8, Math.min(window.y, innerHeight - (box?.height || Math.min(650, innerHeight * 0.88)) - 8));
        }
    }
    watch(
        () => props.refreshSignal,
        () => {
            refresh.value++;
        },
    );
    watch(
        () => props.initialPath,
        (path) => {
            const tab = tabs.value.find((tab) => tab.id === current.value);
            if (!props.active || !tab || tab.path === cleanPath(path)) return;
            tab.path = cleanPath(path);
            tab.input = tab.path;
            tab.entries = [];
            tab.loaded = false;
            tab.selected = [];
            tab.anchor = "";
            tab.scroll = 0;
            tab.history = tab.history.slice(0, tab.historyIndex + 1);
            tab.history.push(tab.path);
            tab.historyIndex = tab.history.length - 1;
            refresh.value++;
        },
    );
    watch(current, () => {
        const tab = tabs.value.find((tab) => tab.id === current.value);
        if (tab) emit("navigate", tab.path);
    });
    onMounted(() => window.addEventListener("resize", fit));
    onUnmounted(() => {
        window.removeEventListener("resize", fit);

    });
</script>
<template><section v-show="active" class="file-explorer"><div ref="tabBar" class="file-tabs-bar" :class="{'tab-dock-target': dockHover}"><ElTabs ref="libraryTabs" v-model="current" class="file-tabs" type="card" closable @tab-remove="close(String($event))"><ElTabPane v-for="tab in tabs" :key="tab.id" :name="tab.id"><template #label><span class="draggable-tab-label" :title="tab.path" @pointerdown="dragTab($event, tab.id)">{{ label(tab) }}</span></template></ElTabPane></ElTabs>
            <div class="file-tab-actions">
                <ElButton text aria-label="新建文件标签" title="新建标签" @click="add()"><Plus :size="18" /></ElButton></div></div>
        <FileBrowser
            v-for="tab in tabs"
            :key="tab.id"
            :tab="tab"
            :active="active && current === tab.id"
            v-model:clipboard="clipboard"
            :api="api"
            :base="base"
            :upload="upload"
            :refresh-signal="refresh"
            @changed="refresh++"
            @navigate="emit('navigate', $event)"
            @new-tab="add"
            @terminal="emit('terminal', $event)"
            @scan="emit('scan', $event)"
            @scan-jobs="emit('scanJobs')" /></section>
    <Teleport to="body"><section
            v-for="window in floating"
            :key="window.tab.id"
            :data-file-window-id="window.tab.id"
            class="floating-file-window"
            :class="{ 'file-window-minimized': window.minimized }"
            :style="{ left: window.x + 'px', top: window.y + 'px' }"
            @pointerdown="front(window)"><div class="floating-file-heading" @pointerdown="drag($event, window)"><b :title="window.tab.path">{{ window.tab.path }}</b>
                <ElButton text :aria-label="window.minimized ? '展开文件窗口' : '最小化文件窗口'" @click="window.minimized = !window.minimized"><Minimize2 :size="16" /></ElButton>

                <ElButton text aria-label="关闭文件窗口" :disabled="window.tab.busy" @click="closeFloating(window)"><X :size="16" /></ElButton></div>
            <FileBrowser
                v-show="!window.minimized"
                :tab="window.tab"
                :active="!window.minimized"
                :api="api"
                :base="base"
                :upload="upload"
                v-model:clipboard="clipboard"
                :refresh-signal="refresh"
                @changed="refresh++"
                @new-tab="add"
                @terminal="emit('terminal', $event)"
                @scan="emit('scan', $event)"
                @scan-jobs="emit('scanJobs')" /></section></Teleport></template>
