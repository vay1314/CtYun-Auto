# CtYunKeeper

`CtYunKeeper`（天翼云守护）是面向天翼云电脑的轻量管理服务，提供多账号保活、平台积分任务、AI 对话、使用时长跟踪、任务状态查询、自动兑换和 Web 管理界面。

> 非中国电信或天翼云官方项目。平台接口可能调整，请遵守服务条款并自行评估使用风险。

## 快速启动

```bash
mkdir -p ./ctyunkeeper-data

docker run -d \
  --name ctyunkeeper \
  -p 9845:9845 \
  -v "$(pwd)/ctyunkeeper-data:/app/data" \
  --restart unless-stopped \
  yin26287903/ctyun-auto:latest
```

打开 `http://服务器IP:9845`，首次访问先设置至少 8 位的管理密码，然后在“账号管理”添加天翼云账号。设备触发短信验证时，页面会自动进入验证码输入流程。

宿主机端口冲突时只需修改左侧端口，例如 `-p 19845:9845`。

## 从源码构建

```bash
git clone https://github.com/yin26287903/ctyun-auto.git
cd ctyun-auto
sh deploy.sh
```

也可以直接构建：

```bash
docker build -f app/Dockerfile \
  --build-arg APP_VERSION="$(cat VERSION)" \
  -t ctyun-auto:local .
```

本地 Go 检查：

```bash
go test ./...
go build ./cmd/ctyun-auto
```

## Web 使用流程

1. 在“账号管理”添加账号，保持设备码稳定。
2. 如平台要求新设备验证，在网页填写收到的短信验证码。
3. 服务会登录账号、读取运行中的云电脑并启动保活。
4. 在“任务中心”运行 AI 对话、挂机或查询任务状态。
5. 需要兑换时，在账号列表进入“兑换”，从实时商品和云电脑列表选择目标并主动启用。

默认 Cron：

```text
0 3,20 * * *   AI 对话：每天 03:00、20:00
0 4,6 * * *    挂机检查：每天 04:00、06:00
```

时区默认为 `Asia/Shanghai`。

## 数据与升级

持久化目录是 `/app/data`：

```text
/app/data/
├─ ctyun-auto.db
├─ .credential_key
├─ .web_session_key
└─ logs/
   ├─ ctyun.log
   └─ tasks/
```

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `APP_PORT` | `9845` | Web 监听端口 |
| `CTYUN_DATA_DIR` | `/app/data` | 数据目录 |
| `CTYUN_STATIC_DIR` | `/app/static` | Web 静态文件目录 |
| `OCR_ENDPOINT` | `https://orc.1999111.xyz/ocr` | 验证码识别服务 |
| `WEB_SECURE_COOKIE` | `false` | HTTPS 反向代理后建议设为 `true` |
| `TZ` | `Asia/Shanghai` | 容器时区 |

## 致谢

感谢以下项目提供的源码、协议研究和实现思路：

- [leleji/CtYun](https://github.com/leleji/CtYun)
- [VanceHud/CtYun](https://github.com/VanceHud/CtYun)
- [bytehola/ctyun-auto](https://github.com/bytehola/ctyun-auto)
- [sml2h3/ddddocr](https://github.com/sml2h3/ddddocr)
