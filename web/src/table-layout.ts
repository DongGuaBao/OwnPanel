import { ref, type Ref } from "vue";

export type TableColumn = {
    key: string;
    label: string;
    value?: (row: any, index: number) => unknown;
    width?: number;
    minWidth?: number;
    fixed?: "left" | "right";
    sortable?: boolean;
    className?: string;
    showOverflowTooltip?: boolean;
};
export type TableLayout = { order: string[]; widths: Record<string, number>; sort: { key: string; direction: "ascending" | "descending" } | null };
const layouts = new Map<string, Ref<TableLayout>>();
export function cleanTableLayout(input: unknown): TableLayout {
    const data = input && typeof input === "object" ? input as Partial<TableLayout> : {};
    const widths: Record<string, number> = {};
    for (const [key, value] of Object.entries(data.widths || {})) {
        if (typeof value === "number" && Number.isFinite(value)) widths[key] = Math.max(44, Math.min(1600, Math.round(value)));
    }
    const sort = data.sort;
    return {
        order: Array.isArray(data.order) ? [...new Set(data.order.filter((key): key is string => typeof key === "string"))] : [],
        widths,
        sort: sort && typeof sort.key === "string" && ["ascending", "descending"].includes(sort.direction) ? sort : null,
    };
}
export function useTableLayout(id: string) {
    const key = "ownpanel-table-v1:" + id;
    if (!layouts.has(key)) {
        let saved: unknown;
        try { saved = JSON.parse(localStorage.getItem(key) || "null"); } catch {}
        layouts.set(key, ref(cleanTableLayout(saved)));
    }
    const state = layouts.get(key)!;
    function save(next: TableLayout) {
        state.value = cleanTableLayout(next);
        try { localStorage.setItem(key, JSON.stringify(state.value)); } catch {}
    }
    return { state, save };
}
const collator = new Intl.Collator("zh-CN", { numeric: true, sensitivity: "base" });
export function compareTableValues(a: unknown, b: unknown): number {
    if (a == null || a === "") return b == null || b === "" ? 0 : 1;
    if (b == null || b === "") return -1;
    if (typeof a === "number" && typeof b === "number") return (Number.isFinite(a) ? a : 0) - (Number.isFinite(b) ? b : 0);
    if (typeof a === "boolean" && typeof b === "boolean") return Number(a) - Number(b);
    return collator.compare(String(a), String(b));
}
