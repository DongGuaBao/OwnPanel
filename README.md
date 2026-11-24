# OwnPanel

OwnPanel 是一个多主机 Linux 运维面板，支持文件管理、持久终端、systemd 系统和用户服务、Nginx 站点与证书、防火墙和 Docker。中心以普通用户运行，通过 SSH 或出站 Agent 管理主机。Linux Agent 以 root 身份运行，面板管理权限可以控制整台主机。

## 从源码启动

需要 Go 1.26.11 或更高版本、Node.js 22 和 npm。在仓库根目录执行：

```sh
git submodule update --init --recursive
npm ci --prefix web
npm run build --prefix web
mkdir -p bin
go build -o bin/ownpanel-server ./cmd/server
go build -o bin/ownpanel-agent ./cmd/agent
./bin/ownpanel-server -listen 127.0.0.1:7080 -web web/dist -data ./data/center
```

打开 http://127.0.0.1:7080，用账号 `admin` 和首次启动生成的 `data/center/initial-password` 登录。在「主机」中添加 SSH 主机。需要从外网访问时，通过 HTTPS 反代发布中心，并限制后端端口的访问来源。数据库、设备 CA、服务凭证和备份含敏感信息，请保存在私有目录。

可选的 APIGateway 票据登录见[网关鉴权](docs/gateway-auth.md)。

## FileSearch

[FileSearch](https://github.com/DongGuaBao/FileSearch) 作为 Git 子模块固定在 `8b68f3b80d48db9eef12229f2465c0fe28a95a16`。克隆时使用 `--recurse-submodules`，或执行上面的初始化命令。

在 Linux 上安装 CMake 3.22+、支持 C++20 的编译器、SQLite3 开发库和 Python 3 后，执行 `cmake -S FileSearch -B data/build/filesearch -DCMAKE_BUILD_TYPE=Release` 和 `cmake --build data/build/filesearch --target FileSearchServiceLinux`。服务接受扫描根目录和私有状态目录两个参数。在 Agent 环境中设置 `OWNPANEL_FILESEARCH_SOCKET`，指向该状态目录的 `search.sock`，即可使用文件名索引和目录大小统计。

## 开发与测试

贡献前请阅读 [AGENTS.md](AGENTS.md)，了解代码组织、界面约定和提交检查。

前端用 Vue 3 和 Element Plus，源码在 `web/src/`。运行 `npm run dev --prefix web` 开启 Vite 开发服务。Go 入口位于 `cmd/server/` 和 `cmd/agent/`，共享实现位于 `internal/panel/`。

```sh
go test ./...
go vet ./...
node web/read-cache-test.cjs
node web/iptables-model-test.cjs
```

安装脚本位于 `scripts/`。在专用测试主机验证涉及 systemd、Nginx、防火墙和 root Agent 的安装与操作。

## 许可证

OwnPanel 使用 [MIT 许可证](LICENSE)。FileSearch 的许可文本见 [FileSearch/LICENSE](FileSearch/LICENSE)。
