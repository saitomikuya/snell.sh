# 安全说明

## 部署边界

- 不要使用 `--privileged`，不要挂载 Docker Socket 或宿主机根目录。
- Docker 部署默认绑定 `0.0.0.0:8080`，方便首次直接访问。必须在云安全组或宿主机防火墙中只向可信 IP 放行管理端口，并立即修改默认密码；长期公网使用建议配置 HTTPS，把 `PANEL_BIND` 改为 `127.0.0.1`，同时设置 `PANEL_SECURE_COOKIE=1`。
- `/data` 同时包含数据库、主密钥、节点秘密和备份，应纳入加密磁盘及访问控制。
- 容器能力只允许 `NET_ADMIN` 和 `NET_RAW`；Web 进程固定使用 UID/GID 10001，Agent 保留受控网络权限。

## 下载边界

运行时安装只接受嵌入目录中的固定版本、URL 和 SHA256。实现拒绝 HTTP、未知主机、超大文件、过多重定向和校验不一致；不执行远程脚本。

## 防火墙边界

Agent 只创建或删除 `inet proxy_panel` 表中的项目规则。`panelctl cleanup-firewall` 不读取 API 传入的命令，不清理其他表。不要在日常开发机设置特权测试变量。

## 漏洞报告

请不要在公开 issue 中附带真实 PSK、SS 密钥、ShadowTLS 密码、Cookie、Session Token、数据库或诊断包。报告应包含脱敏后的复现步骤、受影响版本和预期影响。
