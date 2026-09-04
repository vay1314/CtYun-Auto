import asyncio
import json
import os
import secrets
import sqlite3
import time
from contextlib import asynccontextmanager
from datetime import datetime
from pathlib import Path

from fastapi import FastAPI, Request
from fastapi.responses import HTMLResponse, JSONResponse, RedirectResponse, StreamingResponse
from fastapi.staticfiles import StaticFiles
from fastapi.templating import Jinja2Templates
from starlette.middleware.sessions import SessionMiddleware

from .accounts import (
    DEFAULT_CHAT_CRON,
    DEFAULT_PC_CRON,
    delete_account,
    generate_device_code,
    get_account,
    list_accounts,
    save_account,
)
from .config import APP_ROOT, LOG_DIR, VERSION_FILE, ensure_directories
from .db import database, get_setting, init_db, set_setting
from .security import (
    hash_password,
    new_csrf_token,
    session_secret,
    verify_password,
)
from .tasks import (
    TASK_LABELS,
    TaskManager,
    ctyun_status,
    get_run,
    list_runs,
    read_log,
    redact,
    supervisor_action,
)


LOGIN_WINDOW_SECONDS = 300
LOGIN_MAX_ATTEMPTS = 5
login_attempts: dict[str, list[float]] = {}
task_manager = TaskManager()
STATUS_LABELS = {
    "queued": "排队中",
    "running": "运行中",
    "success": "成功",
    "failed": "失败",
    "stopped": "已停止",
    "interrupted": "已中断",
}


@asynccontextmanager
async def lifespan(_: FastAPI):
    init_db()
    await task_manager.start()
    yield
    await task_manager.close()


app = FastAPI(title="ctyun-auto", docs_url=None, redoc_url=None, lifespan=lifespan)
app.add_middleware(
    SessionMiddleware,
    secret_key=session_secret(),
    max_age=12 * 60 * 60,
    same_site="strict",
    https_only=os.getenv("WEB_SECURE_COOKIE", "false").lower() == "true",
)
app.mount("/static", StaticFiles(directory=APP_ROOT / "web" / "static"), name="static")
templates = Jinja2Templates(directory=APP_ROOT / "web" / "templates")


def project_version() -> str:
    try:
        return VERSION_FILE.read_text(encoding="utf-8").strip()
    except OSError:
        return os.getenv("APP_VERSION", "dev")


def add_flash(request: Request, message: str, category: str = "success") -> None:
    request.session.setdefault("flashes", []).append(
        {"message": message, "category": category}
    )


def csrf_token(request: Request) -> str:
    token = request.session.get("csrf_token")
    if not token:
        token = new_csrf_token()
        request.session["csrf_token"] = token
    return token


async def require_csrf(request: Request) -> bool:
    form = await request.form()
    submitted = str(form.get("csrf_token", ""))
    expected = str(request.session.get("csrf_token", ""))
    return bool(submitted and expected and secrets.compare_digest(submitted, expected))


def require_api_csrf(request: Request) -> bool:
    submitted = request.headers.get("x-csrf-token", "")
    expected = str(request.session.get("csrf_token", ""))
    return bool(submitted and expected and secrets.compare_digest(submitted, expected))


def render(request: Request, template: str, **context):
    context.update(
        {
            "request": request,
            "csrf_token": csrf_token(request),
            "flashes": request.session.pop("flashes", []),
            "version": project_version(),
            "task_labels": TASK_LABELS,
            "status_labels": STATUS_LABELS,
            "current_path": request.url.path,
        }
    )
    return templates.TemplateResponse(request=request, name=template, context=context)


def auth_redirect(request: Request):
    if not get_setting("admin_password_hash"):
        return RedirectResponse("/setup", status_code=303)
    if not request.session.get("authenticated"):
        return RedirectResponse("/login", status_code=303)
    return None


def masked_username(username: str) -> str:
    if len(username) <= 5:
        return username[:1] + "***"
    return username[:3] + "****" + username[-3:]


def recent_system_log(name: str, max_lines: int = 120) -> str:
    allowed = {
        "ctyun": LOG_DIR / "ctyun.log",
        "web": LOG_DIR / "web.log",
        "supervisor": LOG_DIR / "supervisord.log",
    }
    path = allowed.get(name)
    if not path or not path.exists():
        return ""
    return redact(
        "\n".join(
            path.read_text(encoding="utf-8", errors="replace").splitlines()[
                -max_lines:
            ]
        )
    )


@app.get("/health")
async def health():
    return {"status": "ok", "version": project_version()}


@app.get("/setup", response_class=HTMLResponse)
async def setup_page(request: Request):
    if get_setting("admin_password_hash"):
        return RedirectResponse("/login", status_code=303)
    return render(request, "setup.html")


@app.post("/setup")
async def setup_submit(request: Request):
    if get_setting("admin_password_hash"):
        return RedirectResponse("/login", status_code=303)
    if not await require_csrf(request):
        return HTMLResponse("CSRF validation failed", status_code=403)
    form = await request.form()
    password = str(form.get("password", ""))
    confirmation = str(form.get("confirmation", ""))
    if len(password) < 8:
        return render(request, "setup.html", error="管理员密码至少需要 8 个字符")
    if password != confirmation:
        return render(request, "setup.html", error="两次输入的密码不一致")
    set_setting("admin_password_hash", hash_password(password))
    request.session["authenticated"] = True
    add_flash(request, "管理密码已设置")
    return RedirectResponse("/", status_code=303)


@app.get("/login", response_class=HTMLResponse)
async def login_page(request: Request):
    if not get_setting("admin_password_hash"):
        return RedirectResponse("/setup", status_code=303)
    if request.session.get("authenticated"):
        return RedirectResponse("/", status_code=303)
    return render(request, "login.html")


@app.post("/login")
async def login_submit(request: Request):
    if not await require_csrf(request):
        return HTMLResponse("CSRF validation failed", status_code=403)
    client = request.client.host if request.client else "unknown"
    now = time.monotonic()
    attempts = [
        value
        for value in login_attempts.get(client, [])
        if now - value < LOGIN_WINDOW_SECONDS
    ]
    if len(attempts) >= LOGIN_MAX_ATTEMPTS:
        return render(request, "login.html", error="登录尝试过多，请五分钟后再试")
    form = await request.form()
    encoded = get_setting("admin_password_hash") or ""
    if not verify_password(encoded, str(form.get("password", ""))):
        attempts.append(now)
        login_attempts[client] = attempts
        return render(request, "login.html", error="密码不正确")
    login_attempts.pop(client, None)
    request.session.clear()
    request.session["authenticated"] = True
    csrf_token(request)
    return RedirectResponse("/", status_code=303)


@app.post("/logout")
async def logout(request: Request):
    if not await require_csrf(request):
        return HTMLResponse("CSRF validation failed", status_code=403)
    request.session.clear()
    return RedirectResponse("/login", status_code=303)


@app.get("/", response_class=HTMLResponse)
async def dashboard(request: Request):
    guard = auth_redirect(request)
    if guard:
        return guard
    accounts = list_accounts()
    runs = list_runs(8)
    ctyun = await ctyun_status()
    return render(
        request,
        "dashboard.html",
        accounts=accounts,
        runs=runs,
        ctyun=ctyun,
        running_count=len(task_manager.active),
        uptime=datetime.now().astimezone() - task_manager.started_at,
        upstream_revision=os.getenv("CTYUN_REVISION", "master"),
    )


@app.get("/accounts", response_class=HTMLResponse)
async def accounts_page(request: Request):
    guard = auth_redirect(request)
    if guard:
        return guard
    accounts = list_accounts()
    return render(
        request,
        "accounts.html",
        accounts=accounts,
        masked_username=masked_username,
    )


@app.get("/partials/status", response_class=HTMLResponse)
async def status_partial(request: Request):
    guard = auth_redirect(request)
    if guard:
        return HTMLResponse("", status_code=401)
    accounts = list_accounts()
    runs = list_runs(8)
    return render(
        request,
        "partials/status_cards.html",
        accounts=accounts,
        runs=runs,
        ctyun=await ctyun_status(),
        running_count=len(task_manager.active),
    )


@app.get("/accounts/new", response_class=HTMLResponse)
async def account_new_page(request: Request):
    guard = auth_redirect(request)
    if guard:
        return guard
    account = {
        "id": None,
        "name": "",
        "username": "",
        "device_code": generate_device_code(),
        "enabled": 1,
        "chat_enabled": 1,
        "chat_cron": DEFAULT_CHAT_CRON,
        "pc_enabled": 1,
        "pc_cron": DEFAULT_PC_CRON,
    }
    return render(request, "account_form.html", account=account)


@app.get("/accounts/{account_id}/edit", response_class=HTMLResponse)
async def account_edit_page(request: Request, account_id: int):
    guard = auth_redirect(request)
    if guard:
        return guard
    account = get_account(account_id)
    if not account:
        return HTMLResponse("Account not found", status_code=404)
    return render(request, "account_form.html", account=account)


@app.post("/accounts/save")
async def account_save(request: Request):
    guard = auth_redirect(request)
    if guard:
        return guard
    if not await require_csrf(request):
        return HTMLResponse("CSRF validation failed", status_code=403)
    form = await request.form()
    raw_id = str(form.get("account_id", "")).strip()
    account_id = int(raw_id) if raw_id else None
    account_data = {
        "id": account_id,
        "name": str(form.get("name", "")),
        "username": str(form.get("username", "")),
        "device_code": str(form.get("device_code", "")),
        "enabled": int("enabled" in form),
        "chat_enabled": int("chat_enabled" in form),
        "chat_cron": str(form.get("chat_cron", DEFAULT_CHAT_CRON)),
        "pc_enabled": int("pc_enabled" in form),
        "pc_cron": str(form.get("pc_cron", DEFAULT_PC_CRON)),
    }
    try:
        save_account(
            account_id,
            name=account_data["name"],
            username=account_data["username"],
            password=str(form.get("password", "")),
            device_code=account_data["device_code"],
            enabled=bool(account_data["enabled"]),
            chat_enabled=bool(account_data["chat_enabled"]),
            chat_cron=account_data["chat_cron"],
            pc_enabled=bool(account_data["pc_enabled"]),
            pc_cron=account_data["pc_cron"],
        )
    except (ValueError, sqlite3.IntegrityError) as error:
        message = "该账号已经存在" if isinstance(error, sqlite3.IntegrityError) else str(error)
        return render(
            request, "account_form.html", account=account_data, error=message
        )
    await supervisor_action("restart")
    add_flash(request, "账号已保存，CtYun 保活进程已重新加载配置")
    return RedirectResponse("/accounts", status_code=303)


@app.post("/accounts/{account_id}/delete")
async def account_delete(request: Request, account_id: int):
    guard = auth_redirect(request)
    if guard:
        return guard
    if not await require_csrf(request):
        return HTMLResponse("CSRF validation failed", status_code=403)
    delete_account(account_id)
    await supervisor_action("restart")
    add_flash(request, "账号已删除", "warning")
    return RedirectResponse("/accounts", status_code=303)


@app.get("/tasks", response_class=HTMLResponse)
async def tasks_page(request: Request, account_id: int | None = None):
    guard = auth_redirect(request)
    if guard:
        return guard
    return render(
        request,
        "tasks.html",
        runs=list_runs(100, account_id),
        accounts=list_accounts(),
        selected_account=account_id,
    )


@app.post("/accounts/{account_id}/tasks/{task_type}/run")
async def task_run(request: Request, account_id: int, task_type: str):
    guard = auth_redirect(request)
    if guard:
        return guard
    if not await require_csrf(request):
        return HTMLResponse("CSRF validation failed", status_code=403)
    success, result = await task_manager.launch(account_id, task_type)
    if success:
        add_flash(request, f"任务已启动，运行编号 #{result}")
    else:
        add_flash(request, str(result), "error")
    return RedirectResponse("/tasks", status_code=303)


@app.post("/tasks/{run_id}/stop")
async def task_stop(request: Request, run_id: int):
    guard = auth_redirect(request)
    if guard:
        return guard
    if not await require_csrf(request):
        return HTMLResponse("CSRF validation failed", status_code=403)
    stopped = await task_manager.stop(run_id)
    add_flash(request, "任务已停止" if stopped else "任务当前无法停止", "warning")
    return RedirectResponse("/tasks", status_code=303)


@app.get("/logs", response_class=HTMLResponse)
async def logs_page(
    request: Request, run_id: int | None = None, source: str = "ctyun"
):
    guard = auth_redirect(request)
    if guard:
        return guard
    selected_run = get_run(run_id) if run_id else None
    content = read_log(run_id) if run_id else recent_system_log(source)
    return render(
        request,
        "logs.html",
        runs=list_runs(50),
        selected_run=selected_run,
        selected_source=source,
        log_content=content,
    )


@app.get("/logs/{run_id}/stream")
async def log_stream(request: Request, run_id: int):
    guard = auth_redirect(request)
    if guard:
        return guard
    run = get_run(run_id)
    if not run:
        return HTMLResponse("Run not found", status_code=404)
    path = Path(run["log_path"])
    try:
        path.resolve().relative_to(LOG_DIR.resolve())
    except (ValueError, OSError):
        return HTMLResponse("Invalid log path", status_code=400)

    async def events():
        position = path.stat().st_size if path.exists() else 0
        while True:
            if await request.is_disconnected():
                break
            if path.exists():
                with path.open("r", encoding="utf-8", errors="replace") as stream:
                    stream.seek(position)
                    chunk = stream.read()
                    position = stream.tell()
                if chunk:
                    yield f"data: {json.dumps(chunk, ensure_ascii=False)}\n\n"
            current = get_run(run_id)
            if not current or current["status"] not in ("queued", "running"):
                yield "event: done\ndata: true\n\n"
                break
            await asyncio.sleep(1)

    return StreamingResponse(events(), media_type="text/event-stream")


@app.post("/ctyun/restart")
async def restart_ctyun(request: Request):
    guard = auth_redirect(request)
    if guard:
        return guard
    if not await require_csrf(request):
        return HTMLResponse("CSRF validation failed", status_code=403)
    success, message = await supervisor_action("restart")
    add_flash(
        request,
        "CtYun 已重启" if success else f"重启失败：{message}",
        "success" if success else "error",
    )
    return RedirectResponse("/", status_code=303)


@app.get("/settings", response_class=HTMLResponse)
async def settings_page(request: Request):
    guard = auth_redirect(request)
    if guard:
        return guard
    return render(request, "settings.html")


@app.post("/settings/password")
async def settings_password(request: Request):
    guard = auth_redirect(request)
    if guard:
        return guard
    if not await require_csrf(request):
        return HTMLResponse("CSRF validation failed", status_code=403)
    form = await request.form()
    current = str(form.get("current_password", ""))
    password = str(form.get("password", ""))
    confirmation = str(form.get("confirmation", ""))
    encoded = get_setting("admin_password_hash") or ""
    if not verify_password(encoded, current):
        return render(request, "settings.html", error="当前密码不正确")
    if len(password) < 8:
        return render(request, "settings.html", error="新密码至少需要 8 个字符")
    if password != confirmation:
        return render(request, "settings.html", error="两次输入的新密码不一致")
    set_setting("admin_password_hash", hash_password(password))
    add_flash(request, "管理员密码已更新")
    return RedirectResponse("/settings", status_code=303)


@app.get("/api/status")
async def api_status(request: Request):
    guard = auth_redirect(request)
    if guard:
        return JSONResponse({"error": "unauthorized"}, status_code=401)
    ctyun = await ctyun_status()
    accounts = list_accounts()
    active = [
        {"account_id": account_id, "task_type": task_type}
        for account_id, task_type in task_manager.active
    ]
    return {
        "version": project_version(),
        "ctyun": ctyun,
        "accounts": len(accounts),
        "enabled_accounts": sum(1 for account in accounts if account["enabled"]),
        "active_tasks": active,
    }


@app.post("/api/accounts/{account_id}/tasks/{task_type}")
async def api_task_run(request: Request, account_id: int, task_type: str):
    guard = auth_redirect(request)
    if guard or not require_api_csrf(request):
        return JSONResponse({"error": "unauthorized"}, status_code=401)
    success, result = await task_manager.launch(account_id, task_type, "webmcp")
    if not success:
        return JSONResponse({"error": str(result)}, status_code=409)
    return {"run_id": result, "status": "queued"}
