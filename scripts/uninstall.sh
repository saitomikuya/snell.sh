#!/bin/sh
set -eu
BASE_DIR=/opt/proxy-panel
[ "$(id -u)" -eq 0 ] || { echo "请以 root 运行" >&2; exit 1; }
if docker inspect proxy-panel >/dev/null 2>&1; then
  docker exec proxy-panel panelctl cleanup-firewall || true
fi
if [ -f "$BASE_DIR/compose.yaml" ]; then (cd "$BASE_DIR" && docker compose down); else docker rm -f proxy-panel 2>/dev/null || true; fi
if [ "${1:-}" = "--purge" ]; then
  [ "$BASE_DIR" = /opt/proxy-panel ] || exit 1
  rm -rf -- "$BASE_DIR"
  echo "容器与 /opt/proxy-panel 数据已永久删除"
else
  echo "容器已删除；数据保留在 /opt/proxy-panel，可重新安装恢复"
fi
