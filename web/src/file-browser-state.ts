export type FileEntry = { name: string; directory: boolean; symlink?: boolean; size: number; mode: string; uid: number; gid: number; modified: string };
export type FileTab = {
    id: string;
    path: string;
    input: string;
    entries: FileEntry[];
    selected: string[];
    anchor: string;
    focused: string;
    history: string[];
    historyIndex: number;
    scroll: number;
    loaded: boolean;
    busy: boolean;
    dirty: boolean;
};
export type FileClipboard = { action: "copy" | "move"; items: { name: string; path: string }[] } | null;
export function cleanPath(path: string) {
    const parts: string[] = [];
    for (const part of path.split("/")) {
        if (part === "..") parts.pop();
        else if (part && part !== ".") parts.push(part);
    }
    return "/" + parts.join("/");
}
export function newFileTab(id: string, path = "/"): FileTab {
    path = cleanPath(path);
    return { id, path, input: path, entries: [], selected: [], anchor: "", focused: "", history: [path], historyIndex: 0, scroll: 0, loaded: false, busy: false, dirty: false };
}
export function fileSelection(tab: FileTab, name: string, ctrl: boolean, shift: boolean, orderedNames?: string[]) {
    const names = orderedNames || tab.entries.map((entry) => entry.name),
        target = names.indexOf(name),
        anchor = names.indexOf(tab.anchor);
    if (shift && anchor >= 0 && target >= 0) {
        const range = names.slice(Math.min(anchor, target), Math.max(anchor, target) + 1);
        tab.selected = ctrl ? Array.from(new Set([...tab.selected, ...range])) : range;
    } else if (ctrl) {
        tab.selected = tab.selected.includes(name) ? tab.selected.filter((value) => value !== name) : [...tab.selected, name];
        tab.anchor = name;
    } else {
        tab.selected = [name];
        tab.anchor = name;
    }
    tab.focused = name;
}
