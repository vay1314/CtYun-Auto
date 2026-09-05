import json

try:
    from protocols import CtYunProtocolClient
except ImportError:
    from ..protocols import CtYunProtocolClient

from .accounts import get_account_secret
from .db import database, now_text


PLATFORM_TASKS = {
    "login": {"id": 1002, "label": "登录 AI 云电脑", "names": {"登录AI云电脑"}},
    "usage": {"id": 1003, "label": "使用 1 小时", "names": {"使用1小时"}},
    "chat": {"id": 1004, "label": "AI 对话", "names": {"与AI对话1次", "AI对话"}},
}


def _normalize_task(task: dict | None, definition: dict) -> dict:
    if not task:
        return {
            "label": definition["label"],
            "state": "missing",
            "state_label": "未获取",
            "current": 0,
            "total": 0,
        }
    current = int(task.get("currentProgress") or 0)
    total = int(task.get("totalProgress") or 0)
    status = int(task.get("status") or 0)
    completed = status == 2 or (total > 0 and current >= total)
    if completed:
        state, state_label = "success", "已完成"
    elif current > 0:
        state, state_label = "running", "进行中"
    else:
        state, state_label = "pending", "未完成"
    return {
        "label": definition["label"],
        "state": state,
        "state_label": state_label,
        "current": current,
        "total": total,
    }


def _match_tasks(items: list[dict]) -> dict[str, dict]:
    by_id = {}
    by_name = {}
    for item in items:
        try:
            task_id = int(item.get("taskDefId") or 0)
        except (TypeError, ValueError):
            task_id = 0
        if task_id:
            by_id[task_id] = item
        name = "".join(str(item.get("taskDefName") or "").split())
        if name:
            by_name[name] = item
    result = {}
    for key, definition in PLATFORM_TASKS.items():
        task = by_id.get(int(definition["id"]))
        if task is None:
            task = next(
                (by_name[name] for name in definition["names"] if name in by_name),
                None,
            )
        result[key] = _normalize_task(task, definition)
    return result


def save_platform_status(
    account_id: int, total_points: int, tasks: dict[str, dict]
) -> dict:
    updated_at = now_text()
    tasks_json = json.dumps(tasks, ensure_ascii=False, separators=(",", ":"))
    with database() as connection:
        connection.execute(
            "INSERT INTO account_platform_status"
            "(account_id, total_points, tasks_json, updated_at, error) "
            "VALUES (?, ?, ?, ?, '') "
            "ON CONFLICT(account_id) DO UPDATE SET "
            "total_points = excluded.total_points, tasks_json = excluded.tasks_json, "
            "updated_at = excluded.updated_at, error = ''",
            (account_id, total_points, tasks_json, updated_at),
        )
    return {
        "account_id": account_id,
        "total_points": total_points,
        "tasks": tasks,
        "updated_at": updated_at,
        "error": "",
    }


def save_platform_status_error(account_id: int, message: str) -> None:
    with database() as connection:
        connection.execute(
            "INSERT INTO account_platform_status"
            "(account_id, total_points, tasks_json, updated_at, error) "
            "VALUES (?, NULL, '{}', ?, ?) "
            "ON CONFLICT(account_id) DO UPDATE SET "
            "updated_at = excluded.updated_at, error = excluded.error",
            (account_id, now_text(), message[:500]),
        )


def refresh_platform_status(account_id: int) -> dict:
    account = get_account_secret(account_id)
    if not account:
        raise ValueError("账号不存在")
    client = CtYunProtocolClient(
        account["username"], account["password"], account["device_code"]
    )
    client.login()
    tasks = _match_tasks(client.get_task_list())
    total_points = client.get_user_points()
    return save_platform_status(account_id, total_points, tasks)


def list_platform_statuses() -> dict[int, dict]:
    with database() as connection:
        rows = connection.execute(
            "SELECT account_id, total_points, tasks_json, updated_at, error "
            "FROM account_platform_status"
        ).fetchall()
    result = {}
    for row in rows:
        try:
            tasks = json.loads(row["tasks_json"])
        except (TypeError, ValueError):
            tasks = {}
        result[int(row["account_id"])] = {
            "account_id": int(row["account_id"]),
            "total_points": row["total_points"],
            "tasks": tasks if isinstance(tasks, dict) else {},
            "updated_at": row["updated_at"],
            "error": row["error"],
        }
    return result
