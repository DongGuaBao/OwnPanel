export type Match = "" | "=" | "^~" | "~" | "~*";
export type Route = {
    match: Match;
    path: string;
    handler: "static" | "proxy" | "return";
    directoryMode: "inherit" | "root" | "alias";
    directory: string;
    index: string;
    spa: boolean;
    upstream: string;
    status: number;
    response: string;
    access: { action: "allow" | "deny"; address: string }[];
};
export type RouteForm = { root: string; locations: Route[] };
export const newRoute = (path = ""): Route => ({
    match: "",
    path,
    handler: "static",
    directoryMode: "inherit",
    directory: "",
    index: "index.html",
    spa: false,
    upstream: "",
    status: 301,
    response: "",
    access: [],
});
export const defaultRoutes = (): RouteForm => ({ root: "/var/www/html", locations: [newRoute("/")] });
function arg(value: string) {
    if (/[\u0000-\u001f\u007f]/.test(value)) throw Error("路由参数不能包含换行或控制字符");
    return '"' + value.replace(/\\/g, "\\\\").replace(/"/g, '\\"') + '"';
}
function directory(value: string) {
    if (!value.startsWith("/")) throw Error("网站目录必须以 / 开头");
    return arg(value);
}
export function renderRoutes(form: RouteForm) {
    const lines: string[] = ["# OWNPANEL ROUTES BEGIN"];
    if (form.root.trim()) lines.push("root " + directory(form.root.trim()) + ";");
    if (!form.locations.length) throw Error("请添加至少一条 Location");
    const seen = new Set<string>();
    for (const r of form.locations) {
        const path = r.path.trim();
        if (!path || (!r.match.startsWith("~") && !path.startsWith("/"))) throw Error("请输入有效的 Location 路径或正则");
        const identity = (r.match === "^~" ? "" : r.match) + " " + path;
        if (seen.has(identity)) throw Error("重复的 Location：" + path);
        seen.add(identity);
        lines.push("location " + (r.match ? r.match + " " : "") + arg(path) + " {");
        for (const rule of r.handler === "return" ? [] : r.access) {
            const address = rule.address.trim();
            if (!/^(all|unix:|[0-9a-fA-F:.]+(?:\/\d{1,3})?)$/.test(address)) throw Error("访问规则请输入 all、IP 或 CIDR");
            lines.push("    " + rule.action + " " + address + ";");
        }
        if (r.handler === "static") {
            if (r.directoryMode === "inherit" && !form.root.trim()) throw Error("请为 " + path + " 设置目录，或填写站点默认 root");
            if (r.directoryMode !== "inherit") lines.push("    " + r.directoryMode + " " + directory(r.directory.trim()) + ";");
            const index = r.index.trim().split(/\s+/).filter(Boolean);
            if (index.length) lines.push("    index " + index.map(arg).join(" ") + ";");
            if (r.spa) lines.push("    try_files $uri $uri/ /index.html;");
        } else if (r.handler === "proxy") {
            const upstream = r.upstream.trim();
            if (!/^https?:\/\/[^\s;{}"\\]+$/.test(upstream)) throw Error("反代地址须以 http:// 或 https:// 开头");
            lines.push(
                "    proxy_pass " + arg(upstream) + ";",
                "    proxy_http_version 1.1;",
                "    proxy_set_header Host $host;",
                "    proxy_set_header X-Real-IP $remote_addr;",
                "    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;",
                "    proxy_set_header X-Forwarded-Proto $scheme;",
                "    # OWNPANEL WEBSOCKET BEGIN",
                "    proxy_set_header Upgrade $http_upgrade;",
                "    proxy_set_header Connection $connection_upgrade;",
                "    # OWNPANEL WEBSOCKET END",
            );
        } else {
            if (!Number.isInteger(r.status) || r.status < 200 || r.status > 599) throw Error("请输入 200–599 的 HTTP 状态码");
            if ([301, 302, 303, 307, 308].includes(r.status) && !r.response.trim()) throw Error("重定向需要填写目标地址");

            lines.push("    return " + r.status + (r.response ? " " + arg(r.response) : "") + ";");
        }
        lines.push("}");
    }
    lines.push("# OWNPANEL ROUTES END");
    return lines.join("\n");
}
