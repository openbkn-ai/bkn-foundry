#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""JSON 升级文件执行器 - 委托给 RDSDialect 的幂等操作方法"""
import json
from logging import Logger

from server.db.dialect.base import RDSDialect


class JsonExecutor:
    def __init__(self, dialect: RDSDialect, logger: Logger):
        self.dialect = dialect
        self.logger = logger

    def execute(self, json_path: str):
        """执行一个 JSON 升级文件"""
        with open(json_path, "r", encoding="utf-8") as f:
            items = json.load(f)

        for item in items:
            db_name = item["db_name"]
            table_name = item["table_name"]
            obj_type = item["object_type"].upper()
            op = item["operation_type"].upper()
            name = item["object_name"]
            new_name = item.get("new_name", "")
            prop = item.get("object_property", "")
            comment = item.get("object_comment", "")

            self.logger.info(f"  JSON 操作: {op} {obj_type} {db_name}.{table_name}.{name}")

            if obj_type == "COLUMN":
                if op == "ADD":
                    self.dialect.add_column(db_name, table_name, name, prop, comment)
                elif op == "MODIFY":
                    self.dialect.modify_column(db_name, table_name, name, prop, comment)
                elif op == "RENAME":
                    if not new_name:
                        raise Exception(f"RENAME COLUMN 缺少 new_name: {db_name}.{table_name}.{name}")
                    self.dialect.rename_column(db_name, table_name, name, new_name, prop, comment)
                elif op == "DROP":
                    self.dialect.drop_column(db_name, table_name, name)
                else:
                    raise Exception(f"不支持的 operation_type '{op}' for COLUMN")

            elif obj_type in ("INDEX", "UNIQUE INDEX"):
                if op == "ADD":
                    self.dialect.add_index(db_name, table_name, obj_type, name, prop, comment)
                elif op == "RENAME":
                    if not new_name:
                        raise Exception(f"RENAME INDEX 缺少 new_name: {db_name}.{table_name}.{name}")
                    self.dialect.rename_index(db_name, table_name, name, new_name)
                elif op == "DROP":
                    self.dialect.drop_index(db_name, table_name, name)
                else:
                    raise Exception(f"不支持的 operation_type '{op}' for INDEX")

            elif obj_type == "CONSTRAINT":
                if op == "ADD":
                    self.dialect.add_constraint(db_name, table_name, name, prop)
                elif op == "RENAME":
                    if not new_name:
                        raise Exception(f"RENAME CONSTRAINT 缺少 new_name: {db_name}.{table_name}.{name}")
                    self.dialect.rename_constraint(db_name, table_name, name, new_name)
                elif op == "DROP":
                    self.dialect.drop_constraint(db_name, table_name, name)
                else:
                    raise Exception(f"不支持的 operation_type '{op}' for CONSTRAINT")

            elif obj_type == "TABLE":
                if op == "RENAME":
                    if not new_name:
                        raise Exception(f"RENAME TABLE 缺少 new_name: {db_name}.{table_name}")
                    self.dialect.rename_table(db_name, table_name, new_name)
                elif op == "DROP":
                    self.dialect.drop_table(db_name, table_name)
                else:
                    raise Exception(f"不支持的 operation_type '{op}' for TABLE")

            elif obj_type == "DB":
                if op == "DROP":
                    self.dialect.drop_db(db_name)
                else:
                    raise Exception(f"不支持的 operation_type '{op}' for DB")

            else:
                raise Exception(f"不支持的 object_type '{obj_type}': {item}")
