#!/bin/sh
set -eu

DATA_DIR=/app/data

if [ "$(id -u)" = "0" ]; then
    mkdir -p "$DATA_DIR"
    if ! chown -R 10001:10001 "$DATA_DIR"; then
        echo "无法修复 $DATA_DIR 权限；请检查宿主机挂载、只读属性或 NAS ACL。" >&2
        exit 1
    fi
    exec su-exec 10001:10001 /app/ctyun-auto "$@"
fi

exec /app/ctyun-auto "$@"
