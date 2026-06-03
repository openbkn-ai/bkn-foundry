#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""数据库连接 - 适配 RDSConfig，去掉 admin_key/root_conn"""
import rdsdriver

from server.config.models import RDSConfig


class DatabaseConnection:
    def __init__(self, rds_config: RDSConfig):
        self.rds_config = rds_config

    def get_conn(self):
        """获取数据库连接"""
        db = rdsdriver.connect(
            host=self.rds_config.host,
            port=self.rds_config.port,
            user=self.rds_config.user,
            password=self.rds_config.password,
            connect_timeout=20,
            autocommit=True,
            charset="utf8mb4",
            cursorclass=rdsdriver.DictCursor,
        )
        return db
