#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""FetchExecutor._collect_repos 和 _copy_version_dirs 单元测试（无网络/无 git 依赖）"""
import logging
import pytest

from server.fetch.executor import FetchExecutor, DEFAULT_DB_TYPE
from server.config.models import AppConfig, RDSConfig, ServiceConfig


def make_app_config(services: dict, db_types: list, repo_path) -> AppConfig:
    rds = RDSConfig(host="", port=3306, user="", password="", type="mariadb", source_type="internal")
    return AppConfig(rds=rds, services=services, db_types=db_types, repo_path=str(repo_path))


@pytest.fixture
def logger():
    return logging.getLogger("test")


def make_executor(services, db_types, repo_path, logger):
    return FetchExecutor(make_app_config(services, db_types, repo_path), logger)


# ── _collect_repos ─────────────────────────────────────────────────────────────

class TestFetchExecutorCollectRepos:
    def test_copies_version_dirs_to_repo(self, tmp_path, monkeypatch, logger):
        """正常路径：版本目录被复制到 repos/<service>/<db_type>/"""
        monkeypatch.chdir(tmp_path)
        src = tmp_path / "source_code" / "svc-a" / "migrations" / "mariadb" / "1.0.0"
        src.mkdir(parents=True)
        (src / "init.sql").write_text("-- init")

        repo_path = tmp_path / "repos"
        executor = make_executor({"svc-a": ServiceConfig(path="migrations")},
                                 ["mariadb"], repo_path, logger)
        executor._collect_repos()

        assert (repo_path / "svc-a" / "mariadb" / "1.0.0" / "init.sql").exists()

    def test_fallback_to_default_db_type(self, tmp_path, monkeypatch, logger):
        """目标 db_type 目录不存在时，回退到 DEFAULT_DB_TYPE（mariadb）"""
        monkeypatch.chdir(tmp_path)
        src = tmp_path / "source_code" / "svc-a" / "migrations" / DEFAULT_DB_TYPE / "1.0.0"
        src.mkdir(parents=True)
        (src / "init.sql").write_text("-- init")

        repo_path = tmp_path / "repos"
        executor = make_executor({"svc-a": ServiceConfig(path="migrations")},
                                 ["dm8"], repo_path, logger)
        executor._collect_repos()

        # 回退 mariadb 的内容，但复制到 repos/svc-a/dm8/ 下
        assert (repo_path / "svc-a" / "dm8" / "1.0.0" / "init.sql").exists()

    def test_missing_db_type_and_default_raises(self, tmp_path, monkeypatch, logger):
        """既无目标 db_type 目录，也无 DEFAULT_DB_TYPE 目录时，抛出异常"""
        monkeypatch.chdir(tmp_path)
        (tmp_path / "source_code" / "svc-a" / "migrations").mkdir(parents=True)

        repo_path = tmp_path / "repos"
        executor = make_executor({"svc-a": ServiceConfig(path="migrations")},
                                 ["dm8"], repo_path, logger)

        with pytest.raises(Exception, match="缺少目录"):
            executor._collect_repos()

    def test_multiple_services_and_db_types(self, tmp_path, monkeypatch, logger):
        """多个服务 × 多个 db_type 全部正确复制"""
        monkeypatch.chdir(tmp_path)
        for svc in ["svc-a", "svc-b"]:
            for db in ["mariadb", "dm8"]:
                d = tmp_path / "source_code" / svc / "migrations" / db / "1.0.0"
                d.mkdir(parents=True)
                (d / "init.sql").write_text(f"-- {svc} {db}")

        repo_path = tmp_path / "repos"
        services = {
            "svc-a": ServiceConfig(path="migrations"),
            "svc-b": ServiceConfig(path="migrations"),
        }
        executor = make_executor(services, ["mariadb", "dm8"], repo_path, logger)
        executor._collect_repos()

        for svc in ["svc-a", "svc-b"]:
            for db in ["mariadb", "dm8"]:
                assert (repo_path / svc / db / "1.0.0" / "init.sql").exists()


# ── _copy_version_dirs ─────────────────────────────────────────────────────────

class TestFetchExecutorCopyVersionDirs:
    def test_copies_only_version_dirs(self, tmp_path):
        """只复制版本号格式的目录，非版本目录跳过"""
        src = tmp_path / "src"
        dst = tmp_path / "dst"
        src.mkdir()
        dst.mkdir()

        (src / "1.0.0").mkdir()
        (src / "1.0.0" / "init.sql").write_text("-- v1")
        (src / "2.1.3").mkdir()
        (src / "2.1.3" / "init.sql").write_text("-- v2")
        (src / "not_version").mkdir()
        (src / "not_version" / "file.txt").write_text("skip me")
        (src / "README.md").write_text("skip me")

        FetchExecutor._copy_version_dirs(str(src), str(dst))

        assert (dst / "1.0.0" / "init.sql").exists()
        assert (dst / "2.1.3" / "init.sql").exists()
        assert not (dst / "not_version").exists()
        assert not (dst / "README.md").exists()

    def test_empty_source_dir(self, tmp_path):
        """源目录为空时不报错"""
        src = tmp_path / "src"
        dst = tmp_path / "dst"
        src.mkdir()
        dst.mkdir()

        FetchExecutor._copy_version_dirs(str(src), str(dst))

        assert list(dst.iterdir()) == []
