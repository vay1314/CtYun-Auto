# ctyun-auto

`ctyun-auto` 是一个运行在 Docker 中的天翼云电脑管理工具，整合云电脑保活、AI 对话积分任务、使用时长任务、积分与任务状态查询、自动兑换以及 Web 管理面板。

> 本项目不是中国电信或天翼云官方项目，仅供学习与个人自动化使用。平台接口可能随时调整，请遵守相关服务条款并自行承担使用风险。

## 运行结构

```text
浏览器
  └─ Web 管理面板（FastAPI + Jinja/HTMX，端口 9845）
       ├─ SQLite：账号、计划、任务记录、认证缓存
       ├─ Python：AI 对话、积分查询、挂机任务、自动兑换
       └─ Supervisor
            ├─ web：管理面板和任务调度器
            └─ ctyun：CtYun.dll 云电脑连接与 WebSocket 保活
```

## 快速开始

### 方式一：使用 Docker Hub 镜像

适合只需要运行程序、不准备修改源码的用户。以下命令以 Linux 为例：

```bash
mkdir -p ./ctyun-data

docker run -d \
  --name ctyun-auto \
  -p 9845:9845 \
  -v "$(pwd)/ctyun-data:/app/data" \
  --restart unless-stopped \
  yin26287903/ctyun-auto:latest
```

浏览器访问：

```text
http://服务器IP:9845
```

首次访问会要求设置至少 8 位的管理密码。设置完成后进入“账号管理”添加天翼云账号。

如果宿主机的 `9845` 已被占用，可以只修改冒号左侧端口，例如：

```bash
-p 19845:9845
```

此时访问 `http://服务器IP:19845`。

### 方式二：从源码交互部署

适合首次部署或希望本地构建最新 CtYun 源码的用户。需要 Linux、Git 和 Docker：

```bash
git clone https://github.com/vay1314/ctyun-auto.git
cd ctyun-auto
bash deploy.sh
```

部署脚本会：

1. 获取 `leleji/CtYun` 的 `master` 分支最新提交。
2. 在 Docker 构建阶段编译 `CtYun.dll`。
3. 构建本地镜像 `ctyun-auto-sign:v1`。
4. 创建数据目录并启动 Web 面板。
5. 导入首次输入的账号，并生成稳定设备码。
6. 进入一次可交互的 CtYun 设备验证。
7. 配置自动兑换并触发首次 AI 对话和挂机任务。

部署脚本创建的容器名为：

```text
ctyun_sign_<首次输入的账号>
```

### 定时计划

计划使用标准 5 段 Cron 表达式，时区默认为 `Asia/Shanghai`：

```text
分 时 日 月 周
```

默认计划：

```text
0 3,20 * * *   # AI 对话：每天 03:00 和 20:00
0 4,6 * * *    # 云电脑挂机：每天 04:00 和 06:00
```



## 数据持久化

容器数据目录为 `/app/data`，必须挂载到宿主机：

```text
/app/data/
├─ ctyun-auto.db          # 账号、设置、计划、任务历史和平台状态
├─ accounts.json          # CtYun.dll 使用的多账号配置
├─ .credential_key        # 账号密码和认证缓存的加密密钥
├─ .web_session_key       # Web Session 签名密钥
├─ accounts/              # 设备码、兑换配置等账号数据
└─ logs/
   ├─ ctyun.log
   ├─ web.log
   ├─ supervisord.log
   └─ task-*.log
```

请保护并整体备份 `/app/data`。恢复时数据库与 `.credential_key` 必须配套，否则已加密的账号数据无法解密。

## 常用环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `APP_USER` | 空 | 启动时导入的首个天翼云账号，主要用于兼容部署脚本 |
| `APP_PASSWORD` | 空 | 首个账号密码 |
| `DEVICECODE` | 自动生成 | 首个账号使用的稳定设备码 |
| `CHAT_CRON` | `0 3,20 * * *` | 首个账号的 AI 对话计划 |
| `PC_CRON` | `0 4,6 * * *` | 首个账号的挂机计划 |
| `WEB_SECURE_COOKIE` | `false` | 使用 HTTPS 反向代理时设置为 `true` |
| `OCR_ENDPOINT` | 上游 OCR 地址 | 远程验证码识别接口 |

通过 Web 添加的账号不需要设置 `APP_USER`、`APP_PASSWORD` 或 `DEVICECODE`。

## 常用命令

以下示例使用容器名 `ctyun-auto`：

```bash
# 查看容器输出
docker logs -f ctyun-auto

# 查看三个主要进程日志
docker exec ctyun-auto tail -f /app/data/logs/ctyun.log
docker exec ctyun-auto tail -f /app/data/logs/web.log
docker exec ctyun-auto tail -f /app/data/logs/supervisord.log

# 查看 Supervisor 进程状态
docker exec ctyun-auto supervisorctl -c /app/supervisord.conf status

# 重启 CtYun 保活进程，不重启 Web 面板
docker exec ctyun-auto supervisorctl -c /app/supervisord.conf restart ctyun

# 健康检查
curl http://ip:9845/health

# 停止和启动容器
docker stop ctyun-auto
docker start ctyun-auto
```

## 致谢

感谢以下开源项目和作者提供的实现、思路与参考：

- [leleji/CtYun](https://github.com/leleji/CtYun)
- [VanceHud/CtYun](https://github.com/VanceHud/CtYun)
- [bytehola/ctyun-auto](https://github.com/bytehola/ctyun-auto)
- [sml2h3/ddddocr](https://github.com/sml2h3/ddddocr)
