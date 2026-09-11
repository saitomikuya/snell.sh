# 安全说明

## 部署边界

- 不要使用 `--privileged`，不要挂载 Docker Socket 或宿主机根目录。
- Docker 部署默认绑定 `0.0.0.0:8080`，方便首次直接访问。必须在云安全组或宿主机防火墙中只向可信 IP 放行管理端口，并立即修改默认密码；长期公网使用建议配置 HTTPS，把 `PANEL_BIND` 改为 `127.0.0.1`，同时设置 `PANEL_SECURE_COOKIE=1`。
- `/data` 同时包含数据库、主密钥、节点秘密和备份，应纳入加密磁盘及访问控制。
- 容器能力只允许 `NET_ADMIN` 和 `NET_RAW`，并只映射 `/dev/net/tun`；不使用 `privileged` 或 `SYS_ADMIN`。Web 进程固定使用 UID/GID 10001，Agent 保留受控网络权限。

## 下载边界

运行时安装只接受嵌入目录中的固定版本、URL 和 SHA256。实现拒绝 HTTP、未知主机、超大文件、过多重定向和校验不一致；不执行远程脚本。

AnyConnect 证书与中国 CIDR 下载额外拒绝内网、回环、链路本地和保留解析地址，且跨主机重定向被禁止。证书必须通过有效期、域名和私钥匹配校验后才会原子替换；凭据只保存在加密秘密存储中。

## 防火墙边界

Agent 只创建或删除 `inet proxy_panel` 表中的项目规则。AnyConnect 需要 TUN、IPv4 转发和 NAT；面板只检查宿主机 `net.ipv4.ip_forward=1`，不会替用户写 sysctl，也不会修改 UFW、firewalld、Docker 链或其他 nftables 表。`panelctl cleanup-firewall` 不读取 API 传入的命令，不清理其他表。不要在日常开发机设置特权测试变量。

## 漏洞报告

请不要在公开 issue 中附带真实 PSK、SS 密钥、ShadowTLS 密码、AnyConnect 用户密码、证书下载密码、私钥、Cookie、Session Token、数据库或诊断包。报告应包含脱敏后的复现步骤、受影响版本和预期影响。
