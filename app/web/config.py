import os
from pathlib import Path


APP_ROOT = Path(os.getenv("APP_ROOT", "/app"))
DATA_DIR = Path(os.getenv("CTYUN_DATA_DIR", "/app/data"))
DB_PATH = DATA_DIR / "ctyun-auto.db"
LOG_DIR = DATA_DIR / "logs"
ACCOUNT_DATA_DIR = DATA_DIR / "accounts"
ACCOUNTS_JSON = DATA_DIR / "accounts.json"
SESSION_KEY_FILE = DATA_DIR / ".web_session_key"
FERNET_KEY_FILE = DATA_DIR / ".credential_key"
VERSION_FILE = APP_ROOT / "VERSION"
SUPERVISOR_CONFIG = APP_ROOT / "supervisord.conf"
APP_PORT = int(os.getenv("APP_PORT", "9845"))


def ensure_directories() -> None:
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    LOG_DIR.mkdir(parents=True, exist_ok=True)
    ACCOUNT_DATA_DIR.mkdir(parents=True, exist_ok=True)
    try:
        ACCOUNT_DATA_DIR.chmod(0o700)
    except OSError:
        pass
