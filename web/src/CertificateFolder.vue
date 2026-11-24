<script setup lang="ts">
import { operationMessage } from "./operation-message";
import PanelTable from "./PanelTable.vue";
import type { TableColumn } from "./table-layout";
    import { ref, computed, onMounted, onUnmounted, watch } from "vue";
    import {
        ElButton,
        ElInput,
        ElTag,
        ElSelect,
        ElOption,
        ElDialog,
        ElForm,
        ElFormItem,
        ElAlert,
        ElDescriptions,
        ElDescriptionsItem,
        ElDrawer,
        ElMessageBox,
    } from "element-plus";
    import { Upload, RefreshCw } from "lucide-vue-next";
    type Certificate = {
        id: string;
        name: string;
        domains: string[];
        subject: string;
        issuer: string;
        notBefore: string;
        notAfter: string;
        daysRemaining: number;
        status: string;
        serialNumber: string;
        fingerprint: string;
        algorithm: string;
    };
    const props = defineProps<{ api: (url: string, method?: string, body?: unknown) => Promise<any>; nodeId: string; writable: boolean; picker?: boolean; refreshSignal?: number }>();
    const emit = defineEmits<{ select: [pair: { certificate: string; privateKey: string; certificateInfo?: Certificate }] }>();
    const viewRows = ref<Certificate[]>([]);
    const certificates = ref<Certificate[]>([]),
        search = ref(""),
        status = ref(""),
        busy = ref(false),
        error = ref(""),
        importing = ref(false),
        editingID = ref(""),
        detail = ref<Certificate | null>(null),
        preview = ref<Certificate | null>(null);
    const draft = ref({ name: "", certificate: "", privateKey: "" }),
        uploads = ref<HTMLInputElement>(),
        source = ref("pem"),
        chainPath = ref(""),
        keyPath = ref("");
    let disposed = false,
        generation = 0;
    function api(url: string, method = "GET", body?: unknown) {
        if (disposed) throw Error("证书页面已关闭");
        return props.api("/nodes/" + encodeURIComponent(props.nodeId) + url, method, body);
    }
    const labels: Record<string, string> = { valid: "有效", expiring: "即将到期", expired: "已过期", notYetValid: "尚未生效" };
    const tagType = (value: string) => (value === "valid" ? "success" : value === "expiring" ? "warning" : value === "expired" ? "danger" : "info");
    const date = (value: string) => new Date(value).toLocaleString("zh-CN", { hour12: false });
    const rows = computed(() =>
        certificates.value.filter(
            (item) => (!status.value || item.status === status.value) && [item.name, ...item.domains, item.issuer].join(" ").toLowerCase().includes(search.value.trim().toLowerCase()),
        ),
    );
    async function work(fn: () => Promise<void>) {
        busy.value = true;
        error.value = "";
        try {
            await fn();
        } catch (e) {
            if (!disposed) error.value = (e as Error).message;
        } finally {
            if (!disposed) busy.value = false;
        }
    }
    async function load() {
        const turn = ++generation;
        const result = await api("/certificates", "REFRESH");
        if (!disposed && turn === generation) certificates.value = result.certificates;
    }
    function openImport(item?: Certificate) {
        error.value = "";
        editingID.value = item?.id || "";
        draft.value = { name: item?.name || "", certificate: "", privateKey: "" };
        preview.value = null;
        source.value = "pem";
        chainPath.value = "";
        keyPath.value = "";
        importing.value = true;
    }
    function closeImport() {
        if (busy.value) return;
        importing.value = false;
        draft.value = { name: "", certificate: "", privateKey: "" };
        preview.value = null;
    }
    async function readUploads(event: Event) {
        const input = event.target as HTMLInputElement;
        await work(async () => {
            for (const file of Array.from(input.files || [])) {
                const text = await file.text();
                if (disposed) return;
                const certs = text.match(/-----BEGIN CERTIFICATE-----[\s\S]*?-----END CERTIFICATE-----/g);
                const key = text.match(/-----BEGIN (?:RSA |EC |)PRIVATE KEY-----[\s\S]*?-----END (?:RSA |EC |)PRIVATE KEY-----/);
                if (!certs && !key) throw Error(file.name + " 不是支持的 PEM 证书或私钥");
                if (certs) draft.value.certificate = certs.join("\n");
                if (key) draft.value.privateKey = key[0];
            }
        });
        input.value = "";
    }
    async function readHostFiles() {
        await work(async () => {
            if (!chainPath.value.startsWith("/") || !keyPath.value.startsWith("/")) throw Error("请输入主机上的绝对文件路径");
            const [chain, key] = await Promise.all([api("/files/content?path=" + encodeURIComponent(chainPath.value)), api("/files/content?path=" + encodeURIComponent(keyPath.value))]);
            if (disposed) return;
            draft.value.certificate = chain.content;
            draft.value.privateKey = key.content;
            preview.value = await api("/certificates/preview", "POST", draft.value);
        });
    }
    async function inspect() {
        await work(async () => {
            const result = await api("/certificates/preview", "POST", draft.value);
            if (disposed) return;
            preview.value = result;
            if (!draft.value.name) draft.value.name = result.domains[0] || "未命名证书";
        });
    }
    async function save() {
        await work(async () => {
            await api("/certificates" + (editingID.value ? "/" + editingID.value : ""), editingID.value ? "PUT" : "POST", draft.value);
            if (disposed) return;
            importing.value = false;
            draft.value = { name: "", certificate: "", privateKey: "" };
            preview.value = null;
            operationMessage("证书已保存");
            await load();
        });
    }
    async function select(item: Certificate) {
        await work(async () => {
            const pair = await api("/certificates/" + item.id + "/export", "POST");
            if (!disposed) emit("select", { certificate: pair.certificate, privateKey: pair.privateKey, certificateInfo: item });
        });
    }
    async function download(item: Certificate) {
        await work(async () => {
            const pair = await api("/certificates/" + item.id + "/export", "POST");
            if (disposed) return;
            const url = URL.createObjectURL(new Blob([pair.certificate + "\n" + pair.privateKey], { type: "application/x-pem-file" }));
            const anchor = document.createElement("a");
            anchor.href = url;
            anchor.download = item.name.replace(/[^\w.-]/g, "_") + ".pem";
            anchor.click();
            URL.revokeObjectURL(url);
        });
    }
    async function remove(item: Certificate) {
        try {
            await ElMessageBox.confirm("从证书夹删除“" + item.name + "”？已部署到网站的证书不受影响。", "删除证书", { type: "warning", confirmButtonText: "删除", cancelButtonText: "取消" });
        } catch {
            return;
        }
        await work(async () => {
            await api("/certificates/" + item.id, "DELETE");
            if (!disposed) await load();
        });
    }
    watch(
        () => [draft.value.certificate, draft.value.privateKey],
        () => (preview.value = null),
    );
    watch(
        () => props.refreshSignal,
        () => {
            if (!busy.value) void work(load);
        },
    );
    onMounted(() => {
        void work(load);
    });
    onUnmounted(() => {
        disposed = true;
        generation++;
        draft.value = { name: "", certificate: "", privateKey: "" };
        preview.value = null;
    });

const tableColumnsCertificates = computed<TableColumn[]>(()=>[
 {key:'name',label:'名称 / 域名',minWidth:props.picker?170:230,value:r=>r.name},
 {key:'issuer',label:'颁发者',showOverflowTooltip:true,minWidth:props.picker?140:200,value:r=>r.issuer},
 {key:'expiry',label:'到期时间',minWidth:props.picker?165:190,value:r=>Date.parse(r.notAfter)},
 {key:'status',label:'状态',width:props.picker?100:115,value:r=>r.status},
 {key:'actions',label:'操作',width:props.picker?145:270,fixed:'right',sortable:false},
]);
</script>
<template><section class="certificate-folder" :class="{ card: !picker, 'certificate-picker': picker }"><div class="certificate-tools" :class="{ 'card-heading': !picker }"><ElInput v-model="search" aria-label="搜索证书" placeholder="搜索名称、域名或颁发者" clearable />
            <ElSelect v-model="status" aria-label="证书状态" placeholder="全部状态" clearable><ElOption v-for="(label, value) in labels" :key="value" :value="value" :label="label" /></ElSelect>
            <ElButton :disabled="busy" @click="work(load)"><RefreshCw :size="15" />
                刷新证书
            </ElButton>
            <ElButton v-if="writable" type="primary" :disabled="busy" @click="openImport()"><Upload :size="15" />
                导入证书
            </ElButton></div>
        <ElAlert v-if="error && !importing" :title="error" type="error" :closable="false" show-icon />
        <PanelTable :table-id="picker ? 'certificate-picker' : 'certificates'" :rows="rows" :columns="tableColumnsCertificates" row-key="id" @view-order-change="viewRows=$event" class="certificate-table"><template #name="{row}"><b>{{ row.name }}</b>
                    <small class="certificate-domains">{{ row.domains.join(" · ") || "无域名信息" }}</small></template><template #issuer="{row}">{{row.issuer}}</template><template #expiry="{row}">{{ date(row.notAfter) }}
                    <small class="certificate-domains">{{ row.status === "expired" ? "已过期" : row.status === "notYetValid" ? "生效于 " + date(row.notBefore) : "剩余 " + row.daysRemaining + " 天" }}</small></template><template #status="{row}"><ElTag effect="light" :type="tagType(row.status)">{{ labels[row.status] }}</ElTag></template><template #actions="{row}"><ElButton v-if="picker" type="primary" :disabled="busy" @click="select(row as Certificate)">使用此证书</ElButton>
                    <template v-else><ElButton text @click="detail = row as Certificate">详情</ElButton>
                        <ElButton v-if="writable" text :disabled="busy" @click="openImport(row as Certificate)">更新</ElButton>
                        <ElButton text :disabled="busy" title="导出 PEM 包含证书链和私钥" @click="download(row as Certificate)">导出 PEM</ElButton>
                        <ElButton v-if="writable" text type="danger" :disabled="busy" @click="remove(row as Certificate)">删除</ElButton></template></template><template #empty>暂无证书，可导入 PEM 证书链和私钥</template></PanelTable>
        <div class="certificate-mobile-list"><article v-for="item in viewRows" :key="item.id" class="certificate-mobile-item"><div class="certificate-mobile-heading"><b>{{ item.name }}</b>
                    <ElTag :type="tagType(item.status)">{{ labels[item.status] }}</ElTag></div>
                <p class="certificate-domains">{{ item.domains.join(" · ") }}</p>
                <p>颁发者：{{ item.issuer }}</p>
                <p>到期：{{ date(item.notAfter) }}</p>
                <p v-if="item.status === 'valid' || item.status === 'expiring'">剩余 {{ item.daysRemaining }} 天</p>
                <div class="certificate-mobile-actions"><ElButton v-if="picker" type="primary" :disabled="busy" @click="select(item)">使用此证书</ElButton>
                    <template v-else><ElButton text @click="detail = item">详情</ElButton>
                        <ElButton v-if="writable" text :disabled="busy" @click="openImport(item)">更新</ElButton>
                        <ElButton text :disabled="busy" @click="download(item)">导出 PEM</ElButton>
                        <ElButton v-if="writable" text type="danger" :disabled="busy" @click="remove(item)">删除</ElButton></template></div></article>
            <p v-if="!rows.length" class="certificate-domains">暂无证书，可导入 PEM 证书链和私钥</p></div></section>
    <ElDialog
        v-if="importing"
        :model-value="true"
        :title="editingID ? '更新证书' : '导入证书'"
        width="min(860px, calc(100vw - 24px))"
        top="6vh"
        class="certificate-import-dialog"
        append-to-body
        :close-on-click-modal="false"
        :close-on-press-escape="!busy"
        :show-close="!busy"
        @close="closeImport"><ElAlert v-if="error" :title="error" type="error" :closable="false" />
        <ElForm label-position="top" :disabled="busy"><ElFormItem label="证书名称" required><ElInput v-model="draft.name" aria-label="证书名称" maxlength="128" placeholder="例如：主站通配符证书" /></ElFormItem>
            <div class="certificate-source"><ElButton :type="source === 'pem' ? 'primary' : 'default'" plain @click="source = 'pem'">PEM / 本地文件</ElButton>
                <ElButton :type="source === 'host' ? 'primary' : 'default'" plain @click="source = 'host'">从主机文件导入</ElButton></div>
            <template v-if="source === 'host'"><ElFormItem label="主机证书链路径"><ElInput v-model="chainPath" placeholder="/etc/letsencrypt/live/example.com/fullchain.pem" /></ElFormItem>
                <ElFormItem label="主机私钥路径"><ElInput v-model="keyPath" placeholder="/etc/letsencrypt/live/example.com/privkey.pem" /></ElFormItem>
                <ElButton @click="readHostFiles">读取并校验证书</ElButton></template>
            <template v-else><ElButton @click="uploads?.click()">从本地 PEM 文件填入</ElButton>
                <input ref="uploads" type="file" accept=".pem,.crt,.key,.cer" multiple hidden @change="readUploads" />
                <ElRow :gutter="16" ><ElCol :xs="24" :sm="12" ><ElFormItem label="证书链 PEM" required><ElInput type="textarea" v-model="draft.certificate" aria-label="导入证书链" :rows="6" spellcheck="false" /></ElFormItem></ElCol>
                    <ElCol :xs="24" :sm="12" ><ElFormItem label="私钥 PEM" required><ElInput type="textarea" v-model="draft.privateKey" aria-label="导入私钥" :rows="6" spellcheck="false" /></ElFormItem></ElCol></ElRow>
                <ElButton :disabled="!draft.certificate || !draft.privateKey" @click="inspect">解析并校验</ElButton></template>
            <ElDescriptions v-if="preview" :column="1" border class="certificate-preview"><ElDescriptionsItem label="域名">{{ preview.domains.join(" · ") }}</ElDescriptionsItem>
                <ElDescriptionsItem label="颁发者">{{ preview.issuer }}</ElDescriptionsItem>
                <ElDescriptionsItem label="有效期">{{ date(preview.notBefore) }} 至 {{ date(preview.notAfter) }}</ElDescriptionsItem>
                <ElDescriptionsItem label="校验结果"><ElTag :type="tagType(preview.status)">{{ labels[preview.status] }}</ElTag>
                    证书与私钥匹配
                </ElDescriptionsItem></ElDescriptions></ElForm>
        <template #footer><ElButton :disabled="busy" @click="closeImport">取消</ElButton>
            <ElButton type="primary" :loading="busy" @click="save">{{ editingID ? "更新证书" : "保存到证书夹" }}</ElButton></template></ElDialog>
    <ElDrawer v-if="detail" :model-value="true" :title="detail.name" size="min(640px, 96vw)" append-to-body @close="detail = null"><ElDescriptions :column="1" border><ElDescriptionsItem label="域名">{{ detail.domains.join(" · ") }}</ElDescriptionsItem>
            <ElDescriptionsItem label="颁发者">{{ detail.issuer }}</ElDescriptionsItem>
            <ElDescriptionsItem label="使用者">{{ detail.subject }}</ElDescriptionsItem>
            <ElDescriptionsItem label="生效时间">{{ date(detail.notBefore) }}</ElDescriptionsItem>
            <ElDescriptionsItem label="到期时间">{{ date(detail.notAfter) }}</ElDescriptionsItem>
            <ElDescriptionsItem label="状态">{{ labels[detail.status] }}</ElDescriptionsItem>
            <ElDescriptionsItem label="算法">{{ detail.algorithm }}</ElDescriptionsItem>
            <ElDescriptionsItem label="序列号">{{ detail.serialNumber }}</ElDescriptionsItem>
            <ElDescriptionsItem label="SHA-256 指纹"><span class="certificate-fingerprint">{{ detail.fingerprint }}</span></ElDescriptionsItem></ElDescriptions></ElDrawer></template>
