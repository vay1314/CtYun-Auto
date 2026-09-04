import argparse

from .accounts import import_environment_account, write_ctyun_accounts
from .db import init_db


def bootstrap() -> None:
    init_db()
    imported = import_environment_account()
    write_ctyun_accounts()
    if imported:
        print("[*] 已将 APP_USER 导入 Web 管理数据库。")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=["bootstrap"])
    args = parser.parse_args()
    if args.command == "bootstrap":
        bootstrap()


if __name__ == "__main__":
    main()
