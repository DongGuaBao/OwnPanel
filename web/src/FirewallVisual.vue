<script setup lang="ts">
import PanelTable from "./PanelTable.vue";
import type { TableColumn } from "./table-layout";
    import { ref, computed, watch } from "vue";
    import { parseRules, draftRule, buildRule, ruleSummary, fields, type Rule, type Table, type Chain } from "./iptables-model";
    const props = defineProps<{ modelValue: string; writable: boolean }>();
    const emit = defineEmits<{ (e: "update:modelValue", value: string): void }>();
    const parsed = computed(() => parseRules(props.modelValue)),
        tableName = ref(""),
        chainName = ref(""),
        search = ref(""),
        editing = ref(false),
        editIndex = ref(-1),
        editOriginal = ref(""),
        editTable = ref(""),
        editChain = ref(""),
        draft = ref<Record<string, string>>({}),
        error = ref(""),
        newChain = ref("");
    const table = computed(() => parsed.value.tables.find((t) => t.name === tableName.value));
    const chainOptions = computed(() => [...new Set((table.value ? [table.value] : parsed.value.tables).flatMap((t) => t.chains.map((c) => c.name)))]);
    const chain = computed(() => table.value?.chains.find((c) => c.name === chainName.value));
    const destination = computed(() => parsed.value.tables.find((t) => t.name === editTable.value));
    const allRows = computed(() => parsed.value.tables.flatMap((t) => t.chains.flatMap((c) => c.rules.map((r, i) => ({ rule: r, table: t, chain: c, position: i, summary: ruleSummary(r) })))));
    const rows = computed(() =>
        allRows.value.filter(
            (row) =>
                (!tableName.value || row.table.name === tableName.value) &&
                (!chainName.value || row.chain.name === chainName.value) &&
                (!search.value || `${row.table.name} ${row.chain.name} ${row.rule.line}`.toLowerCase().includes(search.value.toLowerCase())),
        ),
    );
    watch(chainOptions, (options) => {
        if (chainName.value && !options.includes(chainName.value)) chainName.value = "";
    });
    function update(lines: string[]) {
        emit("update:modelValue", lines.join("\n"));
    }
    function edit(rule?: Rule, owner?: Table) {
        error.value = "";
        editIndex.value = rule?.index ?? -1;
        editOriginal.value = rule?.line || "";
        const target = owner || table.value || parsed.value.tables.find((t) => t.name === "filter") || parsed.value.tables[0];
        if (!target) return;
        editTable.value = target.name;
        editChain.value = rule?.chain || (target.chains.some((c) => c.name === chainName.value) ? chainName.value : target.chains[0]?.name || "");
        draft.value = draftRule(rule?.args || ["-p", "tcp", "-j", "ACCEPT"]);
        editing.value = true;
    }
    function changeDestination() {
        editChain.value = destination.value?.chains[0]?.name || "";
    }
    function save() {
        try {
            const target = destination.value;
            if (!target || !target.chains.some((c) => c.name === editChain.value)) throw Error("请选择规则所属的表和链");
            if (editIndex.value >= 0 && parsed.value.lines[editIndex.value] !== editOriginal.value) throw Error("规则已变化，请重新打开编辑");
            const line = buildRule(editChain.value, draft.value),
                lines = [...parsed.value.lines];
            if (editIndex.value >= 0) lines[editIndex.value] = line;
            else lines.splice(target.end, 0, line);
            update(lines);
            editing.value = false;
        } catch (e) {
            error.value = (e as Error).message;
        }
    }
    function remove(rule: Rule) {
        if (!confirm("从草稿中删除此规则？点击应用后才在主机上生效。")) return;
        const lines = [...parsed.value.lines];
        lines.splice(rule.index, 1);
        update(lines);
    }
    function move(rule: Rule, owner: Chain, offset: number) {
        const other = owner.rules[owner.rules.findIndex((r) => r.index === rule.index) + offset];
        if (!other) return;
        const lines = [...parsed.value.lines];
        [lines[rule.index], lines[other.index]] = [lines[other.index]!, lines[rule.index]!];
        update(lines);
    }
    function policy(value: string) {
        if (!chain.value) return;
        const lines = [...parsed.value.lines];
        lines[chain.value.index] = `:${chain.value.name} ${value} ${chain.value.counter}`;
        update(lines);
    }
    function addChain() {
        if (!table.value) return;
        if (!/^[A-Za-z0-9_.:+-]{1,64}$/.test(newChain.value) || table.value.chains.some((c) => c.name === newChain.value)) {
            error.value = "链名称无效或重复";
            return;
        }
        const lines = [...parsed.value.lines];
        lines.splice(table.value.start + 1, 0, `:${newChain.value} - [0:0]`);
        update(lines);
        newChain.value = "";
        error.value = "";
    }
    function deleteChain() {
        if (!chain.value || chain.value.policy !== "-" || chain.value.rules.length) return;
        if (!confirm("删除自定义链？被其他规则引用时应用校验会拒绝。")) return;
        const lines = [...parsed.value.lines];
        lines.splice(chain.value.index, 1);
        update(lines);
        chainName.value = "";
    }
const tableColumnsIptables: TableColumn[] = [
    {key:"chain",label:"表 / 链",value:(row)=>row.table.name + "/" + row.chain.name,minWidth:160},
    {key:"sequence",label:"链内顺序",value:(row)=>row.position + 1,minWidth:100},
    {key:"protocol",label:"协议 / 接口",value:(row)=>row.summary.protocol,minWidth:130},
    {key:"source",label:"来源",value:(row)=>row.summary.source,minWidth:130},
    {key:"destination",label:"目标 / 端口",value:(row)=>row.summary.destination,minWidth:130},
    {key:"action",label:"动作",value:(row)=>row.summary.jump,minWidth:130},
    {key:"traffic",label:"包 / 字节",value:(row)=>Number(row.rule.bytes),minWidth:130},
    {key:"actions",label:"操作",sortable:false,fixed:'right',minWidth:180}
];
</script>
<template><section class="card visual-firewall"><div class="card-heading"><h2>可视化规则编辑</h2>
            <span class="tag">编辑草稿 · 应用后生效</span></div>
        <ElForm inline label-position="top" class="toolbar"><ElFormItem label="表筛选"><ElSelect v-model="tableName" aria-label="防火墙表"><ElOption value="" :label="'全部表'" />
                    <ElOption v-for="t in parsed.tables" :key="t.name" :value="t.name" :label="t.name" /></ElSelect></ElFormItem>
            <ElFormItem label="链筛选"><ElSelect v-model="chainName" aria-label="防火墙链"><ElOption value="" :label="'全部链'" />
                    <ElOption v-for="name in chainOptions" :key="name" :value="name" :label="name" /></ElSelect></ElFormItem>
            <ElFormItem label="搜索规则"><ElInput v-model="search" aria-label="搜索防火墙规则" placeholder="IP、端口、协议或动作" /></ElFormItem>
            <ElFormItem :label="`${ table?.name } / ${ chain.name } 默认策略`" v-if="chain && chain.policy !== '-'"><ElSelect :value="chain.policy" aria-label="链默认策略" :disabled="!writable" @change="policy($event)"><ElOption value="ACCEPT">ACCEPT</ElOption>
                    <ElOption value="DROP">DROP</ElOption></ElSelect></ElFormItem>
            <div class="spacer" />
            <span class="muted">{{ rows.length }} / {{ allRows.length }} 条规则</span>
            <ElButton :disabled="!writable || !parsed.tables.length" @click="edit()" native-type="button" type="primary">添加规则</ElButton></ElForm>
        <p v-if="error" class="form-error">{{ error }}</p>
        <p v-for="e in parsed.errors" :key="e" class="form-error">{{ e }}</p>
        <div class="rule-table-scroll"><PanelTable table-id="iptables" :rows="rows" :columns="tableColumnsIptables" row-key="rule.index"><template #chain="{row:row}"><span class="tag">{{ row.table.name }}</span>
                            <small class="mono">{{ row.chain.name }}</small></template><template #sequence="{row:row}">{{ row.position + 1 }}</template><template #protocol="{row:row}">{{ row.summary.protocol || "全部" }}
                            <small>{{ row.summary.input || "任意" }} → {{ row.summary.output || "任意" }}</small></template><template #source="{row:row}">{{ row.summary.source || "任意" }}
                            <small v-if="row.summary.sport">端口 {{ row.summary.sport }}</small></template><template #destination="{row:row}">{{ row.summary.destination || "任意" }}
                            <small v-if="row.summary.dport">端口 {{ row.summary.dport }}</small></template><template #action="{row:row}"><span class="tag">{{ row.summary.jump || "扩展规则" }}</span>
                            <details><summary>完整规则</summary>
                                <code>{{ row.rule.line }}</code></details></template><template #traffic="{row:row}">{{ row.rule.packets }} / {{ row.rule.bytes }}</template><template #actions="{row:row}"><ElSpace wrap class="actions"><ElButton :disabled="!writable" @click="edit(row.rule, row.table)" native-type="button">编辑</ElButton>
                                <ElButton aria-label="上移规则" :disabled="!writable || row.position === 0" @click="move(row.rule, row.chain, -1)" native-type="button">↑</ElButton>
                                <ElButton aria-label="下移规则" :disabled="!writable || row.position === row.chain.rules.length - 1" @click="move(row.rule, row.chain, 1)" native-type="button">
                                    ↓
                                </ElButton>
                                <ElButton class="button red" :disabled="!writable" @click="remove(row.rule)" native-type="button" type="danger" plain>删除</ElButton></ElSpace></template><template #empty>没有匹配的规则</template></PanelTable></div>
        <div v-if="table" class="toolbar chain-toolbar"><ElInput v-model="newChain" aria-label="新链名称" placeholder="自定义链名称" />
            <ElButton :disabled="!writable" @click="addChain" native-type="button">添加链</ElButton>
            <ElButton v-if="chain?.policy === '-'" class="button red" :disabled="!writable || !!chain.rules.length" @click="deleteChain" native-type="button" type="danger" plain>删除空链</ElButton></div></section>
    <ElDialog v-if="editing" :model-value="true" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false" append-to-body destroy-on-close width="min(1120px, 96vw)"><ElForm id="firewallvisual-form-1" label-position="top" class="modal wide component-dialog-body" @submit.prevent="save">
            <p v-if="error" class="form-error">{{ error }}</p>
            <ElRow :gutter="16" v-if="editIndex < 0" ><ElCol :xs="24" :sm="12" ><ElFormItem label="所属表"><ElSelect v-model="editTable" aria-label="新规则所属表" @change="changeDestination"><ElOption v-for="t in parsed.tables" :key="t.name" :value="t.name">{{ t.name }}</ElOption></ElSelect></ElFormItem></ElCol>
                <ElCol :xs="24" :sm="12" ><ElFormItem label="所属链"><ElSelect v-model="editChain" aria-label="新规则所属链"><ElOption v-for="c in destination?.chains" :key="c.name" :value="c.name" :label="c.name" /></ElSelect></ElFormItem></ElCol></ElRow>
            <ElRow :gutter="16" ><ElCol :xs="24" :sm="12" v-for="f in fields" :key="f.key"><ElFormItem :label="`${ f.label }`"  ><ElSelect v-if="f.key === 'protocol'" v-model="draft[f.key]" :disabled="draft._opaque === '1'" aria-label="规则协议"><ElOption value="" :label="'全部'" />
                        <ElOption value="tcp" :label="'tcp'" />
                        <ElOption value="udp" :label="'udp'" />
                        <ElOption value="icmp" :label="'icmp'" />
                        <ElOption value="ipv6-icmp" :label="'ipv6-icmp'" />
                        <ElOption value="all" :label="'all'" /></ElSelect>
                    <ElInput v-else v-model="draft[f.key]" :disabled="draft._opaque === '1'" :aria-label="f.label" :placeholder="f.key === 'jump' ? 'ACCEPT / DROP / 链名称' : '任意'" /></ElFormItem></ElCol></ElRow>
            <p v-if="draft._opaque" class="banner warning">此规则包含反向匹配或特殊扩展，请在下方完整参数中编辑，避免改变原有含义。</p>
            <ElFormItem label="其他匹配 / 扩展参数"><ElInput v-model="draft.extra" aria-label="扩展参数" placeholder="例如 -m conntrack --ctstate ESTABLISHED,RELATED" /></ElFormItem>
            </ElForm><template #header><ElRow justify="space-between" align="middle" ><span class="el-dialog__title">{{ editIndex < 0 ? "添加" : "编辑" }}规则 · {{ editTable }} / {{ editChain }}</span>
                <ElButton text  native-type="button" class="icon-button" @click="editing = false">×</ElButton></ElRow></template><template #footer><ElButton @click="editing = false" native-type="button">取消</ElButton>
                <ElButton form="firewallvisual-form-1" native-type="submit" type="primary">保存到草稿</ElButton></template></ElDialog></template>
