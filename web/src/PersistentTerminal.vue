<script setup lang="ts">
    import { type TabsInstance, ElTabs, ElTabPane, ElButton, ElMessageBox } from "element-plus";
    import { useActiveTabVisibility } from "./tab-drag";
    import { Plus } from "lucide-vue-next";
    import { ref, onMounted, onUnmounted, nextTick, watch } from "vue";
    import { Terminal } from "@xterm/xterm";
    import { FitAddon } from "@xterm/addon-fit";
    import "@xterm/xterm/css/xterm.css";
    const props = defineProps<{
        api: (url: string, method?: string, body?: unknown) => Promise<any>;
        base: string;
        nodeId: string;
        identity: string;
        storageScope?: string;
        excludedId?: string;
        dockTarget?: boolean;
    }>();
    const emit = defineEmits<{ selected: [id: string]; tabDrag: [id: string, event: PointerEvent] }>();
    const libraryTabs = ref<TabsInstance>();
    useActiveTabVisibility(libraryTabs);
    const sessions = ref<any[]>([]),
        current = ref(""),
        connected = ref(false),
        busy = ref(false),
        error = ref(""),
        element = ref<HTMLDivElement | null>(null),
        name = ref("");
    let term: Terminal | null = null,
        fit: FitAddon | null = null,
        ws: WebSocket | null = null,
        observer: ResizeObserver | null = null,
        retry: ReturnType<typeof setTimeout> | undefined,
        poll: ReturnType<typeof setInterval>,
        disposed = false,
        generation = 0,
        attempt = 0;
    const storage = () => `ownpanel-terminal-${props.nodeId}${props.storageScope ? "-" + props.storageScope : ""}`;
    const titlesChanged = () => { void load().catch(() => {}); };
    async function renameSession(session: any) {
        try {
            const { value } = await ElMessageBox.prompt("", "重命名终端", {
                appendTo: element.value?.closest<HTMLElement>(".floating-terminal") || document.body,
                inputValue: session.name,
                inputPlaceholder: "终端名称",
                confirmButtonText: "保存",
                cancelButtonText: "取消",
                inputValidator: (value: string) => !!value.trim() && Array.from(value.trim()).length <= 60 || "请输入 1–60 个字符",
                closeOnClickModal: false,
            });
            if (disposed) return;
            const renamed = await props.api("/terminals/" + encodeURIComponent(session.id), "PATCH", { name: value.trim() });
            session.name = renamed.name;
            window.dispatchEvent(new Event("ownpanel-terminal-titles"));
        } catch (e) {
            if (e !== "cancel" && e !== "close") error.value = (e as Error).message;
        }
    }
    function available() {
        return sessions.value.filter((session) => session.id !== props.excludedId);
    }
    function detach() {
        generation++;
        clearTimeout(retry);
        observer?.disconnect();
        observer = null;
        if (ws) {
            ws.onclose = null;
            ws.close();
            ws = null;
        }
        term?.dispose();
        term = null;
        fit = null;
        connected.value = false;
    }
    const pendingCommands = new Map<string, string>();
    async function load() {
        const result = await props.api("/terminals");
        if (!disposed) sessions.value = result.sessions;
    }
    async function attach(id: string) {
        if (id === props.excludedId || (current.value === id && term)) return;
        detach();
        current.value = id;
        emit("selected", id);
        localStorage.setItem(storage(), id);
        const turn = generation;
        await nextTick();
        if (!element.value || disposed || turn !== generation) return;
        term = new Terminal({
            fontSize: 14,
            fontFamily: "Cascadia Code, Consolas, monospace",
            cursorBlink: true,
            theme: {
                background: "#ffffff",
                foreground: "#243746",
                cursor: "#16675f",
                cursorAccent: "#ffffff",
                selectionBackground: "#cce7e1",
                black: "#253746",
                red: "#b42318",
                green: "#167344",
                yellow: "#8c6400",
                blue: "#2454a6",
                magenta: "#8b368c",
                cyan: "#087d8c",
                white: "#607080",
                brightBlack: "#687887",
                brightRed: "#cc342a",
                brightGreen: "#208454",
                brightYellow: "#987000",
                brightBlue: "#3568bf",
                brightMagenta: "#a24aa0",
                brightCyan: "#148d9a",
                brightWhite: "#243746",
            },
            scrollback: 10000,
        });
        fit = new FitAddon();
        term.loadAddon(fit);
        term.open(element.value);
        fit.fit();
        term.onData((data) => {
            if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "input", data }));
        });
        observer = new ResizeObserver(() => {
            if (disposed || turn !== generation || !element.value?.getClientRects().length) return;
            fit?.fit();
            if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", cols: term?.cols, rows: term?.rows }));
        });
        observer.observe(element.value);
        try {
            const { ticket } = await props.api("/terminal/ticket", "POST");
            if (turn !== generation || disposed) return;
            const socket = new WebSocket(
                `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}${props.base}/terminal?ticket=${encodeURIComponent(ticket)}&session=${encodeURIComponent(id)}`,
            );
            ws = socket;
            socket.binaryType = "arraybuffer";
            socket.onopen = () => {
                if (turn !== generation) return;
                connected.value = true;
                error.value = "";
                attempt = 0;
                socket.send(JSON.stringify({ type: "resize", cols: term?.cols, rows: term?.rows }));
                const command = pendingCommands.get(id);
                if (command) {
                    socket.send(JSON.stringify({ type: "input", data: command }));
                    pendingCommands.delete(id);
                }
                term?.focus();
            };
            socket.onmessage = (e) => {
                if (turn === generation) term?.write(typeof e.data === "string" ? e.data : new Uint8Array(e.data));
            };
            socket.onclose = () => {
                if (turn !== generation || disposed) return;
                connected.value = false;
                term?.writeln("\r\n\x1b[90m[浏览器已断开，正在检查会话…]\x1b[0m");
                retry = setTimeout(
                    async () => {
                        try {
                            await load();
                            if (turn !== generation || disposed) return;
                            const s = sessions.value.find((s) => s.id === id);
                            if (s && !s.ended) {
                                await attach(id);
                            } else term?.writeln("\r\n[进程已结束]");
                        } catch (e) {
                            if (turn === generation) {
                                error.value = (e as Error).message;
                                retry = setTimeout(() => void attach(id), 5000);
                            }
                        }
                    },
                    Math.min(1000 * 2 ** attempt++, 10000),
                );
            };
        } catch (e) {
            error.value = (e as Error).message;
        }
    }
    async function create(directory?: string) {
        busy.value = true;
        error.value = "";
        try {
            const s = await props.api("/terminals", "POST", { name: name.value });
            if (disposed) return;
            name.value = "";
            await load();
            if (disposed) return;
            if (directory) pendingCommands.set(s.id, "cd -- '" + directory.replace(/'/g, "'\"'\"'") + "'\n");
            await attach(s.id);
        } catch (e) {
            if (!disposed) error.value = (e as Error).message;
        } finally {
            if (!disposed) busy.value = false;
        }
    }

    async function closeSession(id: string) {
        if (busy.value || !confirm("关闭这个终端？正在运行的命令会结束。")) return;
        busy.value = true;
        error.value = "";
        try {
            const index = sessions.value.findIndex((session) => session.id === id);
            await props.api("/terminals/" + encodeURIComponent(id), "DELETE");
            pendingCommands.delete(id);
            const wasCurrent = current.value === id;
            if (wasCurrent) {
                detach();
                current.value = "";
                localStorage.removeItem(storage());
            }
            await load();
            if (wasCurrent) {
                const remaining = available(),
                    next = remaining[Math.min(Math.max(index, 0), remaining.length - 1)];
                if (next) await attach(next.id);
            }
        } catch (e) {
            error.value = (e as Error).message;
        } finally {
            busy.value = false;
        }
    }
    async function open(id: string) {
        const turn = generation;
        await load();
        if (disposed || turn !== generation) return;
        await attach(id);
    }
    watch(
        () => props.excludedId,
        async () => {
            if (current.value === props.excludedId) {
                detach();
                current.value = "";
                const next = available().find((s) => !s.ended);
                if (next) await attach(next.id);
            }
        },
    );
    defineExpose({ create, open, detach });
    onMounted(async () => {
        window.addEventListener("ownpanel-terminal-titles", titlesChanged);
        try {
            await load();
            if (disposed) return;
            const saved = localStorage.getItem(storage());
            const s = available().find((s) => s.id === saved) || available().find((s) => !s.ended);
            if (s && props.storageScope !== "floating") await attach(s.id);
        } catch (e) {
            if (!disposed) error.value = (e as Error).message;
        }
        if (disposed) return;
        poll = setInterval(() => {
            if (!disposed)
                void load()
                    .then(async () => {
                        if (!disposed && !current.value && !busy.value && props.storageScope !== "floating") {
                            const next = available().find((s) => !s.ended);
                            if (next) await attach(next.id);
                        }
                    })
                    .catch(() => {});
        }, 5000);
    });
    onUnmounted(() => {
        disposed = true;
        window.removeEventListener("ownpanel-terminal-titles", titlesChanged);
        clearInterval(poll);
        pendingCommands.clear();
        detach();
    });
</script>
<template><div class="persistent-terminal"><p v-if="error" class="banner warning">{{ error }}</p>
        <div class="terminal-session-bar" :class="{'tab-dock-target': dockTarget}"><ElTabs ref="libraryTabs" class="terminal-library-tabs" type="card" closable :model-value="current" @tab-change="attach(String($event))" @tab-remove="closeSession(String($event))"><ElTabPane v-for="session in available()" :key="session.id" :name="session.id" ><template #label><span class="draggable-tab-label" title="双击或右键重命名" @dblclick.stop="renameSession(session)" @contextmenu.prevent.stop="renameSession(session)" @pointerdown="emit('tabDrag', session.id, $event)">{{ session.name + (session.ended ? ' · 已结束' : '') }}</span></template></ElTabPane></ElTabs>
            <div class="terminal-tab-actions">
                <ElButton text aria-label="新建终端" title="新建终端" :disabled="busy" @click="create()"><Plus :size="18" /></ElButton></div></div>
        <section class="terminal-card"><div ref="element" class="terminal-host" />
            <div v-if="!current" class="terminal-empty"><h3>持久终端</h3>
                <ElButton :disabled="busy" @click="create()" native-type="button" type="primary">创建会话</ElButton></div></section></div></template>
