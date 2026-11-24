# 证书夹 API

证书记录按主机保存在中心 SQLite，所有接口均要求面板登录，写入和私钥导出还要求 CSRF token。与主机文件浏览分开，不依赖 Agent 在线。

前缀：`/api/nodes/{nodeId}/certificates`。

| 方法与路径 | 行为 |
| --- | --- |
| `GET /` | 返回元数据列表，不含 PEM 和私钥 |
| `POST /preview` | 解析证书链并校验私钥，返回元数据，不保存 |
| `POST /` | 导入证书，ID 由服务端生成 |
| `GET /{id}` | 返回单条元数据 |
| `PUT /{id}` | 替换该记录的名称、证书链和私钥 |
| `POST /{id}/export` | 明确读取证书链及私钥，用于选择或导出 |
| `DELETE /{id}` | 删除证书夹记录，不改动已部署的网站副本 |

导入、预览及更新体：`{"name":"主站证书","certificate":"PEM chain","privateKey":"PEM key"}`。
证书链仅接受 CERTIFICATE PEM，禁止混入私钥；导入与更新均执行密钥匹配校验。同一主机的相同叶证书指纹不能重复导入。

元数据包含域名、Subject、Issuer、生效/到期时间、剩余天数、序列号、SHA-256 指纹与算法。状态 `valid`、`expiring`（30 天内）、`expired`、`notYetValid` 按请求时间计算；保留已过期证书便于管理，站点应用时仍执行部署校验。

私钥使用中心 AEAD 密钥加密，并绑定主机与记录 ID。列表不返回敏感内容，保存/导出/删除审计不记录 PEM。备份需保留完整中心状态及其密钥。
