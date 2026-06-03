#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""日志工具 - 复用 server-old LogDiy，改名 data-migrator"""
import re
import sys
import logging
from logging import Logger


class PasswordFilter(logging.Filter):
    """屏蔽密码"""

    def filter(self, record: logging.LogRecord) -> bool:
        record.msg = re.sub(
            r'("|\')(password|sentinelPassword)("|\'):([ ]*)("|\')([^"\']*)("|\')',
            r"\1\2\3:\4\5*****\7",
            record.getMessage(),
            flags=re.I,
        )
        return True


class LogDiy:
    _instance = None
    _logger_cache = {}

    def get_logger(self, log_level: str = "INFO") -> Logger:
        level_map = {
            "CRITICAL": logging.CRITICAL,
            "FATAL": logging.FATAL,
            "ERROR": logging.ERROR,
            "WARN": logging.WARNING,
            "WARNING": logging.WARNING,
            "INFO": logging.INFO,
            "DEBUG": logging.DEBUG,
            "NOTSET": logging.NOTSET,
        }
        numeric_level = level_map.get(log_level.upper(), logging.INFO)

        if numeric_level in self._logger_cache:
            return self._logger_cache[numeric_level]

        logger = logging.Logger("data-migrator")
        logger.setLevel(numeric_level)

        stdout_formatter = logging.Formatter(
            "[%(asctime)s] %(filename)s %(funcName)s line:%(lineno)d [%(levelname)s] %(message)s"
        )
        stdout_handler = logging.StreamHandler(sys.stdout)
        stdout_handler.setLevel(numeric_level)
        stdout_handler.setFormatter(stdout_formatter)
        logger.addHandler(stdout_handler)
        logger.addFilter(PasswordFilter())

        self._logger_cache[numeric_level] = logger
        return logger

    @classmethod
    def instance(cls) -> "LogDiy":
        if cls._instance is None:
            cls._instance = cls()
        return cls._instance
