# v0.5 终端、可视化防火墙与 Systemd

以下接口位于 `/api/nodes/<node-id>`，经过现有登录、同源和 CSRF 校验。中心经 Unix Socket 或 mTLS/Yamux 代理到 Agent；SSH 驱动在中心执行同等接口。

## 终端

- `GET /terminals`：返回 `{sessions:[{id,name,created,ended,cols,rows,viewers}]}`。
- `POST /terminals`，`{name:"部署"}`：建立一个独立 PTY，返回其元数据。最多 32 个已保留会话；创建失败不会记录会话。
- `DELETE /terminals/<id>`：关闭进程/SSH channel、观看连接并移除元数据。
- `POST /terminal/ticket`：现有一次性凭证，绑定登录会话和主机，30 秒有效。
- `GET /terminal?ticket=<ticket>&session=<id>`：升级 WebSocket，先回放最近 2 MiB 输出，然后推送实时二进制输出。输入仍为 `{type:"input",data:"..."}`，缩放为 `{type:"resize",cols:120,rows:40}`。缺失的会话返回 404，不会自动创建替代 Shell。

无 `session` 的旧 WebSocket 入口保留一次性会话行为，以兼容原有客户端。新界面总是先创建、再按 ID 附加。刷新、退出登录和连接过期会关闭观看连接，具名会话继续运行。

共享 `terminalHub` 只负责会话表、输出回放和浏览器附加，不负责目标传输。SSH 驱动持有 `ssh.Session` 和既有 20 秒保活的 SSH client；Agent 持有本地 `bash` PTY。后台始终读取输出；慢浏览器断开，不阻塞远端命令。WebSocket 每 20 秒 ping，70 秒没有 pong 会关闭观看连接。

状态存于负责保活的进程内存。中心或出站链路重建不影响 Agent 的会话；中心重启会结束 SSH 会话；Agent 重启会结束其会话。目标重启或 SSH 实际断线不能恢复原 Shell。浏览器连接失败后只会尝试恢复相同 ID。

## 防火墙

iptables 表单直接编辑完整恢复文档的草稿，仍使用现有 `/iptables` 和 `/iptables/confirm` 事务。未修改的行保持原文，包含未知扩展、顺序和计数。不能可靠分解的特殊匹配使用完整参数编辑，不将其错误解释为普通字段。

UFW `/firewall` 新增两个动作：

```json
{"action":"delete-rule","original":"allow 443/tcp"}
```

```json
{"action":"replace","original":"allow 443/tcp","port":"8443","protocol":"tcp","source":"any","policy":"allow"}
```

`original` 必须与当前 `ufw show added` 中的完整规则匹配，不含 `ufw ` 前缀。过期规则和需要复杂引号的规则拒绝表单修改。替换使用同一已备份、已武装恢复任务的事务先删除再添加；失败整体恢复，成功仍需 60 秒内确认。SSH 原始规则身份检查在远程事务锁内再次执行。替换会把规则移到 UFW 配置末尾。

## Systemd

- `GET /systemd`：合并 `list-units --all --output=json` 与 `list-unit-files --output=json`，返回 `{units:[...]}`。
- `GET /systemd/unit?unit=example.service`：返回 `properties`、生效 `config`、最近 200 条 `logs`、OwnPanel `override` 及其 SHA-256 `revision`。无提权账号可读取其现有权限允许的信息。
- `POST /systemd/action`，`{unit,action}`：支持 start、stop、restart、reload、enable、disable、mask、unmask、reset-failed；daemon-reload 无需 unit。enable/disable 不隐式启停服务。
- `POST /systemd/config`，`{unit,action:"create",content}`：创建 `/etc/systemd/system/<unit>`，拒绝覆盖已存在的本地/供应商单元。
- `POST /systemd/config`，`{unit,action:"override",content,revision}`：在文件锁内检查版本，原子替换 `<unit>.d/ownpanel.conf`；空内容删除该覆盖文件。保留其他 drop-in，随后 daemon-reload，不自动重启服务。

单元名称经过格式与长度校验，命令参数使用独立 shell 引用，systemctl 单元参数跟在 `--` 后。写操作通过 Agent root 或已有 SSH sudo 执行。

验收：`scripts/persistent-systemd-integration.py`、`web/persistent-systemd-smoke.cjs`、`web/iptables-model-test.cjs`。浏览器默认还会使用独立 QEMU 虚拟机验证实际网络规则；`OWNPANEL_SKIP_VM=1` 仅跳过虚拟机部分，保留 WSL 的终端、UFW 和 Systemd 验收。

## Systemd 服务工作台（rc.2）

- `GET /systemd` 返回 `custom`、`fragmentPath`、`sourcePath` 与 `origin`，用于自动识别本机自建单元。仅对已有加载单元批量查询属性；未加载的本机单元和模板直接读取单元搜索目录，避免为展示而加载所有系统单元。指向供应商单元的别名不自动归入我的服务，OwnPanel 恢复任务也不归入。
- `GET /systemd/preferences` 返回 `{units:{"app.service":{favorite:true,membership:"my",group:"网站"}}}`。该接口由中心处理，不发送至 Agent。
- `POST /systemd/preferences` 接收 `{unit,favorite?,membership?,group?}`，按主机和单元保存部分更新。`membership` 为 `auto/my/system`，分组最多 64 个 Unicode 字符。恢复全部默认值会移除记录；节点删除一并清理。更新串行合并，避免收藏操作覆盖分组。
- `GET /systemd/logs?unit=app.service&lines=500&since=1h&priority=all` 返回 `{text,lines}`。`lines` 范围 20–2000；`since` 为 all/5m/1h/24h，`priority` 为 all/err/warning/info。界面定时轮询此接口，不反复获取全部配置。
- `GET /systemd/unit?unit=...` 额外返回 `source:{path,content,revision,error}`，真实源文件路径经解析，屏蔽单元的 `/dev/null` 和特殊文件不能作为源配置编辑。
- `POST /systemd/config` 新增 `action:"unit"`，需提供 `unit,path,revision,content`。服务端重新获取真实源路径，在主机共享文件锁内检查位置和原内容版本，校验配置后原子替换，保留原属主与权限。`create` 同样先校验；`override` 保留原接口。

收藏与分类是展示偏好，不是服务器权限边界。系统单元仍可查看、操作和编辑源文件，写操作沿用已有 root/sudo 权限。