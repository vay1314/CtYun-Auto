"""天翼云 AI 对话积分任务。"""

from __future__ import annotations

import os
import random
import sys

try:
    from .protocols import EaiProtocolClient, ProtocolError
except ImportError:  # 兼容容器内以脚本方式直接执行。
    from protocols import EaiProtocolClient, ProtocolError


PRESET_MESSAGES = [
    "今天北京天气怎么样？（简短回答）",
    "给我讲一个冷笑话。（简短回答）",
    "来一首古诗。（简短回答）",
    "空腹可以吃饭吗？（简短回答）",
    "推荐一部人生必看电影。（简短回答）",
]


def main() -> int:
    username = os.getenv("APP_USER", "").strip()
    password = os.getenv("APP_PASSWORD", "")
    device_code = os.getenv("DEVICECODE", "").strip()
    if not username or not password or not device_code:
        print("[!] 缺少 APP_USER、APP_PASSWORD 或 DEVICECODE。")
        return 1

    print("[*] 开始执行 AI 登录。")
    client = EaiProtocolClient(username, password, device_code)
    try:
        user = client.login()
        mobile = str(user.get("mobile") or "")
        masked = f"{mobile[:3]}****{mobile[-4:]}" if len(mobile) >= 7 else "已登录"
        print(f"[*] AI 登录成功：{masked}")

        model = client.choose_model(client.query_models())
        print(f"[*] 使用模型：{model}")
        message = random.choice(PRESET_MESSAGES)
        print(f"[*] 发送积分对话：{message}")
        print("=== AI 助手回复 ===")
        chunks: list[str] = []
        for chunk in client.iter_chat(message, model):
            chunks.append(chunk)
            print(chunk, end="", flush=True)
        print()
        if not "".join(chunks).strip():
            raise ProtocolError("AI 返回内容为空")
        print("[*] AI 对话协议执行完成。")
        return 0
    except ProtocolError as error:
        print(f"[!] AI 任务失败：{error}")
        return 1
    except Exception as error:
        print(f"[!] AI 任务异常：{error}")
        return 1


if __name__ == "__main__":
    sys.exit(main())
