// Keep untouched lines byte-for-byte, including unknown extensions, counters,
// quoting and table order. Only explicitly edited rules are reconstructed.
export function tokens(line: string): string[] {
    const result: string[] = [];
    let word = "",
        quote = "",
        escape = false,
        started = false;
    for (const c of line) {
        if (escape) {
            word += c;
            escape = false;
            started = true;
            continue;
        }
        if (c === "\\" && quote !== "'") {
            escape = true;
            started = true;
            continue;
        }
        if (quote) {
            if (c === quote) quote = "";
            else word += c;
            started = true;
            continue;
        }
        if (c === '"' || c === "'") {
            quote = c;
            started = true;
            continue;
        }
        if (/\s/.test(c)) {
            if (started) {
                result.push(word);
                word = "";
                started = false;
            }
        } else {
            word += c;
            started = true;
        }
    }
    if (escape || quote) throw Error("参数包含未闭合的引号或转义");
    if (started) result.push(word);
    return result;
}
export function quoteToken(s: string) {
    return /^[A-Za-z0-9_./:@,!+=%-]+$/.test(s) ? s : '"' + s.replace(/\\/g, "\\\\").replace(/"/g, '\\"') + '"';
}
export interface Rule {
    index: number;
    chain: string;
    args: string[];
    packets: string;
    bytes: string;
    line: string;
}
export interface Chain {
    index: number;
    name: string;
    policy: string;
    counter: string;
    rules: Rule[];
}
export interface Table {
    name: string;
    start: number;
    end: number;
    chains: Chain[];
}
export function parseRules(text: string): { lines: string[]; tables: Table[]; errors: string[] } {
    const lines = text.split("\n"),
        tables: Table[] = [],
        errors: string[] = [];
    let table: Table | undefined;
    lines.forEach((line, index) => {
        if (line.startsWith("*")) {
            table = { name: line.slice(1).trim(), start: index, end: lines.length, chains: [] };
            tables.push(table);
            return;
        }
        if (!table) return;
        if (line.trim() === "COMMIT") {
            table.end = index;
            table = undefined;
            return;
        }
        const chain = line.match(/^:([^\s]+)\s+(\S+)\s+(\[[^\]]+\])/);
        if (chain) {
            table.chains.push({ index, name: chain[1]!, policy: chain[2]!, counter: chain[3]!, rules: [] });
            return;
        }
        if (!line.trim() || line.startsWith("#")) return;
        try {
            const a = tokens(line);
            let packets = "0",
                bytes = "0";
            const c = a[0]?.match(/^\[(\d+):(\d+)\]$/);
            if (c) {
                packets = c[1]!;
                bytes = c[2]!;
                a.shift();
            }
            if (a[0] === "-A" && a[1]) {
                const target = table.chains.find((c) => c.name === a[1]);
                if (!target) {
                    errors.push(`第 ${index + 1} 行引用未声明的链`);
                    return;
                }
                target.rules.push({ index, chain: a[1], args: a.slice(2), packets, bytes, line });
            } else errors.push(`第 ${index + 1} 行使用未知格式，保留原文`);
        } catch (e) {
            errors.push(`第 ${index + 1} 行：${(e as Error).message}`);
        }
    });
    return { lines, tables, errors };
}
export const fields = [
    { key: "protocol", flag: "-p", label: "协议" },
    { key: "source", flag: "-s", label: "来源 IP / CIDR" },
    { key: "destination", flag: "-d", label: "目标 IP / CIDR" },
    { key: "input", flag: "-i", label: "入接口" },
    { key: "output", flag: "-o", label: "出接口" },
    { key: "sport", flag: "--sport", label: "来源端口" },
    { key: "dport", flag: "--dport", label: "目标端口" },
    { key: "jump", flag: "-j", label: "动作 / 跳转链" },
];
const aliases: Record<string, string> = {
    "--protocol": "-p",
    "--source": "-s",
    "--destination": "-d",
    "--in-interface": "-i",
    "--out-interface": "-o",
    "--source-port": "--sport",
    "--destination-port": "--dport",
    "--jump": "-j",
};
export function draftRule(args: string[]): Record<string, string> {
    const draft: Record<string, string> = { extra: "" },
        extra: string[] = [];
    for (const f of fields) draft[f.key] = "";
    const arity: Record<string, number> = {
        "-m": 1,
        "--match": 1,
        "--comment": 1,
        "--ctstate": 1,
        "--state": 1,
        "--tcp-flags": 2,
        "--syn": 0,
        "--fragment": 0,
        "-f": 0,
        "--to-destination": 1,
        "--to-source": 1,
        "--to-ports": 1,
        "--reject-with": 1,
        "--icmp-type": 1,
        "--icmpv6-type": 1,
        "--dports": 1,
        "--sports": 1,
        "--ports": 1,
        "--log-prefix": 1,
        "--log-level": 1,
        "--set-xmark": 1,
        "--set-mark": 1,
        "--mark": 1,
        "--ctmark": 1,
        "--limit": 1,
        "--limit-burst": 1,
    };
    for (let i = 0; i < args.length; i++) {
        const a = args[i]!,
            f = fields.find((f) => f.flag === (aliases[a] || a));
        if (f && i + 1 < args.length && !draft[f.key] && args[i + 1] !== "!") {
            draft[f.key] = args[++i]!;
            continue;
        }
        const n = arity[a];
        if (n !== undefined && i + n < args.length) {
            extra.push(a, ...args.slice(i + 1, i + n + 1));
            i += n;
            continue;
        }
        // An extension with unknown arity or a negated expression must stay intact.
        // Do not reinterpret a quoted extension value such as --comment "-s".
        for (const f of fields) draft[f.key] = "";
        draft._opaque = "1";
        draft.extra = args.map(quoteToken).join(" ");
        return draft;
    }
    draft.extra = extra.map(quoteToken).join(" ");
    return draft;
}
export function buildRule(chain: string, draft: Record<string, string>): string {
    if (!/^[A-Za-z0-9_.:+-]{1,64}$/.test(chain)) throw Error("链名称无效");
    const args = ["-A", chain];
    for (const f of fields) {
        const v = draft[f.key]?.trim();
        if (v) {
            if (/[\r\n\x00]/.test(v)) throw Error("字段不能换行");
            args.push(f.flag, v);
        }
    }
    args.push(...tokens(draft.extra || ""));
    return args.map(quoteToken).join(" ");
}
export function ruleSummary(rule: Rule) {
    const d = draftRule(rule.args);
    return d._opaque ? { ...d, protocol: "扩展匹配", source: "查看完整规则", destination: "查看完整规则", jump: "高级规则" } : d;
}
