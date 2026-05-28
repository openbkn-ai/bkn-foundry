#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
import os
import logging
import pytest

from server.config.models import AppConfig, RDSConfig
from server.migrate.script_selector import ScriptSelector


# ── fixtures ──────────────────────────────────────────────────────────────────

def make_selector(tmp_path, db_type="mariadb") -> ScriptSelector:
    rds = RDSConfig(host="", port=3306, user="", password="", type=db_type, source_type="internal")
    cfg = AppConfig(rds=rds, repo_path=str(tmp_path))
    return ScriptSelector(cfg, logging.getLogger("test"))


def mk_version(base: str, service: str, db_type: str, version: str,
               scripts: list = None, has_init: bool = False):
    """在 tmp_path 下创建版本目录结构并写入空文件"""
    path = os.path.join(base, service, db_type, version)
    os.makedirs(path, exist_ok=True)
    if has_init:
        open(os.path.join(path, "init.sql"), "w").close()
    for name in (scripts or []):
        open(os.path.join(path, name), "w").close()
    return path


# ── get_all_versions ──────────────────────────────────────────────────────────

class TestGetAllVersions:
    def test_returns_version_dirs(self, tmp_path):
        mk_version(tmp_path, "svc", "mariadb", "1.0.0")
        mk_version(tmp_path, "svc", "mariadb", "1.1.0")
        sel = make_selector(tmp_path)
        assert set(sel.get_all_versions("svc")) == {"1.0.0", "1.1.0"}

    def test_ignores_non_version_dirs(self, tmp_path):
        mk_version(tmp_path, "svc", "mariadb", "1.0.0")
        os.makedirs(os.path.join(tmp_path, "svc", "mariadb", "latest"))
        sel = make_selector(tmp_path)
        assert sel.get_all_versions("svc") == ["1.0.0"]

    def test_missing_db_type_dir_returns_empty(self, tmp_path):
        sel = make_selector(tmp_path)
        assert sel.get_all_versions("nonexistent") == []

    def test_different_db_type_not_included(self, tmp_path):
        mk_version(tmp_path, "svc", "dm8", "1.0.0")
        sel = make_selector(tmp_path, db_type="mariadb")
        assert sel.get_all_versions("svc") == []


# ── find_init_sql ─────────────────────────────────────────────────────────────

class TestFindInitSql:
    def test_finds_init_in_latest_version(self, tmp_path):
        mk_version(tmp_path, "svc", "mariadb", "1.0.0", has_init=True)
        mk_version(tmp_path, "svc", "mariadb", "1.1.0", has_init=True)
        sel = make_selector(tmp_path)
        path, version = sel.find_init_sql("svc")
        assert path.endswith(os.path.join("1.1.0", "init.sql"))
        assert version == "1.1.0"

    def test_returns_none_when_max_version_has_no_init(self, tmp_path):
        # 只看最大版本，最大版本无 init.sql 则返回 (None, None)
        mk_version(tmp_path, "svc", "mariadb", "1.0.0", has_init=True)
        mk_version(tmp_path, "svc", "mariadb", "1.1.0")
        sel = make_selector(tmp_path)
        assert sel.find_init_sql("svc") == (None, None)

    def test_no_init_sql_anywhere_returns_none(self, tmp_path):
        mk_version(tmp_path, "svc", "mariadb", "1.0.0")
        sel = make_selector(tmp_path)
        assert sel.find_init_sql("svc") == (None, None)

    def test_no_versions_returns_none(self, tmp_path):
        sel = make_selector(tmp_path)
        assert sel.find_init_sql("svc") == (None, None)

    def test_single_version_with_init(self, tmp_path):
        mk_version(tmp_path, "svc", "mariadb", "0.4.0", has_init=True)
        sel = make_selector(tmp_path)
        path, version = sel.find_init_sql("svc")
        assert version == "0.4.0"
        assert "0.4.0" in path


# ── _collect_scripts_from_dir ─────────────────────────────────────────────────

class TestCollectScriptsFromDir:
    def test_collects_sql_and_py(self, tmp_path):
        path = mk_version(tmp_path, "svc", "mariadb", "1.0.0",
                          scripts=["01-add.sql", "02-fix.py"])
        sel = make_selector(tmp_path)
        scripts = sel._collect_scripts_from_dir(path)
        names = [os.path.basename(s) for s in scripts]
        assert names == ["01-add.sql", "02-fix.py"]

    def test_sorted_by_number(self, tmp_path):
        path = mk_version(tmp_path, "svc", "mariadb", "1.0.0",
                          scripts=["03-c.sql", "01-a.sql", "02-b.py"])
        sel = make_selector(tmp_path)
        scripts = sel._collect_scripts_from_dir(path)
        names = [os.path.basename(s) for s in scripts]
        assert names == ["01-a.sql", "02-b.py", "03-c.sql"]

    def test_init_sql_excluded(self, tmp_path):
        path = mk_version(tmp_path, "svc", "mariadb", "1.0.0",
                          scripts=["01-add.sql"], has_init=True)
        sel = make_selector(tmp_path)
        scripts = sel._collect_scripts_from_dir(path)
        names = [os.path.basename(s) for s in scripts]
        assert "init.sql" not in names
        assert "01-add.sql" in names

    def test_unlabeled_files_excluded(self, tmp_path):
        path = mk_version(tmp_path, "svc", "mariadb", "1.0.0",
                          scripts=["01-add.sql", "README.md", "notes.txt"])
        sel = make_selector(tmp_path)
        scripts = sel._collect_scripts_from_dir(path)
        names = [os.path.basename(s) for s in scripts]
        assert names == ["01-add.sql"]

    def test_empty_dir_returns_empty(self, tmp_path):
        path = mk_version(tmp_path, "svc", "mariadb", "1.0.0")
        sel = make_selector(tmp_path)
        assert sel._collect_scripts_from_dir(path) == []

    def test_nonexistent_dir_returns_empty(self, tmp_path):
        sel = make_selector(tmp_path)
        assert sel._collect_scripts_from_dir(str(tmp_path / "nonexistent")) == []


# ── select_upgrade_scripts ────────────────────────────────────────────────────

class TestSelectUpgradeScripts:
    def test_no_versions_returns_empty(self, tmp_path):
        sel = make_selector(tmp_path)
        files, max_v, has = sel.select_upgrade_scripts("svc", "1.0.0")
        assert files == [] and max_v == "" and has is False

    def test_upgrade_skips_installed_and_older(self, tmp_path):
        mk_version(tmp_path, "svc", "mariadb", "1.0.0",
                   scripts=["01-old.sql"], has_init=True)
        mk_version(tmp_path, "svc", "mariadb", "1.1.0",
                   scripts=["01-new.sql"])
        mk_version(tmp_path, "svc", "mariadb", "1.2.0",
                   scripts=["01-newer.sql"])
        sel = make_selector(tmp_path)
        files, max_v, has = sel.select_upgrade_scripts("svc", "1.1.0")
        assert has is True
        assert max_v == "1.2.0"
        all_scripts = [os.path.basename(s) for _, scripts in files for s in scripts]
        assert "01-old.sql" not in all_scripts
        assert "01-new.sql" not in all_scripts
        assert "01-newer.sql" in all_scripts

    def test_already_latest_returns_no_scripts(self, tmp_path):
        mk_version(tmp_path, "svc", "mariadb", "1.0.0",
                   scripts=["01-add.sql"], has_init=True)
        sel = make_selector(tmp_path)
        files, max_v, has = sel.select_upgrade_scripts("svc", "1.0.0")
        assert has is False
        assert max_v == "1.0.0"

    def test_init_sql_not_included_in_upgrade(self, tmp_path):
        # 升级时 init.sql 不应出现在脚本列表
        mk_version(tmp_path, "svc", "mariadb", "1.0.0", has_init=True)
        mk_version(tmp_path, "svc", "mariadb", "1.1.0",
                   scripts=["01-add.sql"], has_init=True)
        sel = make_selector(tmp_path)
        files, _, _ = sel.select_upgrade_scripts("svc", "1.0.0")
        all_scripts = [os.path.basename(s) for _, scripts in files for s in scripts]
        assert "init.sql" not in all_scripts

    def test_scripts_ordered_across_versions(self, tmp_path):
        mk_version(tmp_path, "svc", "mariadb", "1.0.0", has_init=True)
        mk_version(tmp_path, "svc", "mariadb", "1.1.0",
                   scripts=["02-b.sql", "01-a.sql"])
        mk_version(tmp_path, "svc", "mariadb", "1.2.0",
                   scripts=["01-c.sql"])
        sel = make_selector(tmp_path)
        files, _, _ = sel.select_upgrade_scripts("svc", "1.0.0")
        # 每个版本内部按编号排序，files 为 [(version, [scripts]), ...]
        v1_version, v1_scripts = files[0]
        assert v1_version == "1.1.0"
        assert [os.path.basename(s) for s in v1_scripts] == ["01-a.sql", "02-b.sql"]
        v2_version, v2_scripts = files[1]
        assert v2_version == "1.2.0"
        assert [os.path.basename(s) for s in v2_scripts] == ["01-c.sql"]

    def test_version_dir_with_only_init_produces_no_scripts(self, tmp_path):
        mk_version(tmp_path, "svc", "mariadb", "1.0.0", has_init=True)
        mk_version(tmp_path, "svc", "mariadb", "1.1.0", has_init=True)
        sel = make_selector(tmp_path)
        files, max_v, has = sel.select_upgrade_scripts("svc", "1.0.0")
        # 1.1.0 只有 init.sql，无增量脚本
        assert has is False
        assert max_v == "1.1.0"

    def test_returns_version_and_scripts_tuple(self, tmp_path):
        mk_version(tmp_path, "svc", "mariadb", "1.0.0", has_init=True)
        mk_version(tmp_path, "svc", "mariadb", "1.1.0",
                   scripts=["01-add.sql"])
        sel = make_selector(tmp_path)
        files, _, _ = sel.select_upgrade_scripts("svc", "1.0.0")
        assert len(files) == 1
        version, scripts = files[0]
        assert version == "1.1.0"
        assert [os.path.basename(s) for s in scripts] == ["01-add.sql"]
