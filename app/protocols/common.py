from __future__ import annotations

import base64
import binascii
import os
import threading
import time
from pathlib import Path
from typing import Any
from uuid import uuid4

import requests


DEFAULT_OCR_ENDPOINT = "https://orc.1999111.xyz/ocr"
DEFAULT_CAPTCHA_LOG_LIMIT = 100


class ProtocolError(RuntimeError):
    """远端接口返回了不可恢复的错误。"""

    def __init__(self, message: str, *, code: int | str | None = None) -> None:
        super().__init__(message)
        self.code = code


class RemoteOcrSolver:
    """复用 CtYun 上游验证码识别服务。"""

    _instance: "RemoteOcrSolver | None" = None
    _lock = threading.Lock()

    def __new__(cls) -> "RemoteOcrSolver":
        with cls._lock:
            if cls._instance is None:
                instance = super().__new__(cls)
                instance.endpoint = (
                    os.getenv("OCR_ENDPOINT", DEFAULT_OCR_ENDPOINT).strip()
                    or DEFAULT_OCR_ENDPOINT
                )
                instance.debug_enabled = os.getenv("CAPTCHA_DEBUG", "").lower() in {
                    "1",
                    "true",
                    "yes",
                    "on",
                }
                data_dir = Path(os.getenv("CTYUN_DATA_DIR", "/app/data"))
                instance.debug_dir = Path(
                    os.getenv("CAPTCHA_DEBUG_DIR", str(data_dir / "logs" / "captcha"))
                )
                try:
                    instance.debug_limit = max(
                        1,
                        int(
                            os.getenv(
                                "CAPTCHA_DEBUG_LIMIT", str(DEFAULT_CAPTCHA_LOG_LIMIT)
                            )
                        ),
                    )
                except ValueError:
                    instance.debug_limit = DEFAULT_CAPTCHA_LOG_LIMIT
                instance.session = requests.Session()
                instance.session.headers.update(
                    {"Referer": "https://pc.ctyun.cn/"}
                )
                cls._instance = instance
        return cls._instance

    @staticmethod
    def _image_extension(image: bytes) -> str:
        if image.startswith(b"\x89PNG\r\n\x1a\n"):
            return ".png"
        if image.startswith(b"\xff\xd8\xff"):
            return ".jpg"
        if image.startswith((b"GIF87a", b"GIF89a")):
            return ".gif"
        return ".bin"

    def _save_debug_image(self, image: bytes) -> Path | None:
        if not self.debug_enabled:
            return None
        try:
            self.debug_dir.mkdir(parents=True, exist_ok=True)
            filename = (
                f"captcha-{time.strftime('%Y%m%d-%H%M%S')}-"
                f"{time.time_ns() % 1_000_000_000:09d}-{uuid4().hex[:8]}"
                f"{self._image_extension(image)}"
            )
            path = self.debug_dir / filename
            path.write_bytes(image)
            images = sorted(
                (item for item in self.debug_dir.glob("captcha-*") if item.is_file()),
                key=lambda item: item.stat().st_mtime_ns,
                reverse=True,
            )
            for expired in images[self.debug_limit :]:
                expired.unlink(missing_ok=True)
            print(f"[*] 验证码图片已保存：{path}", flush=True)
            return path
        except OSError as error:
            print(f"[!] 验证码图片保存失败：{error}", flush=True)
            return None

    def solve(self, image: bytes) -> str:
        if not image:
            raise ProtocolError("验证码图片为空")
        debug_path = self._save_debug_image(image)
        encoded = base64.b64encode(image).decode("ascii")
        last_error: Exception | None = None
        for attempt in range(1, 4):
            try:
                with self.session.post(
                    self.endpoint,
                    files={"image": (None, encoded, "text/plain; charset=utf-8")},
                    timeout=(5, 15),
                ) as response:
                    response.raise_for_status()
                    payload = require_json_object(response, "验证码识别")
                result = "".join(str(payload.get("data") or "").split())
                if not result:
                    raise ProtocolError("验证码识别结果为空")
                if self.debug_enabled:
                    location = f"，图片：{debug_path}" if debug_path else ""
                    print(f"[*] 验证码识别结果：{result}{location}", flush=True)
                return result
            except (requests.RequestException, ProtocolError) as error:
                last_error = error
                if attempt < 3:
                    time.sleep(0.5 * attempt)
        raise ProtocolError(f"验证码识别失败：{last_error}")


def decode_base64_image(value: str) -> bytes:
    payload = value.split(",", 1)[-1].strip()
    try:
        return base64.b64decode(payload, validate=True)
    except (binascii.Error, ValueError) as error:
        raise ProtocolError("验证码图片不是有效的 Base64 数据") from error


def require_json_object(response: Any, context: str) -> dict:
    try:
        payload = response.json()
    except ValueError as error:
        raise ProtocolError(f"{context}返回的不是 JSON（HTTP {response.status_code}）") from error
    if not isinstance(payload, dict):
        raise ProtocolError(f"{context}返回格式异常")
    return payload
