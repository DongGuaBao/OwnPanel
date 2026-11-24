<script setup lang="ts">
    import { ref } from "vue";
    import { ElButton, ElDescriptions, ElDescriptionsItem, ElInput, ElFormItem, ElAlert } from "element-plus";
    const props = defineProps<{ target: any; saved?: boolean }>();
    const emit = defineEmits<{ select: [target: any] }>();
    const manual = ref(false);
    const date = (value: string) => (value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "—");
</script>
<template><div class="certificate-selection"><ElDescriptions v-if="target.certificateInfo" :column="1" border><ElDescriptionsItem label="当前证书">{{ target.certificateInfo.name || target.certificateInfo.domains?.join(" · ") || "站点证书" }}</ElDescriptionsItem>
            <ElDescriptionsItem label="域名">{{ target.certificateInfo.domains?.join(" · ") }}</ElDescriptionsItem>
            <ElDescriptionsItem label="颁发者">{{ target.certificateInfo.issuer }}</ElDescriptionsItem>
            <ElDescriptionsItem label="到期时间">{{ date(target.certificateInfo.notAfter) }}</ElDescriptionsItem></ElDescriptions>
        <ElAlert
            v-else
            :title="
                target.certificate ? '已填写证书，保存时校验' : saved ? '已部署证书，留空会继续使用' + (target.certificateExpiry ? '，到期：' + date(target.certificateExpiry) : '') : '尚未选择证书'
            "
            type="info"
            :closable="false" />
        <ElSpace wrap class="certificate-selection-actions"><ElButton type="primary" plain @click="emit('select', target)">从证书夹选择</ElButton>
            <ElButton text @click="manual = !manual">{{ manual ? "收起 PEM 输入" : "手动输入 PEM" }}</ElButton></ElSpace>
        <ElRow :gutter="16" v-if="manual" ><ElCol :xs="24" :sm="12" ><ElFormItem label="证书链 PEM（留空保留）"><ElInput type="textarea" v-model="target.certificate" :rows="6" resize="none" aria-label="站点证书链" spellcheck="false" /></ElFormItem></ElCol>
            <ElCol :xs="24" :sm="12" ><ElFormItem label="私钥 PEM（留空保留）"><ElInput type="textarea" v-model="target.privateKey" :rows="6" resize="none" aria-label="站点私钥" spellcheck="false" /></ElFormItem></ElCol></ElRow></div></template>
