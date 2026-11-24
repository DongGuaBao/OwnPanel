<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import SystemdManager from './SystemdManager.vue';

type Api = (url: string, method?: string, body?: unknown, onFresh?: (value: any) => void) => Promise<any>;
const props = defineProps<{api: Api; writable: boolean; refreshSignal?: number; nodeId?: string; sshUser?: string}>();
const systemScope = ':system';
const selection = ref(systemScope);
const account = computed(() => selection.value === systemScope ? '' : selection.value);
const users = ref<string[]>([]);
const loading = ref(false);
const error = ref('');
let generation = 0;

async function loadUsers(refresh = false) {
  const turn = ++generation;
  loading.value = true;
  error.value = '';
  try {
    const apply = (result: {users?: string[]}) => {
      if (turn === generation && Array.isArray(result.users)) users.value = result.users;
    };
    const result = await props.api('/systemd/users', refresh ? 'REFRESH' : 'GET', undefined, apply);
    if (!Array.isArray(result.users)) throw new Error('请更新 Agent 后查看用户服务');
    apply(result);
  } catch (err) {
    if (turn === generation) error.value = (err as Error).message;
  } finally {
    if (turn === generation) loading.value = false;
  }
}

const scopedApi = computed<Api>(() => {
  const user = account.value;
  return (url, method, body, onFresh) => {
    let target = url;
    if (user) {
      if (/^\/systemd\/(categories|preferences)(?:\?|$)/.test(url)) {
        target += (url.includes('?') ? '&' : '?') + 'user=' + encodeURIComponent(user);
      } else {
        target = '/systemd/user/' + encodeURIComponent(user) + url.slice('/systemd'.length);
      }
    }
    return props.api(target, method, body, onFresh);
  };
});

onMounted(() => void loadUsers());
watch(() => props.refreshSignal, () => void loadUsers(true));
</script>

<template>
  <SystemdManager :key="account" :node-id="nodeId" :account="account" :refresh-signal="refreshSignal" :api="scopedApi" :writable="writable || (!!account && account === sshUser)">
    <template #manager="{ locked }">
      <ElSpace wrap>
        <ElSelect v-model="selection" aria-label="Systemd 服务作用域" :disabled="locked" :loading="loading" class="systemd-manager-select">
          <ElOption :value="systemScope" label="系统服务" />
          <ElOption v-for="user in users" :key="user" :value="user" :label="'用户服务 · ' + user" />
          <ElOption v-if="!users.length" value=":unavailable" :label="error ? '用户服务读取失败' : '无运行中的用户实例'" disabled />
        </ElSelect>
        <ElTooltip v-if="error" :content="error"><ElButton text @click="loadUsers(true)">重试读取用户</ElButton></ElTooltip>
      </ElSpace>
    </template>
  </SystemdManager>
</template>
