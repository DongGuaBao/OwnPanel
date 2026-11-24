// Only inventory/status responses are cached. Logs, terminal data, credentials
// and file contents always use a fresh request.
type Entry = { time: number; value: any };
const entries = new Map<string, Entry>(),
    pending = new Map<string, Promise<any>>(),
    listeners = new Map<string, Set<(value: any) => void>>();
const storageKey = "ownpanel-read-cache-v1";
const versions = new Map<string, number>();
const copy = (value: any) => structuredClone(value);
function allowed(url: string) {
    if (/[?&]view=activity(?:&|$)/.test(url)) return false;
    return /^\/api\/nodes(?:$|\/[^/]+\/(?:status|systemd(?:\/(?:preferences|users|user\/[^/?]+))?|nginx\/(?:sites|existing)|files|firewall|iptables|metrics\/history)(?:\?|$))/.test(url);
}
try {
    const saved = JSON.parse(sessionStorage.getItem(storageKey) || "[]");
    for (const [url, item] of saved) {
        if (allowed(url) && Date.now() - item.time < 24 * 60 * 60 * 1000) entries.set(url, item);
    }
} catch {}
function persist() {
    try {
        const saved = [...entries].slice(-60).map(([url, item]) => {
            if (!url.endsWith('/nginx/sites')) return [url, item];
            const sites = item.value.sites.map(({ files, ...site }: Record<string, unknown>) => site);
            return [url, { ...item, value: { ...item.value, sites } }];
        });
        sessionStorage.setItem(storageKey, JSON.stringify(saved));
    } catch {}
}
let snapshotPending = false;
function flushSnapshot() {
    if (!snapshotPending) return;
    snapshotPending = false;
    persist();
}
function scheduleSnapshot() {
    if (snapshotPending) return;
    snapshotPending = true;
    if (typeof window === 'undefined') flushSnapshot();
    else if (window.requestIdleCallback) window.requestIdleCallback(flushSnapshot, { timeout: 1500 });
    else window.setTimeout(flushSnapshot, 100);
}
if (typeof window !== 'undefined') window.addEventListener('pagehide', flushSnapshot);

export function clearReadCache(prefix?: string, exact = false) {
    const matches = (url: string) => !prefix || (exact ? url === prefix : url.startsWith(prefix));
    for (const url of new Set([...versions.keys(), ...entries.keys(), ...pending.keys()]))
        if (matches(url)) {
            versions.set(url, (versions.get(url) || 0) + 1);
            entries.delete(url);
            pending.delete(url);
            listeners.delete(url);
        }
    if (prefix) scheduleSnapshot();
    else { snapshotPending = false; persist(); }
}
export function expireReadCache(prefix: string, exact = false) {
    for (const url of new Set([...entries.keys(), ...pending.keys()]))
        if (exact ? url === prefix : url.startsWith(prefix)) {
            const item = entries.get(url);
            if (item) item.time = 0;
            versions.set(url, (versions.get(url) || 0) + 1);
            pending.delete(url);
            listeners.delete(url);
        }
    scheduleSnapshot();
}
export async function cachedRead(url: string, fetcher: () => Promise<any>, fresh?: (value: any) => void, force = false): Promise<any> {
    if (!allowed(url)) return fetcher();
    if (force) {
        pending.delete(url);
        listeners.delete(url);
        versions.set(url, (versions.get(url) || 0) + 1);
    }
    const cached = entries.get(url);
    const request = (): Promise<any> => {
        let current = pending.get(url);
        if (current) return current;
        const version = versions.get(url) || 0;
        versions.set(url, version);
        current = fetcher()
            .then(async (value) => {
                if (version !== (versions.get(url) || 0)) return request();
                entries.set(url, { time: Date.now(), value: copy(value) });
                scheduleSnapshot();
                for (const fn of listeners.get(url) || []) fn(copy(value));
                if (cached && typeof window !== "undefined") window.dispatchEvent(new CustomEvent("ownpanel-cache-status", { detail: { url, error: "" } }));
                return value;
            })
            .catch((error) => {
                if (entries.has(url) && version === (versions.get(url) || 0) && typeof window !== "undefined")
                    window.dispatchEvent(new CustomEvent("ownpanel-cache-status", { detail: { url, error: error.message || String(error) } }));
                throw error;
            })
            .finally(() => {
                if (pending.get(url) === current) {
                    pending.delete(url);
                    listeners.delete(url);
                }
            });
        pending.set(url, current);
        return current;
    };
    if (force) return copy(await request());
    if (cached) {
        const stale = Date.now() - cached.time > 10000;
        if (fresh && (stale || pending.has(url))) {
            if (!listeners.has(url)) listeners.set(url, new Set());
            listeners.get(url)!.add(fresh);
        }
        if (stale) void request().catch(() => {});
        return copy(cached.value);
    }
    return copy(await request());
}
