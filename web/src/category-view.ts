export type CategoryTab = { id: string; label: string };
export function readCategoryView(key: string): { selected: string; order: string[] } {
    try {
        const value = JSON.parse(localStorage.getItem(key) || "{}");
        return { selected: typeof value.selected === "string" ? value.selected : "", order: Array.isArray(value.order) ? value.order.filter((id: unknown) => typeof id === "string") : [] };
    } catch {
        return { selected: "", order: [] };
    }
}
export function orderedCategoryTabs(tabs: CategoryTab[], order: string[]) {
    const rank = new Map(order.map((id, index) => [id, index]));
    return tabs.map((tab, index) => ({ ...tab, index })).sort((a, b) => (rank.get(a.id) ?? order.length + a.index) - (rank.get(b.id) ?? order.length + b.index));
}
export function saveCategoryView(key: string, selected: string, order: string[]) {
    try {
        localStorage.setItem(key, JSON.stringify({ selected, order }));
    } catch {
        /* Preferences must not block navigation. */
    }
}
