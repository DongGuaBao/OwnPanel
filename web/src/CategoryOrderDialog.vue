<script setup lang="ts">
    import { ref, watch } from "vue";
    import { ElDialog, ElButton } from "element-plus";
    import { GripVertical, ArrowUp, ArrowDown } from "lucide-vue-next";
    const props = defineProps<{ items: { id: string; label: string }[]; busy: boolean; error: string }>();
    const emit = defineEmits<{ close: []; save: [ids: string[]] }>();
    const items = ref([...props.items]),
        dragged = ref("");
    watch(
        () => props.items,
        (value) => {
            items.value = [...value];
        },
    );
    function startDrag(event: DragEvent, id: string) {
        dragged.value = id;
        if (event.dataTransfer) {
            event.dataTransfer.effectAllowed = "move";
            event.dataTransfer.setData("text/plain", id);
        }
    }
    function move(from: number, to: number) {
        if (props.busy || from < 0 || to < 0 || to >= items.value.length) return;
        const next = [...items.value];
        next.splice(to, 0, next.splice(from, 1)[0]);
        items.value = next;
    }
</script>
<template><ElDialog :model-value="true" title="分类排序" width="min(480px, 94vw)" append-to-body :close-on-click-modal="false" :show-close="!busy" :close-on-press-escape="!busy" @close="emit('close')"><div class="category-order-list"><div
                v-for="(item, index) in items"
                :key="item.id"
                class="category-order-row"
                :draggable="!busy"
                @dragstart="startDrag($event, item.id)"
                @dragover.prevent
                @drop.prevent="
                    move(
                        items.findIndex((value) => value.id === dragged),
                        index,
                    )
                "
                @dragend="dragged = ''"><GripVertical :size="16" />
                <span>{{ item.label }}</span>
                <ElButton :disabled="busy || index === 0" :aria-label="'上移 ' + item.label" @click="move(index, index - 1)"><ArrowUp :size="14" /></ElButton>
                <ElButton :disabled="busy || index === items.length - 1" :aria-label="'下移 ' + item.label" @click="move(index, index + 1)"><ArrowDown :size="14" /></ElButton></div></div>
        <p v-if="error" class="form-error">{{ error }}</p>
        <template #footer><ElButton :disabled="busy" @click="emit('close')">取消</ElButton>
            <ElButton
                type="primary"
                :loading="busy"
                @click="
                    emit(
                        'save',
                        items.map((item) => item.id),
                    )
                ">
                保存排序
            </ElButton></template></ElDialog></template>
