# 网站监听与证书回显

站点与反代使用同一模型，Agent 和 SSH 共用生成逻辑。

| 字段 | 含义 |
| --- | --- |
| `httpEnabled` | 是否监听明文 HTTP |
| `port` | HTTP TCP 端口（新模型） |
| `httpsEnabled` | 是否监听 HTTPS TCP |
| `tlsPort` | 自定义 SSL/HTTPS TCP 端口 |
| `quic` | 是否启用 QUIC/HTTP/3 |
| `quicPort` | 自定义 QUIC UDP 端口 |
| `tls` | HTTPS 或 QUIC 需要证书时为 true |

至少启用一种协议；HTTP 与 HTTPS 不能共用 TCP 端口，QUIC 的 UDP 端口可与 HTTPS TCP 同号。未启用的协议不会生成监听。QUIC 生成对应 UDP listen、HTTP/3 和真实端口的 Alt-Svc，并先检查目标 Nginx 的 HTTP/3 模块。

旧记录没有 `httpEnabled`/`httpsEnabled` 时保持原来行为：`tls=false` 为 HTTP `port`，`tls=true` 为 HTTPS `port`。新编辑器会显式填写开关和独立端口，不把旧的自定义 SSL 端口改成 443。ACME HTTP-01 专用流程仍需外部 80 端口验证，但新 UI 会询问签发后 SSL 端口。

`certificateInfo` 为公开信息，单证书及每个 binding 均可带此字段。列表会解析实际部署的证书文件更新域名、颁发者和有效期；保留证书库名称/ID，不返回私钥。编辑时 PEM 留空继续使用原文件，证书信息卡仍可见。

修改协议生成和证书回显需要更新 Agent。项目中的 root Agent 由用户手动执行 `sudo systemctl restart ownpanel-dev-agent.service` 加载新构建。
