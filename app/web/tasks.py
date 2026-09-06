import asyncio
import os
import re
import signal
from datetime import datetime, timedelta
from pathlib import Path

from croniter import croniter

from .accounts import get_account_secret
from .config import APP_ROOT, LOG_DIR, SUPERVISOR_CONFIG
from .db import database, now_text
from .platform_status import (
    refresh_platform_status,
    save_platform_status_error,
)


TASK_COMMANDS = {
    "chat": ["python3", str(APP_ROOT / "login_script.py")],
    "pc": ["python3", str(APP_ROOT / "pc_login.py")],
}
TASK_LABELS = {"chat": "AI 对话积分", "pc": "云电脑挂机"}
SECRET_PATTERN = re.compile(
    r"(?i)(authorization|cookie|token|password|passwd)(\s*[:=]\s*)([^\s,;]+)"
)
SUPERVISOR_RUNNING_PATTERN = re.compile(
    r"^\S+\s+RUNNING\s+pid\s+(?P<pid>\d+),\s+uptime\s+(?P<uptime>.+)$"
)
SUPERVISOR_STATE_PATTERN = re.compile(r"^\S+\s+(?P<status>[A-Z]+)")


def redact(text: str) -> str:
    return SECRET_PATTERN.sub(r"\1\2***", text)


def format_supervisor_uptime(value: str) -> str:
    match = re.fullmatch(
        r"(?:(?P<days>\d+)\s+days?,\s*)?"
        r"(?P<hours>\d+):(?P<minutes>\d{2}):(?P<seconds>\d{2})",
        value.strip(),
    )
    if not match:
        return value.strip()
    days = int(match.group("days") or 0)
    hours = int(match.group("hours"))
    minutes = int(match.group("minutes"))
    seconds = int(match.group("seconds"))
    parts = []
    if days:
        parts.append(f"{days}天")
    if hours:
        parts.append(f"{hours}小时")
    if minutes:
        parts.append(f"{minutes}分")
    parts.append(f"{seconds}秒")
    return "".join(parts)


def describe_supervisor_status(text: str) -> tuple[str, str]:
    running = SUPERVISOR_RUNNING_PATTERN.match(text)
    if running:
        uptime = format_supervisor_uptime(running.group("uptime"))
        return "running", f"进程号 {running.group('pid')} · 已运行 {uptime}"

    state_match = SUPERVISOR_STATE_PATTERN.match(text)
    status = state_match.group("status") if state_match else "UNKNOWN"
    descriptions = {
        "STARTING": ("unknown", "保活进程正在启动"),
        "STOPPING": ("unknown", "保活进程正在停止"),
        "STOPPED": ("stopped", "保活进程已停止"),
        "EXITED": ("stopped", "保活进程已退出"),
        "BACKOFF": ("stopped", "启动失败，正在重试"),
        "FATAL": ("stopped", "保活进程启动失败"),
        "UNKNOWN": ("unknown", "暂时无法读取保活进程状态"),
    }
    return descriptions.get(status, descriptions["UNKNOWN"])


def list_runs(limit: int = 100, account_id: int | None = None):
    query = (
        "SELECT task_runs.*, accounts.name AS account_name, accounts.username "
        "FROM task_runs LEFT JOIN accounts ON accounts.id = task_runs.account_id"
    )
    parameters: list[object] = []
    if account_id is not None:
        query += " WHERE task_runs.account_id = ?"
        parameters.append(account_id)
    query += " ORDER BY task_runs.id DESC LIMIT ?"
    parameters.append(limit)
    with database() as connection:
        return connection.execute(query, parameters).fetchall()


def get_run(run_id: int):
    with database() as connection:
        return connection.execute(
            "SELECT task_runs.*, accounts.name AS account_name, accounts.username "
            "FROM task_runs LEFT JOIN accounts ON accounts.id = task_runs.account_id "
            "WHERE task_runs.id = ?",
            (run_id,),
        ).fetchone()


def read_log(run_id: int, max_lines: int = 500) -> str:
    run = get_run(run_id)
    if not run:
        return ""
    path = Path(run["log_path"])
    try:
        path.resolve().relative_to(LOG_DIR.resolve())
    except (ValueError, OSError):
        return ""
    if not path.exists():
        return ""
    lines = path.read_text(encoding="utf-8", errors="replace").splitlines()
    return "\n".join(lines[-max_lines:])


class TaskManager:
    def __init__(self) -> None:
        self.active: dict[tuple[int, str], asyncio.subprocess.Process | None] = {}
        self.run_tasks: dict[int, asyncio.Task] = {}
        self.stopping: set[int] = set()
        self.status_locks: dict[int, asyncio.Lock] = {}
        self.scheduler_task: asyncio.Task | None = None
        self.started_at = datetime.now().astimezone()

    async def start(self) -> None:
        self.scheduler_task = asyncio.create_task(self._scheduler_loop())

    async def close(self) -> None:
        if self.scheduler_task:
            self.scheduler_task.cancel()
            try:
                await self.scheduler_task
            except asyncio.CancelledError:
                pass
        for process in list(self.active.values()):
            if process and process.returncode is None:
                try:
                    os.killpg(process.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
        for task in list(self.run_tasks.values()):
            task.cancel()
        if self.run_tasks:
            await asyncio.gather(*self.run_tasks.values(), return_exceptions=True)
        with database() as connection:
            connection.execute(
                "UPDATE task_runs SET status = 'interrupted', finished_at = ?, "
                "message = 'Web 服务停止，任务已中断' "
                "WHERE status IN ('queued', 'running')",
                (now_text(),),
            )

    async def launch(
        self, account_id: int, task_type: str, trigger_source: str = "manual"
    ) -> tuple[bool, int | str]:
        if task_type not in TASK_COMMANDS:
            return False, "未知任务类型"
        key = (account_id, task_type)
        if key in self.active:
            return False, "该账号的同类任务正在运行"
        account = get_account_secret(account_id)
        if not account:
            return False, "账号不存在"
        if not account["enabled"]:
            return False, "账号已禁用"
        if account["device_status"] == "pending":
            return False, "账号正在等待设备验证"

        with database() as connection:
            cursor = connection.execute(
                "INSERT INTO task_runs(account_id, task_type, trigger_source, status, "
                "started_at, log_path, message) VALUES (?, ?, ?, 'queued', ?, '', '')",
                (account_id, task_type, trigger_source, now_text()),
            )
            run_id = int(cursor.lastrowid)
            log_path = LOG_DIR / f"task-{run_id}.log"
            connection.execute(
                "UPDATE task_runs SET log_path = ? WHERE id = ?",
                (str(log_path), run_id),
            )
        self.active[key] = None
        task = asyncio.create_task(self._execute(run_id, account, task_type, log_path))
        self.run_tasks[run_id] = task
        return True, run_id

    async def refresh_account_status(self, account_id: int) -> dict:
        lock = self.status_locks.setdefault(account_id, asyncio.Lock())
        async with lock:
            account = get_account_secret(account_id)
            if not account:
                raise ValueError("账号不存在")
            if account["device_status"] == "pending":
                raise ValueError("账号正在等待设备验证")
            try:
                return await asyncio.to_thread(refresh_platform_status, account_id)
            except Exception as error:
                try:
                    await asyncio.to_thread(
                        save_platform_status_error, account_id, redact(str(error))
                    )
                except Exception:
                    pass
                raise

    async def _execute(
        self, run_id: int, account: dict, task_type: str, log_path: Path
    ) -> None:
        key = (int(account["id"]), task_type)
        environment = os.environ.copy()
        environment.update(
            {
                "APP_USER": account["username"],
                "APP_PASSWORD": account["password"],
                "DEVICECODE": account["device_code"],
                "CTYUN_ACCOUNT_ID": str(account["id"]),
                "RUNNING_IN_DOCKER": "true",
                "PYTHONUNBUFFERED": "1",
            }
        )
        process = None
        try:
            log_path.parent.mkdir(parents=True, exist_ok=True)
            with log_path.open("a", encoding="utf-8") as log:
                log.write(
                    f"[{now_text()}] 启动 {TASK_LABELS[task_type]}，账号：{account['name']}\n"
                )
                log.flush()
                process = await asyncio.create_subprocess_exec(
                    *TASK_COMMANDS[task_type],
                    cwd=APP_ROOT,
                    env=environment,
                    stdout=asyncio.subprocess.PIPE,
                    stderr=asyncio.subprocess.STDOUT,
                    start_new_session=True,
                )
                self.active[key] = process
                with database() as connection:
                    connection.execute(
                        "UPDATE task_runs SET status = 'running' WHERE id = ?", (run_id,)
                    )
                assert process.stdout is not None
                while True:
                    line = await process.stdout.readline()
                    if not line:
                        break
                    log.write(redact(line.decode("utf-8", errors="replace")))
                    log.flush()
                exit_code = await process.wait()
                if run_id in self.stopping:
                    status = "stopped"
                    message = "由管理员停止"
                else:
                    status = "success" if exit_code == 0 else "failed"
                    message = (
                        "任务执行完成" if exit_code == 0 else f"任务退出码 {exit_code}"
                    )
                log.write(f"[{now_text()}] {message}\n")
            with database() as connection:
                connection.execute(
                    "UPDATE task_runs SET status = ?, finished_at = ?, exit_code = ?, "
                    "message = ? WHERE id = ?",
                    (status, now_text(), exit_code, message, run_id),
                )
            if status != "stopped":
                try:
                    snapshot = await self.refresh_account_status(int(account["id"]))
                    with log_path.open("a", encoding="utf-8") as log:
                        log.write(
                            f"[{now_text()}] 平台任务状态已更新，"
                            f"总积分：{snapshot['total_points']}\n"
                        )
                except Exception as status_error:
                    with log_path.open("a", encoding="utf-8") as log:
                        log.write(
                            f"[{now_text()}] 平台任务状态查询失败："
                            f"{redact(str(status_error))}\n"
                        )
        except Exception as error:
            message = redact(str(error))
            with database() as connection:
                connection.execute(
                    "UPDATE task_runs SET status = 'failed', finished_at = ?, message = ? "
                    "WHERE id = ?",
                    (now_text(), message, run_id),
                )
        finally:
            self.active.pop(key, None)
            self.run_tasks.pop(run_id, None)
            self.stopping.discard(run_id)

    async def stop(self, run_id: int) -> bool:
        run = get_run(run_id)
        if not run or run["status"] not in ("queued", "running"):
            return False
        key = (int(run["account_id"]), run["task_type"])
        process = self.active.get(key)
        if process is None:
            queued_task = self.run_tasks.pop(run_id, None)
            if queued_task:
                queued_task.cancel()
            self.active.pop(key, None)
            with database() as connection:
                connection.execute(
                    "UPDATE task_runs SET status = 'stopped', finished_at = ?, "
                    "message = '由管理员停止' WHERE id = ?",
                    (now_text(), run_id),
                )
            return True
        self.stopping.add(run_id)
        if process.returncode is None:
            try:
                os.killpg(process.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                await asyncio.wait_for(process.wait(), timeout=10)
            except asyncio.TimeoutError:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
        with database() as connection:
            connection.execute(
                "UPDATE task_runs SET status = 'stopped', finished_at = ?, "
                "message = '由管理员停止' WHERE id = ?",
                (now_text(), run_id),
            )
        return True

    async def _scheduler_loop(self) -> None:
        while True:
            try:
                await self._run_due_schedules()
            except Exception as error:
                print(f"[!] 调度检查失败: {redact(str(error))}")
            await asyncio.sleep(20)

    async def _run_due_schedules(self) -> None:
        now = datetime.now().astimezone().replace(second=0, microsecond=0)
        minute_key = now.isoformat(timespec="minutes")
        with database() as connection:
            rows = connection.execute(
                "SELECT id, chat_enabled, chat_cron, pc_enabled, pc_cron "
                "FROM accounts WHERE enabled = 1 AND device_status != 'pending'"
            ).fetchall()
            connection.execute(
                "DELETE FROM scheduler_claims WHERE minute_key < ?",
                ((now - timedelta(days=7)).isoformat(timespec="minutes"),),
            )
        for row in rows:
            schedules = (
                ("chat", bool(row["chat_enabled"]), row["chat_cron"]),
                ("pc", bool(row["pc_enabled"]), row["pc_cron"]),
            )
            for task_type, enabled, expression in schedules:
                if not enabled or not croniter.match(expression, now):
                    continue
                with database() as connection:
                    cursor = connection.execute(
                        "INSERT OR IGNORE INTO scheduler_claims(account_id, task_type, minute_key) "
                        "VALUES (?, ?, ?)",
                        (row["id"], task_type, minute_key),
                    )
                    claimed = cursor.rowcount == 1
                if claimed:
                    await self.launch(int(row["id"]), task_type, "schedule")


async def supervisor_action(action: str) -> tuple[bool, str]:
    if action not in {"restart", "start", "stop"}:
        return False, "不支持的操作"
    try:
        process = await asyncio.create_subprocess_exec(
            "supervisorctl",
            "-c",
            str(SUPERVISOR_CONFIG),
            action,
            "ctyun",
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.STDOUT,
        )
        output, _ = await asyncio.wait_for(process.communicate(), timeout=20)
        message = output.decode("utf-8", errors="replace").strip()
        return process.returncode == 0, message
    except Exception as error:
        return False, str(error)


async def ctyun_status() -> dict:
    try:
        process = await asyncio.create_subprocess_exec(
            "supervisorctl",
            "-c",
            str(SUPERVISOR_CONFIG),
            "status",
            "ctyun",
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.STDOUT,
        )
        output, _ = await asyncio.wait_for(process.communicate(), timeout=5)
        text = output.decode("utf-8", errors="replace").strip()
        state, detail = describe_supervisor_status(text)
        return {"state": state, "detail": detail}
    except Exception as error:
        return {"state": "unknown", "detail": str(error)}
