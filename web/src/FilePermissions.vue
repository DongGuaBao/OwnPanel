<script setup lang="ts">
    import { ref, onMounted, onUnmounted, computed } from "vue";
    import { ElDialog, ElForm, ElFormItem, ElInput, ElSelect, ElOption, ElCheckbox, ElButton, ElAlert } from "element-plus";
    const props = defineProps<{
        entry: { name: string; mode: string; uid: number; gid: number; directory: boolean; symlink?: boolean };
        path: string;
        api: (url: string, method?: string, body?: unknown) => Promise<any>;
    }>();
    const emit = defineEmits<{ close: []; saved: [] }>();
    const mode = ref(props.entry.mode),
        uid = ref(String(props.entry.uid)),
        gid = ref(String(props.entry.gid)),
        recursive = ref(false),
        busy = ref(false),
        loading = ref(true),
        error = ref(""),
        accountError = ref("");
    const users = ref<{ name: string; uid: number; gid: number }[]>([]),
        groups = ref<{ name: string; gid: number }[]>([]);
    let disposed = false;
    const validMode = computed(() => /^[0-7]{3,4}$/.test(mode.value));
    const columns = [
            { label: "所有者", shift: 6 },
            { label: "用户组", shift: 3 },
            { label: "其他人", shift: 0 },
        ],
        rights = [
            { label: "读取", bit: 4 },
            { label: "写入", bit: 2 },
            { label: "执行", bit: 1 },
        ];
    function checked(bit: number) {
        return validMode.value && !!(parseInt(mode.value, 8) & bit);
    }
    function toggle(bit: number, value: unknown) {
        const next = parseInt(mode.value, 8) || 0;
        mode.value = (value ? next | bit : next & ~bit).toString(8).padStart(4, "0");
    }
    function ownerChanged(value: unknown) {
        const user = users.value.find((item) => String(item.uid) === value);
        if (user) gid.value = String(user.gid);
    }
    async function save() {
        error.value = "";
        if (!validMode.value) {
            error.value = "请输入 3 或 4 位八进制权限";
            return;
        }
        if (!/^\d+$/.test(uid.value) || !/^\d+$/.test(gid.value) || Number(uid.value) > 2147483647 || Number(gid.value) > 2147483647) {
            error.value = "请选择账号和用户组，或输入有效的数字 UID/GID";
            return;
        }
        busy.value = true;
        try {
            await props.api("/files/action", "POST", { action: "permissions", path: props.path, mode: mode.value, uid: Number(uid.value), gid: Number(gid.value), recursive: recursive.value });
            if (!disposed) emit("saved");
        } catch (e) {
            if (!disposed) error.value = (e as Error).message;
        } finally {
            if (!disposed) busy.value = false;
        }
    }
    onMounted(async () => {
        try {
            const result = await props.api("/files/identities");
            if (!disposed) {
                users.value = result.users;
                groups.value = result.groups;
            }
        } catch {
            if (!disposed) accountError.value = "账号列表读取失败，可输入数字 UID/GID";
        } finally {
            if (!disposed) loading.value = false;
        }
    });
    onUnmounted(() => {
        disposed = true;
    });
</script>
<template><ElDialog
        :model-value="true"
        :title="entry.name + ' · 权限与属主'"
        width="min(640px, 94vw)"
        append-to-body
        :close-on-click-modal="false"
        :close-on-press-escape="!busy"
        :show-close="!busy"
        @close="emit('close')"><ElAlert v-if="error" :title="error" type="error" :closable="false" />
        <ElForm label-position="top" @submit.prevent="save"><div class="file-permission-grid"><fieldset v-for="column in columns" :key="column.label"><legend>{{ column.label }}</legend>
                    <ElCheckbox
                        v-for="right in rights"
                        :key="right.bit"
                        :disabled="busy"
                        :aria-label="column.label + right.label"
                        :model-value="checked(right.bit << column.shift)"
                        @change="toggle(right.bit << column.shift, $event)">{{ right.label }}</ElCheckbox></fieldset></div>
            <ElRow :gutter="16" ><ElCol :xs="24" :sm="8" ><ElFormItem label="数字权限"><ElInput v-model="mode" aria-label="数字权限" :disabled="busy" maxlength="4" /></ElFormItem></ElCol>
                <ElCol :xs="24" :sm="8" ><ElFormItem label="所有者"><ElSelect v-model="uid" aria-label="文件所有者" filterable allow-create default-first-option :loading="loading" :disabled="busy" @change="ownerChanged"><ElOption v-if="!users.some((user) => String(user.uid) === uid)" :value="uid" :label="'UID ' + uid" />
                        <ElOption v-for="user in users" :key="user.name" :value="String(user.uid)" :label="user.name + ' (' + user.uid + ')'" /></ElSelect></ElFormItem></ElCol>
                <ElCol :xs="24" :sm="8" ><ElFormItem label="用户组"><ElSelect v-model="gid" aria-label="文件用户组" filterable allow-create default-first-option :loading="loading" :disabled="busy"><ElOption v-if="!groups.some((group) => String(group.gid) === gid)" :value="gid" :label="'GID ' + gid" />
                        <ElOption v-for="group in groups" :key="group.name" :value="String(group.gid)" :label="group.name + ' (' + group.gid + ')'" /></ElSelect></ElFormItem></ElCol></ElRow>
            <ElFormItem><ElSpace wrap><ElCheckbox
                    v-for="item in [
                        { label: 'SetUID', bit: 2048 },
                        { label: 'SetGID', bit: 1024 },
                        { label: 'Sticky', bit: 512 },
                    ]"
                    :key="item.bit"
                    :disabled="busy"
                    :model-value="checked(item.bit)"
                    @change="toggle(item.bit, $event)">{{ item.label }}</ElCheckbox></ElSpace></ElFormItem>
            <ElCheckbox v-if="entry.directory && !entry.symlink" v-model="recursive" :disabled="busy || path === '/'">应用到目录及所有子目录、文件</ElCheckbox>
            <p v-if="accountError" class="form-error">{{ accountError }}</p></ElForm>
        <template #footer><ElButton :disabled="busy" @click="emit('close')">取消</ElButton>
            <ElButton type="primary" :loading="busy" @click="save">应用</ElButton></template></ElDialog></template>
