#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""版本号工具 - 合并 tools/utils/version.py + server-old/src/utils/util.py"""
import re
from typing import List, Optional


def is_version_dir(version_str: str) -> bool:
    """判断目录名是否为合法版本号"""
    try:
        [int(n) for n in version_str.split(".")]
        return True
    except (ValueError, AttributeError):
        return False


def compare_version(v1: str, v2: str) -> int:
    """
    比较两个标准语义化版本号，返回 -1/0/1。
    仅支持全数字格式: 1.0.0, 1.4.20.1
    """
    try:
        arr1 = [int(n) for n in v1.split(".")]
        arr2 = [int(n) for n in v2.split(".")]
    except ValueError as ex:
        raise Exception(f"版本号必须为全数字的语义化版本 (如 1.0.0), 解析失败: v1={v1}, v2={v2}, 详情: {ex}")

    max_len = max(len(arr1), len(arr2))
    arr1 += [0] * (max_len - len(arr1))
    arr2 += [0] * (max_len - len(arr2))
    for a, b in zip(arr1, arr2):
        if a > b:
            return 1
        elif a < b:
            return -1
    return 0


def get_max_version(versions: List[str]) -> Optional[str]:
    """返回列表中的最大版本号"""
    if not versions:
        return None
    max_v = versions[0]
    for v in versions[1:]:
        if compare_version(v, max_v) > 0:
            max_v = v
    return max_v


def get_min_version(versions: List[str]) -> Optional[str]:
    """返回列表中的最小版本号"""
    if not versions:
        return None
    min_v = versions[0]
    for v in versions[1:]:
        if compare_version(v, min_v) < 0:
            min_v = v
    return min_v


def sort_versions(versions: List[str]) -> List[str]:
    """对版本号列表排序（升序）"""
    arr = list(versions)
    for i in range(1, len(arr)):
        key = arr[i]
        j = i - 1
        while j >= 0 and compare_version(arr[j], key) > 0:
            arr[j + 1] = arr[j]
            j -= 1
        arr[j + 1] = key
    return arr


def extract_number(file_path: str) -> int:
    """从文件名中提取序号，如 01-xxx.sql -> 1"""
    s = file_path.split("/")[-1]
    match = re.search(r"^(\d+)-.*\.(sql|py|json)$", s)
    if match:
        return int(match.group(1))
    raise Exception(f"The upgrade file name must match NN-xxx.sql|py|json, filename: {s}")


class VersionUtil:
    """版本号对象，支持比较和排序"""

    def __init__(self, version_str: str):
        self.VersionStr = version_str
        self.Version = [int(n) for n in version_str.split(".")]

    def __str__(self):
        return self.VersionStr

    def __repr__(self):
        return self.VersionStr

    def __lt__(self, other):
        max_len = max(len(self.Version), len(other.Version))
        arr1 = self.Version + [0] * (max_len - len(self.Version))
        arr2 = other.Version + [0] * (max_len - len(other.Version))
        for a, b in zip(arr1, arr2):
            if a != b:
                return a < b
        return False

    def __ge__(self, other):
        max_len = max(len(self.Version), len(other.Version))
        arr1 = self.Version + [0] * (max_len - len(self.Version))
        arr2 = other.Version + [0] * (max_len - len(other.Version))
        for a, b in zip(arr1, arr2):
            if a != b:
                return a > b
        return True

    def __eq__(self, other):
        max_len = max(len(self.Version), len(other.Version))
        arr1 = self.Version + [0] * (max_len - len(self.Version))
        arr2 = other.Version + [0] * (max_len - len(other.Version))
        return arr1 == arr2

    def __hash__(self):
        return hash(self.VersionStr)
