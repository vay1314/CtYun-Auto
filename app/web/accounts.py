import json
import os
import re
import secrets
import string
from pathlib import Path

from croniter import croniter

from .config import ACCOUNTS_JSON
from .db import database, now_text
from .security import decrypt_secret, encrypt_secret


DEFAULT_CHAT_CRON = "0 3,20 * * *"
DEFAULT_PC_CRON = "0 4,6 * * *"


def validate_cron(expression: str) -> str:
    expression = " ".join(expression.split())
    if len(expression.split()) != 5 or not croniter.is_valid(expression):
        raise ValueError("Cron 表达式必须是有效的 5 段格式")
    return expression


def generate_device_code() -> str:
    alphabet = string.ascii_letters + string.digits
    return "web_" + "".join(secrets.choice(alphabet) for _ in range(32))


def list_accounts():
    with database() as connection:
        return connection.execute(
            "SELECT * FROM accounts ORDER BY enabled DESC, id ASC"
        ).fetchall()


def get_account(account_id: int):
    with database() as connection:
        return connection.execute(
            "SELECT * FROM accounts WHERE id = ?", (account_id,)
        ).fetchone()


def get_account_secret(account_id: int) -> dict | None:
    row = get_account(account_id)
    if not row:
        return None
    result = dict(row)
    result["password"] = decrypt_secret(result.pop("password_encrypted"))
    return result


def save_account(
    account_id: int | None,
    *,
    name: str,
    username: str,
    password: str,
    device_code: str,
    enabled: bool,
    chat_enabled: bool,
    chat_cron: str,
    pc_enabled: bool,
    pc_cron: str,
) -> int:
    name = name.strip()
    username = username.strip()
    device_code = device_code.strip() or generate_device_code()
    if not name or not username:
        raise ValueError("名称和账号不能为空")
    if not re.fullmatch(r"[A-Za-z0-9@._+\-]{3,128}", username):
        raise ValueError("账号只能包含字母、数字及 @ . _ + -")
    chat_cron = validate_cron(chat_cron)
    pc_cron = validate_cron(pc_cron)
    timestamp = now_text()

    with database() as connection:
        if account_id is None:
            if not password:
                raise ValueError("新增账号必须填写密码")
            cursor = connection.execute(
                "INSERT INTO accounts(name, username, password_encrypted, device_code, "
                "enabled, chat_enabled, chat_cron, pc_enabled, pc_cron, created_at, updated_at) "
                "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
                (
                    name,
                    username,
                    encrypt_secret(password),
                    device_code,
                    int(enabled),
                    int(chat_enabled),
                    chat_cron,
                    int(pc_enabled),
                    pc_cron,
                    timestamp,
                    timestamp,
                ),
            )
            account_id = int(cursor.lastrowid)
        else:
            existing = connection.execute(
                "SELECT username, password_encrypted, device_code FROM accounts "
                "WHERE id = ?",
                (account_id,),
            ).fetchone()
            if not existing:
                raise ValueError("账号不存在")
            encrypted_password = (
                encrypt_secret(password) if password else existing["password_encrypted"]
            )
            credentials_changed = bool(password) or any(
                (
                    existing["username"] != username,
                    existing["device_code"] != device_code,
                )
            )
            connection.execute(
                "UPDATE accounts SET name = ?, username = ?, password_encrypted = ?, "
                "device_code = ?, enabled = ?, chat_enabled = ?, chat_cron = ?, "
                "pc_enabled = ?, pc_cron = ?, updated_at = ? WHERE id = ?",
                (
                    name,
                    username,
                    encrypted_password,
                    device_code,
                    int(enabled),
                    int(chat_enabled),
                    chat_cron,
                    int(pc_enabled),
                    pc_cron,
                    timestamp,
                    account_id,
                ),
            )
            if credentials_changed:
                connection.execute(
                    "DELETE FROM account_auth_cache WHERE account_id = ?", (account_id,)
                )
    write_ctyun_accounts()
    return account_id


def delete_account(account_id: int) -> None:
    with database() as connection:
        connection.execute("DELETE FROM accounts WHERE id = ?", (account_id,))
    write_ctyun_accounts()


def write_ctyun_accounts() -> None:
    accounts = []
    for row in list_accounts():
        if not row["enabled"]:
            continue
        accounts.append(
            {
                "name": row["name"],
                "user": row["username"],
                "password": decrypt_secret(row["password_encrypted"]),
                "deviceCode": row["device_code"],
            }
        )
    payload = {"keepAliveSeconds": 60, "accounts": accounts}
    temporary = ACCOUNTS_JSON.with_suffix(".json.tmp")
    temporary.write_text(
        json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8"
    )
    try:
        temporary.chmod(0o600)
    except OSError:
        pass
    os.replace(temporary, ACCOUNTS_JSON)


def import_environment_account() -> bool:
    username = os.getenv("APP_USER", "").strip()
    password = os.getenv("APP_PASSWORD", "")
    if not username or not password:
        return False
    with database() as connection:
        exists = connection.execute(
            "SELECT 1 FROM accounts WHERE username = ?", (username,)
        ).fetchone()
    if exists:
        chat_cron = os.getenv("CHAT_CRON", "").strip()
        pc_cron = os.getenv("PC_CRON", "").strip()
        if chat_cron or pc_cron:
            with database() as connection:
                current = connection.execute(
                    "SELECT chat_cron, pc_cron FROM accounts WHERE username = ?",
                    (username,),
                ).fetchone()
                connection.execute(
                    "UPDATE accounts SET chat_cron = ?, pc_cron = ?, updated_at = ? "
                    "WHERE username = ?",
                    (
                        validate_cron(chat_cron or current["chat_cron"]),
                        validate_cron(pc_cron or current["pc_cron"]),
                        now_text(),
                        username,
                    ),
                )
        return False
    save_account(
        None,
        name=f"账号 {username[-4:]}",
        username=username,
        password=password,
        device_code=os.getenv("DEVICECODE", ""),
        enabled=True,
        chat_enabled=True,
        chat_cron=os.getenv("CHAT_CRON", DEFAULT_CHAT_CRON),
        pc_enabled=True,
        pc_cron=os.getenv("PC_CRON", DEFAULT_PC_CRON),
    )
    return True
