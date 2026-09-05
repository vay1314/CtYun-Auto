from __future__ import annotations

import hashlib
import time
from dataclasses import dataclass
from typing import Any

import requests

from .common import NumericOcrSolver, ProtocolError, require_json_object


PC_API_BASE = "https://desk.ctyun.cn:8810"
SELFORDER_BASE = "https://desk.ctyun.cn/selforder/api"
PC_VERSION = "103020001"
POINTS_VERSION = "204000100"
DEVICE_TYPE = "60"


@dataclass(frozen=True)
class LoginInfo:
    user_id: int
    tenant_id: int
    secret_key: str
    user_name: str
    bonded_device: bool


class CtYunProtocolClient:
    """复用 leleji/CtYun 已验证的 HTTP 登录与签名算法。"""

    def __init__(
        self,
        username: str,
        password: str,
        device_code: str,
        *,
        timeout: tuple[int, int] = (8, 25),
        session: requests.Session | None = None,
    ) -> None:
        self.username = username
        self.password = password
        self.device_code = device_code
        self.timeout = timeout
        self.session = session or requests.Session()
        self.login_info: LoginInfo | None = None
        self._last_request_id = 0
        self.session.headers.update(
            {
                "User-Agent": (
                    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
                    "AppleWebKit/537.36 (KHTML, like Gecko) "
                    "Chrome/137.0.0.0 Safari/537.36"
                ),
                "ctg-devicetype": DEVICE_TYPE,
                "ctg-version": PC_VERSION,
                "ctg-devicecode": device_code,
                "Referer": "https://pc.ctyun.cn/",
            }
        )

    @staticmethod
    def _sha256(value: str) -> str:
        return hashlib.sha256(value.encode("utf-8")).hexdigest()

    def _next_request_id(self) -> str:
        current = int(time.time() * 1000)
        if current <= self._last_request_id:
            current = self._last_request_id + 1
        self._last_request_id = current
        return str(current)

    def _result(self, response: requests.Response, context: str) -> dict:
        payload = require_json_object(response, context)
        code = payload.get("code", payload.get("resultCode", -1))
        if response.status_code >= 400 or code not in (0, "0"):
            message = payload.get("msg") or payload.get("resultMsg") or "未知错误"
            raise ProtocolError(f"{context}失败：{message}", code=code)
        return payload

    def _post_pc(self, path: str, *, data=None, json_data=None) -> dict:
        headers = self.signed_headers(version=PC_VERSION) if self.login_info else None
        response = self.session.post(
            f"{PC_API_BASE}{path}",
            data=data,
            json=json_data,
            headers=headers,
            timeout=self.timeout,
        )
        return self._result(response, path)

    def login(self, max_attempts: int = 3) -> LoginInfo:
        self.login_info = None
        last_error: Exception | None = None
        for attempt in range(1, max_attempts + 1):
            try:
                challenge = self._post_pc(
                    "/api/auth/client/genChallengeData", json_data={}
                ).get("data")
                if not isinstance(challenge, dict):
                    raise ProtocolError("登录挑战返回格式异常")
                challenge_code = str(
                    challenge.get("challengeCode")
                    or challenge.get("ChallengeCode")
                    or ""
                )
                challenge_id = str(
                    challenge.get("challengeId")
                    or challenge.get("ChallengeId")
                    or ""
                )
                if not challenge_code or not challenge_id:
                    raise ProtocolError("登录挑战缺少 challengeCode/challengeId")

                captcha_response = self.session.get(
                    f"{PC_API_BASE}/api/auth/client/captcha",
                    params={
                        "height": 36,
                        "width": 85,
                        "userInfo": self.username,
                        "mode": "auto",
                        "_t": int(time.time() * 1000),
                    },
                    timeout=self.timeout,
                )
                captcha_response.raise_for_status()
                captcha = NumericOcrSolver().solve(captcha_response.content)
                if not captcha:
                    raise ProtocolError("登录图形验证码识别失败")

                form = {
                    "userAccount": self.username,
                    "password": self._sha256(self.password + challenge_code),
                    "sha256Password": self._sha256(
                        self._sha256(self.password) + challenge_code
                    ),
                    "challengeId": challenge_id,
                    "captchaCode": captcha,
                    "deviceCode": self.device_code,
                    "deviceName": "Chrome浏览器",
                    "deviceType": DEVICE_TYPE,
                    "deviceModel": "Windows NT 10.0; Win64; x64",
                    "appVersion": "3.2.0",
                    "sysVersion": "Windows NT 10.0; Win64; x64",
                    "clientVersion": PC_VERSION,
                }
                payload = self._post_pc("/api/auth/client/login", data=form)
                data = payload.get("data")
                if not isinstance(data, dict):
                    raise ProtocolError("登录响应缺少账号信息")
                info = LoginInfo(
                    user_id=int(data["userId"]),
                    tenant_id=int(data["tenantId"]),
                    secret_key=str(data["secretKey"]),
                    user_name=str(data.get("userName") or self.username),
                    bonded_device=bool(data.get("bondedDevice")),
                )
                if not info.secret_key:
                    raise ProtocolError("登录响应缺少 secretKey")
                self.login_info = info
                return info
            except (requests.RequestException, ProtocolError, KeyError, ValueError) as error:
                last_error = error
                if isinstance(error, ProtocolError) and error.code not in (None, -1):
                    message = str(error)
                    if "用户名或密码错误" in message:
                        raise
                if attempt < max_attempts:
                    time.sleep(1)
        raise ProtocolError(f"云电脑登录失败：{last_error}")

    def send_sms_code(self) -> None:
        response = self.session.get(
            f"{PC_API_BASE}/api/auth/client/validateCode/captcha",
            params={"width": 120, "height": 40, "_t": int(time.time() * 1000)},
            headers=self.signed_headers(version=PC_VERSION),
            timeout=self.timeout,
        )
        response.raise_for_status()
        captcha = NumericOcrSolver().solve(response.content)
        if not captcha:
            raise ProtocolError("短信图形验证码识别失败")
        response = self.session.get(
            f"{PC_API_BASE}/api/cdserv/client/device/getSmsCode",
            params={"mobilePhone": self.username, "captchaCode": captcha},
            headers=self.signed_headers(version=PC_VERSION),
            timeout=self.timeout,
        )
        self._result(response, "发送短信验证码")

    def bind_device(self, verification_code: str) -> None:
        response = self.session.post(
            f"{PC_API_BASE}/api/cdserv/client/device/binding",
            params={
                "verificationCode": verification_code,
                "deviceName": "Chrome浏览器",
                "deviceCode": self.device_code,
                "deviceModel": "Windows NT 10.0; Win64; x64",
                "sysVersion": "Windows NT 10.0; Win64; x64",
                "appVersion": "3.2.0",
                "hostName": "pc.ctyun.cn",
                "deviceInfo": "Win32",
            },
            headers=self.signed_headers(version=PC_VERSION),
            timeout=self.timeout,
        )
        self._result(response, "绑定设备")

    def signed_headers(
        self, *, version: str = POINTS_VERSION, points_api: bool = False
    ) -> dict[str, str]:
        info = self.login_info
        if info is None:
            raise ProtocolError("尚未登录，不能生成 ctg 签名")
        timestamp = str(int(time.time() * 1000))
        request_id = self._next_request_id()
        source = (
            DEVICE_TYPE
            + request_id
            + str(info.tenant_id)
            + timestamp
            + str(info.user_id)
            + version
            + info.secret_key
        )
        headers = {
            "ctg-appmodel": "2",
            "ctg-device-model": "xiaomicc",
            "ctg-devicecode": self.device_code,
            "ctg-devicetype": DEVICE_TYPE,
            "ctg-requestid": request_id,
            "ctg-signaturestr": hashlib.md5(source.encode("utf-8")).hexdigest().upper(),
            "ctg-softwarecode": "web_client",
            "ctg-tenantid": str(info.tenant_id),
            "ctg-timestamp": timestamp,
            "ctg-userid": str(info.user_id),
            "ctg-version": version,
        }
        if points_api:
            headers.update(
                {
                    "ctg-authenticate": "1",
                    "ctg-appchannel": "1",
                    "ctg-device-manu": "Xiaomi",
                }
            )
        return headers

    def _selforder(self, method: str, path: str, **kwargs: Any) -> dict:
        headers = {
            "Accept": "application/json, text/plain, */*",
            "Referer": "https://desk.ctyun.cn/selforder/points.html",
            **dict(kwargs.pop("headers", {})),
        }
        if method.upper() != "GET":
            headers["Origin"] = "https://desk.ctyun.cn"
        headers.update(self.signed_headers(points_api=True))
        response = self.session.request(
            method,
            f"{SELFORDER_BASE}{path}",
            headers=headers,
            timeout=self.timeout,
            **kwargs,
        )
        return self._result(response, path)

    def get_task_list(self) -> list[dict]:
        data = self._selforder(
            "GET", "/marketing/userPoints/getTaskList"
        ).get("data", [])
        return data if isinstance(data, list) else []

    def get_usage_task(self) -> dict | None:
        for task in self.get_task_list():
            if task.get("taskDefId") == 1003 or task.get("taskDefName") == "使用1小时":
                return task
        return None

    def get_user_points(self) -> int:
        data = self._selforder(
            "GET", "/marketing/userPoints/getUserPoints"
        ).get("data", [])
        if not isinstance(data, list):
            return 0
        for item in data:
            if item.get("pointType") == 1 or item.get("pointTypeName") == "通用积分":
                return int(item.get("points") or 0)
        return 0

    def get_desktops(self) -> list[dict]:
        payload = self._post_pc(
            "/api/desktop/client/pageDesktop",
            json_data={
                "getCnt": 20,
                "desktopTypes": ["1", "2001", "2002", "2003"],
                "sortType": "createTimeV1",
            },
        )
        data = payload.get("data") or {}
        desktops = data.get("desktopList", []) if isinstance(data, dict) else []
        return desktops if isinstance(desktops, list) else []

    def get_rewards(self) -> list[dict]:
        data = self._selforder(
            "GET",
            "/selforder/prod/get",
            params={"prodId": 17000000, "prodCode": "POINTS"},
        ).get("data", [])
        rewards: list[dict] = []
        for mall in data if isinstance(data, list) else []:
            for series in mall.get("series", []):
                if series.get("expireDate") is not None:
                    continue
                for sku in series.get("sku", []):
                    if sku.get("expireDate") is None:
                        rewards.append(
                            {
                                "prodId": int(sku.get("prodId") or 0),
                                "prodName": str(sku.get("prodName") or "").strip(),
                                "costPoints": int(sku.get("costPoints") or 0),
                                "description": str(sku.get("description") or "").strip(),
                                "prodType": str(sku.get("prodType") or "").strip(),
                            }
                        )
        return rewards

    def place_order(self, payload: dict) -> dict:
        return self._selforder(
            "POST", "/selforder/paas/placeOrder", json=payload
        )
