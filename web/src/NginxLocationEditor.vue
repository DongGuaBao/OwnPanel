<script setup lang="ts">
    import { ElFormItem, ElCard, ElButton, ElInput, ElInputNumber, ElSelect, ElOption, ElCheckbox } from "element-plus";
    import { Plus, Trash2 } from "lucide-vue-next";
    import type { RouteForm } from "./nginx-routes";
    defineProps<{ route: RouteForm["locations"][number]; index: number }>();
    const emit = defineEmits<{ remove: [] }>();
</script>
<template>
    <ElCard shadow="never" class="route-card"><template #header><div class="route-card-heading"><b>
                        路由 {{ index + 1 }}
                        <span>{{ route.path || "/" }}</span></b>
                    <ElButton text type="danger" :aria-label="'删除路由 ' + (index + 1)" @click="emit('remove')"><Trash2 :size="14" />
                        删除
                    </ElButton></div></template>
            <ElRow :gutter="16" ><ElCol :xs="24" :sm="12" ><ElFormItem label="匹配方式"><ElSelect v-model="route.match" placeholder="前缀匹配" :aria-label="'路由匹配方式 ' + (index + 1)"><ElOption value="" label="前缀匹配" />
                        <ElOption value="=" label="精确匹配" />
                        <ElOption value="^~" label="优先前缀" />
                        <ElOption value="~" label="正则" />
                        <ElOption value="~*" label="正则（忽略大小写）" /></ElSelect></ElFormItem></ElCol>
                <ElCol :xs="24" :sm="12" ><ElFormItem label="路径或正则"><ElInput v-model="route.path" :aria-label="'路由路径 ' + (index + 1)" placeholder="/api/" /></ElFormItem></ElCol>
                <ElCol :xs="24" :sm="12" ><ElFormItem label="处理方式"><ElSelect v-model="route.handler" :aria-label="'路由处理方式 ' + (index + 1)"><ElOption value="static" label="静态文件" />
                        <ElOption value="proxy" label="反向代理" />
                        <ElOption value="return" label="返回 / 跳转" /></ElSelect></ElFormItem></ElCol>
                <template v-if="route.handler === 'static'"><ElCol :xs="24" :sm="12" ><ElFormItem label="目录映射"><ElSelect v-model="route.directoryMode"><ElOption value="inherit" label="使用站点默认目录" />
                            <ElOption value="root" label="指定 root" />
                            <ElOption value="alias" label="指定 alias" /></ElSelect></ElFormItem></ElCol>
                    <ElCol :xs="24" :sm="12" v-if="route.directoryMode !== 'inherit'"><ElFormItem  label="目录"><ElInput v-model="route.directory" placeholder="/srv/website/" /></ElFormItem></ElCol>
                    <ElCol :xs="24" :sm="12" ><ElFormItem label="首页文件"><ElInput v-model="route.index" placeholder="index.html" /></ElFormItem></ElCol>
                    <ElCol :xs="24" :sm="12" ><ElFormItem class="route-checkbox"><ElCheckbox v-model="route.spa">找不到文件时回退到 /index.html</ElCheckbox></ElFormItem></ElCol></template>
                <ElCol :xs="24" :sm="12" v-else-if="route.handler === 'proxy'"><ElFormItem  label="上游地址"><ElInput v-model="route.upstream" :aria-label="'路由上游地址 ' + (index + 1)" placeholder="http://127.0.0.1:3000" /></ElFormItem></ElCol>
                <template v-else><ElCol :xs="24" :sm="12" ><ElFormItem label="HTTP 状态码"><ElInputNumber v-model="route.status" :min="200" :max="599" controls-position="right" /></ElFormItem></ElCol>
                    <ElCol :xs="24" :sm="12" ><ElFormItem label="跳转地址或响应正文"><ElInput v-model="route.response" placeholder="https://example.com/" /></ElFormItem></ElCol></template></ElRow>
            <div v-if="route.handler !== 'return'" class="route-access"><div v-for="(rule, j) in route.access" :key="j" class="route-access-row"><ElSelect v-model="rule.action" aria-label="访问规则动作"><ElOption value="allow" label="允许" />
                        <ElOption value="deny" label="拒绝" /></ElSelect>
                    <ElInput v-model="rule.address" aria-label="访问来源" placeholder="all、IP 或 CIDR" />
                    <ElButton text type="danger" aria-label="删除访问规则" @click="route.access.splice(j, 1)"><Trash2 :size="15" /></ElButton></div>
                <ElButton text type="primary" @click="route.access.push({ action: 'allow', address: 'all' })"><Plus :size="14" />
                    添加访问规则
                </ElButton>
                <small v-if="route.access.length">按顺序匹配，第一条命中生效。</small></div></ElCard>
</template>
