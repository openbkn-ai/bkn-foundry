#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""DM8 校验实现 — 继承 DM8Dialect (DB) + LintDM8 (静态校验)"""
from logging import Logger

from server.config.models import CheckRulesConfig
from server.db.dialect.dm8 import DM8Dialect
from server.lint.rds.dm8 import LintDM8


class VerifyDM8(DM8Dialect, LintDM8):
    """
    DM8 完整校验类：
    - DM8Dialect 提供 DB 连接、run_sql、add_column 等操作
    - LintDM8 提供 check_init、check_update、check_column 等静态校验
    """

    def __init__(self, conn_config: dict, check_rules: CheckRulesConfig, logger: Logger):
        DM8Dialect.__init__(self, conn_config, logger)
        self.check_rules = check_rules
