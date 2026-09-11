# Proxy Panel 完整使用说明

本文面向准备把 Proxy Panel 部署到 Debian/Ubuntu VPS 的使用者，覆盖安装、访问、节点管理、客户端导入、流量管理、更新备份、日常维护和排障。

> [!IMPORTANT]
> Proxy Panel 是基于 [`jinqians/snell.sh`](https://github.com/jinqians/snell.sh)、[`jinqians/ss-2022.sh`](https://github.com/jinqians/ss-2022.sh) 和 [`MoeClub/ocserv_docker`](https://github.com/MoeClub/ocserv_docker) 的功能与配置语义开发的可视化二次项目，不是上游官方版本。遇到面板自身问题请在本仓库反馈，不要让上游作者承担本项目的支持责任。

## 1. 当前可用范围

当前版本适合在一台 VPS 上用一个容器管理以下服务：

- Snell v5 节点；
- Shadowsocks Rust / SS-2022 节点；
- 以 Snell 或 SS-2022 为后端的 ShadowTLS v3 前端；
- 基于 ocserv 1.5.0、兼容 Cisco Secure Client/OpenConnect 的多 AnyConnect 节点；
- AnyConnect 用户名/密码、按用户全隧道/中国直连路由组，以及证书和中国 CIDR 定时更新；
- 节点启停、重启、日志和客户端配置；
- 公网端口流量统计、月限额、暂停和恢复；
- 上游版本检查、受控运行时更新、配置备份和恢复；
- 中文行为审计和管理员密码管理；
- 可持久化记忆选择的白天/黑夜界面模式。

当前限制：

- 自动下载并经过固定 SHA256 校验的 Snell 运行时只有 v5.0.1。界面虽然保留 v4/v6 配置项，但在补齐对应版本的固定运行时目录和校验清单前，不应切换为 v4/v6；
- simple-obfs 的配置结构已经预留，但二进制自动安装尚未完成；
- 面板没有替你修改云厂商安全组；
- ShadowTLS 主要承载 TCP。SS-2022 的 UDP 仍连接原始 SS 端口；
- 正式支持 Linux VPS，不支持把 Docker Desktop 当作生产环境。

更细的完成度见[实现状态](./IMPLEMENTATION_STATUS.md)。

## 2. 安装前准备

### 2.1 支持环境

| 项目 | 要求 |
|---|---|
| 系统 | Debian 12+ 或 Ubuntu 22.04 LTS+ |
| 架构 | `linux/amd64` 或 `linux/arm64` |
| 容器 | Docker Engine，带 `docker compose` 插件 |
| 权限 | 安装时可使用 root / sudo |
| 网络 | 能通过 HTTPS 访问 GitHub、`dl.nssurge.com` 和对应 Release 下载地址 |
| 内核 | 支持 nftables；AnyConnect 还需 `/dev/net/tun`、已启用 IPv4 转发；容器需要 `NET_ADMIN`、`NET_RAW` |

检查命令：

```bash
uname -m
docker version
docker compose version
sudo nft --version
test -c /dev/net/tun && echo TUN_OK
sysctl net.ipv4.ip_forward
```

### 2.2 规划端口

默认首次启动会创建四个节点：

| 节点 | 默认监听 | 是否对公网开放 |
|---|---|---|
| Snell 主节点 | `0.0.0.0:6160/tcp+udp` | 是；也可在面板中改为本机地址 |
| Snell ShadowTLS | `0.0.0.0:8443/tcp` | 是 |
| SS-2022 主节点 | `0.0.0.0` 上的可用端口，TCP+UDP | 是，UDP 需直连原始端口 |
| SS-2022 ShadowTLS | 首次启动时选择另一个可用 TCP 端口 | 是 |
| AnyConnect | 用户配置，默认 `443/tcp+udp` | 是 |

实际随机端口以面板显示为准。云安全组和宿主机防火墙至少要放行你准备使用的公网节点端口：

```bash
# 示例：Snell + ShadowTLS
sudo ufw allow 8443/tcp

# 示例：SS-2022，端口请替换为面板实际值
sudo ufw allow 20000/tcp
sudo ufw allow 20000/udp
```

面板默认监听 `0.0.0.0:8080`。为便于直接访问，请向你自己的可信公网 IP 放行 `8080/tcp`，不要对整个互联网无限制开放管理端口。如果选择 SSH 隧道访问，则不需要放行 `8080`。

## 3. 安装

### 3.1 Docker Hub 一键部署（推荐）

镜像 `saitomikuya/proxy-panel:latest` 同时发布 `linux/amd64` 和 `linux/arm64`。已经安装 Docker 的 VPS 直接执行：

```bash
sudo docker run -d --name proxy-panel --pull=always --restart unless-stopped --network host --device /dev/net/tun:/dev/net/tun --cap-add NET_ADMIN --cap-add NET_RAW -v /opt/proxy-panel:/data -e PANEL_BIND=0.0.0.0 -e PANEL_PORT=8080 -e TZ=Asia/Shanghai saitomikuya/proxy-panel:latest
```

Docker 会自动拉取匹配当前 CPU 架构的镜像，持久化数据保存在 `/opt/proxy-panel`。容器健康后直接打开 `http://你的服务器IP:8080`。

### 3.2 从源码构建并直接启动

在 VPS 上克隆你的仓库并构建镜像：

```bash
git clone https://github.com/saitomikuya/snell.sh.git proxy-panel
cd proxy-panel
docker build --build-arg VERSION=0.1.0 -t proxy-panel:local .
```

启动容器：

```bash
sudo install -d -m 0750 /opt/proxy-panel
sudo PROXY_PANEL_IMAGE=proxy-panel:local \
  docker compose -f compose.example.yaml up -d
```

检查状态：

```bash
docker compose -f compose.example.yaml ps
docker logs --tail=100 proxy-panel
docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' proxy-panel
```

首次启动需要下载三类协议运行时，日志短时间出现下载和校验信息是正常现象。最终健康状态应为 `healthy`。

### 3.3 使用安装脚本

安装脚本默认拉取同一个 Docker Hub 镜像：

```bash
git clone https://github.com/saitomikuya/snell.sh.git proxy-panel
cd proxy-panel
sudo ./scripts/install.sh
```

安装器会：

- 检查 Linux、CPU 架构、Docker Engine 和 Compose 插件；
- 检查 TUN 和 IPv4 转发状态；Compose 需要映射 `/dev/net/tun`，缺少时安装器会停止并给出提示；
- 创建 `/opt/proxy-panel`；
- 写入 `/opt/proxy-panel/compose.yaml` 和 `.env`；
- 拉取镜像并等待健康检查；
- 重复运行时保留现有数据和配置。

### 3.4 环境变量

安装脚本方式的配置位于 `/opt/proxy-panel/.env`。修改后执行 `cd /opt/proxy-panel && sudo docker compose up -d` 使其生效。

| 变量 | 默认值 | 说明 |
|---|---|---|
| `PROXY_PANEL_IMAGE` | `saitomikuya/proxy-panel:latest` | 要运行的面板镜像 |
| `PANEL_BIND` | `0.0.0.0` | 面板监听地址；改为 `127.0.0.1` 可限制为本机访问 |
| `PANEL_PORT` | `8080` | 面板监听端口 |
| `PANEL_SECURE_COOKIE` | `0` | HTTPS 反代时设为 `1` |
| `PANEL_AUTO_INSTALL_RUNTIMES` | `1` | 首次启动自动下载固定版本运行时；设为 `0` 可关闭 |
| `TZ` | `Asia/Shanghai` | 容器时区 |

## 4. 第一次访问和改密

### 4.1 直接通过服务器 IP 访问（默认）

确认云安全组或宿主机防火墙已仅向你的可信公网 IP 放行 `8080/tcp`，然后在浏览器打开：

```text
http://你的服务器IP:8080
```

此方式无需 SSH 隧道。首次进入立即完成第 4.4 节的改密操作；长期使用建议升级为 HTTPS。

### 4.2 HTTPS 反向代理（推荐长期使用）

把 `PANEL_BIND` 改为 `127.0.0.1`，在宿主机使用 Caddy、Nginx 等反向代理到 `127.0.0.1:8080`。例如 Caddy：

```caddyfile
panel.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

并在 `/opt/proxy-panel/.env` 中设置：

```dotenv
PANEL_BIND=127.0.0.1
PANEL_PORT=8080
PANEL_SECURE_COOKIE=1
```

随后重建容器：

```bash
cd /opt/proxy-panel
sudo docker compose up -d
```

### 4.3 SSH 隧道（可选）

如果不希望开放 `8080/tcp`，先把 `PANEL_BIND` 改为 `127.0.0.1` 并重建容器，再在自己的电脑上执行：

```bash
ssh -L 8080:127.0.0.1:8080 root@你的服务器地址
```

保持终端连接，然后打开 <http://127.0.0.1:8080>。

### 4.4 首次登录

1. 使用默认密码 `password` 登录；
2. 系统会强制进入修改密码页面；
3. 新密码需要 4–128 个 Unicode 字符，不能继续使用 `password`；
4. 改密会注销全部会话，请使用新密码重新登录。

忘记密码时在 VPS 上执行：

```bash
docker exec proxy-panel panelctl reset-password password
```

密码会重置为 `password`，所有会话会失效，下次登录仍须立即改密。

## 5. 理解默认节点拓扑

默认 Snell 组合：

```text
客户端 ──TCP 8443──> ShadowTLS ──本机 TCP 6160──> Snell
```

客户端可以连接 Snell 的 `0.0.0.0:6160`，也可以连接 ShadowTLS 的公网端口。使用 ShadowTLS 时，TCP 流量会转发到本机 Snell 后端；ShadowTLS 只承载 TCP，Snell 的 UDP/QUIC 需要直连 Snell 端口并放行 UDP。

默认 SS-2022 组合：

```text
TCP：客户端 ──ShadowTLS 公网端口──> ShadowTLS ──本机转发──> SS-2022
UDP：客户端 ──SS-2022 原始 UDP 端口────────────────────> SS-2022
```

因此使用 SS-2022 + ShadowTLS 时，需要同时放行 ShadowTLS 的 TCP 端口和 SS-2022 的 UDP 端口。

## 6. 面板操作

### 6.1 仪表盘

仪表盘显示三类节点数量、累计流量和每个节点的实际进程状态：

- `运行中`：期望状态和实际进程一致；
- `已停止`：节点当前未运行；
- `启动中/停止中`：Agent 正在协调进程；
- `异常`：查看节点下方错误信息和“日志”。

右上角“刷新”会重新读取节点、流量、备份、审计和更新状态。旁边的“白天模式/黑夜模式”按钮可随时切换外观，浏览器会记住你的选择。

### 6.2 创建或编辑 Snell 节点

1. 进入“节点管理”；
2. 选择 `Snell` 标签页；
3. 点击“新建节点”；
4. 填写名称、监听地址和端口；
5. 当前请选择 `v5`；
6. 密钥留空会自动生成；编辑时留空会保留原密钥；
7. 点击“验证并应用”。

默认 Snell 节点使用 `0.0.0.0`，便于直接使用 TCP/UDP/QUIC；如果只把它作为 ShadowTLS TCP 后端并希望隐藏原始端口，可改为 `127.0.0.1`。改为公网监听后，请按需在云安全组和防火墙放行 TCP/UDP 端口。

### 6.3 创建或编辑 SS-2022 节点

1. 选择 `SS-2022` 标签页并点击“新建节点”；
2. 监听地址通常填写 `0.0.0.0`；
3. 选择加密方法，默认 `2022-blake3-aes-128-gcm`；
4. 选择 `TCP + UDP`、`仅 TCP` 或 `仅 UDP`；
5. 密钥留空会按所选算法自动生成；
6. 保存后按模式在安全组和防火墙放行 TCP/UDP 端口。

如果手工填写 SS-2022 密钥，它必须是 Base64，解码长度要与算法匹配：AES-128 为 16 字节，AES-256/ChaCha20 为 32 字节。

### 6.4 创建 ShadowTLS 节点

1. 先创建并确认 Snell 或 SS-2022 后端可运行；
2. 选择 `ShadowTLS` 标签页并点击“新建节点”；
3. 监听地址填写 `0.0.0.0`，端口选择未占用的 TCP 端口；
4. 在“后端节点”中选择目标 Snell/SS-2022；
5. 填写可信、可正常进行 TLS 握手的 SNI，例如 `www.microsoft.com`；
6. `wildcard-sni` 建议保持“关闭”；“全部”风险最高；
7. 密码留空自动生成，保存后放行 ShadowTLS TCP 端口。

不能在还有 ShadowTLS 依赖时删除后端节点。应先删除或改绑 ShadowTLS，再删除后端。

### 6.5 创建和管理 AnyConnect 节点

1. 选择 `AnyConnect` 标签页并新建节点，填写证书所覆盖的连接域名和监听端口；
2. 地址池默认 `192.168.144.0/24`，多个节点不能重叠；全隧道 DNS、中国直连 DNS、MTU、最大客户端数、单用户连接数和 UDP/DTLS 均可独立配置；
3. 填写完整证书链与加密私钥的 HTTPS 下载地址、下载密码；仅密码的分发中心可将 Basic 用户名留空；
4. 分别为证书和中国 CIDR 选择手动、每天或每周更新时间，时间按容器 `TZ` 解释；
5. 保存后进入“用户与资源”创建用户名/密码，并为每个用户选择“全隧道”“中国直连”或“登录时选择”；
6. 需要时可在同一弹窗立即刷新证书或 CIDR，并查看最近成功时间、指纹、条目数和脱敏错误。

证书更新只有在链顺序、有效期、连接域名和私钥全部匹配时才会原子切换。CIDR 默认读取 APNIC 中国 IPv4 数据，无损合并后为 Cisco 客户端选取覆盖面最大的 1197 条，再为中国直连 DNS 添加最多 3 条主机路由；也支持自定义 CIDR 或 ocserv `no-route` 数据源。更新异常会继续使用上一版本。完整前置条件与安全边界见 [AnyConnect / ocserv 使用说明](./ANYCONNECT.md)。

### 6.6 节点操作按钮

| 操作 | 效果 |
|---|---|
| 编辑 | 校验端口和配置，生成候选配置并应用；失败时保留最后可用版本 |
| 启动/停止 | 修改期望状态，由 Agent 启停协议进程 |
| 重启 | 按依赖关系重启节点 |
| 配置 | 显示完整客户端配置、分享链接和支持时的二维码 |
| 日志 | 显示最近 200 行脱敏运行日志 |
| 删除 | 删除节点；有下游依赖时会拒绝 |

## 7. 客户端导入

点击节点右侧“配置”会显示适合当前组合的内容：

- Snell：Surge `[Proxy]` 节点行，以及供特定客户端使用的 Snell 分享链接；
- SS-2022：SIP002 链接、Shadowrocket 二维码、Surge 和 Clash/Mihomo 配置；
- ShadowTLS + Snell：Surge 和 Clash/Mihomo 组合配置；
- ShadowTLS + SS-2022：Shadowrocket 组合链接/二维码、Surge 和 Clash/Mihomo 配置。
- AnyConnect：Cisco Secure Client 服务器地址，以及 OpenConnect 命令示例；密码不会显示在客户端配置中。

使用注意：

- 配置页面包含完整密钥，只在可信设备和安全会话中打开；
- Snell 没有通用、可验证的扫码 URI，因此不会生成误导性的二维码；
- 如果你通过 `127.0.0.1` 的 SSH 隧道访问面板，生成配置里的服务器地址可能显示为 `127.0.0.1` 或 `localhost`。导入前把它替换为 VPS 公网 IP 或代理域名；
- 通过 VPS 域名的 HTTPS 反代访问时，面板通常会使用该域名生成客户端配置；
- SS-2022 + ShadowTLS 的 UDP 仍使用配置中提示的原始 SS 端口；
- 客户端能力有差异，界面标记“不支持”的格式不要强行导入。

## 8. 流量管理

“流量管理”只列出监听公网地址的节点。统计以公网端口为边界，排除容器内 ShadowTLS 到后端的本机转发，避免重复计算。

页面顶部还提供整个项目的总流量计数器。项目总限额拥有与单节点一致的月限额、重置日、暂停、恢复和清零操作；达到总限额时会暂停所有公网节点，新计费周期只自动恢复没有被单独暂停的节点。

可执行的操作：

- “设置”：填写月流量限额（GiB）和每月重置日；`0` 表示不限；
- “暂停”：立即阻断该节点的公网端口；
- “恢复”：解除手动暂停；
- “清零”：清空当前周期上传/下载计数。

重置日范围为 1–28。达到限额后节点会自动暂停，到下一个计费周期自动恢复。流量统计来自 nftables 计数器，不等同于云厂商账单。

## 9. 更新与备份

### 9.1 检查上游版本

进入“更新与备份”，点击“检查上游版本”。面板只比较 Snell Server、shadowsocks-rust 和 ShadowTLS 三项运行时的官方版本，以及本地适配器声明的兼容版本。

“可更新”表示已进入内置校验目录，可以直接“备份并更新”。“待适配”表示发现了尚未内置校验的新版本，可以先从卡片中的官方来源下载对应服务器架构的发布文件，再使用下方上传入口手动适配。

### 9.2 应用运行时更新

点击“备份并更新”后，系统会：

1. 创建一致性备份；
2. 从白名单 HTTPS 地址下载运行时；
3. 校验大小、超时和固定 SHA256；
4. 分阶段安装；
5. 重启相关节点；
6. 失败时回滚节点版本。

更新期间相关节点会短暂中断，不要在关键业务窗口操作。

### 9.3 手动上传适配

当新版本显示“待适配”时，点击“上传适配”会定位到手动上传区域：

1. 确认服务器显示的架构为 `amd64` 或 `arm64`；
2. 从对应卡片的“查看上游来源”下载同架构官方文件；
3. 填写版本、安装包格式和官方公布的 64 位 SHA-256；
4. 选择文件并确认来源后，点击“上传、校验并适配”。

上传文件最大 100 MiB。服务器会再次校验 SHA-256、归档结构、预期二进制名称和 ELF 架构；只接受 Snell Server、shadowsocks-rust、ShadowTLS，不接受安装脚本。通过校验后会自动备份、原子安装并更新同类节点，节点应用失败时自动恢复原版本。中断遗留的临时上传文件会在下次上传时自动清理。

### 9.4 创建和恢复备份

“创建备份”会保存：

- SQLite 一致性快照；
- `/data/secrets/master.key`；
- 节点生成配置；
- 上游元数据。
- AnyConnect 用户、资源状态、当前证书/私钥和中国 CIDR 资源。

备份文件位于 `/opt/proxy-panel/backups`。建议另外复制到受保护的异地存储，因为删除 VPS 会同时丢失本机备份。

在线恢复前系统会再创建一个当前状态快照。恢复会短暂停止协议进程并注销全部登录会话。在线恢复要求备份中的主密钥与当前安装一致；跨机器或主密钥不同的恢复需要离线迁移整个数据目录。

完整迁移建议：

```bash
cd /opt
sudo tar -czf proxy-panel-data.tar.gz proxy-panel
```

请像保护密码一样保护该归档，它包含解密节点密钥所需的主密钥。

## 10. 日志、审计和安全设置

- “日志与审计 → 节点运行日志”：从左侧选择任意节点，按需查看最近 100、300 或 1000 行并手动刷新；
- “系统设置 → 日志存储”可关闭节点运行日志写入以节省性能；关闭后仍会读取进程输出避免阻塞，管理审计日志不受影响。日志总上限按 MB 配置，运行代理会定期删除最旧文件。
- “日志与审计 → 管理审计”：记录登录、改密、节点变更、启停、流量操作、日志设置、更新、备份和恢复；
- “系统设置”：修改管理员密码，并配置全部节点日志共用的空间上限（默认 10 MB，可设 1–1024 MB）。保存后会立即尝试清理，Agent 也每 3 分钟检查一次，超限时优先删除最旧的轮转日志；管理审计每小时裁剪为最近 500 条；
- Cookie 使用 `HttpOnly`、`SameSite=Strict`，写操作还要求 CSRF 和 Origin 校验；
- Web 进程不直接执行任意 Shell，受控 Agent 只接受固定 RPC。

## 11. 日常维护命令

```bash
# 查看容器和健康状态
docker ps --filter name=proxy-panel
docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' proxy-panel

# 查看容器日志
docker logs --tail=200 proxy-panel
docker logs -f proxy-panel

# 查看版本
docker exec proxy-panel panel version

# 重置管理员密码
docker exec proxy-panel panelctl reset-password password

# 清理本项目管理的 nftables 表
docker exec proxy-panel panelctl cleanup-firewall

# 重启容器
docker restart proxy-panel
```

## 12. 更新容器和卸载

使用安装脚本部署时，在仓库目录执行：

```bash
sudo ./scripts/update.sh
```

更新脚本会先把 `/opt/proxy-panel` 打包备份，拉取新镜像并等待健康检查；失败时回滚到旧镜像。

从旧版升级时，更新脚本不会擅自把已有的 `PANEL_BIND=127.0.0.1` 改成公网监听。希望切换为直接访问时，编辑 `/opt/proxy-panel/.env`，改为 `PANEL_BIND=0.0.0.0`，然后执行：

```bash
cd /opt/proxy-panel
sudo docker compose up -d --force-recreate
```

只删除容器、保留数据：

```bash
sudo ./scripts/uninstall.sh
```

重新安装时会继续使用 `/opt/proxy-panel` 中的数据库和密钥。

永久删除容器和全部数据：

```bash
sudo ./scripts/uninstall.sh --purge
```

`--purge` 不可恢复。执行前确认已经把需要的备份复制到其他位置。

## 13. 数据目录

容器内 `/data` 对应宿主机 `/opt/proxy-panel`：

```text
/opt/proxy-panel/
├── db/panel.db                 # SQLite 数据库
├── secrets/master.key          # 解密节点秘密的主密钥
├── config/                     # 当前生成配置及最后可用快照
├── anyconnect/                 # 已校验证书、私钥和中国 CIDR 资源
├── runtime/                    # 已校验运行时和 Agent Socket
├── logs/                       # 节点滚动日志
├── backups/                    # 面板创建的备份
├── upstream/                   # 上游版本元数据
├── compose.yaml                # 安装脚本生成的 Compose 文件
└── .env                        # 部署环境变量
```

不要只复制 `panel.db` 而遗漏 `master.key`，否则数据库中的加密秘密无法恢复。

## 14. 常见问题排查

### 面板打不开

```bash
docker ps --filter name=proxy-panel
docker logs --tail=200 proxy-panel
curl -v http://127.0.0.1:8080/healthz
```

- 直接访问方式：确认 `PANEL_BIND=0.0.0.0`，并检查云安全组和宿主机防火墙是否允许你的来源 IP 访问 `8080/tcp`；
- SSH 隧道方式：确认已把 `PANEL_BIND` 改为 `127.0.0.1`、SSH 会话仍在且两端端口都是 `8080`；
- HTTPS 方式：确认反向代理运行、域名解析正确，并能访问 `127.0.0.1:8080`；
- 修改过 `PANEL_PORT` 时，隧道和反代目标也要同步修改。

### 容器一直不健康

- 检查是否能访问 GitHub Release 和 `dl.nssurge.com`；
- 检查 `/opt/proxy-panel` 是否可写、磁盘是否已满；
- 检查 8080 和节点端口是否被其他进程占用；
- 不要在生产 VPS 设置 `PANEL_AUTO_INSTALL_RUNTIMES=0`，除非你已经手动准备了完整运行时目录。

### 节点显示异常或不断重启

- 先点“日志”查看最后错误；
- 确认没有切换到尚未准备运行时的 Snell v4/v6；
- 确认端口未被宿主机其他进程占用；
- 确认 ShadowTLS 后端存在且先于前端运行；
- 确认 SNI 是合法域名且服务器能访问目标站点。

### 客户端无法连接

- 核对安全组、UFW/nftables 是否放行了正确的 TCP/UDP 端口；
- 核对客户端服务器地址是否误用了 SSH 隧道的 `127.0.0.1`；
- 核对导入的是 ShadowTLS 公网端口还是后端端口；
- SS-2022 + ShadowTLS 使用 UDP 时，还要放行并填写原始 SS UDP 端口；
- 重新打开“配置”，避免使用已经修改过的旧密钥。

AnyConnect 还需检查：

- `/dev/net/tun` 已映射到容器，宿主机 `net.ipv4.ip_forward=1`；
- TCP 端口已放行；启用 UDP/DTLS 时同号 UDP 端口也已放行；
- Cisco 客户端使用的服务器域名与面板连接域名及证书 SAN 一致；
- 执行 `sudo nft list table inet proxy_panel` 能看到对应地址池的转发和 masquerade 规则；
- 执行 `sudo iptables -S PROXY-PANEL-VPN` 能看到同时限定节点 TUN 接口和 VPN 地址池的转发规则；
- 宿主机已有 UFW/firewalld 转发策略不会在 `DOCKER-USER` 之前额外丢弃 VPN 转发流量。

### 登录后立刻返回登录页

- HTTPS 反代时设置 `PANEL_SECURE_COOKIE=1`；
- 直接 HTTP 或 SSH 隧道测试时保持 `PANEL_SECURE_COOKIE=0`；
- 确认代理保留原始 `Host`，浏览器不要混用多个域名访问同一面板；
- 重置或修改密码会主动注销旧会话，这是预期行为。

### 流量一直为零

- 只有公网监听节点会显示在流量页；
- 先确认客户端确实通过该节点端口产生流量；
- 确认容器保留 `NET_ADMIN`、`NET_RAW`，且宿主机 nftables 可用；
- 执行 `sudo nft list table inet proxy_panel` 检查本项目专属表是否存在。

## 15. 本地开发与验证

要求 Go 1.25+、Node.js 24、pnpm 10：

```bash
cd web
pnpm install --frozen-lockfile
pnpm test
pnpm run build
cd ..
go vet ./...
go test -race ./...
go build -o bin/panel ./cmd/panel
```

macOS 可测试面板和 API，但不会安装 Linux 运行时或修改 nftables：

```bash
export PANEL_DATA_DIR="$PWD/.dev-data"
export PANEL_AUTO_INSTALL_RUNTIMES=0
./bin/panel agent
```

另开一个终端：

```bash
export PANEL_DATA_DIR="$PWD/.dev-data"
export PANEL_AUTO_INSTALL_RUNTIMES=0
export PANEL_BIND=127.0.0.1
./bin/panel server
```

打开 <http://127.0.0.1:8080>。

## 16. 反馈问题时请提供

提交 Issue 前请先删除密钥、密码、客户端链接和公网 IP，再附上：

- 系统版本与 CPU 架构；
- Docker 与 Compose 版本；
- 面板版本；
- 节点类型、监听模式和出现问题的操作；
- 面板节点日志和 `docker logs --tail=200 proxy-panel` 中相关的脱敏片段；
- 使用服务器 IP 直接访问、SSH 隧道或 HTTPS 反向代理中的哪一种方式。

不要公开上传 `/opt/proxy-panel`、备份归档、`panel.db` 或 `master.key`。
