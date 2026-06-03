#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""YAML 配置加载 + AppConfig 构建"""
import os
from logging import Logger
from typing import Dict, List, Optional

import yaml

from server.config.models import AppConfig, RDSConfig, ServiceConfig, CheckRulesConfig


def _parse_rds_config(cfg: dict) -> RDSConfig:
    """从 YAML 配置中解析 RDS 连接配置"""
    rds_data = (cfg.get("depServices") or {}).get("rds") or {}
    return RDSConfig(
        host=rds_data.get("host", ""),
        port=int(rds_data.get("port", 3306)),
        user=rds_data.get("user", ""),
        password=rds_data.get("password", ""),
        type=rds_data.get("type", "mariadb"),
        source_type=rds_data.get("source_type", "internal"),
    )


def _load_services(cfg: dict, service_filter: Optional[List[str]]) -> Dict[str, ServiceConfig]:
    """从 YAML 配置中解析服务列表，按 service_filter 过滤"""
    raw_services = cfg.get("services", {})
    services = {}
    for name, info in raw_services.items():
        if service_filter and name not in service_filter:
            continue
        services[name] = ServiceConfig(
            project=info.get("project", ""),
            repo=info.get("repo", ""),
            ref=info.get("ref", ""),
            path=info.get("path", ""),
            check_from=info.get("check_from"),
        )
    return services


def _load_check_rules(cfg: dict) -> CheckRulesConfig:
    """从 YAML 配置中解析校验规则"""
    raw_rules = cfg.get("check_rules", {})
    return CheckRulesConfig(
        check_type=raw_rules.get("check_type", 1),
        allow_none_primary_key=raw_rules.get("allow_none_primary_key", False),
        allow_foreign_key=raw_rules.get("allow_foreign_key", False),
        allow_python_exception=raw_rules.get("allow_python_exception", False),
    )


def _set_dep_services_env(cfg: dict, rds_config: RDSConfig):
    """将所有依赖服务配置注入环境变量，供自定义迁移脚本使用"""
    os.environ.setdefault("DB_HOST", rds_config.host)
    os.environ.setdefault("DB_PORT", str(rds_config.port))
    os.environ.setdefault("DB_USER", rds_config.user)
    os.environ.setdefault("DB_PASSWD", rds_config.password)
    os.environ["DB_TYPE"] = rds_config.type
    os.environ["DB_SOURCE_TYPE"] = rds_config.source_type

    dep = cfg.get("depServices", {}) or {}

    mongodb = dep.get("mongodb", {}) or {}
    if mongodb:
        os.environ.setdefault("MONGODB_HOST", str(mongodb.get("host", "")))
        os.environ.setdefault("MONGODB_PORT", str(mongodb.get("port", "")))
        os.environ.setdefault("MONGODB_USER", str(mongodb.get("user", "")))
        os.environ.setdefault("MONGODB_PASSWORD", str(mongodb.get("password", "")))
        options = mongodb.get("options", {}) or {}
        os.environ.setdefault("MONGODB_AUTH_SOURCE", str(options.get("authSource", "")))

    opensearch = dep.get("opensearch", {}) or {}
    if opensearch:
        os.environ.setdefault("OPENSEARCH_HOST", str(opensearch.get("host", "")))
        os.environ.setdefault("OPENSEARCH_PORT", str(opensearch.get("port", "")))
        os.environ.setdefault("OPENSEARCH_USER", str(opensearch.get("user", "")))
        os.environ.setdefault("OPENSEARCH_PASSWORD", str(opensearch.get("password", "")))
        os.environ.setdefault("OPENSEARCH_PROTOCOL", str(opensearch.get("protocol", "")))

    redis = dep.get("redis", {}) or {}
    if redis:
        os.environ.setdefault("REDIS_CONNECT_TYPE", str(redis.get("connectType", "")))
        connect_info = redis.get("connectInfo", {}) or {}
        os.environ.setdefault("REDIS_HOST", str(connect_info.get("host", "")))
        os.environ.setdefault("REDIS_PORT", str(connect_info.get("port", "")))
        os.environ.setdefault("REDIS_USERNAME", str(connect_info.get("username", "")))
        os.environ.setdefault("REDIS_PASSWORD", str(connect_info.get("password", "")))


_DEFAULT_SECRET_PATH = "/etc/data-migrator/secret.yaml"


def _load_secret_config(cfg: dict, logger: Logger, secret_config_path: Optional[str]):
    """读取 secret-config.yaml，将 depServices 覆盖写入 cfg"""
    path = secret_config_path or _DEFAULT_SECRET_PATH
    if not os.path.exists(path):
        return
    logger.info(f"加载 secret-config 文件: {path}")
    with open(path, "r", encoding="utf-8") as f:
        secret_config = yaml.safe_load(f) or {}
    if "depServices" in secret_config:
        cfg["depServices"] = secret_config["depServices"]


def load_config(config_path: str, service_filter: Optional[List[str]], logger: Logger, secret_config_path: Optional[str] = None) -> AppConfig:
    """
    加载 YAML 配置文件并构建 AppConfig。
    service_filter: CLI 传入的 --service 参数，用于过滤服务范围；None 表示全部。
    """
    logger.info(f"加载配置文件: {config_path}")
    with open(config_path, "r", encoding="utf-8") as f:
        cfg = yaml.safe_load(f)

    _load_secret_config(cfg, logger, secret_config_path)

    rds_config = _parse_rds_config(cfg)
    services = _load_services(cfg, service_filter)
    check_rules = _load_check_rules(cfg)

    _set_dep_services_env(cfg, rds_config)

    app_config = AppConfig(
        rds=rds_config,
        services=services,
        db_types=[t.lower() for t in cfg.get("db_types", ["mariadb"])],
        databases=[d.lower() for d in cfg.get("databases", [])],
        check_rules=check_rules,
        repo_path=os.path.join(os.getcwd(), "repos"),
        renamed_services=cfg.get("renamed_services") or [],
        service_filter=service_filter or None,
    )

    logger.info(f"配置加载完成, 服务数: {len(services)}, db_type: {rds_config.type}")
    return app_config
