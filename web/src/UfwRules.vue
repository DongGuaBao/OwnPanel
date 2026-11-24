<script setup lang="ts">
import PanelTable from "./PanelTable.vue";
import type { TableColumn } from "./table-layout";
    import { ref, computed } from "vue";
    import { tokens } from "./iptables-model";
    const props = defineProps<{ added: string; writable: boolean; api: (url: string, method?: string, body?: unknown) => Promise<any> }>();
    const emit = defineEmits<{ (e: "changed"): void }>();
    const editing = ref(false),
        original = ref(""),
        port = ref(""),
        protocol = ref("tcp"),
        source = ref("any"),
        policy = ref("allow"),
        busy = ref(false),
        removing = ref(""),
        error = ref("");
    const rules = computed(() =>
        props.added
            .split("\n")
            .filter((l) => l.startsWith("ufw "))
            .map((line) => {
                const raw = line.slice(4);
                let a: string[] = [];
                try {
                    a = tokens(raw);
                } catch {}
                let p = "",
                    proto = "tcp",
                    src = "any";
                const idx = a.indexOf("port");
                if (idx >= 0) {
                    p = a[idx + 1] || "";
                    proto = a[a.indexOf("proto") + 1] || "tcp";
                    const from = a.indexOf("from");
                    if (from >= 0) src = a[from + 1] || "any";
                } else {
                    const match = a[1]?.match(/^(\d+(?::\d+)?)(?:\/(tcp|udp))?$/);
                    if (match) {
                        p = match[1]!;
                        proto = match[2] || "tcp";
                    }
                }
                const expected = a[0] + " from " + src + " to any port " + p + " proto " + proto;
                const simple = (a.length === 2 && !!p) || raw === expected;
                return {
                    raw,
                    port: p,
                    protocol: proto,
                    source: src,
                    policy: a[0] || "",
                    editable: simple && ["allow", "deny"].includes(a[0] || ""),
                    deletable: ["allow", "deny", "reject", "limit"].includes(a[0] || "") && !/["'\\]/.test(raw),
                };
            }),
    );
    function edit(r: any) {
        original.value = r.raw;
        port.value = r.port;
        protocol.value = r.protocol;
        source.value = r.source;
        policy.value = r.policy;
        error.value = "";
        editing.value = true;
    }
    async function save() {
        if (busy.value) return;
        busy.value = true;
        try {
            await props.api("/firewall", "POST", { action: "replace", original: original.value, port: port.value, protocol: protocol.value, source: source.value, policy: policy.value });
            editing.value = false;
            emit("changed");
        } catch (e) {
            error.value = (e as Error).message;
        } finally {
            busy.value = false;
        }
    }
    async function remove(raw: string) {
        if (busy.value || !confirm("删除此 UFW 规则？未确认将在 60 秒后恢复。")) return;
        busy.value = true;
        removing.value = raw;
        try {
            await props.api("/firewall", "POST", { action: "delete-rule", original: raw });
            emit("changed");
        } catch (e) {
            error.value = (e as Error).message;
        } finally {
            busy.value = false;
            removing.value = "";
        }
    }
const tableColumnsUfw: TableColumn[] = [
    {key:"sequence",label:"顺序",value:(r,i)=>i + 1,minWidth:100},
    {key:"policy",label:"策略",value:(r,i)=>r.policy,minWidth:130},
    {key:"port",label:"端口 / 协议",value:(r,i)=>Number(r.port.split(':')[0]) || 0,minWidth:160},
    {key:"source",label:"来源",value:(r,i)=>r.source,minWidth:130},
    {key:"actions",label:"操作",sortable:false,fixed:'right',minWidth:180}
];
</script>
<template><section class="card visual-firewall"><div class="card-heading"><slot name="toolbar" /></div>
        <p v-if="error" class="form-error">{{ error }}</p>
        <div class="rule-table-scroll"><PanelTable table-id="ufw" :rows="rules" :columns="tableColumnsUfw" row-key="raw"><template #sequence="{row:r,index:i}">{{ i + 1 }}</template><template #policy="{row:r,index:i}"><span class="tag">{{ r.policy }}</span></template><template #port="{row:r,index:i}">{{ r.port || "复杂规则" }} / {{ r.protocol }}
                            <details><summary>完整规则</summary>
                                <code>ufw {{ r.raw }}</code></details></template><template #source="{row:r,index:i}">{{ r.source }}</template><template #actions="{row:r,index:i}"><ElSpace wrap class="actions"><ElButton :disabled="!writable || busy || !r.editable" @click="edit(r)" native-type="button">编辑</ElButton>
                                <ElButton class="button red" :disabled="!writable || busy || !r.deletable" :loading="removing===r.raw" @click="remove(r.raw)" native-type="button" type="danger" plain>删除</ElButton></ElSpace></template><template #empty>暂无已配置规则</template></PanelTable></div></section>
    <ElDialog v-if="editing" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(640px, 94vw)"><ElForm id="ufwrules-form-1" label-position="top" class="modal component-dialog-body" @submit.prevent="save">
            <p v-if="error" class="form-error">{{ error }}</p>
            <ElFormItem label="端口 / 范围"><ElInput v-model="port" aria-label="UFW 编辑端口" required /></ElFormItem>
            <ElFormItem label="协议"><ElSelect v-model="protocol" aria-label="UFW 编辑协议"><ElOption value="tcp" :label="'tcp'" />
                    <ElOption value="udp" :label="'udp'" /></ElSelect></ElFormItem>
            <ElFormItem label="来源 IP / CIDR"><ElInput v-model="source" required /></ElFormItem>
            <ElFormItem label="策略"><ElSelect v-model="policy" aria-label="UFW 编辑策略"><ElOption value="allow" :label="'允许'" />
                    <ElOption value="deny" :label="'拒绝'" /></ElSelect></ElFormItem>
            <p class="muted">保存后 60 秒内确认保留，否则自动恢复。</p>
            </ElForm><template #header><ElRow justify="space-between" align="middle" ><span class="el-dialog__title">编辑 UFW 规则</span>
                <ElButton text  native-type="button" class="icon-button" :disabled="busy" @click="editing = false">×</ElButton></ElRow></template><template #footer><ElButton form="ufwrules-form-1" :loading="busy" :disabled="busy" native-type="submit" type="primary">保存并应用</ElButton></template></ElDialog></template>
