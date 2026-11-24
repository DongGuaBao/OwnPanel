<script setup lang="ts">
    import { ElDialog, ElTabs, ElTabPane, ElInput, ElButton, ElAlert } from "element-plus";
    const props = defineProps<{ site: string; files: Record<string, string>; busy: boolean; error: string }>();
    const tab = defineModel<string>("tab", { required: true });
    const emit = defineEmits<{ close: []; save: [] }>();
</script>
<template><ElDialog
        :model-value="true"
        :title="site + ' / 配置文件'"
        class="site-config-dialog"
        width="min(1120px, calc(100vw - 24px))"
        top="5vh"
        append-to-body
        :close-on-click-modal="false"
        :show-close="!busy"
        :close-on-press-escape="!busy"
        @close="emit('close')"><ElAlert v-if="error" :title="error" type="error" :closable="false" />
        <ElTabs v-model="tab"><ElTabPane v-for="(_, name) in files" :key="name" :name="String(name)" :label="String(name)" /></ElTabs>
        <ElInput v-model="files[tab]" type="textarea" resize="none" :disabled="busy" class="site-config-textarea" aria-label="站点配置正文" spellcheck="false" />
        <template #footer><ElButton :disabled="busy" @click="emit('close')">关闭</ElButton>
            <ElButton type="primary" :loading="busy" @click="emit('save')">检查并应用当前文件</ElButton></template></ElDialog></template>
