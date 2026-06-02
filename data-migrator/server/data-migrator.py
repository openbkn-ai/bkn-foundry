#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""
Data Migrator - 统一入口
支持四个子命令: fetch / lint / verify / migrate
"""
import argparse
import sys
import os

# 将 server/ 所在目录加入 sys.path，以支持 from server.xxx import
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="data-migrator",
        description="数据库迁移引擎：fetch / lint / verify / migrate",
    )
    subparsers = parser.add_subparsers(dest="command", help="子命令")

    # ── fetch ──
    fetch_parser = subparsers.add_parser("fetch", help="从 Git 仓库拉取并收集迁移脚本")
    fetch_parser.add_argument("--config", required=True, help="YAML 配置文件路径")
    fetch_parser.add_argument("--service", nargs="*", default=None, help="指定本次拉取的服务（默认全部）")
    fetch_parser.add_argument("--log-level", default="INFO", help="日志级别")

    # ── lint ──
    lint_parser = subparsers.add_parser("lint", help="静态校验：目录结构 + SQL 语法（无需 DB 连接）")
    lint_parser.add_argument("--config", required=True, help="YAML 配置文件路径")
    lint_parser.add_argument("--service", nargs="*", default=None, help="指定本次校验的服务（默认全部）")
    lint_parser.add_argument("--log-level", default="INFO", help="日志级别")

    # ── verify ──
    verify_parser = subparsers.add_parser("verify", help="执行校验：连接测试 DB，运行 SQL + schema 对比")
    verify_parser.add_argument("--config", required=True, help="YAML 配置文件路径")
    verify_parser.add_argument("--verify-rds-config", default=None, dest="verify_rds_config", help="多 DB 对比连接配置文件路径")
    verify_parser.add_argument("--service", nargs="*", default=None, help="指定本次校验的服务（默认全部）")
    verify_parser.add_argument("--log-level", default="INFO", help="日志级别")

    # ── migrate ──
    migrate_parser = subparsers.add_parser("migrate", help="执行数据库初始化和升级迁移")
    migrate_parser.add_argument("--config", required=True, help="YAML 配置文件路径")
    migrate_parser.add_argument("--secret-config", default=None, dest="secret_config", help="依赖服务连接配置文件路径")
    migrate_parser.add_argument("--service", nargs="*", default=None, help="指定本次迁移的服务（默认全部）")
    migrate_parser.add_argument("--log-level", default="INFO", help="日志级别")

    return parser


def main():
    parser = build_parser()
    args = parser.parse_args()

    if args.command is None:
        parser.print_help()
        sys.exit(1)

    from server.utils.log import LogDiy
    logger = LogDiy.instance().get_logger(args.log_level)

    if args.command == "fetch":
        from server.config.loader import load_config
        app_config = load_config(args.config, args.service, logger)

        from server.fetch.executor import FetchExecutor
        executor = FetchExecutor(app_config, logger)
        executor.run()

    elif args.command == "lint":
        from server.config.loader import load_config
        app_config = load_config(args.config, args.service, logger)

        from server.lint.executor import LintExecutor
        executor = LintExecutor(app_config, logger)
        executor.run()

    elif args.command == "verify":
        from server.config.loader import load_config
        app_config = load_config(args.config, args.service, logger)

        from server.verify.executor import VerifyExecutor
        executor = VerifyExecutor(app_config, logger, args.verify_rds_config)
        executor.run()

    elif args.command == "migrate":
        from server.config.loader import load_config
        app_config = load_config(args.config, args.service, logger, args.secret_config)

        from server.migrate.executor import MigrationExecutor
        executor = MigrationExecutor(app_config, logger)
        executor.run()


if __name__ == "__main__":
    main()
