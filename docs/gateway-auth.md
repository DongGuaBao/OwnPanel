## 公网通过 APIGateway 登录

公网模式仍由用户态 Nginx 终止 HTTPS，中心保留 HTTP 监听。先在 APIGateway 发布包含浏览器登录挑战和 JSON 换票接口的新版。在网关注册 portal 应用 `ownpanel`，登录方式选 `code`，权限为 `ownpanel.admin`，应用 URL 填面板域名的 `https://面板域名/auth/callback`（自定义端口也要写入）。该权限等同于完整主机管理权限。为该应用生成内部服务凭证，只允许中心来源，启用网关内部鉴权接口。面板不需要网关签名私钥。

将服务凭证单独保存为 `data/center/gateway.token`，权限设为 0600。中心环境文件可放在 `data/center/gateway.env`，示例中的地址和域名须替换成实际值。

```ini
OWNPANEL_PUBLIC_ORIGIN=https://panel.example.com
OWNPANEL_TRUSTED_PROXIES=127.0.0.1,::1
OWNPANEL_GATEWAY_URL=https://login.example.com
OWNPANEL_GATEWAY_TOKEN_FILE=/srv/ownpanel/data/center/gateway.token
```

网关提供单独的后端入口时，可配置 `OWNPANEL_GATEWAY_INTERNAL_URL`。支持 HTTPS 或字面回环 IP 的 HTTP 源地址。换票使用 JSON 请求体，服务认证使用请求头。公网代理只代理面板，内部换票入口只允许中心访问。首次从网关门户进入时，面板会回到网关完成一次绑定当前浏览器的登录。

通过中心的 **用户服务** drop-in 加载必需的环境文件：

```ini
[Service]
EnvironmentFile=/srv/ownpanel/data/center/gateway.env
```

反代覆盖写入 `X-Real-IP`，转发原始 Host，保留浏览器 Origin 和终端 WebSocket Upgrade。面板与网关的访问日志使用不含查询参数的 `$uri`，避免记录回调 code、终端 ticket 或登录跳转参数。防火墙和安全组应限制 7080 仅对可信反代开放，同时确认 IPv4、IPv6、其他反代入口。来自配置代理以外的 TCP 对端也会被中心拒绝。直属本机回环健康检查需要将回环加入可信来源。

启用前先升级各节点 Agent，再重启读取上述配置的中心服务。安排维护窗口：Agent 重启会结束其终端会话。

网关登录会话有效期为 1 小时。管理 API 每次查权，已建立的终端每 30 秒查权一次，单次超时 4 秒。查权失败会吊销当前面板会话并关闭连接。持久终端归属网关用户 ID，同一账号可在其他浏览器重新连接。中心的 `data/center/audit.jsonl` 记录该用户的写请求和终端握手，排除查询和正文。

上线前用无权限和有权限的账号分别测试，检查撤权后 API 和已打开的终端，确认非可信来源无法直达后端。单独备份网关接入凭证和环境配置，中心快照仍按包含 SSH 凭证和设备 CA 私钥的敏感备份管理。
