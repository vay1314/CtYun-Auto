from __future__ import annotations

import base64
import hashlib
import json
import secrets
import string
import time
import uuid
from typing import Iterator
from urllib.parse import parse_qs, urlparse

import requests
from cryptography.hazmat.primitives import padding as symmetric_padding
from cryptography.hazmat.primitives.asymmetric import padding as asymmetric_padding
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
from cryptography.hazmat.primitives.serialization import load_der_public_key

from .common import (
    NumericOcrSolver,
    ProtocolError,
    decode_base64_image,
    require_json_object,
)


IAM_BASE = "https://desk.ctyun.cn/cloudB/dy/iam"
EAI_BASE = "https://eaichat.ctyun.cn"
GATEWAY_URL = "https://gwyilian.ctyun.cn/server/eaiSysInfo"
EAI_VERSION = "202060305"
GATEWAY_AES_KEY = b"chinatelecom@cnn"
REDIRECT_URI = "https://eaichat.ctyun.cn:443/chat/#/aichat"
CAS_SERVICE = REDIRECT_URI
RANDOM_CHARS = string.ascii_letters + string.digits


class EaiProtocolClient:
    def __init__(
        self,
        username: str,
        password: str,
        device_seed: str,
        *,
        timeout: tuple[int, int] = (8, 30),
        session: requests.Session | None = None,
    ) -> None:
        self.username = username
        self.password = password
        self.timeout = timeout
        self.session = session or requests.Session()
        digest = hashlib.sha256(("eai:" + device_seed).encode("utf-8")).hexdigest()
        self.device_code = "iam:" + digest[:32]
        self.xuid = "pubweb_" + str(uuid.UUID(digest[:32]))
        self.session_key: str | None = None
        self.iam_data: dict = {}
        self.session.headers.update(
            {
                "User-Agent": (
                    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
                    "AppleWebKit/537.36 (KHTML, like Gecko) "
                    "Chrome/137.0.0.0 Safari/537.36"
                )
            }
        )

    @staticmethod
    def _sha256(value: str) -> str:
        return hashlib.sha256(value.encode("utf-8")).hexdigest()

    @staticmethod
    def _compact_json(value: object) -> str:
        return json.dumps(value, ensure_ascii=False, separators=(",", ":"))

    def _eai_headers(self, tenant_id: int | str | None = None) -> dict[str, str]:
        return {
            "YL-Main-Version": EAI_VERSION,
            "YL-Product-Id": "5",
            "x-eai-env": "pubWeb",
            "x-eai-tenant-id": "" if tenant_id is None else str(tenant_id),
            "x-eai-gw-code": "",
            "x-eai-version": EAI_VERSION,
            "x-eai-env-code": "",
            "x-eai-xuid": self.xuid,
            "x-eai-mode": "eai",
            "x-client-trace-id": str(uuid.uuid4()),
            "x-eai-source": "web-eai",
            "Origin": EAI_BASE,
            "Referer": f"{EAI_BASE}/chat/",
        }

    def _signed_headers(
        self,
        *,
        params: dict | None = None,
        body_text: str | None = None,
        tenant_id: int | str | None = None,
    ) -> dict[str, str]:
        if not self.session_key:
            raise ProtocolError("AI 尚未授权，不能生成请求签名")
        query = "&".join(
            f"{key}={value}"
            for key, value in sorted((params or {}).items())
            if value is not None
        )
        parts: list[str] = []
        if query:
            parts.append(query)
        if body_text is not None:
            parts.append(hashlib.md5(body_text.encode("utf-8")).hexdigest())
        timestamp = str(int(time.time() * 1000))
        random_value = "".join(secrets.choice(RANDOM_CHARS) for _ in range(8))
        parts.extend((self.session_key, timestamp, random_value))
        signature = hashlib.sha256("&".join(parts).encode("utf-8")).hexdigest()
        headers = self._eai_headers(tenant_id)
        headers.update(
            {
                "Web-Signature": signature,
                "Web-Random": random_value,
                "Web-Timestamp": timestamp,
            }
        )
        return headers

    def _iam_login(self, max_attempts: int = 3) -> dict:
        iam_headers = {
            "Origin": "https://desk.ctyun.cn",
            "Referer": f"{IAM_BASE}/",
        }
        self.session.get(
            f"{IAM_BASE}/api/auth/iam/cas/login",
            params={"service": CAS_SERVICE, "consent": "false"},
            headers=iam_headers,
            timeout=self.timeout,
        ).raise_for_status()

        captcha_code: str | None = None
        captcha_key: str | None = None
        last_message = "登录失败"
        for attempt in range(1, max_attempts + 1):
            body = {
                "userAccount": self.username,
                "password": self._sha256(self.password),
                "deviceCode": self.device_code,
                "deviceName": "iam:web",
            }
            if captcha_code and captcha_key:
                body["captchaCode"] = captcha_code
                body["captchaCodeKey"] = captcha_key
            response = self.session.post(
                f"{IAM_BASE}/api/auth/iam/login",
                json=body,
                headers=iam_headers,
                timeout=self.timeout,
            )
            payload = require_json_object(response, "AI IAM 登录")
            code = payload.get("code")
            if response.status_code < 400 and code in (0, "0"):
                data = payload.get("data")
                if not isinstance(data, dict):
                    raise ProtocolError("AI IAM 登录响应缺少 data")
                return data

            last_message = str(payload.get("msg") or payload.get("message") or code)
            if attempt >= max_attempts:
                break
            captcha_response = self.session.get(
                f"{IAM_BASE}/api/auth/iam/captcha",
                params={"width": 100, "height": 40, "userInfo": self.username},
                headers=iam_headers,
                timeout=self.timeout,
            )
            captcha_payload = require_json_object(captcha_response, "获取 AI 验证码")
            captcha_key = captcha_response.headers.get("ctg-captcha-key")
            image_value = captcha_payload.get("data")
            if not captcha_key or not isinstance(image_value, str):
                raise ProtocolError("AI 验证码响应缺少图片或 captcha key")
            captcha_code = NumericOcrSolver().solve(decode_base64_image(image_value))
            if not captcha_code:
                raise ProtocolError("AI 图形验证码识别失败")
        raise ProtocolError(f"AI IAM 登录失败：{last_message}")

    @staticmethod
    def _decrypt_aes_ecb(ciphertext: str, key: bytes) -> bytes:
        encrypted = base64.b64decode("".join(ciphertext.split()))
        decryptor = Cipher(algorithms.AES(key), modes.ECB()).decryptor()
        padded = decryptor.update(encrypted) + decryptor.finalize()
        unpadder = symmetric_padding.PKCS7(128).unpadder()
        return unpadder.update(padded) + unpadder.finalize()

    def _fetch_public_key(self):
        response = self.session.get(
            GATEWAY_URL,
            headers=self._eai_headers(),
            timeout=self.timeout,
        )
        payload = require_json_object(response, "获取 AI 网关配置")
        if response.status_code >= 400 or payload.get("resultCode") not in (0, "0"):
            raise ProtocolError("获取 AI 网关配置失败", code=payload.get("resultCode"))
        encrypted = payload.get("data")
        if not isinstance(encrypted, str):
            raise ProtocolError("AI 网关配置缺少加密数据")
        try:
            gateway = json.loads(
                self._decrypt_aes_ecb(encrypted, GATEWAY_AES_KEY).decode("utf-8")
            )
            sso = gateway["sso"]
            key_id = str(sso["ssopkid"])
            public_key = load_der_public_key(
                base64.b64decode("".join(str(sso["ssopk"]).split()))
            )
        except (KeyError, ValueError, TypeError, json.JSONDecodeError) as error:
            raise ProtocolError("AI 网关公钥解析失败") from error
        if not key_id or getattr(public_key, "key_size", 0) < 1024:
            raise ProtocolError("AI 网关返回了无效 RSA 公钥")
        return key_id, public_key

    @staticmethod
    def _ticket_from_return_url(return_url: str) -> str:
        parsed = urlparse(return_url)
        fragment_query = parsed.fragment.partition("?")[2]
        ticket = parse_qs(fragment_query).get("ticket", [""])[0]
        if not ticket:
            ticket = parse_qs(parsed.query).get("ticket", [""])[0]
        if not ticket:
            raise ProtocolError("IAM 登录响应中没有 AI ticket")
        return ticket

    def _authorize(self, ticket: str) -> None:
        last_error: Exception | None = None
        for attempt in range(2):
            try:
                key_id, public_key = self._fetch_public_key()
                client_key = "".join(secrets.choice(RANDOM_CHARS) for _ in range(16))
                encrypted_key = public_key.encrypt(
                    client_key.encode("utf-8"), asymmetric_padding.PKCS1v15()
                ).hex()
                response = self.session.post(
                    f"{EAI_BASE}/sso/login/v2/iam/ticketAuthorize",
                    data={
                        "loginType": "iamTicket",
                        "clientId": "eaiapp",
                        "iamTicket": ticket,
                        "redirectUri": REDIRECT_URI,
                        "clientKey": encrypted_key,
                        "clientKeyId": key_id,
                    },
                    headers=self._eai_headers(),
                    timeout=self.timeout,
                )
                payload = require_json_object(response, "AI SSO 授权")
                if response.status_code >= 400 or payload.get("resultCode") not in (0, "0"):
                    raise ProtocolError(
                        str(payload.get("resultMsg") or "AI SSO 授权失败"),
                        code=payload.get("resultCode"),
                    )
                encrypted_session_key = payload.get("data", {}).get("sessionKey")
                if not isinstance(encrypted_session_key, str):
                    raise ProtocolError("AI SSO 响应缺少 sessionKey")
                self.session_key = self._decrypt_aes_ecb(
                    encrypted_session_key, client_key.encode("utf-8")
                ).decode("utf-8")
                if not self.session_key:
                    raise ProtocolError("AI sessionKey 解密结果为空")
                return
            except (requests.RequestException, ProtocolError, ValueError) as error:
                last_error = error
                self.session_key = None
                if attempt == 0:
                    continue
        raise ProtocolError(f"AI SSO 授权失败：{last_error}")

    def login(self) -> dict:
        # 避免同一客户端对象重新登录失败后继续误用旧会话。
        self.session_key = None
        self.iam_data = {}
        self.iam_data = self._iam_login()
        if self.iam_data.get("needSmsValidate") is True:
            raise ProtocolError("AI IAM 登录要求短信校验，当前任务无法无人值守完成")
        ticket = self._ticket_from_return_url(str(self.iam_data.get("returnUrl") or ""))
        self._authorize(ticket)
        return self.query_user_info()

    def _request(
        self,
        method: str,
        path: str,
        *,
        params: dict | None = None,
        json_data: object | None = None,
        stream: bool = False,
        tenant_id: int | str | None = None,
    ) -> requests.Response:
        body_text = self._compact_json(json_data) if json_data is not None else None
        headers = self._signed_headers(
            params=params, body_text=body_text, tenant_id=tenant_id
        )
        if body_text is not None:
            headers["Content-Type"] = "application/json"
        response = self.session.request(
            method,
            f"{EAI_BASE}{path}",
            params=params,
            data=body_text.encode("utf-8") if body_text is not None else None,
            headers=headers,
            timeout=self.timeout,
            stream=stream,
        )
        if response.status_code in (401, 403):
            response.close()
            raise ProtocolError("AI 登录状态或签名已失效", code=response.status_code)
        try:
            response.raise_for_status()
        except requests.HTTPError:
            response.close()
            raise
        return response

    def query_user_info(self) -> dict:
        response = self._request("GET", "/ai/portal/wenc/v1/user/queryUserInfo")
        payload = require_json_object(response, "查询 AI 用户信息")
        if payload.get("resultCode") not in (0, "0"):
            raise ProtocolError(
                str(payload.get("resultMsg") or "查询 AI 用户信息失败"),
                code=payload.get("resultCode"),
            )
        data = payload.get("data")
        return data if isinstance(data, dict) else {}

    def query_models(self) -> list[dict]:
        response = self._request(
            "GET", "/ai/portal/wenc/v2/openai/chat/queryModels"
        )
        payload = require_json_object(response, "查询 AI 模型")
        if payload.get("resultCode") not in (0, "0"):
            raise ProtocolError(
                str(payload.get("resultMsg") or "查询 AI 模型失败"),
                code=payload.get("resultCode"),
            )
        data = payload.get("data")
        return data if isinstance(data, list) else []

    @staticmethod
    def choose_model(models: list[dict]) -> str:
        available = [
            model
            for model in models
            if str(model.get("status") or "").lower() in {"available", "avaiable"}
            and model.get("keyModel")
        ]
        if not available:
            raise ProtocolError("当前没有可用的 AI 模型")
        available.sort(key=lambda item: int(item.get("recommend") or 0), reverse=True)
        return str(available[0]["keyModel"])

    def iter_chat(self, message: str, model: str) -> Iterator[str]:
        payload = {
            "key_model": model,
            "messages": [
                {
                    "role": "user",
                    "content": message,
                    "verify_id": str(uuid.uuid4()),
                    "ref": {"type": "file", "file": []},
                }
            ],
            "stream": True,
            "client_retry": True,
            "web_search": True,
            "tenantId": self.iam_data.get("tenantId"),
            "enable_thinking": False,
        }
        response = self._request(
            "POST",
            "/ai/portal/wenc/v3/openai/chat/completions",
            json_data=payload,
            stream=True,
            tenant_id=self.iam_data.get("tenantId"),
        )
        try:
            for raw_line in response.iter_lines(decode_unicode=False):
                if not raw_line:
                    continue
                line = raw_line.decode("utf-8", errors="replace")
                if not line.startswith("data:"):
                    continue
                value = line[5:].strip()
                if value == "[DONE]":
                    return
                try:
                    event = json.loads(value)
                except json.JSONDecodeError:
                    continue
                if event.get("code") not in (None, 0, "0"):
                    raise ProtocolError(
                        str(event.get("message") or event.get("msg") or "AI 对话失败"),
                        code=event.get("code"),
                    )
                choices = event.get("choices") or []
                if not choices:
                    continue
                choice = choices[0]
                delta = choice.get("delta") or {}
                content = delta.get("content")
                if content:
                    yield str(content)
                if choice.get("finish_reason") == "stop":
                    return
        finally:
            response.close()

    def chat(self, message: str, model: str) -> str:
        return "".join(self.iter_chat(message, model))
