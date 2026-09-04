"""
云电脑首页登录脚本。
"""

import argparse
import atexit  # 新增导入 atexit 模块
import calendar
import datetime
import hashlib
import json
import os
import sys
import threading
import time
from pathlib import Path
from typing import Dict, Optional

import ddddocr
import requests
from DrissionPage import ChromiumOptions, ChromiumPage

sys.stdout.reconfigure(encoding="utf-8")
sys.stderr.reconfigure(encoding="utf-8")

LOGIN_URL = "https://pc.ctyun.cn/#/login"
DESKTOP_URL = "https://pc.ctyun.cn/#/desktop-list"
DESKTOP_DETAIL_URL_KEY = "/desktop?id="
HANG_SECONDS = 80 * 60
REWARD_LIST_URL = (
    "https://desk.ctyun.cn/selforder/api/selforder/prod/get"
    "?prodId=17000000&prodCode=POINTS"
)
PLACE_ORDER_URL = "https://desk.ctyun.cn/selforder/api/selforder/paas/placeOrder"
POINTS_TASK_LIST_URL = (
    "https://desk.ctyun.cn/selforder/api/marketing/userPoints/getTaskList"
)
RESTART_AT_FILE = "/tmp/ctyun_restart_at"


def get_account_data_dir(username: str, running_in_docker: bool) -> str:
    """返回不暴露账号明文的账号专属持久化目录。"""
    if not running_in_docker:
        return "."
    account_key = hashlib.sha256(username.encode("utf-8")).hexdigest()[:16]
    path = f"/app/data/accounts/{account_key}"
    Path(path).mkdir(parents=True, exist_ok=True, mode=0o700)
    return path


def init_browser_options(running_in_docker: bool) -> ChromiumOptions:
    """初始化 Chromium 启动参数。"""
    options = ChromiumOptions()
    options.set_argument("--no-sandbox")
    options.set_argument("--disable-gpu")
    options.set_argument("--disable-dev-shm-usage")
    options.set_argument("--window-size=1920,1080")
    if running_in_docker:
        options.headless()
    return options


def get_auth_data_file(username: str, running_in_docker: bool) -> str:
    """构造账号专属 authData 文件路径。"""
    if running_in_docker:
        path = os.path.join(get_account_data_dir(username, True), "auth_data.json")
        legacy_path = f"/app/data/ctyun_authData_{username}_.json"
        if not os.path.exists(path) and os.path.exists(legacy_path):
            os.replace(legacy_path, path)
        return path
    return f"./ctyun_authData_{username}_.json"


def save_auth_data(page: ChromiumPage, file_path: str) -> None:
    """保存 localStorage.authData 到本地。"""
    try:
        Path(file_path).parent.mkdir(parents=True, exist_ok=True)
        auth_data = read_auth_data(page)
        if not auth_data:
            print(f"[-] 未获取到 authData，跳过保存: {file_path}")
            return
        with open(file_path, "w", encoding="utf-8") as f:
            json.dump(auth_data, f, ensure_ascii=False, indent=4)
        print("[*] authData 已保存")
    except Exception as e:
        print(f"[!] 保存 authData 失败: {e}")


def load_auth_data_from_file(file_path: str) -> dict:
    """从本地读取 authData。"""
    if not os.path.exists(file_path):
        return {}
    try:
        with open(file_path, "r", encoding="utf-8") as f:
            auth_data = json.load(f)
        if isinstance(auth_data, dict):
            return auth_data
        return {}
    except Exception as e:
        print(f"[!] 读取 authData 文件失败: {e}")
        return {}


def get_device_code(username: str, running_in_docker: bool) -> str:
    """读取或输入 web_device_code。"""
    env_device = os.getenv("DEVICECODE")
    if env_device:
        return env_device.strip()

    if running_in_docker:
        file_path = os.path.join(get_account_data_dir(username, True), "device_code")
        legacy_path = f"/app/data/.devicecode_{username}"
        if not os.path.exists(file_path) and os.path.exists(legacy_path):
            os.replace(legacy_path, file_path)
    else:
        file_path = f"./.devicecode_{username}"
    if os.path.exists(file_path):
        with open(file_path, "r", encoding="utf-8") as f:
            value = f.read().strip()
            if value:
                return value
    device_code = ""
    while not device_code:
        device_code = input("请输入 web_device_code: ").strip()
        if not device_code:
            print("[-] web_device_code 不能为空，请重新输入。")
    with open(file_path, "w", encoding="utf-8") as f:
        f.write(device_code)
    print(f"[*] web_device_code 已保存: {file_path}")
    return device_code


def set_web_device_code(page: ChromiumPage, device_code: str) -> None:
    """写入 localStorage.web_device_code。"""
    script = f"localStorage.setItem('web_device_code', {json.dumps(device_code)});"
    page.run_js(script)
    print("[*] 已写入 localStorage.web_device_code")


def get_auth_expired_at_ms(hours: int = 72) -> int:
    """生成 authExpiredAt 时间戳（毫秒）。"""
    return int((time.time() + hours * 3600) * 1000)


def inject_auth_data_if_exists(page: ChromiumPage, auth_data_file: str) -> None:
    """若本地 authData 文件存在则注入 localStorage.authData。"""
    auth_data = load_auth_data_from_file(auth_data_file)
    if not auth_data:
        return
    auth_expired_at = str(get_auth_expired_at_ms(hours=72))
    page.run_js(
        f"localStorage.setItem('authExpiredAt', {json.dumps(auth_expired_at)});"
    )
    auth_data_text = json.dumps(auth_data, ensure_ascii=False, separators=(",", ":"))
    page.run_js(f"localStorage.setItem('authData', {json.dumps(auth_data_text)});")
    print(f"[*] 已注入 localStorage.authData: {auth_data_file}")


def inject_local_storage_session(
    page: ChromiumPage, device_code: str, auth_data_file: str
) -> None:
    """注入 web_device_code、authData"""
    set_web_device_code(page, device_code)
    inject_auth_data_if_exists(page, auth_data_file)


def first_available(page: ChromiumPage, selectors: list[str], timeout: float = 2):
    """按顺序查找首个可用元素。"""
    for selector in selectors:
        ele = page.ele(selector, timeout=timeout)
        if ele:
            return ele
    return None


def fill_credentials(page: ChromiumPage, username: str, password: str) -> None:
    """填写账号与密码。"""
    account_input = first_available(
        page,
        [
            'css:input[placeholder*="手机号"]',
            'css:input[placeholder*="账号"]',
            'css:input[type="text"]',
        ],
        timeout=60,
    )
    password_input = first_available(
        page,
        [
            'css:input[placeholder*="密码"]',
            'css:input[type="password"]',
        ],
        timeout=10,
    )

    if not account_input or not password_input:
        raise RuntimeError("未找到账号或密码输入框。")

    account_input.clear()
    account_input.input(username)
    password_input.clear()
    password_input.input(password)


def fill_captcha_if_possible(page: ChromiumPage) -> bool:
    """识别并填写图形验证码。"""
    captcha_img = page.ele("css:img.code-img", timeout=2)
    captcha_input = first_available(
        page,
        [
            'css:input[placeholder*="请输入验证码"]',
            'xpath://input[contains(@placeholder,"请输入验证码")]',
        ],
        timeout=1,
    )

    if not captcha_img or not captcha_input:
        return False

    try:
        image_bytes = captcha_img.get_screenshot(as_bytes=True)
        captcha_code = get_bytes_numeric_captcha(image_bytes).strip()
        print(f"[*] 图形验证码识别结果: {captcha_code}")
        if not captcha_code:
            return False
        time.sleep(1)
        captcha_input.clear()
        captcha_input.input(captcha_code)
        print(f"[*] 已填写图形验证码: {captcha_code}")
        time.sleep(2)
        return True
    except Exception as e:
        print(f"[!] 图形验证码识别失败: {e}")
        return False


def click_login_button(page: ChromiumPage) -> None:
    """点击登录按钮。"""
    login_btn = page.ele("css:button.btn-submit-pc", timeout=5)
    if not login_btn:
        raise RuntimeError("未找到登录按钮 button.btn-submit-pc。")
    login_btn.click()


def get_latest_toast(page: ChromiumPage, timeout: float = 4) -> str:
    """读取最新 toast 文本。"""
    end_time = time.time() + timeout
    while time.time() < end_time:
        toast_eles = page.eles("css:.el-message__content")
        text_list = [
            ele.text.strip()
            for ele in toast_eles
            if ele and ele.text and ele.text.strip()
        ]
        if text_list:
            return text_list[-1]
        time.sleep(0.2)
    return ""


def refresh_captcha_image(page: ChromiumPage) -> None:
    """点击验证码图片以刷新验证码。"""
    captcha_img = page.ele("css:img.code-img", timeout=1)
    if captcha_img:
        captcha_img.click()


def read_auth_data(page: ChromiumPage) -> dict:
    """读取 localStorage.authData。"""
    raw_data = page.run_js("return localStorage.getItem('authData');")
    if not raw_data:
        return {}
    if isinstance(raw_data, dict):
        return raw_data
    if isinstance(raw_data, str):
        try:
            return json.loads(raw_data)
        except json.JSONDecodeError:
            return {}
    return {}


def is_login_success(page: ChromiumPage) -> bool:
    """判断是否已登录成功。"""
    if DESKTOP_URL in page.url or "/desktop-list" in page.url:
        return True
    if "/login" in page.url:
        return False
    time.sleep(1)
    auth_data = read_auth_data(page)
    return bool(auth_data.get("logined"))


def wait_desktop_list_refresh_done(page: ChromiumPage, timeout: int = 60) -> None:
    """等待 desktop-list 刷新动画结束。"""
    end_time = time.time() + timeout
    seen_loading = False
    while time.time() < end_time:
        current_url = page.url or ""
        date = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        print(f"\r[*] {date} 页面状态检测中...", end="")
        if "/login" in current_url or DESKTOP_DETAIL_URL_KEY in current_url:
            return

        loading_ele = page.ele("css:.rotate-animtion", timeout=0.3)
        if loading_ele:
            seen_loading = True
            time.sleep(0.2)
            continue

        if seen_loading:
            # 连续未检测到刷新动画，视为加载完成
            time.sleep(0.8)
            if not page.ele("css:.rotate-animtion", timeout=0.2):
                return

        # 刷新动画可能很快结束，出现关键元素时也可提前退出
        if page.ele("css:div.empty-desc", timeout=0.2):
            return
        if page.ele("css:div.desktopcom-enter", timeout=0.2):
            return
        time.sleep(1)
    print("\r[*] desktop-list 刷新超时。")


def get_desktop_state(page: ChromiumPage) -> str:
    """识别 desktop-list 的状态。"""
    current_url = page.url or ""
    if DESKTOP_DETAIL_URL_KEY in current_url:
        return "desktop_entered_auto"
    if "/login" in current_url:
        return "auth_expired"

    empty_desc = page.ele("css:div.empty-desc", timeout=0.5)
    if empty_desc:
        return "no_desktop"

    enter_buttons = page.eles("css:div.desktopcom-enter")
    has_cloud_pc = False
    has_cloud_phone = False
    for btn in enter_buttons:
        text = (btn.text or "").strip()
        if "进入AI云电脑" in text:
            has_cloud_pc = True
        if "进入AI云手机" in text:
            has_cloud_phone = True

    if has_cloud_pc:
        return "has_pc_button"
    if has_cloud_phone:
        return "only_phone"
    return "unknown"


def click_enter_ai_pc(page: ChromiumPage) -> bool:
    """点击“进入AI云电脑”按钮。"""
    enter_buttons = page.eles("css:div.desktopcom-enter")
    for btn in enter_buttons:
        text = (btn.text or "").strip()
        if "进入AI云电脑" in text:
            btn.click()
            return True
    return False


def wait_desktop_opened(page: ChromiumPage, timeout: int = 270) -> bool:
    """等待进入云电脑页面。"""
    end_time = time.time() + timeout
    while time.time() < end_time:
        current_url = page.url or ""
        if DESKTOP_DETAIL_URL_KEY in current_url:
            return True
        if "/login" in current_url:
            return False
        time.sleep(0.5)
    print("[*] 进入云电脑超时。")
    return False


def open_points_center_and_print(page: ChromiumPage, timeout: int = 60) -> int:
    """打开积分中心并输出积分详情。"""
    try:
        locator = "xpath://span[contains(string(), '积分中心')]"
        target_element = page.ele(locator, timeout=120)

        if not target_element:
            print("\r[-] 未找到积分中心入口。")
            return 0

        clicked = target_element.click(by_js=True)
        if not clicked:
            print("\r[-] 积分中心入口点击失败。")
            return 0
        time.sleep(5)
        end_time = time.time() + timeout
        while time.time() < end_time:
            if page.ele('css:iframe[src*="points.html"]', timeout=0.5):
                break
            time.sleep(0.3)

        iframe_ele = page.ele('css:iframe[src*="points.html"]', timeout=30)
        if not iframe_ele:
            print("\r[-] 未找到积分中心 iframe。")
            return 0

        frame = page.get_frame(iframe_ele)
        if not frame:
            print("\r[-] 无法切换到积分中心 iframe。")
            return 0

        time.sleep(5)
        general_points = ""
        root_element = None
        try:
            root_element = frame.ele("tag:div@class:points-list", timeout=60)
        except Exception:
            print("[*] 积分中心页面加载过久")

        if root_element:
            # @@ 表示同时满足多个属性匹配，定位同时拥有 flex 和 flex-column 类的 div 区块
            block_elements = root_element.eles("tag:div@@class:flex@@class:flex-column")

            for block in block_elements:
                title_element = block.ele("tag:p@class:text-title")
                desc_element = block.ele("tag:p@class:text-desc")

                # 安全提取文本内容，避免因元素不存在而引发 AttributeError
                value_text = title_element.text.strip() if title_element else ""
                name_text = desc_element.text.strip() if desc_element else ""

                # 执行匹配逻辑：精确匹配，或包含“通用积分”且排除“云智手机”
                if name_text == "通用积分" or (
                    "通用积分" in name_text and "云智手机" not in name_text
                ):
                    general_points = value_text
                    break

        if not general_points:
            print("\r[-] 未读取到通用积分。")
            return 0

        print(f"\r[*] 目前积分: {general_points}")
        return int(general_points)
    except Exception as e:
        print(f"[-] 无法获取积分中心数据：{e}")
        return 0


def wait_for_points_with_points(
    page: ChromiumPage,
    total_seconds: int = HANG_SECONDS,
    step: int = 10,
    running_in_docker: bool = False,
    config_redeem_only: bool = False,
) -> None:
    """进入云电脑后挂机等待积分，结束前打印积分详情。"""
    print("[*] 已进入云电脑")
    remaining = total_seconds
    # 超时时间
    max_time = 360
    refresh_retry_count_max = 13
    last_progress = 0
    packet_retry_count = 0
    refresh_retry_count = 0
    last_progress_update_time = time.time()
    redeem_config_checked = False
    url = "https://desk.ctyun.cn/selforder/api/marketing/userPoints/getTaskList"
    # 初始状态开启监听和界面
    page.listen.start(url)
    current_points = open_points_center_and_print(page)
    packet = page.listen.wait(timeout=20)
    while remaining > 0:
        # 开始挂机，获取积分中心数据，然后无限循环，并获取网络数据包判断是否完成挂机
        current_time_str = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        if not packet:
            packet_retry_count += 1
            print(
                f"\r[-] {current_time_str} 未捕获到积分数据包，正在重试 ({packet_retry_count}/6)"
            )
            time.sleep(10)

            if packet_retry_count >= 6:
                print(f"[-] {current_time_str} 连续 6 次未捕获到数据包，程序终止。")
                sys.exit(1)

            page.refresh()
            time.sleep(5)
            page.listen.start(url)
            current_points = open_points_center_and_print(page)
            packet = page.listen.wait(timeout=20)
            continue

        # 成功捕获到包，清零数据包重试计数器
        packet_retry_count = 0
        headers = packet.request.headers

        if not redeem_config_checked:
            config = ensure_redeem_config(
                page, clean_headers(headers), running_in_docker, config_redeem_only
            )
            redeem_config_checked = True
            enabled = config.get("enabled")

            if config_redeem_only:
                if not config or "enabled" not in config:
                    print("[!] 未完成兑换配置。")
                    sys.exit(1)
                print("[*] 兑换配置检查完成。")
                print("[*] 兑换配置流程结束。")
                sys.exit(0)

            if not enabled:
                print("[*] 自动兑换配置已禁用，继续执行挂机任务 。\n")
            else:
                print("[*] 已开启积分兑换，继续执行挂机任务 。\n")

        current_progress = fetch_current_progress(url, headers)

        if current_progress is not None and current_progress > 0:
            print(
                f"\r[-] {current_time_str} 挂机剩余 {60 - (current_progress // 60)} 分钟。",
                end="",
            )
            # 进度发生实际变化
            if current_progress != last_progress:
                print(
                    f"\r[-] {current_time_str} 进度更新，目前已挂机 {current_progress // 60} 分钟。"
                )
                last_progress = current_progress
                last_progress_update_time = time.time()
            # 3600 代表任务完成
            if current_progress >= 3600:
                print(f"\r[-] {current_time_str} 挂机任务完成。")
                refreshed_points = open_points_center_and_print(page)
                if refreshed_points > 0:
                    current_points = refreshed_points
                auto_redeem_reward_after_hang(
                    page, headers, running_in_docker, current_points
                )
                sys.exit(0)

        time_since_last_update = time.time() - last_progress_update_time

        if time_since_last_update >= max_time:
            refresh_retry_count += 1
            print(f"\n[-] {current_time_str} 刷新页面 ({refresh_retry_count}/13)")

            if refresh_retry_count >= refresh_retry_count_max:
                print(f"[-] {current_time_str} 刷新页面重试次数达到上限，程序终止。")
                sys.exit(1)

            # 刷新页面
            page.refresh()
            time.sleep(5)

            # 刷新页面后，必须重置最后更新时间戳，避免下一轮循环直接再次触发刷新
            last_progress_update_time = time.time()
            continue

        time.sleep(step)
        remaining -= step
    print("\r[*] 挂机等待完成。")


def fetch_current_progress(url: str, headers: Dict[str, str]) -> int:
    """
    向指定的 URL 发起 GET 请求，并直接解析提取 currentProgress 的值。
    Args:
        url (str): 接口的目标 URL。
        headers (Dict[str, str]): 请求头字典。

    Returns:
        Optional[Any]: 成功提取到进度值则返回该值；如果请求失败或数据不存在则返回 None。
    """
    try:
        headers = clean_headers(headers)

        response = requests.get(url, headers=headers, timeout=10)

        response.raise_for_status()

        data = response.json()
        task_list = data.get("data")
        if not isinstance(task_list, list):
            return 0

        for task in task_list:
            if task.get("taskDefName") == "使用1小时":
                return task.get("currentProgress")
        return 0

    except (requests.RequestException, ValueError) as error:
        current_time = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        print(f"[{current_time}] 获取或解析数据失败: {error}")
        return 0


def get_redeem_config_path(running_in_docker: bool) -> str:
    """兑换配置路径，按账号保存到持久化目录。"""
    if running_in_docker:
        username = os.getenv("APP_USER", "")
        return os.path.join(
            get_account_data_dir(username, True), "redeem_config.json"
        )
    return "./redeem_config.json"


def load_redeem_config(path: str) -> dict:
    if not os.path.exists(path):
        return {}
    try:
        with open(path, "r", encoding="utf-8") as f:
            data = json.load(f)
        return data if isinstance(data, dict) else {}
    except Exception as e:
        print(f"[!] 读取兑换配置失败: {e}")
        return {}


def save_redeem_config(path: str, config: dict) -> None:
    try:
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(config, f, ensure_ascii=False, indent=2)
        print(f"[*] 兑换配置已保存: {path}")
    except Exception as e:
        print(f"[!] 保存兑换配置失败: {e}")


def clean_headers(headers: Dict[str, str]) -> Dict[str, str]:
    clean = {}
    for k, v in dict(headers).items():
        if not str(k).startswith(":"):
            clean[str(k)] = v
    return clean


def parse_general_points(points_text: str) -> int:
    digits = "".join(ch for ch in str(points_text) if ch.isdigit())
    return int(digits) if digits else 0


def parse_desktops_from_session(page: ChromiumPage) -> list[dict]:
    raw = page.run_js("return sessionStorage.getItem('desktops');")
    if not raw:
        return []
    try:
        data = json.loads(raw) if isinstance(raw, str) else raw
        return data if isinstance(data, list) else []
    except Exception:
        return []


def fetch_redeemable_rewards(headers: Dict[str, str]) -> list[dict]:
    try:
        response = requests.get(REWARD_LIST_URL, headers=headers, timeout=15)
        response.raise_for_status()
        result = response.json()
    except Exception as e:
        print(f"[!] 获取兑换奖励列表失败: {e}")
        return []

    if result.get("code") == 40010:
        print("[!] 当前登录信息已过期，请重新登录后再试兑换。")
        return []

    rewards: list[dict] = []
    for mall in result.get("data", []):
        for series in mall.get("series", []):
            if series.get("expireDate") is not None:
                continue
            for sku in series.get("sku", []):
                if sku.get("expireDate") is not None:
                    continue
                prod_id = int(sku.get("prodId", 0))
                prod_name = str(sku.get("prodName", "")).strip()
                cost_points = int(sku.get("costPoints", 0))
                description = str(sku.get("description", "")).strip()
                prod_type = str(sku.get("prodType", "")).strip()
                rewards.append(
                    {
                        "prodId": prod_id,
                        "prodName": prod_name,
                        "costPoints": cost_points,
                        "description": description,
                        "prodType": prod_type,
                    }
                )
    return rewards


def _input_index(max_index: int, prompt: str) -> int:
    while True:
        raw = input(prompt).strip()
        if raw.isdigit():
            idx = int(raw)
            if 1 <= idx <= max_index:
                return idx - 1
        print(f"[-] 请输入 1 到 {max_index} 的数字。")


def _input_non_negative_int(prompt: str, default_value: int) -> int:
    while True:
        raw = input(prompt).strip()
        if raw == "":
            return default_value
        if raw.isdigit():
            return int(raw)
        print("[-] 请输入大于等于 0 的整数。")


def _input_positive_int(prompt: str, default_value: int) -> int:
    while True:
        raw = input(prompt).strip()
        if raw == "":
            return default_value
        if raw.isdigit() and int(raw) > 0:
            return int(raw)
        print("[-] 请输入大于 0 的整数。")


def _input_month_days(prompt: str) -> list[int]:
    while True:
        raw = input(prompt).strip()
        if raw == "":
            print("[-] 不能为空，请输入如 1,15,28 或 -1")
            continue
        values = []
        valid = True
        for item in raw.split(","):
            item = item.strip()
            if item == "-1":
                values.append(-1)
                continue
            if not item.isdigit():
                valid = False
                break
            day = int(item)
            if day < 1 or day > 31:
                valid = False
                break
            values.append(day)
        if not valid or not values:
            print("[-] 格式错误，请输入 1-31 或 -1，并用逗号分隔，如 1,15,28,-1")
            continue
        return sorted(set(values))


def prompt_redeem_schedule() -> dict:
    print("\n兑换时间设置：")
    print("1. 每日兑换（默认）")
    print("2. 每隔N日兑换")
    print("3. 每月几号兑换（逗号分隔，支持 -1 表示月末最后一天）")
    while True:
        choice = input("请选择时间策略 [1/2/3，默认1]: ").strip()
        if choice in ("", "1"):
            return {"scheduleType": "daily"}
        if choice == "2":
            interval_days = _input_positive_int("请输入间隔天数N [默认1]: ", 1)
            return {"scheduleType": "interval_days", "intervalDays": interval_days}
        if choice == "3":
            month_days = _input_month_days("请输入每月兑换日期（如 1,15,28,-1）: ")
            return {"scheduleType": "monthly_days", "monthlyDays": month_days}
        print("[-] 请输入 1、2 或 3。")


def should_redeem_today(config: dict, today: datetime.date) -> tuple[bool, str]:
    schedule_type = str(config.get("scheduleType") or "daily").strip()
    last_redeem_date = str(config.get("lastRedeemDate") or "").strip()
    today_str = today.isoformat()

    if last_redeem_date == today_str:
        return False, f"今天({today_str})已兑换过，跳过。"

    if schedule_type == "daily":
        return True, "每日兑换策略，允许执行。"

    if schedule_type == "interval_days":
        interval_days = int(config.get("intervalDays", 1) or 1)
        if interval_days < 1:
            interval_days = 1
        if not last_redeem_date:
            return True, "间隔兑换策略首次执行。"
        try:
            last_day = datetime.date.fromisoformat(last_redeem_date)
        except ValueError:
            return True, "上次兑换日期格式异常，允许执行。"
        passed_days = (today - last_day).days
        if passed_days >= interval_days:
            return True, f"已间隔 {passed_days} 天，满足每隔 {interval_days} 天兑换。"
        return False, f"距上次仅 {passed_days} 天，未到每隔 {interval_days} 天。"

    if schedule_type == "monthly_days":
        month_days = config.get("monthlyDays", [])
        if not isinstance(month_days, list):
            return False, "每月兑换日期配置错误，跳过。"
        try:
            allow_month_end = False
            allowed_days = set()
            for day in month_days:
                day_num = int(day)
                if day_num == -1:
                    allow_month_end = True
                elif 1 <= day_num <= 31:
                    allowed_days.add(day_num)
        except Exception:
            return False, "每月兑换日期配置错误，跳过。"
        allowed_days = sorted(allowed_days)
        if not allowed_days and not allow_month_end:
            return False, "每月兑换日期为空，跳过。"
        last_day = calendar.monthrange(today.year, today.month)[1]
        if allow_month_end and today.day == last_day:
            return True, f"今天是 {today.day} 号（本月最后一天），命中每月兑换日。"
        if today.day in allowed_days:
            return True, f"今天是 {today.day} 号，命中每月兑换日。"
        display_days = [*allowed_days]
        if allow_month_end:
            display_days.append(-1)
        return False, f"今天是 {today.day} 号，不在每月兑换日 {display_days} 中。"

    return True, "未知策略，按每日策略执行。"


def prompt_and_create_redeem_config(
    page: ChromiumPage, headers: Dict[str, str], running_in_docker: bool
) -> dict:
    if not sys.stdin.isatty():
        print("[*] 检测到后台启动模式，跳过兑换配置交互。")
        print(
            "[*] 如需配置兑换，请在容器内手动执行: python3 /app/pc_login.py --config-redeem"
        )
        return {}

    while True:
        print("\n\n=== 自动兑换配置 ===")
        enable_input = input("是否启用自动兑换奖励? [Y/n]: ").strip().lower()
        if enable_input in ("", "y", "yes"):
            enable_redeem = True
            break
        if enable_input in ("n", "no"):
            enable_redeem = False
            break
        print("[-] 请输入 y 或 n。")

    if not enable_redeem:
        disabled_config = {"enabled": False}
        save_redeem_config(get_redeem_config_path(running_in_docker), disabled_config)
        print("[*] 已设置为不启用自动兑换。")
        return disabled_config

    desktops = parse_desktops_from_session(page)
    if not desktops:
        print("[!] sessionStorage 未读取到 desktops，无法配置自动兑换。")
        return {}

    rewards = fetch_redeemable_rewards(headers)
    if not rewards:
        print("[!] 未获取到可兑换奖励，无法配置自动兑换。")
        return {}

    print("\n=== 自动兑换配置（首次）===")
    print("可选设备：")
    for idx, item in enumerate(desktops, start=1):
        print(
            f"{idx}. {item.get('objName', '未知设备')} "
            f"(desktopId={item.get('desktopId', '')})"
        )
    desktop_idx = _input_index(len(desktops), "请选择设备序号: ")
    desktop = desktops[desktop_idx]

    print("\n可选奖励：")
    for idx, reward in enumerate(rewards, start=1):
        print(
            f"{idx}. {reward['prodName']} {reward['costPoints']}积分 (prodId={reward['prodId']}, "
            f"prodType={reward['prodType']})"
        )
    reward_idx = _input_index(len(rewards), "请选择奖励序号: ")
    reward = rewards[reward_idx]

    print("\n兑换次数设置：")
    max_redeem_times = _input_non_negative_int(
        "每次挂机完成最多兑换次数 [默认0=按积分尽量兑换]: ", 0
    )
    schedule_config = prompt_redeem_schedule()

    config = {
        "enabled": True,
        "desktopId": str(desktop.get("desktopId", "")).strip(),
        "prodId": int(reward["prodId"]),
        "prodName": reward["prodName"],
        "prodType": reward["prodType"],
        "costPoints": int(reward["costPoints"]),
        "maxRedeemTimes": int(max_redeem_times),
        "lastRedeemDate": "",
    }
    config.update(schedule_config)
    save_redeem_config(get_redeem_config_path(running_in_docker), config)
    return config


def ensure_redeem_config(
    page: ChromiumPage,
    headers: Dict[str, str],
    running_in_docker: bool,
    config_redeem_only: bool = False,
) -> dict:
    path = get_redeem_config_path(running_in_docker)
    config = load_redeem_config(path)
    if config_redeem_only and config:
        file_path = Path(path)
        file_path.unlink()
        return prompt_and_create_redeem_config(page, headers, running_in_docker)
    if config and "enabled" in config:
        return config
    return prompt_and_create_redeem_config(page, headers, running_in_docker)


def build_place_order_payload(
    sku_prod_id: int, desktop_id: int, prod_type: str, cost_points: int, times: int
) -> dict:
    return {
        "busiChannel": "010",
        "orderType": 1,
        "pointType": 1,
        "points": int(cost_points) * int(times),
        "sku": [
            {
                "execSort": idx + 1,
                "prodId": int(sku_prod_id),
                "prodType": prod_type,
                "attrs": [{"attrKey": "bindDesktopId", "attrVal": int(desktop_id)}],
            }
            for idx in range(times)
        ],
    }


def try_redeem_reward_once(
    headers: Dict[str, str], payload: dict, times: int, cost_points: int
) -> bool:
    try:
        response = requests.post(
            PLACE_ORDER_URL, json=payload, headers=headers, timeout=20
        )
        data = response.json()
    except Exception as e:
        print(f"[!] 兑换请求失败: {e}")
        return False

    code = data.get("code")
    if code == 0:
        print(f"[*] 兑换成功：本次兑换 {times} 次，共消耗 {times * cost_points} 积分。")
        return True
    if code == 40010:
        print("[!] 兑换失败：当前登录信息已过期，请重新登录。")
        return False
    if code == 30010:
        print("[!] 兑换失败：资源施工中，请稍后再试。")
        return False
    print(f"[!] 兑换失败：code={code}, msg={data.get('msg', '未知错误')}")
    return False


def auto_redeem_reward_after_hang(
    page: ChromiumPage,
    request_headers: Dict[str, str],
    running_in_docker: bool,
    current_points: int,
) -> None:
    headers = clean_headers(request_headers)
    config_path = get_redeem_config_path(running_in_docker)
    config = ensure_redeem_config(page, headers, running_in_docker)
    if not config:
        return

    try:
        enabled = config.get("enabled")
        if not enabled:
            print("[*] 自动兑换配置已禁用，跳过。")
            return
        desktop_id = int(str(config.get("desktopId", "")).strip())
        prod_id = int(config.get("prodId"))
        prod_type = str(config.get("prodType", "")).strip()
        cost_points = int(config.get("costPoints", 0))
        max_redeem_times = int(config.get("maxRedeemTimes", 1))
        prod_name = str(config.get("prodName", "")).strip()
    except Exception:
        print("[!] 自动兑换配置格式错误，跳过。")
        return

    if cost_points <= 0:
        print("[!] 配置中的 costPoints 异常，跳过兑换。")
        return

    today = datetime.date.today()
    can_redeem_today, reason = should_redeem_today(config, today)
    if not can_redeem_today:
        print(f"[*] 兑换计划未执行：{reason}")
        return
    print(f"[*] 兑换计划命中：{reason}")

    if current_points > 0:
        can_redeem_times = current_points // cost_points
        if can_redeem_times <= 0:
            print(
                f"[*] 当前积分 {current_points} 小于单次兑换所需 {cost_points}，本次不兑换。"
            )
            return
        final_times = (
            can_redeem_times
            if max_redeem_times == 0
            else min(can_redeem_times, max_redeem_times)
        )
    else:
        final_times = 1 if max_redeem_times == 0 else max_redeem_times
        print("[!] 当前积分读取失败，按配置次数降级尝试兑换。")

    print(
        f"[*] 准备自动兑换({prod_name})：prodId={prod_id}, desktopId={desktop_id}, "
        f"prodType={prod_type}, costPoints={cost_points}, 尝试={final_times}次。"
    )
    restart_at = int(time.time()) + 120
    attempt_times = final_times
    while attempt_times > 0:
        payload = build_place_order_payload(
            prod_id, desktop_id, prod_type, cost_points, attempt_times
        )
        if try_redeem_reward_once(headers, payload, attempt_times, cost_points):
            config["lastRedeemDate"] = today.isoformat()
            save_redeem_config(config_path, config)
            if running_in_docker:
                try:
                    with open(RESTART_AT_FILE, "w", encoding="utf-8") as f:
                        f.write(str(restart_at))
                    print("[*] 已设置 CtYun.dll 在 2 分钟后自动重启。")
                except Exception as e:
                    print(f"[!] 写入重启计划失败: {e}")
            return
        attempt_times -= 1

    print("[!] 自动兑换未成功，可稍后重试。")


def execute_login(
    page: ChromiumPage,
    username: str,
    password: str,
    max_retries: int = 6,
) -> bool:
    """执行云电脑登录流程。"""
    # 最大重试次数设置
    for attempt_ in range(1, max_retries + 1):
        try:
            page.get(LOGIN_URL)

            # 等待页面开始加载，如果 30 秒未响应，DrissionPage 可能会抛出异常或后续操作失败
            is_loaded = page.wait.doc_loaded(timeout=30)

            if not is_loaded:
                raise TimeoutError("[*] 等待页面加载响应超时 (30秒)")

            # 填写账号密码
            fill_credentials(page, username, password)
            break

        except Exception:
            # save_screenshot(page)
            if attempt_ < max_retries:
                print("[*] 等待 3 秒后进行下一次进入...")
                time.sleep(3)
            else:
                print(f"[-] 已达到最大重试次数 ({max_retries} 次)，网页加载失败。")
                sys.exit(1)

    for attempt in range(1, max_retries + 1):
        try:
            print(f"[*] 登录尝试 {attempt}/{max_retries}")
            if is_login_success(page):
                return True

            fill_captcha_if_possible(page)
            click_login_button(page)

            toast_text = get_latest_toast(page, timeout=20)
            if toast_text:
                print(f"[*] 登录提示: {toast_text}")

            if "用户名或密码错误" in toast_text:
                return False
            if "图形验证码错误" in toast_text:
                refresh_captcha_image(page)
                continue
            if "请输入图形验证码" in toast_text:
                continue

            if is_login_success(page):
                return True
        except Exception:
            if attempt < max_retries:
                print("[*] 等待 3 秒后进行下一次重试登录...")
                time.sleep(3)
            else:
                print(f"[-] 已达到最大重试次数 ({max_retries} 次)")
                sys.exit(1)

    return False


class NumericOcrSolver:
    """单例数字 OCR 识别器。"""

    _instance: Optional["NumericOcrSolver"] = None
    _lock = threading.Lock()

    def __new__(cls) -> "NumericOcrSolver":
        with cls._lock:
            if cls._instance is None:
                cls._instance = super().__new__(cls)
                cls._instance._init_engine()
        return cls._instance

    def _init_engine(self) -> None:
        self.ocr = ddddocr.DdddOcr(show_ad=False)
        self.ocr.set_ranges(0)

    def solve(self, image_data: bytes) -> str:
        try:
            return self.ocr.classification(image_data)
        except Exception:
            return ""


def get_bytes_numeric_captcha(image_bytes: bytes) -> str:
    solver = NumericOcrSolver()
    return solver.solve(image_bytes)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="天翼云电脑挂机与积分兑换脚本")
    parser.add_argument(
        "--config-redeem",
        action="store_true",
        help="仅执行兑换配置交互，不进入挂机流程",
    )
    return parser.parse_args()


def main(config_redeem_only: bool = False) -> None:
    username = os.getenv("APP_USER")
    password = os.getenv("APP_PASSWORD")
    running_in_docker = os.getenv("RUNNING_IN_DOCKER") == "true"

    if not username or not password:
        print("[!] 缺少环境变量 APP_USER 或 APP_PASSWORD。")
        sys.exit(1)

    auth_data_file = get_auth_data_file(username, running_in_docker)
    device_code = get_device_code(username, running_in_docker)

    options = init_browser_options(running_in_docker)
    page = ChromiumPage(addr_or_opts=options)
    atexit.register(page.quit)

    try:
        # 先注入本地会话，再进入 desktop-list 判断状态
        page.get(LOGIN_URL)
        inject_local_storage_session(page, device_code, auth_data_file)
        page.refresh()
        time.sleep(2)

        relogin_attempts = 0
        max_relogin_attempts = 3
        unknown_attempts = 0
        desktop_opened = False

        while True:
            page.get(DESKTOP_URL)
            time.sleep(1)
            wait_desktop_list_refresh_done(page, timeout=60)
            state = get_desktop_state(page)
            print(f"\r[*] desktop-list 状态: {state}")

            if state == "auth_expired" or state == "unknown":
                if relogin_attempts >= max_relogin_attempts:
                    print("[!] 重登次数已达上限。")
                    sys.exit(1)
                relogin_attempts += 1
                print(
                    f"[*] 检测到未登录或登录态过期，开始账号密码重登 ({relogin_attempts}/{max_relogin_attempts})"
                )
                if not execute_login(page, username, password):
                    print("[!] 重新登录失败。")
                    sys.exit(1)
                save_auth_data(page, auth_data_file)
                continue

            if state == "no_desktop":
                print("[*] 当前账号无云电脑资源，任务结束。")
                sys.exit(0)

            if state == "only_phone":
                print("[*] 当前账号仅有云手机资源，任务结束。")
                sys.exit(0)

            if state == "has_pc_button":
                print("[*] 检测到“进入AI云电脑”按钮，准备进入云电脑。")
                if not click_enter_ai_pc(page):
                    print("[!] 未能点击“进入AI云电脑”按钮。")
                    continue
                if not wait_desktop_opened(page, timeout=240):
                    print("[!] 点击后未进入云电脑页面。")
                    continue

                desktop_opened = True
                break

            if state == "desktop_entered_auto":
                print("[*] 已自动进入云电脑页面。")
                desktop_opened = True
                break

            unknown_attempts += 1
            if unknown_attempts >= 3:
                print("[!] 无法识别 desktop-list 页面状态，任务结束。")
                sys.exit(1)
            print(f"[-] 未识别到明确状态，重试中 ({unknown_attempts}/3)")
            time.sleep(2)

        if not desktop_opened:
            print("[!] 未进入云电脑页面。")
            sys.exit(1)

        auth_data = read_auth_data(page)
        mobile = auth_data.get("mobilephone") if auth_data else None
        if mobile:
            print(f"[*] 登录成功账号: {mobile}")
        else:
            print("[-] 登录成功，但未能读取 authData.mobilephone。")

        wait_for_points_with_points(
            page,
            HANG_SECONDS,
            running_in_docker=running_in_docker,
            config_redeem_only=config_redeem_only,
        )
        page.quit()

    except Exception as e:
        # save_screenshot(page)
        print(f"[!] 执行异常: {e}")
        sys.exit(1)


def save_screenshot(page: ChromiumPage) -> None:
    file_name = f"{os.getenv('APP_USER')}_{datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')}"
    if os.getenv("RUNNING_IN_DOCKER") == "true":
        path = "/app/data"
    else:
        path = "./"
    page.get_screenshot(path=path, name=file_name, full_page=True)


if __name__ == "__main__":
    args = parse_args()
    print("[*] 开始进行云电脑挂机")
    main(config_redeem_only=args.config_redeem)
