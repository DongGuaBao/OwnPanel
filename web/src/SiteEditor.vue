<script setup lang="ts">
    import { ref, watch, nextTick } from "vue";
    import { ElDialog, ElForm, ElFormItem, ElTabs, ElTabPane, ElInput, ElInputNumber, ElButton, ElCheckbox, ElAlert, ElCard } from "element-plus";
    import NginxRouteBuilder from "./NginxRouteBuilder.vue";
    import CertificateSelection from "./CertificateSelection.vue";
    import type { RouteForm } from "./nginx-routes";
    const props = defineProps<{ revision: string; busy: boolean; saveStage: string; error: string; originalRoutingOnly: boolean }>();
    const emit = defineEmits<{ close: []; save: []; certificate: [target: any]; rawRouting: []; addBinding: [] }>();
    const site = defineModel<any>("site", { required: true }),
        routes = defineModel<RouteForm>("routes", { required: true });
    const advanced = defineModel<boolean>("advanced", { required: true }),
        multiple = defineModel<boolean>("multiple", { required: true });
    const tab = ref("routing"),
        validation = ref("");
    const formElement = ref<InstanceType<typeof ElForm>>();
    watch(
        () => [site.value.httpsEnabled, site.value.quic],
        () => {
            site.value.tls = !!(site.value.httpsEnabled || site.value.quic);
        },
        { immediate: true },
    );
    watch(tab, async () => {
        await nextTick();
        formElement.value?.$el?.closest(".el-dialog__body")?.scrollTo({ top: 0 });
    });
    function submit() {
        if (props.busy) return;
        validation.value = "";
        if (!multiple.value && !site.value.domains.trim()) {
            tab.value = "routing";
            validation.value = "请填写站点域名";
            return;
        }
        if (!site.value.httpEnabled && !site.value.httpsEnabled && !site.value.quic) {
            tab.value = "tls";
            validation.value = "至少启用 HTTP、HTTPS 或 QUIC 中的一种";
            return;
        }
        for (const [enabled, port, label] of [
            [site.value.httpEnabled, site.value.port, "HTTP"],
            [site.value.httpsEnabled, site.value.tlsPort, "SSL / HTTPS"],
            [site.value.quic, site.value.quicPort, "QUIC"],
        ] as const) {
            if (enabled && (!Number.isInteger(port) || port < 1 || port > 65535)) {
                tab.value = label === "HTTP" ? "routing" : "tls";
                validation.value = label + " 端口应为 1–65535";
                return;
            }
        }
        if (site.value.httpEnabled && site.value.httpsEnabled && site.value.port === site.value.tlsPort) {
            validation.value = "HTTP 与 HTTPS 不能共用同一个 TCP 端口";
            return;
        }
        if (site.value.tls && multiple.value && (!site.value.bindings.length || site.value.bindings.some((b: any) => !b.domains.trim()))) {
            tab.value = "tls";
            validation.value = "请填写每组证书的域名";
            return;
        }
        emit("save");
    }
</script>
<template><ElDialog
        :model-value="true"
        :title="revision ? '编辑站点' : '创建站点'"
        class="site-editor-dialog"
        width="min(960px, calc(100vw - 24px))"
        top="5vh"
        :show-close="!busy"
        :close-on-click-modal="false"
        :close-on-press-escape="!busy"
        append-to-body
        destroy-on-close
        @close="emit('close')"><ElAlert v-if="error || validation" :title="error || validation" type="error" :closable="false" show-icon class="site-editor-error" />
        <ElForm ref="formElement" :model="site" label-position="top" :disabled="busy" @submit.prevent="submit"><ElTabs v-model="tab" class="site-editor-tabs"><ElTabPane label="站点与路由" name="routing"><ElRow :gutter="16" ><ElCol :xs="24" :sm="12" ><ElFormItem label="域名" required><ElInput v-model="site.domains" aria-label="站点域名" placeholder="example.com www.example.com（空格分隔）" :disabled="multiple" /></ElFormItem></ElCol>
                        <ElCol :xs="24" :sm="12" ><ElFormItem class="site-http-field" label="HTTP 端口"><ElInputNumber v-model="site.port" aria-label="HTTP 监听端口" :disabled="!site.httpEnabled" :min="1" :max="65535" controls-position="right" />
                        <ElCheckbox v-model="site.httpEnabled">启用 HTTP</ElCheckbox></ElFormItem></ElCol></ElRow>
                    <NginxRouteBuilder v-if="!advanced" v-model="routes" />
                    <template v-else><ElAlert v-if="originalRoutingOnly" title="此站点包含自定义路由，请保留原始指令。" type="info" :closable="false" />
                        <ElFormItem label="路由配置"><ElInput v-model="site.routing" type="textarea" resize="none" :rows="12" class="code-editor" aria-label="路由配置" spellcheck="false" /></ElFormItem></template>
                    <ElButton v-if="!advanced" text type="primary" @click="emit('rawRouting')">切换为原始路由配置</ElButton>
                    <ElButton v-else-if="!originalRoutingOnly" text type="primary" @click="advanced = false">返回可视化路由</ElButton></ElTabPane>
                <ElTabPane label="HTTPS 与证书" name="tls"><ElRow :gutter="16" ><ElCol :xs="24" :sm="12" ><div><ElFormItem><ElCheckbox v-model="site.httpsEnabled">启用 HTTPS</ElCheckbox></ElFormItem>
                            <ElFormItem v-if="site.httpsEnabled" label="SSL / HTTPS 端口" required><ElInputNumber v-model="site.tlsPort" aria-label="SSL / HTTPS 端口" :min="1" :max="65535" controls-position="right" /></ElFormItem></div></ElCol>
                        <ElCol :xs="24" :sm="12" ><div><ElFormItem><ElCheckbox v-model="site.quic">启用 QUIC / HTTP/3</ElCheckbox></ElFormItem>
                            <ElFormItem v-if="site.quic" label="QUIC / UDP 端口" required><ElInputNumber v-model="site.quicPort" aria-label="QUIC / UDP 端口" :min="1" :max="65535" controls-position="right" /></ElFormItem></div></ElCol></ElRow>
                    <template v-if="site.tls"><ElFormItem><ElCheckbox v-model="multiple" @change="multiple && !site.bindings.length && emit('addBinding')">按域名绑定多个证书</ElCheckbox></ElFormItem>
                        <template v-if="multiple"><ElCard v-for="(binding, index) in (site.bindings as {id:string;domains:string}[])" :key="binding.id" shadow="never" class="site-certificate-card"><template #header><div class="site-binding-heading"><b>证书绑定 {{ index + 1 }}</b>
                                        <ElButton text type="danger" @click="site.bindings.splice(index, 1)">移除此绑定</ElButton></div></template>
                                <ElFormItem label="绑定域名" required><ElInput v-model="binding.domains" :aria-label="'证书绑定域名 ' + (index + 1)" /></ElFormItem>
                                <CertificateSelection :target="binding" :saved="!!revision" @select="emit('certificate', $event)" /></ElCard>
                            <ElButton @click="emit('addBinding')">添加域名与证书绑定</ElButton></template>
                        <template v-else><CertificateSelection :target="site" :saved="!!revision" @select="emit('certificate', $event)" /></template></template></ElTabPane>
                <ElTabPane label="高级配置" name="custom"><ElFormItem label="自定义 server 指令 · custom.conf"><ElInput v-model="site.custom" type="textarea" resize="none" :rows="14" class="code-editor" placeholder="例如 client_max_body_size 50m;" spellcheck="false" /></ElFormItem></ElTabPane></ElTabs></ElForm>
        <template #footer><ElButton :disabled="busy" @click="emit('close')">取消</ElButton>
            <ElButton type="primary" :loading="busy" @click="submit">{{saveStage || "检查并保存"}}</ElButton></template></ElDialog></template>
