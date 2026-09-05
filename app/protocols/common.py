from __future__ import annotations

import base64
import binascii
import threading
from typing import Any


class ProtocolError(RuntimeError):
    """远端接口返回了不可恢复的错误。"""

    def __init__(self, message: str, *, code: int | str | None = None) -> None:
        super().__init__(message)
        self.code = code


class NumericOcrSolver:
    """延迟初始化的本地数字验证码识别器。"""

    _instance: "NumericOcrSolver | None" = None
    _lock = threading.Lock()

    def __new__(cls) -> "NumericOcrSolver":
        with cls._lock:
            if cls._instance is None:
                instance = super().__new__(cls)
                instance._engine = None
                cls._instance = instance
        return cls._instance

    def solve(self, image: bytes) -> str:
        if self._engine is None:
            import ddddocr

            self._engine = ddddocr.DdddOcr(show_ad=False)
            self._engine.set_ranges(0)
        result = str(self._engine.classification(image) or "").strip()
        return "".join(character for character in result if character.isdigit())


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
