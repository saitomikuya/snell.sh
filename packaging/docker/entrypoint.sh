#!/bin/sh
set -eu
install -d -m 2770 -o "${PANEL_UID:-10001}" -g "${PANEL_GID:-10001}" /data
# The agent service intentionally runs as root for firewall and process
# management. Repair persistent trees left by older images where its atomic
# config/certificate writes were root-owned, otherwise panel-web cannot read
# them while creating an import safety backup.
for path in /data/config /data/anyconnect /data/backups; do
  if [ -e "$path" ]; then
    chown -R "${PANEL_UID:-10001}:${PANEL_GID:-10001}" "$path"
  fi
done

# Alpine and other minimal Linux hosts may have the TUN kernel module loaded
# without creating the device node. When requested, create the standard Linux
# TUN character device inside the container. Docker still needs a matching
# device-cgroup rule (see the Alpine instructions); an explicit --device mount
# remains the preferred and most portable setup when the host node exists.
if [ "${PANEL_AUTO_CREATE_TUN:-1}" = "1" ] && [ ! -c /dev/net/tun ]; then
  if install -d -m 0755 /dev/net 2>/dev/null && mknod /dev/net/tun c 10 200 2>/dev/null; then
    chmod 0666 /dev/net/tun
    echo "[proxy-panel] created /dev/net/tun in the container"
  else
    echo "[proxy-panel] warning: /dev/net/tun is unavailable; AnyConnect nodes require the host TUN module and device mapping" >&2
  fi
fi
install -d -m 0711 -o 0 -g 0 "${PANEL_ANYCONNECT_RUNTIME_DIR:-/run/proxy-panel/anyconnect}"
exec "$@"
