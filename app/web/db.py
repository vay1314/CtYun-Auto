import sqlite3
from contextlib import contextmanager
from datetime import datetime
from typing import Iterator

from .config import DB_PATH, ensure_directories


SCHEMA = """
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    username TEXT NOT NULL UNIQUE,
    password_encrypted TEXT NOT NULL,
    device_code TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    chat_enabled INTEGER NOT NULL DEFAULT 1,
    chat_cron TEXT NOT NULL DEFAULT '0 3,20 * * *',
    pc_enabled INTEGER NOT NULL DEFAULT 1,
    pc_cron TEXT NOT NULL DEFAULT '0 4,6 * * *',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS task_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
    task_type TEXT NOT NULL,
    trigger_source TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    exit_code INTEGER,
    log_path TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS scheduler_claims (
    account_id INTEGER NOT NULL,
    task_type TEXT NOT NULL,
    minute_key TEXT NOT NULL,
    PRIMARY KEY (account_id, task_type, minute_key)
);

CREATE TABLE IF NOT EXISTS account_platform_status (
    account_id INTEGER PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    total_points INTEGER,
    tasks_json TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_task_runs_started_at
ON task_runs(started_at DESC);

CREATE INDEX IF NOT EXISTS idx_task_runs_account_type
ON task_runs(account_id, task_type, started_at DESC);
"""


def now_text() -> str:
    return datetime.now().astimezone().isoformat(timespec="seconds")


def connect() -> sqlite3.Connection:
    ensure_directories()
    connection = sqlite3.connect(DB_PATH, timeout=30)
    connection.row_factory = sqlite3.Row
    connection.execute("PRAGMA foreign_keys = ON")
    connection.execute("PRAGMA busy_timeout = 30000")
    return connection


@contextmanager
def database() -> Iterator[sqlite3.Connection]:
    connection = connect()
    try:
        yield connection
        connection.commit()
    except Exception:
        connection.rollback()
        raise
    finally:
        connection.close()


def init_db() -> None:
    ensure_directories()
    with database() as connection:
        connection.execute("PRAGMA journal_mode = WAL")
        connection.executescript(SCHEMA)
        connection.execute("PRAGMA optimize")
        connection.execute(
            "UPDATE task_runs SET status = 'interrupted', finished_at = ?, "
            "message = '服务重启，任务状态已重置' WHERE status IN ('queued', 'running')",
            (now_text(),),
        )


def get_setting(key: str) -> str | None:
    with database() as connection:
        row = connection.execute(
            "SELECT value FROM settings WHERE key = ?", (key,)
        ).fetchone()
    return row["value"] if row else None


def set_setting(key: str, value: str) -> None:
    with database() as connection:
        connection.execute(
            "INSERT INTO settings(key, value, updated_at) VALUES (?, ?, ?) "
            "ON CONFLICT(key) DO UPDATE SET value = excluded.value, "
            "updated_at = excluded.updated_at",
            (key, value, now_text()),
        )
