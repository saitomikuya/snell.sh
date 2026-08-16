# Proxy Panel

Proxy Panel 是面向 Debian/Ubuntu VPS 的单容器中文代理管理面板。它用 Go 提供 Web/API 与受控 Runtime Agent，用 Vue 3 提供响应式界面，并把数据库、配置、运行时、日志和备份统一持久化到 `/data`。

本项目是在 [jinqians/snell.sh](https://github.com/jinqians/snell.sh) 与 [jinqians/ss-2022.sh](https://github.com/jinqians/ss-2022.sh) 的功能和配置语义基础上进行的可视化二次开发，不是上游官方版本。新增能力主要包括中文 Web 面板、节点操作、客户端配置、流量限额、备份恢复、版本检查和行为审计。

## 一键部署

Docker Hub 镜像 `saitomikuya/proxy-panel:latest` 同时支持 `linux/amd64` 和 `linux/arm64`。已经安装 Docker 的 Debian/Ubuntu VPS 可直接执行：

```bash
sudo docker run -d --name proxy-panel --pull=always --restart unless-stopped --network host --cap-add NET_ADMIN --cap-add NET_RAW -v /opt/proxy-panel:/data -e PANEL_BIND=127.0.0.1 -e PANEL_PORT=8080 -e TZ=Asia/Shanghai saitomikuya/proxy-panel:latest
```

在自己的电脑建立 SSH 隧道：

```bash
ssh -L 8080:127.0.0.1:8080 root@你的服务器地址
```

然后打开 <http://127.0.0.1:8080>，使用默认密码 `password` 登录并按提示立即改密。公网端口、安全组、HTTPS 反代和面板操作请继续阅读[完整使用说明](./docs/USER_GUIDE.md)。

## 使用入口

- 第一次部署、面板逐项操作、客户端导入和排障：阅读[完整使用说明](./docs/USER_GUIDE.md)；
- 已实现功能与仍有限制的项目：阅读[实现状态](./docs/IMPLEMENTATION_STATUS.md)；
- 安全设计、威胁边界与测试要求：阅读[安全说明](./SECURITY.md)；
- 只想本地构建体验：可直接跳到下方“本地开发”或“构建和运行容器”。

> 当前自动安装并验证的 Snell 运行时为 v5.0.1。界面保留的 v4/v6 选项需要对应固定运行时和校验清单补齐后再使用；其他已知限制以完整使用说明和实现状态为准。

本仓库依据 [DEVELOPMENT_SPEC.md](./DEVELOPMENT_SPEC.md) 开发。当前版本已经形成可构建、可测试、可运行的纵向闭环；尚未完成的全功能项单独列在“实现状态”中，不用静态按钮冒充可用功能。

## 已实现

- 无用户名的单密码登录，默认密码固定为 `password`。
- Argon2id 密码哈希；首次登录强制改密；改密或重置后注销全部 Session。
- `HttpOnly`、`SameSite=Strict` Cookie，CSRF 双提交校验、Origin 校验和按 IP 登录限速。
- SQLite WAL、版本化迁移、主密钥 AES-256-GCM 加密节点秘密。
- Snell、SS-2022、ShadowTLS 节点数据模型、CRUD、依赖和 TCP/UDP 端口冲突校验。
- Web 与 Agent 之间的 Unix Socket 类型化 RPC；没有任意 Shell 或路径读写接口。
- 固定路径配置生成、候选配置、最后可用快照、原子替换、失败回滚。
- 多实例启动、停止、重启、进程组信号、崩溃退避、依赖启动顺序和滚动脱敏日志。
- 默认 Snell v5.0.1、ShadowTLS v0.2.25 和 shadowsocks-rust v1.24.0 运行时：白名单 HTTPS、大小/超时限制、固定 SHA256、staging 解包和原子安装。
- 默认部署 Snell + ShadowTLS 与 SS-2022 + ShadowTLS 两套组合；SS 的 UDP 继续使用原始 SS 端口。
- Snell/Surge、SS/SIP002、ShadowTLS 组合配置和二维码；完整配置在已登录且已完成首次改密的安全会话中直接展示。
- nftables 公网端口字节采样、按节点周期累计、流量限额、手动/自动暂停恢复；只操作 `inet proxy_panel` 专属表。
- 节点运行时输出与中文启停、重启、流量活动和限额行为日志。
- SQLite 一致性备份、哈希校验、路径穿越防护、恢复前自动备份、在线恢复和 Session 注销。
- 中文桌面/手机面板：仪表盘、节点、流量、上游版本对比与一键更新、备份、中文行为审计和安全设置。
- 单容器多进程监督、非 root Web、root Agent、host 网络示例、NET_ADMIN/NET_RAW、健康检查。
- 幂等安装、镜像更新回滚和默认保留数据的卸载脚本。

## 仍需完成后才能称为“全功能版”

- 节点 CPU/内存/连接数等更细的单节点运行指标。
- 中国大陆 CIDR 数据集的下载、原子集合切换和定时更新。
- simple-obfs 二进制的安全安装与失败回滚（配置 Schema 已预留）。
- Snell v4/v6 的固定版本运行时目录和校验清单；当前自动安装并默认运行 v5。
- SSE 持续日志流及其连接/速率限制；当前接口返回最近 200 行。
- 异步更新 Job 进度与手动选择历史运行时回滚；当前一键更新会先自动备份，失败时回滚节点版本。
- 更完整的宿主机和单节点指标，以及一次性 Linux VM 中的真实协议与特权防火墙验收。
- 镜像 SBOM、漏洞扫描、签名/provenance 和真实发布流水线。

详见 [实现状态](./docs/IMPLEMENTATION_STATUS.md)。

## 架构

```text
Browser → panel server（UID 10001）→ SQLite / encrypted secrets
                 │
                 └── Unix Socket typed RPC → panel agent（受控高权限）
                                              ├── Snell instances
                                              ├── ssserver instances
                                              ├── ShadowTLS instances
                                              └── proxy_panel nftables table
```

Web 不执行协议命令，不接收任意命令字符串。Agent 只根据节点 ID 从数据库读取结构化数据，并将其映射到 `/data` 下的固定路径和参数数组。

## 本地开发

要求 Go 1.25+、Node.js 24 和 pnpm 10。

```bash
cd web
pnpm install --frozen-lockfile
pnpm run build
cd ..
go test ./...
go build -o bin/panel ./cmd/panel
```

启动两个进程；macOS 本地测试不会安装 Linux 运行时或修改防火墙：

```bash
export PANEL_DATA_DIR="$PWD/.dev-data"
export PANEL_AUTO_INSTALL_RUNTIMES=0
./bin/panel agent
```

在另一终端：

```bash
export PANEL_DATA_DIR="$PWD/.dev-data"
export PANEL_AUTO_INSTALL_RUNTIMES=0
./bin/panel server
```

打开 `http://127.0.0.1:8080`，首次使用 `password` 登录并修改密码。

## 构建和运行容器

```bash
docker build --build-arg VERSION=0.1.0 -t proxy-panel:local .
PROXY_PANEL_IMAGE=proxy-panel:local docker compose -f compose.example.yaml up -d
```

Compose 示例具备以下边界：

- `network_mode: host`
- 仅添加 `NET_ADMIN`、`NET_RAW`
- 不使用 `--privileged`
- 不挂载 Docker Socket
- 只把 `/opt/proxy-panel` 挂载为 `/data`
- 面板默认绑定 `127.0.0.1:8080`

如果设置 `PANEL_BIND=0.0.0.0`，必须先部署可信 HTTPS 反向代理，并设置 `PANEL_SECURE_COOKIE=1`。

## VPS 安装

从仓库检出目录运行：

```bash
sudo PROXY_PANEL_IMAGE=你的镜像地址 ./scripts/install.sh
```

安装器不会静默安装 Docker。它写入 `/opt/proxy-panel/compose.yaml` 与 `.env`，重复执行不会覆盖已有数据。

更新与卸载：

```bash
sudo ./scripts/update.sh
sudo ./scripts/uninstall.sh
sudo ./scripts/uninstall.sh --purge  # 明确永久删除 /opt/proxy-panel
```

## 维护命令

```bash
docker exec proxy-panel panelctl reset-password password
docker exec proxy-panel panelctl cleanup-firewall
docker exec proxy-panel panel version
```

重置密码会恢复为 `password`、重新进入强制改密状态并注销全部会话。

## 运行时供应链

生产镜像不包含 Snell、shadowsocks-rust 或 ShadowTLS 二进制。Agent 首次启动时只从 [运行时目录](./internal/runtime/catalog.yaml) 中列出的固定 URL 下载，并校验固定 SHA256：

- Snell v5.0.1：`dl.nssurge.com`
- shadowsocks-rust v1.24.0：官方 GitHub Release
- ShadowTLS v0.2.25：官方 GitHub Release

下载限制为 100 MiB、2 分钟、最多 5 次重定向；重定向目标也必须在白名单内。Snell 的再分发条件未确认，因此只允许运行时从 Surge 官方源下载，不写入镜像或仓库。

## 安全与测试

普通测试绝不修改本机 nftables/iptables。特权测试必须在一次性 Linux VM/CI 中显式设置 `RUN_PRIVILEGED_TESTS=1`。参见 [SECURITY.md](./SECURITY.md)。

```bash
go vet ./...
go test -race ./...
cd web && pnpm run typecheck && pnpm test && pnpm run build
sh -n scripts/*.sh packaging/docker/entrypoint.sh packaging/s6/*/run
```

## 数据目录

数据库位于 `/data/db/panel.db`，主密钥位于 `/data/secrets/master.key`。不要只复制数据库而遗漏主密钥；否则加密秘密无法恢复。内置备份同时保存数据库、主密钥、配置和上游元数据，并设置为仅服务用户可访问。

## 开源许可与致谢

本仓库作为 [`jinqians/snell.sh`](https://github.com/jinqians/snell.sh) 的可视化二次开发 Fork，沿用 GNU General Public License v3.0，详见 [LICENSE](./LICENSE)。原项目、SS-2022 参考项目及各运行时的许可和再分发说明见 [THIRD_PARTY_NOTICES.md](./THIRD_PARTY_NOTICES.md)。
