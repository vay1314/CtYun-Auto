#!/bin/bash
set +e

RESTART_AT_FILE="/tmp/ctyun_restart_at"

should_restart_now() {
    [ -f "$RESTART_AT_FILE" ] || return 1
    local restart_at
    restart_at=$(tr -d '[:space:]' < "$RESTART_AT_FILE" 2>/dev/null)
    if ! [[ "$restart_at" =~ ^[0-9]+$ ]]; then
        echo "[!] 无效的 CtYun 重启计划，已清理。"
        rm -f "$RESTART_AT_FILE"
        return 1
    fi
    [ "$(date +%s)" -ge "$restart_at" ]
}

run_with_watch() {
    local duration="$1"
    local scheduled_restart=0

    timeout --foreground "$duration" dotnet /app/CtYun.dll &
    local process_pid=$!

    while kill -0 "$process_pid" 2>/dev/null; do
        if should_restart_now; then
            echo "[*] 自动兑换后的 CtYun 重启计划已到时。"
            scheduled_restart=1
            rm -f "$RESTART_AT_FILE"
            kill "$process_pid" 2>/dev/null || true
            sleep 1
            pkill -f "dotnet /app/CtYun.dll" 2>/dev/null || true
            break
        fi
        sleep 2
    done

    wait "$process_pid"
    local exit_code=$?
    [ "$scheduled_restart" -eq 1 ] && return 200
    return "$exit_code"
}

while true; do
    echo "======================================================"
    echo "[*] 启动 CtYun.dll..."
    run_with_watch 24h
    exit_code=$?

    if [ "$exit_code" -eq 200 ]; then
        echo "[*] 已按兑换计划完成重启。"
    elif [ "$exit_code" -eq 124 ]; then
        echo "[*] CtYun 已运行 24 小时，执行周期重启。"
    else
        echo "[!] CtYun 已退出，退出码: $exit_code。"
    fi

    echo "[*] 120 秒后重新启动 CtYun。"
    sleep 120
done
