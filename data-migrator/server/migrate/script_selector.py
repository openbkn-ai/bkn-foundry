#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
"""脚本选择器 - 版本扫描、init.sql 查找、脚本排序

新目录结构：脚本直接放在 <version>/ 下，无 pre/ 子目录。
"""
import os
import re
from logging import Logger
from typing import List, Optional, Tuple

from server.config.models import AppConfig, DEFAULT_DB_TYPE_FALLBACK
from server.utils.version import (
    compare_version, get_max_version, sort_versions, extract_number, is_version_dir
)


class ScriptSelector:
    def __init__(self, app_config: AppConfig, logger: Logger):
        self.app_config = app_config
        self.logger = logger

    def get_service_db_type_path(self, service_name: str) -> str:
        """获取 <script_dir>/<service>/<db_type>/ 路径，目录不存在时 fallback 到 mariadb"""
        path = os.path.join(
            self.app_config.repo_path,
            service_name,
            self.app_config.rds.type.lower(),
        )
        if not os.path.isdir(path):
            fallback = os.path.join(self.app_config.repo_path, service_name, DEFAULT_DB_TYPE_FALLBACK)
            self.logger.warning(
                f"目录 {path} 不存在，fallback 到 mariadb 目录: {fallback}"
            )
            return fallback
        return path

    def get_all_versions(self, service_name: str) -> List[str]:
        """获取服务下所有版本目录"""
        db_type_path = self.get_service_db_type_path(service_name)
        if not os.path.isdir(db_type_path):
            return []
        return [d for d in os.listdir(db_type_path)
                if os.path.isdir(os.path.join(db_type_path, d)) and is_version_dir(d)]

    def get_max_version(self, service_name: str) -> Optional[str]:
        """获取服务的最大版本号"""
        versions = self.get_all_versions(service_name)
        return get_max_version(versions)

    def find_init_sql(self, service_name: str) -> Tuple[Optional[str], Optional[str]]:
        """取最大版本下的 init.sql（lint 保证每个版本均存在），返回 (path, version)"""
        max_version = self.get_max_version(service_name)
        if not max_version:
            return None, None
        db_type_path = self.get_service_db_type_path(service_name)
        init_path = os.path.join(db_type_path, max_version, "init.sql")
        if not os.path.isfile(init_path):
            return None, None
        return init_path, max_version

    def select_upgrade_scripts(self, service_name: str, installed_version: str,
                               last_script: str = "") -> Tuple[List[Tuple[str, List[str]]], str, bool]:
        """
        选择 installed_version 之后待执行的升级脚本。

        last_script 是 task 表的断点（"<version>/<filename>"）。断点是已装版本下带编号的脚本时，
        该版本中编号更大的脚本也会选出：开发版的同一版本目录会陆续追加脚本，已升到该版本的环境
        否则永远不会执行它们。断点是 init.sql 或属于其他版本时，已装版本整体视为已完成。

        返回: (upgrade_files_list, max_version, has_scripts)
          - upgrade_files_list: [(version, [scripts]), ...]
          - max_version: 最大版本号
          - has_scripts: 是否有脚本需要执行
        """
        versions = sort_versions(self.get_all_versions(service_name))
        if not versions:
            return [], "", False

        max_version = versions[-1]
        db_type_path = self.get_service_db_type_path(service_name)
        resume_after = self._resume_number(installed_version, last_script)

        upgrade_files_list = []

        for version in versions:
            cmp = compare_version(version, installed_version)
            if cmp < 0 or (cmp == 0 and resume_after is None):
                continue

            version_path = os.path.join(db_type_path, version)
            scripts = self._collect_scripts_from_dir(version_path)
            if cmp == 0:
                scripts = [s for s in scripts if extract_number(s) > resume_after]

            if scripts:
                upgrade_files_list.append((version, scripts))

        has_scripts = len(upgrade_files_list) > 0
        return upgrade_files_list, max_version, has_scripts

    @staticmethod
    def _resume_number(installed_version: str, last_script: str) -> Optional[int]:
        """断点是已装版本下带编号的脚本时返回其编号，否则返回 None。"""
        version, _, filename = (last_script or "").partition("/")
        if version != installed_version or not re.match(r"^\d+-.*\.(sql|py|json)$", filename):
            return None
        return extract_number(filename)

    def find_latest_script(self, service_name: str, version: str) -> Optional[str]:
        """返回版本目录下编号最大的升级脚本文件名，没有时返回 None。

        init.sql 是该版本当时全部脚本的快照，首次安装以此作为断点。
        """
        version_path = os.path.join(self.get_service_db_type_path(service_name), version)
        scripts = self._collect_scripts_from_dir(version_path)
        return os.path.basename(scripts[-1]) if scripts else None

    def _collect_scripts_from_dir(self, version_path: str) -> List[str]:
        """
        收集版本目录下的升级脚本（NN-xxx.sql/py，跳过 init.sql）。
        .json 文件输出警告并跳过。
        """
        if not os.path.isdir(version_path):
            return []

        scripts = []
        for filename in os.listdir(version_path):
            filepath = os.path.join(version_path, filename)
            if not os.path.isfile(filepath):
                continue

            # 跳过 init.sql
            if filename == "init.sql":
                continue

            # 匹配 NN-xxx.sql、NN-xxx.py 或 NN-xxx.json
            if re.match(r"^\d+-.*\.(sql|py|json)$", filename):
                scripts.append(filepath)

        if scripts:
            scripts.sort(key=extract_number)
        return scripts
