import { onMounted, onUnmounted, nextTick, type Ref } from "vue";
import type { TabsInstance } from "element-plus";

export type DragPoint = { x: number; y: number };
export function overTabBar(element: Element | null, point: DragPoint) {
    if (!element?.getClientRects().length) return false;
    const box = element.getBoundingClientRect();
    return point.x >= box.left - 8 && point.x <= box.right + 8 && point.y >= box.top - 8 && point.y <= box.bottom + 8;
}

// Pointer events work for both mouse and touch; a short click still selects a tab.
export function useTabDrag() {
    let stop: (() => void) | undefined;
    onUnmounted(() => stop?.());
    const begin = (event: PointerEvent, handlers: {
        move: (point: DragPoint, delta: DragPoint) => void;
        end?: (point: DragPoint) => void;
        cancel?: () => void;
    }) => {
        if (event.button !== 0 || (event.target as Element).closest("button, .is-icon-close")) return;
        stop?.();
        const start = { x: event.clientX, y: event.clientY }, pointer = event.pointerId;
        let moved = false;
        const point = (e: PointerEvent) => ({ x: e.clientX, y: e.clientY });
        const move = (e: PointerEvent) => {
            if (e.pointerId !== pointer) return;
            const delta = { x: e.clientX - start.x, y: e.clientY - start.y };
            if (!moved && Math.hypot(delta.x, delta.y) < 8) return;
            moved = true;
            e.preventDefault();
            handlers.move(point(e), delta);
        };
        const cleanup = () => {
            window.removeEventListener("pointermove", move);
            window.removeEventListener("pointerup", end);
            window.removeEventListener("pointercancel", cancel);
            window.removeEventListener("blur", cancel);
            stop = undefined;
        };
        const end = (e: PointerEvent) => {
            if (e.pointerId !== pointer) return;
            cleanup();
            if (moved) {
                // A drag must not also activate or close the tab under the release.
                const suppress = (click: MouseEvent) => { click.preventDefault(); click.stopImmediatePropagation(); };
                document.addEventListener("click", suppress, { capture: true, once: true });
                setTimeout(() => document.removeEventListener("click", suppress, true), 0);
                handlers.end?.(point(e));
            }
        };
        const cancel = () => { cleanup(); handlers.cancel?.(); };
        stop = cancel;
        window.addEventListener("pointermove", move, { passive: false });
        window.addEventListener("pointerup", end);
        window.addEventListener("pointercancel", cancel);
        window.addEventListener("blur", cancel);
    };
    begin.cancel = () => stop?.();
    return begin;
}

// Keep the active drag handle reachable after resizing or showing a hidden page.
export function useActiveTabVisibility(tabs: Ref<TabsInstance | undefined>) {
    let observer: ResizeObserver | undefined, frame = 0;
    onMounted(() => {
        observer = new ResizeObserver(() => {
            cancelAnimationFrame(frame);
            // Element Plus measures overflow in rAF; scroll after that measurement.
            frame = requestAnimationFrame(() => {
                frame = requestAnimationFrame(() => { void nextTick(() => { const nav = tabs.value?.tabNavRef; if (typeof nav?.scrollToActiveTab === "function") void nav.scrollToActiveTab(); }); });
            });
        });
        if (tabs.value?.$el) observer.observe(tabs.value.$el);
    });
    onUnmounted(() => { observer?.disconnect(); cancelAnimationFrame(frame); });
}
