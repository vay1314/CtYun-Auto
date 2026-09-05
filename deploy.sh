#!/bin/bash
set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

run_pc_login_until_hang_then_background() {
    local container_name="$1"
    local log_file="/app/data/pc_login_once.log"
    local pid_file="/app/data/pc_login_once.pid"
    local exit_file="/app/data/pc_login_once.exit"
    local max_wait_seconds=900
    local waited_seconds=0
    local printed_lines=0

    echo -e "${YELLOW}[*] 启动云电脑一小时使用积分任务...${NC}"

    docker exec "$container_name" sh -c "rm -f '$log_file' '$pid_file' '$exit_file'; nohup sh -c 'env PYTHONUNBUFFERED=1 python3 -u /app/pc_login.py > \"$log_file\" 2>&1; echo \$? > \"$exit_file\"' >/dev/null 2>&1 & echo \$! > '$pid_file'"

    while true; do
        local new_lines
        new_lines=$(docker exec "$container_name" sh -c "if [ -f '$log_file' ]; then sed -n '$((printed_lines + 1)),\$p' '$log_file'; fi")

        if [ -n "$new_lines" ]; then
            while IFS= read -r line; do
                echo "$line"
                printed_lines=$((printed_lines + 1))
            done <<< "$new_lines"
        fi

        if docker exec "$container_name" sh -c "grep -aEq '使用1小时进度' '$log_file'"; then
            echo -e "${GREEN}[*] 已检测到积分任务轮询，已转后台继续运行。${NC}"
            return 0
        fi

        if ! docker exec "$container_name" sh -c "[ -f '$pid_file' ] && kill -0 \$(cat '$pid_file') 2>/dev/null"; then
            local exit_code
            exit_code=$(docker exec "$container_name" sh -c "cat '$exit_file' 2>/dev/null || echo 1")
            if [ "$exit_code" = "0" ]; then
                echo -e "${GREEN}[*] 首次积分任务已执行完成。${NC}"
                return 0
            fi
            echo -e "${RED}[!] 任务已提前退出（退出码 $exit_code）${NC}"
            echo -e "${YELLOW}[*] 最近日志如下：${NC}"
            docker exec "$container_name" sh -c "tail -n 30 '$log_file' 2>/dev/null || true"
            return 1
        fi

        sleep 2
        waited_seconds=$((waited_seconds + 2))
        if [ "$waited_seconds" -ge "$max_wait_seconds" ]; then
            echo -e "${YELLOW}[!] 等待超过 $((max_wait_seconds / 60)) 分钟，未检测到挂机提示。继续后台运行。${NC}"
            return 0
        fi
    done
}

wait_for_supervisor() {
    local container_name="$1"
    local attempt
    for attempt in $(seq 1 30); do
        if docker exec "$container_name" supervisorctl -c /app/supervisord.conf status >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    echo -e "${RED}[!] Supervisor 启动超时，请查看容器日志。${NC}"
    return 1
}

echo -e "${GREEN}=== 天翼云电脑保活自动化部署 ===${NC}\n"

# 1. 环境与目录检查
if ! command -v docker &> /dev/null; then
    echo -e "${RED}[!] 错误: 未安装 Docker。${NC}"
    exit 1
fi

if [ ! -f "app/Dockerfile" ]; then
    echo -e "${RED}[!] 错误: 未找到 app/Dockerfile，请确认处于项目根目录。${NC}"
    exit 1
fi

# 2. 读取配置
read -e -p "账号 (APP_USER): " APP_USER
[ -z "$APP_USER" ] && {
    echo -e "${RED}[!] 账号不能为空。${NC}"
    exit 1
}

read -r -s -p "密码 (APP_PASSWORD): " APP_PASSWORD
echo
echo ""
[ -z "$APP_PASSWORD" ] && {
    echo -e "${RED}[!] 密码不能为空。${NC}"
    exit 1
}

# 与 Web 数据库和 CtYun.dll 共用同一稳定设备码。
DEVICECODE="web_$(printf '%s' "$APP_USER" | sha256sum | cut -c1-32)"

while true; do
    read -e -p "数据目录 [留空默认 ~/data]: " INPUT_DIR
    if [ -z "$INPUT_DIR" ]; then
        DATA_DIR="$HOME/data"
        break
    elif [[ "$INPUT_DIR" == /* ]]; then
        DATA_DIR="$INPUT_DIR"
        break
    elif [[ "$INPUT_DIR" == ~* ]]; then
        DATA_DIR="${INPUT_DIR/#\~/$HOME}"
        break
    else
        echo -e "${RED}[!] 请输入绝对路径（以 / 或 ~ 开头）。${NC}"
    fi
done

mkdir -p "$DATA_DIR"

# 3. 构建镜像并清理同名容器
echo -e "${YELLOW}[*] 正在构建镜像...${NC}"
CTYUN_REF=$(git ls-remote https://github.com/leleji/CtYun.git refs/heads/master | awk 'NR == 1 {print $1}')
if ! [[ "$CTYUN_REF" =~ ^[0-9a-f]{40}$ ]]; then
    echo -e "${RED}[!] 无法获取 CtYun 最新源码提交。${NC}"
    exit 1
fi
APP_VERSION=$(tr -d '[:space:]' < VERSION)
if ! [[ "$APP_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo -e "${RED}[!] VERSION 必须使用 X.Y.Z 格式。${NC}"
    exit 1
fi
docker build -q -f app/Dockerfile --build-arg CTYUN_REF="$CTYUN_REF" --build-arg APP_VERSION="$APP_VERSION" -t ctyun-auto-sign:v1 . > /dev/null

CONTAINER_NAME="ctyun_sign_${APP_USER}"
if [ "$(docker ps -aq -f name=^${CONTAINER_NAME}$)" ]; then
    docker rm -f "$CONTAINER_NAME" > /dev/null
fi

# 4. 首次运行提示
echo -e "\n${RED}=== 首次运行风控提醒 ===${NC}"
echo -e "容器会先在后台启动 Web 面板，再进入一次 CtYun 交互验证。"
echo -e "如要求短信验证码，请直接输入；看到 ${GREEN}保活任务启动${NC} 后按 ${YELLOW}Ctrl+C${NC} 结束验证。"
echo -e "===========================\n"

read -p "确认后按【回车键】启动容器..."

# 5. 后台启动容器，再单独执行可交互的首次设备验证
docker run -d \
  --name "$CONTAINER_NAME" \
  -e APP_USER="$APP_USER" \
  -e APP_PASSWORD="$APP_PASSWORD" \
  -e DEVICECODE="$DEVICECODE" \
  -v "$DATA_DIR":/app/data \
  -p 9845:9845 \
  --restart unless-stopped \
  ctyun-auto-sign:v1

wait_for_supervisor "$CONTAINER_NAME"
docker exec "$CONTAINER_NAME" supervisorctl -c /app/supervisord.conf stop ctyun >/dev/null
echo -e "${YELLOW}[*] 开始 CtYun 交互验证，完成后按 Ctrl+C 返回部署流程。${NC}"
docker exec -it "$CONTAINER_NAME" dotnet /app/CtYun.dll || true
docker exec "$CONTAINER_NAME" supervisorctl -c /app/supervisord.conf start ctyun >/dev/null

# 6. 验证后的自动化首次任务
echo -e "\n${YELLOW}[*] 交互验证已结束，正在检查容器运行状态...${NC}"
sleep 2
if [ "$(docker inspect -f '{{.State.Running}}' "$CONTAINER_NAME" 2>/dev/null)" == "true" ]; then
    echo -e "${GREEN}[*] 容器后台运行正常。${NC}"

    echo -e "------------------------------------------------------"
    echo -e "${YELLOW}[*] 配置首次兑换策略...${NC}"
    docker exec -it "$CONTAINER_NAME" python3 /app/pc_login.py --config-redeem

    echo -e "------------------------------------------------------"
    echo -e "${YELLOW}[*] 执行AI对话积分任务...${NC}"
    docker exec -it "$CONTAINER_NAME" python3 /app/login_script.py

    echo -e "------------------------------------------------------"
    if ! run_pc_login_until_hang_then_background "$CONTAINER_NAME"; then
        echo -e "${YELLOW}[!] 未进入挂机阶段，请稍后查看容器日志排查。${NC}"
    fi
    echo -e "------------------------------------------------------"

    echo -e "${GREEN}[*] 首次积分任务已触发完成，后续将由 Web 调度器接管。${NC}"
else
    echo -e "${RED}[!] 警告：检测到容器已停止。${NC}"
    echo -e "补救措施：请执行 ${YELLOW}docker start ${CONTAINER_NAME}${NC} 重新启动容器。"
    echo -e "然后手动执行 ${YELLOW}docker exec -it ${CONTAINER_NAME} python3 /app/login_script.py${NC}。"
    echo -e "如需手动运行：${YELLOW}docker exec -it ${CONTAINER_NAME} env PYTHONUNBUFFERED=1 python3 -u /app/pc_login.py${NC}。"
fi

# 7. 结束信息
echo -e "\n${GREEN}[*] 部署与首次配置完成。${NC}"
echo -e "容器名: ${CONTAINER_NAME}"
echo -e "数据目录: ${DATA_DIR}"
echo -e "Web 面板: ${YELLOW}http://127.0.0.1:9845${NC}"
echo -e "自动兑换奖励配置: docker exec -it "$CONTAINER_NAME" python3 /app/pc_login.py --config-redeem"
echo -e "日志查询: ${YELLOW}docker logs -f ${CONTAINER_NAME}${NC}"
echo -e "启动/停止: ${YELLOW}docker start/stop ${CONTAINER_NAME}${NC}"
