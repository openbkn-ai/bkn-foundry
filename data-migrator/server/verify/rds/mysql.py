#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""MySQL 校验实现 — 继承 MySQLDialect (DB) + LintMySQL (静态校验)"""
from logging import Logger

from server.config.models import CheckRulesConfig
from server.db.dialect.mysql import MySQLDialect
from server.lint.rds.mariadb import LintMariaDB


class VerifyMySQL(MySQLDialect, LintMariaDB):
    """
    MySQL 完整校验类：
    - MySQLDialect 提供 DB 连接、run_sql、add_column 等操作
    - LintMySQL 提供 check_init、check_update、check_column 等静态校验
    """

    def __init__(self, conn_config: dict, check_rules: CheckRulesConfig, logger: Logger):
        MySQLDialect.__init__(self, conn_config, logger)
        self.check_rules = check_rules
