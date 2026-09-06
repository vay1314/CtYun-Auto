"""CtYun.dll 保活配套的积分查询与自动兑换。"""

from __future__ import annotations

import argparse
import calendar
import datetime
import hashlib
import json
import os
import sys
import time
from pathlib import Path

try:
    from .protocols import CtYunProtocolClient, ProtocolError
except ImportError:  # 兼容容器内以脚本方式直接执行。
    from protocols import CtYunProtocolClient, ProtocolError


HANG_SECONDS = 80 * 60
POLL_SECONDS = 30
RESTART_AT_FILE = Path("/tmp/ctyun_restart_at")


def get_account_data_dir(username: str, running_in_docker: bool) -> Path:
    if not running_in_docker:
        return Path(".")
    account_key = hashlib.sha256(username.encode("utf-8")).hexdigest()[:16]
    path = Path("/app/data/accounts") / account_key
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    return path


def get_redeem_config_path(username: str, running_in_docker: bool) -> Path:
    return get_account_data_dir(username, running_in_docker) / "redeem_config.json"


def load_redeem_config(path: Path) -> dict:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
        return data if isinstance(data, dict) else {}
    except FileNotFoundError:
        return {}
    except (OSError, ValueError) as error:
        print(f"[!] 读取兑换配置失败：{error}")
        return {}


def save_redeem_config(path: Path, config: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(".json.tmp")
    temporary.write_text(
        json.dumps(config, ensure_ascii=False, indent=2), encoding="utf-8"
    )
    try:
        temporary.chmod(0o600)
    except OSError:
        pass
    os.replace(temporary, path)
    print(f"[*] 兑换配置已保存：{path}")


def _input_index(max_index: int, prompt: str) -> int:
    while True:
        value = input(prompt).strip()
        if value.isdigit() and 1 <= int(value) <= max_index:
            return int(value) - 1
        print(f"[-] 请输入 1 到 {max_index}。")


def _input_non_negative_int(prompt: str, default: int) -> int:
    while True:
        value = input(prompt).strip()
        if not value:
            return default
        if value.isdigit():
            return int(value)
        print("[-] 请输入非负整数。")


def _input_positive_int(prompt: str, default: int) -> int:
    while True:
        value = _input_non_negative_int(prompt, default)
        if value > 0:
            return value
        print("[-] 请输入大于 0 的整数。")


def _input_month_days(prompt: str) -> list[int]:
    while True:
        try:
            values = [int(item.strip()) for item in input(prompt).split(",")]
            if not values or any(day != -1 and not 1 <= day <= 31 for day in values):
                raise ValueError
            return sorted(set(values))
        except ValueError:
            print("[-] 请输入 1-31 或 -1，并用逗号分隔。")


def prompt_redeem_schedule() -> dict:
    print("\n兑换时间设置：")
    print("1. 每日兑换（默认）")
    print("2. 每隔 N 日兑换")
    print("3. 每月指定日期兑换（-1 表示月末）")
    while True:
        choice = input("请选择 [1/2/3，默认1]：").strip()
        if choice in ("", "1"):
            return {"scheduleType": "daily"}
        if choice == "2":
            return {
                "scheduleType": "interval_days",
                "intervalDays": _input_positive_int("间隔天数 [默认1]：", 1),
            }
        if choice == "3":
            return {
                "scheduleType": "monthly_days",
                "monthlyDays": _input_month_days("日期（如 1,15,-1）："),
            }
        print("[-] 请输入 1、2 或 3。")


def should_redeem_today(config: dict, today: datetime.date) -> tuple[bool, str]:
    last = str(config.get("lastRedeemDate") or "")
    if last == today.isoformat():
        return False, "今天已经兑换过"
    schedule_type = str(config.get("scheduleType") or "daily")
    if schedule_type == "daily":
        return True, "每日兑换计划"
    if schedule_type == "interval_days":
        interval = max(1, int(config.get("intervalDays") or 1))
        if not last:
            return True, "间隔兑换首次执行"
        try:
            passed = (today - datetime.date.fromisoformat(last)).days
        except ValueError:
            return True, "上次日期异常，允许执行"
        return passed >= interval, f"已间隔 {passed}/{interval} 天"
    if schedule_type == "monthly_days":
        days = {int(day) for day in config.get("monthlyDays", [])}
        month_end = calendar.monthrange(today.year, today.month)[1]
        matched = today.day in days or (-1 in days and today.day == month_end)
        return matched, f"每月兑换日 {sorted(days)}"
    return False, "未知兑换计划"


def build_place_order_payload(
    product_id: int,
    desktop_id: int,
    product_type: str,
    cost_points: int,
    times: int,
) -> dict:
    return {
        "busiChannel": "010",
        "orderType": 1,
        "pointType": 1,
        "points": cost_points * times,
        "sku": [
            {
                "execSort": index + 1,
                "prodId": product_id,
                "prodType": product_type,
                "attrs": [{"attrKey": "bindDesktopId", "attrVal": desktop_id}],
            }
            for index in range(times)
        ],
    }


def configure_redeem(client: CtYunProtocolClient, config_path: Path) -> int:
    if not sys.stdin.isatty():
        print("[!] 兑换配置需要交互终端。")
        return 1
    desktops = client.get_desktops()
    rewards = client.get_rewards()
    if not desktops:
        print("[!] 当前账号没有云电脑。")
        return 1
    if not rewards:
        print("[!] 当前没有可兑换商品。")
        return 1

    if input("是否启用自动兑换？[Y/n]：").strip().lower() in {"n", "no"}:
        save_redeem_config(config_path, {"enabled": False})
        return 0
    print("\n云电脑：")
    for index, desktop in enumerate(desktops, 1):
        print(
            f"{index}. "
            f"{desktop.get('objName') or desktop.get('desktopName') or desktop.get('desktopCode') or '未知'}"
        )
    desktop = desktops[_input_index(len(desktops), "选择云电脑：")]
    desktop_id = desktop.get("desktopId") or desktop.get("objId")
    if desktop_id in (None, ""):
        print("[!] 所选云电脑缺少 desktopId，无法配置兑换。")
        return 1

    print("\n兑换商品：")
    for index, reward in enumerate(rewards, 1):
        print(f"{index}. {reward['prodName']}（{reward['costPoints']} 积分）")
    reward = rewards[_input_index(len(rewards), "选择商品：")]
    maximum = _input_non_negative_int("每次最多兑换次数 [0=按积分尽量兑换]：", 0)
    config = {
        "enabled": True,
        "desktopId": str(desktop_id),
        "prodId": reward["prodId"],
        "prodName": reward["prodName"],
        "prodType": reward["prodType"],
        "costPoints": reward["costPoints"],
        "maxRedeemTimes": maximum,
        "lastRedeemDate": "",
        **prompt_redeem_schedule(),
    }
    save_redeem_config(config_path, config)
    return 0


def auto_redeem(
    client: CtYunProtocolClient,
    config_path: Path,
    current_points: int,
    running_in_docker: bool,
) -> None:
    config = load_redeem_config(config_path)
    if not config:
        save_redeem_config(config_path, {"enabled": False})
        print("[*] 尚未配置自动兑换，已保持禁用。")
        return
    if not config.get("enabled"):
        print("[*] 自动兑换未启用。")
        return
    allowed, reason = should_redeem_today(config, datetime.date.today())
    if not allowed:
        print(f"[*] 不执行兑换：{reason}。")
        return
    try:
        cost = int(config["costPoints"])
        maximum = int(config.get("maxRedeemTimes") or 0)
        available = current_points // cost
        times = available if maximum == 0 else min(available, maximum)
        if times <= 0:
            print("[*] 当前积分不足以兑换配置商品。")
            return
        payload = build_place_order_payload(
            int(config["prodId"]), int(config["desktopId"]),
            str(config["prodType"]), cost, times,
        )
    except (KeyError, TypeError, ValueError, ZeroDivisionError):
        print("[!] 自动兑换配置格式错误。")
        return

    # 网络结果不明确时不自动重试，避免重复下单。
    result = client.place_order(payload)
    print(f"[*] 兑换成功：{config.get('prodName', '')} × {times}。")
    config["lastRedeemDate"] = datetime.date.today().isoformat()
    config["lastOrderResult"] = {
        "date": config["lastRedeemDate"], "code": result.get("code")
    }
    save_redeem_config(config_path, config)
    if running_in_docker:
        RESTART_AT_FILE.write_text(str(int(time.time()) + 120), encoding="utf-8")
        print("[*] 已安排 CtYun.dll 在两分钟后重启。")


def ensure_bound(client: CtYunProtocolClient) -> None:
    info = client.login_info
    if info is None or info.bonded_device:
        return
    if not sys.stdin.isatty():
        raise ProtocolError("设备尚未绑定，请先交互运行 CtYun.dll 完成短信验证")
    client.send_sms_code()
    code = input("短信验证码：").strip()
    if not code:
        raise ProtocolError("短信验证码不能为空")
    client.bind_device(code)
    print("[*] 设备绑定成功。")


def run_points_task(
    client: CtYunProtocolClient,
    config_path: Path,
    running_in_docker: bool,
) -> int:
    started = time.monotonic()
    last_progress: int | None = None
    print("[*] CtYun.dll 负责云电脑连接，开始查询积分进度。")
    while time.monotonic() - started <= HANG_SECONDS:
        try:
            task = client.get_usage_task()
            if task is None:
                raise ProtocolError("积分任务列表中没有“使用1小时”")
            progress = int(task.get("currentProgress") or 0)
            total = int(task.get("totalProgress") or 3600)
            status = int(task.get("status") or 0)
            if progress != last_progress:
                print(f"[*] 使用1小时进度：{progress}/{total}，状态={status}")
                last_progress = progress
            if progress >= total or status == 2:
                points = client.get_user_points()
                print(f"[*] 挂机积分任务完成，当前通用积分：{points}")
                auto_redeem(client, config_path, points, running_in_docker)
                return 0
        except ProtocolError as error:
            if error.code in (40010, 401, 403):
                print("[*] 登录状态失效，正在重新登录。")
                client.login()
                ensure_bound(client)
                save_web_auth_cache(client)
            else:
                raise
        time.sleep(POLL_SECONDS)
    print("[!] 等待80分钟后积分任务仍未完成。")
    return 1


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="天翼云电脑积分任务")
    parser.add_argument("--config-redeem", action="store_true", help="交互配置自动兑换")
    return parser.parse_args()


def save_web_auth_cache(client: CtYunProtocolClient) -> None:
    account_id = os.getenv("CTYUN_ACCOUNT_ID", "").strip()
    if not account_id:
        return
    try:
        from web.auth_cache import save_auth_cache

        save_auth_cache(int(account_id), client.export_login_info())
    except Exception as error:
        print(f"[!] 面板认证缓存更新失败，不影响挂机任务：{error}")


def main() -> int:
    args = parse_args()
    username = os.getenv("APP_USER", "").strip()
    password = os.getenv("APP_PASSWORD", "")
    device_code = os.getenv("DEVICECODE", "").strip()
    running_in_docker = os.getenv("RUNNING_IN_DOCKER") == "true"
    if not username or not password or not device_code:
        print("[!] 缺少 APP_USER、APP_PASSWORD 或 DEVICECODE。")
        return 1
    config_path = get_redeem_config_path(username, running_in_docker)
    client = CtYunProtocolClient(username, password, device_code)
    try:
        info = client.login()
        ensure_bound(client)
        save_web_auth_cache(client)
        print(f"[*] 云电脑登录成功：{info.user_name}")
        if args.config_redeem:
            return configure_redeem(client, config_path)
        return run_points_task(client, config_path, running_in_docker)
    except ProtocolError as error:
        print(f"[!] 云电脑积分任务失败：{error}")
        return 1
    except Exception as error:
        print(f"[!] 云电脑积分任务异常：{error}")
        return 1


if __name__ == "__main__":
    sys.exit(main())
