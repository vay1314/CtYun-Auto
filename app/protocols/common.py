from __future__ import annotations

import base64
import binascii
import os
import threading
import time
from typing import Any

import requests


DEFAULT_OCR_ENDPOINT = "https://orc.1999111.xyz/ocr"


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
                instance.session = requests.Session()
                cls._instance = instance
        return cls._instance

    def solve(self, image: bytes) -> str:
        if not image:
            raise ProtocolError("验证码图片为空")
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
                result = str(payload.get("data") or "").strip()
                if not result or not result.isdigit():
                    raise ProtocolError("验证码识别结果不是有效数字")
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
