#!/bin/sh
set -eu

IMAGE="ctyun-keeper:local"
CONTAINER="ctyun-keeper"
PORT="${APP_PORT:-9845}"
DATA_DIR="${CTYUN_DATA_DIR:-$(pwd)/ctyun-keeper-data}"
VERSION="$(tr -d '[:space:]' < VERSION)"

case "$VERSION" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "VERSION 必须使用 X.Y.Z 格式" >&2; exit 1 ;;
esac
command -v docker >/dev/null 2>&1 || { echo "未安装 Docker" >&2; exit 1; }
mkdir -p "$DATA_DIR"

echo "构建 CtYunKeeper v$VERSION（Go + Alpine）..."
docker build -f app/Dockerfile --build-arg APP_VERSION="$VERSION" -t "$IMAGE" .
if docker container inspect "$CONTAINER" >/dev/null 2>&1; then
  docker rm -f "$CONTAINER" >/dev/null
fi
docker run -d \
  --name "$CONTAINER" \
  -p "$PORT:9845" \
  -v "$DATA_DIR:/app/data" \
  --restart unless-stopped \
  "$IMAGE" >/dev/null

echo "部署完成：http://127.0.0.1:$PORT"
echo "账号、设备短信验证、任务和自动兑换均在 Web 面板配置。"
echo "数据目录：$DATA_DIR"
echo "日志命令：docker logs -f $CONTAINER"
