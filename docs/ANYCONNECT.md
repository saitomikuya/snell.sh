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

面板只在自己的 `inet proxy_panel` nftables 表中添加 VPN 转发和 masquerade 规则，不修改 UFW、firewalld、Docker 链或其他表；节点停止、删除或执行 `panelctl cleanup-firewall` 时会清理这些规则。TUN 设备和转发/NAT 规则是 VPN 工作所必需的最小宿主机网络影响。

## 创建节点

在“节点管理 → AnyConnect → 新建节点”中配置：

- 连接域名：必须被服务器证书的 SAN 覆盖；
- 监听端口：默认 `443`，TCP/UDP 使用同一端口；
- VPN 地址池：默认 `192.168.144.0/24`，多个 AnyConnect 节点的地址池不能重叠；
- DNS：默认 `1.1.1.1,8.8.8.8`；
- MTU：默认 `1340`；
- 最大客户端数：默认 `32`；
- 单用户同时连接数：默认 `2`；
- UDP/DTLS：默认启用，可按节点关闭。

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

默认 CIDR 来源是 APNIC 的 `delegated-apnic-latest`，只接收国家码为 `CN`、类型为 IPv4、状态为 allocated/assigned 的记录。也可换成每行一个规范公网 IPv4 CIDR 的 HTTPS 数据源。更新内容必须通过解析和条目数量合理性检查，之后才会原子替换；失败保留上一版。

证书和 CIDR 均支持“仅手动”“每天某时”或“每周某天某时”。计划按容器 `TZ` 执行；失败后至少间隔 30 分钟再自动重试。也可在“用户与资源”中立即刷新。

## 客户端

Cisco Secure Client 新建连接时填写面板“配置”弹窗中的服务器地址，再输入分配的用户名和密码。OpenConnect 示例：

```bash
sudo openconnect --protocol=anyconnect --user USERNAME https://vpn.example.com
```

若端口不是 443，服务器地址需包含端口。固定路由组用户不需要选择；“登录时选择”用户会看到路由组选项。
