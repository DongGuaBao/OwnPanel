<script setup lang="ts">
    import { ElFormItem, ElButton, ElInput } from "element-plus";
    import { Plus } from "lucide-vue-next";
    import { newRoute, type RouteForm } from "./nginx-routes";
    import NginxLocationEditor from "./NginxLocationEditor.vue";
    const model = defineModel<RouteForm>({ required: true });
</script>
<template><div class="route-builder"><ElFormItem label="站点默认目录（root）"><ElInput v-model="model.root" aria-label="站点默认目录" placeholder="/var/www/html" /></ElFormItem>
        <NginxLocationEditor v-for="(route, i) in model.locations" :key="i" :route="route" :index="i" @remove="model.locations.splice(i, 1)" />
        <ElButton @click="model.locations.push(newRoute())"><Plus :size="15" />
            添加路由
        </ElButton></div></template>
