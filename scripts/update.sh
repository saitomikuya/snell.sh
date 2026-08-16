#!/bin/sh
set -eu
BASE_DIR=/opt/proxy-panel
[ "$(id -u)" -eq 0 ] || { echo "请以 root 运行" >&2; exit 1; }
[ -f "$BASE_DIR/compose.yaml" ] || { echo "尚未安装 Proxy Panel" >&2; exit 1; }
cd "$BASE_DIR"
old_image=$(docker inspect --format '{{.Config.Image}}' proxy-panel)
backup="proxy-panel-data-$(date -u +%Y%m%dT%H%M%SZ).tar.gz"
tar -C "$BASE_DIR" --exclude='./*.tar.gz' -czf "$backup" .
docker compose pull
if docker compose up -d && timeout 180 sh -c 'until [ "$(docker inspect --format '"'"'{{if .State.Health}}{{.State.Health.Status}}{{end}}'"'"' proxy-panel 2>/dev/null)" = healthy ]; do sleep 3; done'; then
  echo "更新成功；更新前备份：$BASE_DIR/$backup"
  if grep -q '^PANEL_BIND=127\.0\.0\.1$' "$BASE_DIR/.env" 2>/dev/null; then
    echo "已保留原有本机监听配置；如需通过服务器 IP 直接访问，请将 $BASE_DIR/.env 中 PANEL_BIND 改为 0.0.0.0 后重建容器"
  fi
  exit 0
fi
echo "健康检查失败，正在回滚到 $old_image" >&2
PROXY_PANEL_IMAGE="$old_image" docker compose up -d --force-recreate
exit 1
