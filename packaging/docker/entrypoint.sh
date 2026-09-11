#!/bin/sh
set -eu
install -d -m 2770 -o "${PANEL_UID:-10001}" -g "${PANEL_GID:-10001}" /data
install -d -m 0711 -o 0 -g 0 "${PANEL_ANYCONNECT_RUNTIME_DIR:-/run/proxy-panel/anyconnect}"
exec "$@"
