#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""共享测试 fixtures"""
import logging
import pytest

from server.config.models import AppConfig, RDSConfig, CheckRulesConfig
from server.verify.check_config import CheckConfig


def make_check_config(
    allow_none_primary_key: bool = False,
    allow_foreign_key: bool = False,
) -> CheckConfig:
    rds = RDSConfig(host="", port=3306, user="", password="", type="mariadb", source_type="internal")
    rules = CheckRulesConfig(
        allow_none_primary_key=allow_none_primary_key,
        allow_foreign_key=allow_foreign_key,
    )
    return CheckConfig(AppConfig(rds=rds, check_rules=rules))


@pytest.fixture
def logger():
    return logging.getLogger("test")


@pytest.fixture
def cfg():
    return make_check_config()


@pytest.fixture
def cfg_allow_no_pk():
    return make_check_config(allow_none_primary_key=True)


@pytest.fixture
def cfg_allow_fk():
    return make_check_config(allow_foreign_key=True)
