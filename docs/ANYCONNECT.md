# AnyConnect / ocserv 使用说明

Proxy Panel 可在同一个容器中管理多个 ocserv 1.5.0 节点，兼容 Cisco Secure Client（AnyConnect）和 OpenConnect。认证仅使用面板管理的用户名/密码，不启用客户端证书认证。

## 宿主机前置条件

容器不使用 `privileged` 或 `SYS_ADMIN`。镜像在编译 ocserv 时禁用 Linux namespaces，并使用 seccomp 隔离的非特权 worker；VPN 数据转发必须具备以下条件：

- 宿主机存在 `/dev/net/tun`，Compose 将它映射进容器；
- 容器保留 `NET_ADMIN` 和 `NET_RAW`；
- 宿主机管理员已经启用 `net.ipv4.ip_forward=1`；面板只检查，不会修改此 sysctl；
- 云安全组或防火墙放行节点的 TCP 端口；启用 DTLS 时还要放行同号 UDP 端口。

检查示例：

```bash
test -c /dev/net/tun && echo TUN_OK
sysctl net.ipv4.ip_forward
```

面板在自己的 `inet proxy_panel` nftables 表中添加 VPN 转发和 masquerade 规则。由于 Docker 通常把宿主机 `FORWARD` 默认策略设为 `DROP`，面板还会在 Docker 官方预留的 `DOCKER-USER` 钩子下挂接专用 `PROXY-PANEL-VPN` 链；规则同时限定节点的 TUN 接口和 VPN 地址池，不修改 Docker 自身规则或其他转发流量。节点停止或删除时会清理对应规则。TUN 设备和这些可追踪、可清理的转发/NAT 规则是 VPN 工作所必需的最小宿主机网络影响。

## 创建节点

在“节点管理 → AnyConnect → 新建节点”中配置：

- 连接域名：必须被服务器证书的 SAN 覆盖；
- 监听端口：默认 `443`，TCP/UDP 使用同一端口；
- VPN 地址池：默认 `192.168.144.0/24`，多个 AnyConnect 节点的地址池不能重叠；
- 全隧道 DNS：默认 `1.1.1.1,8.8.8.8`，DNS 查询进入 VPN；
- 中国直连 DNS：默认 `223.5.5.5,119.29.29.29`，`ChinaDirect` 不强制 DNS 进入隧道，这些国内 DNS 随中国路由从客户端本地网络直连；
- MTU：默认 `1340`；
- 最大客户端数：默认 `32`；
- 单用户同时连接数：默认 `2`；
- UDP/DTLS：默认启用，可按节点关闭。

### 客户端节点列表（profile.xml）

勾选“下发可选服务器列表”后，可按一行一个节点填写：

```text
日本东京 | jp.example.com:443
美国西海岸 | us.example.com:443
```

保存时面板会自动把当前节点放在第一项，并在 ocserv 配置中启用 `user-profile`。Cisco Secure Client 可在连接页选择列表中的其他服务器；各独立服务器面板手动填写相同文本即可保持列表一致。列表只影响客户端服务器选择，不改变 AnyConnect 用户的全隧道/中国直连路由组。

镜像内置的 ocserv 固定为 1.5.0。其官方源码发布包在构建时通过固定 SHA-256 校验，不执行远程安装脚本。

## 证书分发中心

分别填写完整证书链和私钥的 HTTPS 下载地址，可选 Basic Auth 用户名，并填写下载密码。用户名为空时仍会以空用户名发送 Basic Auth，适配仅使用密码的分发中心。私钥口令留空时默认与下载密码相同。

每次更新会依次检查：

1. 下载地址必须是 HTTPS，禁止 URL 内嵌凭据；
2. DNS 解析结果不得是内网、回环、链路本地或保留地址；
3. 重定向不得离开原始主机，响应大小不得超过 8 MiB；
4. PEM 证书链可解析、链顺序与逐级签名正确，叶证书已生效且剩余有效期超过 24 小时；
5. 叶证书匹配节点连接域名；
6. 私钥能用配置的口令解密，并与证书公钥一致。

全部通过后才会原子切换到新版本；失败时继续使用上一版，并在“用户与资源”中显示错误。证书下载密码、私钥口令和用户密码均保存在现有 AES-256-GCM 加密秘密存储中，不会出现在节点 API、审计详情或客户端配置里。

## 中国直连与用户路由组

每个用户可独立分配：

- `全隧道`：默认路由进入 VPN；
- `中国直连`：默认路由进入 VPN，但中国大陆 IPv4 前缀通过 `no-route` 下发，由客户端本地直连；
- `登录时选择`：连接时在上述两组中选择。

默认 CIDR 来源是 APNIC 的 `delegated-apnic-latest`，只接收国家码为 `CN`、类型为 IPv4、状态为 allocated/assigned 的记录。面板会先排除特殊用途地址、无损合并连续网段，然后按覆盖地址数从大到小下发最多 1197 条，为 1–3 个中国直连 DNS 保留主机路由；未被选中的小网段继续走 VPN，不会将非中国地址扩大为本地直连。

面板的 `cidr` 格式同时接受每行一个的 CIDR 和 `no-route = IP/子网掩码` 格式，因此也可使用 [`lgdglgc/ocserv`](https://github.com/lgdglgc/ocserv) 这类约 200 条的粗粒度列表。这类列表条目更少，但可能将较多相邻非中国地址也视为本地直连，应由管理员明确选择。

Cisco Secure Client 5.1 的静态 IPv4 分流上限为 1200 条。APNIC 全量 CN 分配数据会产生数千条路由，服务端虽然能显示，客户端却会截断。当前筛选策略在不误放非中国地址的前提下，对最新 APNIC 数据可覆盖约 98% 的中国 IPv4 地址空间。自定义 CIDR/no-route 列表超过 1197 条时会拒绝更新，避免加上 DNS 直连路由后超限而静默漏分流。更新内容必须通过公网地址、条目数和覆盖地址数检查，之后才会原子替换；失败保留上一版。已缓存的超限路由会在下次配置生成时自动按新规则重新拉取。

证书和 CIDR 均支持“仅手动”“每天某时”或“每周某天某时”。计划按容器 `TZ` 执行；失败后至少间隔 30 分钟再自动重试。也可在“用户与资源”中立即刷新。

## 客户端

Cisco Secure Client 新建连接时填写面板“配置”弹窗中的服务器地址，再输入分配的用户名和密码。OpenConnect 示例：

```bash
sudo openconnect --protocol=anyconnect --user USERNAME https://vpn.example.com
```

若端口不是 443，服务器地址需包含端口。固定路由组用户不需要选择；“登录时选择”用户会看到路由组选项。
