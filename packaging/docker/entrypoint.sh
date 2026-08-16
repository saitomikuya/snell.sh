#!/bin/sh
set -eu
install -d -m 2770 -o "${PANEL_UID:-10001}" -g "${PANEL_GID:-10001}" /data
exec "$@"
