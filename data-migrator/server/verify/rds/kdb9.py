#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""KDB9 校验实现 — 继承 KDB9Dialect (DB) + LintKDB9 (静态校验)"""
from logging import Logger

from server.config.models import CheckRulesConfig
from server.db.dialect.kdb9 import KDB9Dialect
from server.lint.rds.kdb9 import LintKDB9


class VerifyKDB9(KDB9Dialect, LintKDB9):
    """
    KDB9 完整校验类：
    - KDB9Dialect 提供 DB 连接、run_sql、add_column 等操作
    - LintKDB9 提供 check_init、check_update、check_column 等静态校验
    """

    def __init__(self, conn_config: dict, check_rules: CheckRulesConfig, logger: Logger):
        KDB9Dialect.__init__(self, conn_config, logger)
        self.check_rules = check_rules
