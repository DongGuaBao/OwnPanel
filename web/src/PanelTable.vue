<script setup lang="ts">
import { computed, nextTick, normalizeClass, onMounted, onUnmounted, ref, watch } from "vue";
import { ElTable, ElTableColumn, ElDropdown, ElDropdownMenu, ElDropdownItem, type TableInstance } from "element-plus";
import { compareTableValues, useTableLayout, type TableColumn } from "./table-layout";
import { useTabDrag } from "./tab-drag";

const props = withDefaults(defineProps<{
    tableId: string;
    rows: any[];
    columns: TableColumn[];
    rowKey?: string | ((row: any) => string | number);
    rowClassName?: (row: any) => unknown;
    groupBy?: (row: any) => number;
}>(), { rowKey: "id" });
const emit = defineEmits<{
    rowClick: [row: any, column: unknown, event: MouseEvent];
    rowDblclick: [row: any, column: unknown, event: MouseEvent];
    rowContextmenu: [row: any, column: unknown, event: MouseEvent];
    viewOrderChange: [rows: any[]];
}>();
defineSlots<{ [name: string]: (scope: any) => any }>();
const { state, save } = useTableLayout(props.tableId);
const table = ref<TableInstance>(), dropTarget = ref("");
const beginDrag = useTabDrag();
const tableWidth = ref(0), dialogTable = ref(false);
let observer: ResizeObserver | undefined;
onMounted(() => {
    dialogTable.value = !!table.value?.$el.closest(".el-dialog");
    observer = new ResizeObserver(([entry]) => { tableWidth.value = entry.contentRect.width; });
    if (table.value?.$el) observer.observe(table.value.$el);
});
onUnmounted(() => observer?.disconnect());
const columnOrder = computed(() => {
    const movable = props.columns.filter((column) => !column.fixed);
    const rank = new Map(state.value.order.map((key, index) => [key, index]));
    movable.sort((a, b) => (rank.get(a.key) ?? 10000) - (rank.get(b.key) ?? 10000));
    return [...props.columns.filter((column) => column.fixed === "left"), ...movable, ...props.columns.filter((column) => column.fixed === "right")];
});
const indices = computed(() => new Map(props.rows.map((row, index) => [row, index])));
const keyOf = (row: any) => String(typeof props.rowKey === "function" ? props.rowKey(row) : row[props.rowKey]);
const indexOf = (row: any) => indices.value.get(row) ?? 0;
const sortedRows = computed(() => {
    const sort = state.value.sort, column = props.columns.find((item) => item.key === sort?.key && item.sortable !== false);
    if (!sort || !column?.value) return props.rows;
    return [...props.rows].sort((a, b) => {
        const group = props.groupBy ? props.groupBy(a) - props.groupBy(b) : 0;
        if (group) return group;
        const av = column.value!(a, indexOf(a)), bv = column.value!(b, indexOf(b));
        if (av == null || av === '') return bv == null || bv === '' ? indexOf(a) - indexOf(b) : 1;
        if (bv == null || bv === '') return -1;
        const comparison = compareTableValues(av, bv);
        return (sort.direction === "descending" ? -comparison : comparison) || indexOf(a) - indexOf(b);
    });
});
watch(sortedRows, (rows) => emit("viewOrderChange", rows), { immediate: true, flush: "post" });
function changeSort({ prop, order }: { prop: string | null; order: "ascending" | "descending" | null }) {
    if (!prop) order = null;
    if ((state.value.sort?.key || undefined) === (order ? prop : undefined) && (state.value.sort?.direction || null) === order) return;
    save({ ...state.value, sort: order && prop ? { key: prop, direction: order } : null });
}
// Keep other mounted panes in sync with shared preferences, including floating files.
watch(() => state.value.sort, async (sort) => {
    await nextTick();
    const current = table.value?.store.states;
    if (current?.sortProp.value === sort?.key && current?.sortOrder.value === sort?.direction) return;
    if (sort) table.value?.sort(sort.key, sort.direction);
    else table.value?.clearSort();
});
function resize(width: number, _oldWidth: number, column: { columnKey: string }) {
    save({ ...state.value, widths: { ...state.value.widths, [column.columnKey]: width } });
}
function reset() {
    save({ order: [], widths: {}, sort: null });
    table.value?.clearSort();
}
function dragColumn(event: PointerEvent, key: string) {
    beginDrag(event, {
        move(point) {
            const target = document.elementsFromPoint(point.x, point.y)
                .map((element) => element.closest<HTMLElement>("[data-table-column]") || element.closest("th")?.querySelector<HTMLElement>("[data-table-column]"))
                .find((element) => element?.dataset.tableId === props.tableId);
            const candidate = props.columns.find((item) => item.key === target?.dataset.tableColumn);
            dropTarget.value = candidate && !candidate.fixed && candidate.key !== key ? candidate.key : "";
        },
        end() {
            if (dropTarget.value) {
                const order = columnOrder.value.filter((column) => !column.fixed).map((column) => column.key);
                const from = order.indexOf(key), to = order.indexOf(dropTarget.value);
                if (from >= 0 && to >= 0) { order.splice(from, 1); order.splice(to, 0, key); save({ ...state.value, order }); }
            }
            dropTarget.value = "";
        },
        cancel() { dropTarget.value = ""; },
    });
}
</script>

<template>
    <ElTable ref="table" class="panel-table" :data-table-id="tableId" :data="sortedRows" :row-key="keyOf" border height="100%" :max-height="dialogTable ? 'calc(100dvh - 320px)' : undefined"
        :default-sort="state.sort ? { prop: state.sort.key, order: state.sort.direction } : undefined"
        :row-class-name="({ row }) => normalizeClass(rowClassName?.(row))"
        @sort-change="changeSort" @header-dragend="resize"
        @row-click="(row,column,event)=>emit('rowClick',row,column,event)"
        @row-dblclick="(row,column,event)=>emit('rowDblclick',row,column,event)"
        @row-contextmenu="(row,column,event)=>emit('rowContextmenu',row,column,event)">
        <ElTableColumn v-for="column in columnOrder" :key="column.key" :column-key="column.key" :prop="column.key"
            :label="column.label" :align="column.key === 'selection' ? 'center' : 'left'" :sortable="column.sortable !== false && !!column.value ? 'custom' : false"
            :width="state.widths[column.key] || column.width" :min-width="column.minWidth || 120"
            :show-overflow-tooltip="column.showOverflowTooltip" :fixed="column.fixed === 'right' && tableWidth < 700 ? false : column.fixed" :class-name="column.className" :resizable="true">
            <template #header>
                <slot :name="'header-'+column.key">
                    <ElDropdown trigger="contextmenu" @command="reset">
                        <span :data-table-column="column.key" :data-table-id="tableId"
                            class="table-column-label" :class="{ 'table-column-drop': dropTarget === column.key, 'table-column-movable': !column.fixed }"
                            @pointerdown="!column.fixed && dragColumn($event,column.key)">{{ column.label }}</span>
                        <template #dropdown><ElDropdownMenu><ElDropdownItem command="reset">恢复表格默认设置</ElDropdownItem></ElDropdownMenu></template>
                    </ElDropdown>
                </slot>
            </template>
            <template #default="{ row }"><slot :name="column.key" :row="row" :index="indexOf(row)">{{ column.value?.(row,indexOf(row)) ?? '—' }}</slot></template>
        </ElTableColumn>
        <template #empty><slot name="empty">暂无数据</slot></template>
    </ElTable>
</template>
