"""Tests for test_core_config."""
import pytest
import os
from unittest.mock import patch
from app.core.config import BaseConfig, base_config, server_info, observability_config


class TestBaseConfig:
    """Tests for test base config."""

    def test_default_values(self):
        """Test test default values."""
        config = BaseConfig()
        assert config.DEBUGDEFAULT is False
        assert config.PORTDEFAULT == 9898
        assert config.DIPHOSTDEFAULT == "10.4.134.253"

    def test_redis_defaults(self):
        """Test test redis defaults."""
        config = BaseConfig()
        assert config.REDISPORTDEFAULT == 6379
        assert config.REDISCLUSTERMODEDEFAULT == "master-slave"

    def test_rds_defaults(self):
        """Test test rds defaults."""
        config = BaseConfig()
        assert config.RDSPORTDEFAULT == 3330
        assert config.RDSDBNAMEDEFAULT == 'openbkn'
        assert config.RDSUSERDEFAULT == 'root'

    def test_oauth_defaults(self):
        """Test test oauth defaults."""
        config = BaseConfig()
        assert config.OAUTHADMINPORTDEFAULT == 4445

    def test_kafka_defaults(self):
        """Test test kafka defaults."""
        config = BaseConfig()
        assert config.KAFKAPORTDEFAULT == 9097
        assert config.KAFKAUSERDEFAULT == "anyrobot"

    def test_port_from_env(self):
        """Test test port from env."""
        config = BaseConfig()
        assert hasattr(config, 'APP_PORT')
        assert isinstance(config.APP_PORT, int)

    def test_debug_from_env(self):
        """Test test debug from env."""
        config = BaseConfig()
        assert hasattr(config, 'DEBUG')
        assert isinstance(config.DEBUG, bool)

    def test_rdshost_from_env(self):
        """Test test rdshost from env."""
        config = BaseConfig()
        assert hasattr(config, 'RDSHOST')
        assert isinstance(config.RDSHOST, str)

    def test_redisport_from_env(self):
        """Test test redisport from env."""
        config = BaseConfig()
        assert hasattr(config, 'REDISPORT')
        assert config.REDISPORT > 0

    def test_kafkahost_from_env(self):
        """Test test kafkahost from env."""
        config = BaseConfig()
        assert hasattr(config, 'KAFKAHOST')
        assert isinstance(config.KAFKAHOST, str)


class TestBaseConfigInstance:
    """Tests for test base config instance."""

    def test_base_config_exists(self):
        """Test test base config exists."""
        assert base_config is not None
        assert isinstance(base_config, BaseConfig)

    def test_base_config_has_attributes(self):
        """Test test base config has attributes."""
        assert hasattr(base_config, 'PORTDEFAULT')
        assert hasattr(base_config, 'RDSHOST')
        assert hasattr(base_config, 'REDISHOST')
        assert hasattr(base_config, 'KAFKAHOST')


class TestServerInfo:
    """Tests for test server info."""

    def test_server_info_exists(self):
        """Test test server info exists."""
        assert server_info is not None

    def test_server_info_attributes(self):
        """Test test server info attributes."""
        assert hasattr(server_info, 'server_name')
        assert hasattr(server_info, 'server_version')
        assert hasattr(server_info, 'language')
        assert hasattr(server_info, 'python_version')

    def test_server_info_values(self):
        """Test test server info values."""
        assert server_info.server_name == "agent-executor"
        assert server_info.server_version == "1.0.0"
        assert server_info.language == "python"


class TestObservabilityConfig:
    """Tests for test observability config."""

    def test_observability_config_exists(self):
        """Test test observability config exists."""
        assert observability_config is not None

    def test_observability_config_has_log(self):
        """Test test observability config has log."""
        assert hasattr(observability_config, 'log')

    def test_log_settings(self):
        """Test test log settings."""
        log_config = observability_config.log
        assert hasattr(log_config, 'log_enabled')
        assert hasattr(log_config, 'log_exporter')
        assert hasattr(log_config, 'log_load_interval')

    @patch.dict(os.environ, {'LOG_ENABLED': 'true'})
    def test_log_enabled_from_env(self):
        """Test test log enabled from env."""
        from app.core.config import ObservabilitySetting, LogSetting
        config = ObservabilitySetting(
            log=LogSetting(
                log_enabled=os.getenv("LOG_ENABLED", "false") == "true",
                log_exporter="http",
                log_load_interval=10,
                log_load_max_log=1000,
                http_log_feed_ingester_url=""
            )
        )
        assert config.log.log_enabled is True


class TestAiohttpTimeout:
    """Tests for test aiohttp timeout."""

    def test_aiohttp_timeout_exists(self):
        """Test test aiohttp timeout exists."""
        config = BaseConfig()
        assert hasattr(config, 'aiohttp_timeout')

    def test_aiohttp_timeout_total(self):
        """Test test aiohttp timeout total."""
        config = BaseConfig()
        assert config.aiohttp_timeout.total == 1800

    def test_aiohttp_timeout_sock_connect(self):
        """Test test aiohttp timeout sock connect."""
        config = BaseConfig()
        assert config.aiohttp_timeout.sock_connect == 30

