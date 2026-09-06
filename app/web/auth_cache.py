import json

from .db import database, now_text
from .security import decrypt_secret, encrypt_secret


def clear_auth_cache(account_id: int) -> None:
    with database() as connection:
        connection.execute(
            "DELETE FROM account_auth_cache WHERE account_id = ?", (account_id,)
        )


def load_auth_cache(account_id: int) -> dict | None:
    with database() as connection:
        row = connection.execute(
            "SELECT login_info_encrypted FROM account_auth_cache WHERE account_id = ?",
            (account_id,),
        ).fetchone()
    if not row:
        return None
    try:
        value = json.loads(decrypt_secret(row["login_info_encrypted"]))
        if not isinstance(value, dict):
            raise ValueError("登录缓存不是对象")
        return value
    except Exception:
        clear_auth_cache(account_id)
        return None


def save_auth_cache(account_id: int, login_info: dict) -> None:
    encrypted = encrypt_secret(
        json.dumps(login_info, ensure_ascii=False, separators=(",", ":"))
    )
    with database() as connection:
        connection.execute(
            "INSERT INTO account_auth_cache"
            "(account_id, login_info_encrypted, updated_at) VALUES (?, ?, ?) "
            "ON CONFLICT(account_id) DO UPDATE SET "
            "login_info_encrypted = excluded.login_info_encrypted, "
            "updated_at = excluded.updated_at",
            (account_id, encrypted, now_text()),
        )
