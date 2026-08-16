#!/bin/sh
set -eu

BASE_DIR=/opt/proxy-panel
IMAGE=${PROXY_PANEL_IMAGE:-saitomikuya/proxy-panel:latest}

[ "$(id -u)" -eq 0 ] || { echo "请以 root 运行安装器" >&2; exit 1; }
[ "$(uname -s)" = Linux ] || { echo "仅支持 Linux" >&2; exit 1; }
case "$(uname -m)" in x86_64|aarch64|arm64) ;; *) echo "仅支持 amd64/arm64" >&2; exit 1;; esac
command -v docker >/dev/null 2>&1 || { echo "未安装 Docker Engine，请先按 Docker 官方文档安装" >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "缺少 Docker Compose 插件" >&2; exit 1; }

install -d -m 0750 "$BASE_DIR"
if [ ! -f "$BASE_DIR/.env" ]; then
  umask 077
  {
    echo "PROXY_PANEL_IMAGE=$IMAGE"
    echo "PANEL_BIND=${PANEL_BIND:-0.0.0.0}"
    echo "PANEL_PORT=${PANEL_PORT:-8080}"
    echo "PANEL_SECURE_COOKIE=${PANEL_SECURE_COOKIE:-0}"
    echo "TZ=${TZ:-Asia/Shanghai}"
  } > "$BASE_DIR/.env"
fi
if [ ! -f "$BASE_DIR/compose.yaml" ]; then
  install -m 0644 "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)/compose.example.yaml" "$BASE_DIR/compose.yaml"
fi

cd "$BASE_DIR"
docker compose pull
docker compose up -d
attempt=0
while [ "$attempt" -lt 60 ]; do
  status=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' proxy-panel 2>/dev/null || true)
  [ "$status" = healthy ] && break
  [ "$status" = unhealthy ] && { docker compose logs --tail=100; exit 1; }
  attempt=$((attempt+1)); sleep 2
done
[ "${status:-}" = healthy ] || { echo "容器未在超时时间内健康启动" >&2; exit 1; }
port=$(sed -n 's/^PANEL_PORT=//p' "$BASE_DIR/.env" | tail -n 1)
bind=$(sed -n 's/^PANEL_BIND=//p' "$BASE_DIR/.env" | tail -n 1)
port=${port:-8080}
if [ "$bind" = "127.0.0.1" ] || [ "$bind" = "::1" ] || [ "$bind" = "localhost" ]; then
  echo "安装完成，但检测到已有本机监听配置：http://127.0.0.1:$port"
  echo "如需直接访问，请将 $BASE_DIR/.env 中 PANEL_BIND 改为 0.0.0.0 后重新运行 docker compose up -d"
else
  echo "安装完成：http://你的服务器IP:$port"
fi
echo "默认密码：password（首次登录必须修改）"
echo "请在云安全组/防火墙中仅向可信 IP 放行 TCP $port，并尽快配置 HTTPS"
