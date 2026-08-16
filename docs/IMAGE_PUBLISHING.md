# Docker Hub 镜像发布说明

本仓库通过 [`.github/workflows/docker-publish.yml`](../.github/workflows/docker-publish.yml) 构建并发布 `linux/amd64`、`linux/arm64` 多架构镜像。

## 首次配置

在 GitHub 仓库的 `Settings → Secrets and variables → Actions` 中创建两个 Repository secret：

| Secret | 内容 |
|---|---|
| `DOCKERHUB_USERNAME` | Docker Hub 用户名，例如 `saitomikuya` |
| `DOCKERHUB_TOKEN` | Docker Hub 账号创建的只读写 Access Token，不要使用账号明文密码 |

Docker Hub Access Token 至少需要对 `saitomikuya/proxy-panel` 具备 Read & Write 权限。

## 触发规则

- 推送到 `main`：发布 `latest` 与 `sha-<短提交号>`；
- 推送 `v*` 标签：发布同名版本标签与 `sha-<短提交号>`；
- Actions 页面手动运行：按当前分支生成对应标签。

## 验证多架构清单

工作流成功后执行：

```bash
docker buildx imagetools inspect saitomikuya/proxy-panel:latest
```

输出应同时包含：

```text
linux/amd64
linux/arm64
```

不要把 Docker Hub Token 写入仓库、工作流 YAML、Issue 或构建日志。
