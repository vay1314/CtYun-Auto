import time
from dataclasses import dataclass

try:
    from protocols import CtYunProtocolClient, ProtocolError
except ImportError:
    from ..protocols import CtYunProtocolClient, ProtocolError

from .accounts import get_account_secret
from .auth_cache import clear_auth_cache, load_auth_cache, save_auth_cache


AUTH_ERROR_CODES = {"40010", "401", "403", "-401", "-403"}
VERIFICATION_TTL_SECONDS = 10 * 60


@dataclass
class PendingVerification:
    client: CtYunProtocolClient
    expires_at: float


_pending: dict[int, PendingVerification] = {}


class VerificationSessionExpired(ProtocolError):
    """设备验证所需的登录会话已经不可用。"""


def cancel_device_verification(account_id: int) -> None:
    _pending.pop(account_id, None)


def _is_auth_error(error: ProtocolError) -> bool:
    return str(error.code) in AUTH_ERROR_CODES


def _account_client(account_id: int) -> CtYunProtocolClient:
    account = get_account_secret(account_id)
    if not account:
        raise ValueError("账号不存在")
    return CtYunProtocolClient(
        account["username"], account["password"], account["device_code"]
    )


def _login_and_cache(client: CtYunProtocolClient, account_id: int) -> bool:
    info = client.login(max_attempts=8)
    save_auth_cache(account_id, client.export_login_info())
    return info.bonded_device


def begin_device_verification(account_id: int) -> bool:
    client = _account_client(account_id)
    cached = load_auth_cache(account_id)
    if cached:
        try:
            info = client.restore_login_info(cached)
        except ProtocolError:
            clear_auth_cache(account_id)
        else:
            if info.bonded_device:
                return True
            try:
                client.send_sms_code()
            except ProtocolError as error:
                if not _is_auth_error(error):
                    raise
                clear_auth_cache(account_id)
            else:
                _pending[account_id] = PendingVerification(
                    client=client,
                    expires_at=time.monotonic() + VERIFICATION_TTL_SECONDS,
                )
                return False

    if _login_and_cache(client, account_id):
        return True
    client.send_sms_code()
    _pending[account_id] = PendingVerification(
        client=client,
        expires_at=time.monotonic() + VERIFICATION_TTL_SECONDS,
    )
    return False


def complete_device_verification(account_id: int, code: str) -> None:
    pending = _pending.get(account_id)
    if pending and pending.expires_at <= time.monotonic():
        _pending.pop(account_id, None)
        pending = None

    if pending:
        account = get_account_secret(account_id)
        if not account or (
            pending.client.username != account["username"]
            or pending.client.device_code != account["device_code"]
        ):
            _pending.pop(account_id, None)
            raise VerificationSessionExpired(
                "账号或设备码已经变更，请重新获取短信验证码"
            )
        client = pending.client
    else:
        cached = load_auth_cache(account_id)
        if not cached:
            raise VerificationSessionExpired(
                "验证会话已失效，请重新获取短信验证码", code=401
            )
        client = _account_client(account_id)
        try:
            client.restore_login_info(cached)
        except ProtocolError as error:
            clear_auth_cache(account_id)
            raise VerificationSessionExpired(
                "验证会话已失效，请重新获取短信验证码", code=error.code
            ) from error

    try:
        client.bind_device(code)
    except ProtocolError as error:
        if _is_auth_error(error):
            _pending.pop(account_id, None)
            clear_auth_cache(account_id)
            raise VerificationSessionExpired(
                "验证会话已失效，请重新获取短信验证码", code=error.code
            ) from error
        raise

    _pending.pop(account_id, None)
    login_info = client.export_login_info()
    login_info["bondedDevice"] = True
    save_auth_cache(account_id, login_info)
