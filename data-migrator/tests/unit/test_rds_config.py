#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""RDSConfig source_type 校验 + loader 默认值 + secret_config 加载测试"""
import textwrap
import tempfile
import os
import logging
import pytest

from server.config.models import RDSConfig
from server.config.loader import load_config


def make_rds(**kwargs) -> RDSConfig:
    defaults = dict(host="h", port=3306, user="u", password="p", type="mariadb")
    defaults.update(kwargs)
    return RDSConfig(**defaults)


def write_yaml(content: str) -> str:
    f = tempfile.NamedTemporaryFile(mode="w", suffix=".yaml", delete=False)
    f.write(textwrap.dedent(content))
    f.close()
    return f.name


logger = logging.getLogger("test")


class TestRDSConfigSourceTypeValidation:
    def test_internal_is_valid(self):
        rds = make_rds(source_type="internal")
        assert rds.source_type == "internal"

    def test_external_is_valid(self):
        rds = make_rds(source_type="external")
        assert rds.source_type == "external"

    def test_invalid_value_raises(self):
        with pytest.raises(ValueError, match="source_type 非法值"):
            make_rds(source_type="unknown")

    def test_empty_string_raises(self):
        with pytest.raises(ValueError, match="source_type 非法值"):
            make_rds(source_type="")

    def test_case_sensitive_raises(self):
        with pytest.raises(ValueError, match="source_type 非法值"):
            make_rds(source_type="Internal")


class TestLoaderSourceTypeDefault:
    def test_source_type_defaults_to_internal(self):
        path = write_yaml("""
            depServices:
              rds:
                host: localhost
                port: 3306
                user: root
                password: pass
                type: mariadb
        """)
        try:
            cfg = load_config(path, None, logger)
            assert cfg.rds.source_type == "internal"
        finally:
            os.unlink(path)

    def test_source_type_external_loaded(self):
        path = write_yaml("""
            depServices:
              rds:
                host: localhost
                port: 3306
                user: root
                password: pass
                type: mariadb
                source_type: external
        """)
        try:
            cfg = load_config(path, None, logger)
            assert cfg.rds.source_type == "external"
        finally:
            os.unlink(path)

    def test_invalid_source_type_in_yaml_raises(self):
        path = write_yaml("""
            depServices:
              rds:
                host: localhost
                port: 3306
                user: root
                password: pass
                type: mariadb
                source_type: typo
        """)
        try:
            with pytest.raises(ValueError, match="source_type 非法值"):
                load_config(path, None, logger)
        finally:
            os.unlink(path)


class TestSecretLoading:
    def test_secret_overwrites_dep_services(self):
        """secret-config.yaml 存在时，depServices 覆盖 config.yaml 中的同名字段"""
        config_path = write_yaml("""
            depServices:
              rds:
                host: config-host
                port: 3306
                user: config-user
                password: config-pass
                type: mariadb
        """)
        secret_path = write_yaml("""
            depServices:
              rds:
                host: secret-host
                port: 3307
                user: secret-user
                password: secret-pass
                type: dm8
                source_type: external
        """)
        try:
            cfg = load_config(config_path, None, logger, secret_path)
            assert cfg.rds.host == "secret-host"
            assert cfg.rds.port == 3307
            assert cfg.rds.type == "dm8"
            assert cfg.rds.source_type == "external"
        finally:
            os.unlink(config_path)
            os.unlink(secret_path)

    def test_missing_secret_file_no_error(self):
        """secret-config 文件不存在时静默跳过，使用 config.yaml 中的 depServices"""
        config_path = write_yaml("""
            depServices:
              rds:
                host: config-host
                port: 3306
                user: root
                password: pass
                type: mariadb
        """)
        try:
            cfg = load_config(config_path, None, logger, "/nonexistent/secret-config.yaml")
            assert cfg.rds.host == "config-host"
        finally:
            os.unlink(config_path)

    def test_secret_none_uses_config(self):
        """secret_path=None 且默认路径不存在时，使用 config.yaml 中的 depServices"""
        config_path = write_yaml("""
            depServices:
              rds:
                host: only-host
                port: 3306
                user: root
                password: pass
                type: mariadb
        """)
        try:
            cfg = load_config(config_path, None, logger, None)
            assert cfg.rds.host == "only-host"
        finally:
            os.unlink(config_path)
