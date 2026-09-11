# 实现状态

更新时间：2026-09-10

| 里程碑 | 状态 | 说明 |
|---|---|---|
| M0 仓库与基线 | 完成 | Go/Vue、迁移、容器、CI、固定依赖 |
| M1 认证与面板 | 完成 | 默认密码、强制改密、Argon2id、Session、CSRF、限速、中文响应式 UI |
| M2 Runtime Agent | 完成 | 类型化 Unix RPC、配置事务、进程组、依赖、日志、退避、假运行时集成测试 |
| M3 真实协议 | 部分完成 | Snell、SS-2022、ShadowTLS 与 AnyConnect/ocserv 配置和运行时完成；Snell v4/v6 与 simple-obfs 自动安装待补 |
| M4 网络高级功能 | 部分完成 | 专属 nftables 表、字节采样、流量限额、AnyConnect NAT 及中国 CIDR 定时原子更新完成；更完整宿主机指标待补 |
| M5 更新与备份 | 部分完成 | 三项运行时官方版本对比、安全一键更新、带 SHA-256/ELF 架构验证的手动上传适配、自动备份、节点回滚和备份恢复完成；异步 Job 进度待补 |
| M6 发布 | 部分完成 | Docker、Compose、s6、脚本、CI、文档和 Docker Hub 多架构镜像完成；安全扫描与签名待外部 CI |

## 已验证路径

- 默认密码登录返回受限 Session。
- 受限 Session 访问 `/api/v1/nodes` 返回 `403 PASSWORD_CHANGE_REQUIRED`。
- 缺少 CSRF 的改密请求被拒绝。
- 合规改密后全部旧 Session 失效，新密码可重新登录。
- SQLite 并发首次迁移和默认四节点初始化幂等，包括 Snell + ShadowTLS 与 SS-2022 + ShadowTLS。
- 假协议二进制可完成配置应用、启动、停止和退出状态协调。
- 配置与客户端链接生成、SS-2022 密钥长度和日志脱敏单元测试通过。
- 公网端口 nftables 字节采样、节点与项目月周期切换、流量限额和手动/自动暂停恢复已通过单元测试。
- 备份可恢复节点数据，恢复前自动创建快照并注销 Session。
- 手动上传运行时会限制类型、版本、格式和大小，并复核 SHA-256、预期二进制名称及 ELF 架构。
- 同一 AnyConnect 节点可按用户固定“全隧道”或“中国直连”，也可允许用户登录时选择；用户名密码和证书分发凭据均加密保存。
- AnyConnect 证书会校验 PEM 链顺序、有效期、域名和私钥配对，CIDR/证书更新失败均保留上一可用版本。
- ocserv 1.5.0 官方源码包固定 SHA-256 的多阶段容器构建已通过，运行容器只需映射 TUN 并保留 `NET_ADMIN`/`NET_RAW`。
- Vue 登录、改密、桌面仪表盘、项目总流量、组合配置二维码、三项运行时更新中心、节点日志、中文审计和 390px 手机布局经过本地浏览器检查。

## 发布阻塞项

1. Snell 二进制再分发许可仍未确认，不能内置到镜像。
2. 已有代理协议完成 Debian 实机验收；AnyConnect 仍需在带 TUN 的一次性 Linux VPS 上完成 Cisco Secure Client 与 OpenConnect 端到端互通。
3. Docker Hub 已发布 `linux/amd64` 与 `linux/arm64` 镜像；仓库仍未配置自动签名和发布凭据，因此 CI 只构建本地测试镜像。
