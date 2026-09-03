"""Tests for conftest."""
import sys
import os

# The service now defaults to AUTH_ENABLED=true so a deployment that forgets
# the variable fails closed. The suite predates that default and asserts the
# anonymous-allow behaviour, so pin the open value before project code is
# imported; cases that exercise the auth path patch base_config themselves.
os.environ.setdefault("AUTH_ENABLED", "false")
from unittest.mock import Mock, AsyncMock, MagicMock, patch

# Add the project root directory to sys.path.
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), '../..')))

# Mock private dependency modules before importing project code.
# Mock tlogging. AR-related code has been removed; keep this comment for possible future restoration.
# tlogging_mock = MagicMock()
# tlogging_mock.SamplerLogger = MagicMock()
# sys.modules['tlogging'] = tlogging_mock

# Mock rdsdriver
rdsdriver_mock = MagicMock()
sys.modules['rdsdriver'] = rdsdriver_mock

# Mock OpenTelemetry-related modules.
opentelemetry_mock = MagicMock()
sys.modules['opentelemetry'] = opentelemetry_mock
sys.modules['opentelemetry.trace'] = MagicMock()
sys.modules['opentelemetry.propagate'] = MagicMock()
sys.modules['opentelemetry.sdk'] = MagicMock()
sys.modules['opentelemetry.sdk.trace'] = MagicMock()
sys.modules['opentelemetry.sdk.trace.export'] = MagicMock()
sys.modules['opentelemetry.sdk.resources'] = MagicMock()
sys.modules['opentelemetry.exporter'] = MagicMock()
sys.modules['opentelemetry.exporter.otlp'] = MagicMock()
sys.modules['opentelemetry.exporter.otlp.proto'] = MagicMock()
sys.modules['opentelemetry.exporter.otlp.proto.grpc'] = MagicMock()
sys.modules['opentelemetry.exporter.otlp.proto.grpc.trace_exporter'] = MagicMock()
sys.modules['opentelemetry.instrumentation'] = MagicMock()
sys.modules['opentelemetry.instrumentation.fastapi'] = MagicMock()

# Mock arrow, the date/time library.
arrow_mock = MagicMock()
arrow_mock.now = MagicMock(return_value=MagicMock())
arrow_mock.get = MagicMock(return_value=MagicMock())
sys.modules['arrow'] = arrow_mock

# Mock dbutilsx, the private database connection pool.
dbutilsx_mock = MagicMock()
dbutilsx_mock.pooled_db = MagicMock()
dbutilsx_mock.pooled_db.PooledDB = MagicMock()
dbutilsx_mock.pooled_db.PooledDBInfo = MagicMock()
sys.modules['dbutilsx'] = dbutilsx_mock
sys.modules['dbutilsx.pooled_db'] = dbutilsx_mock.pooled_db

# Mock exporter, the private log exporter. AR-related code has been removed; keep this comment for possible future restoration.
# exporter_mock = MagicMock()
# sys.modules['exporter'] = exporter_mock
# sys.modules['exporter.resource'] = MagicMock()
# sys.modules['exporter.resource.resource'] = MagicMock()
# sys.modules['exporter.ar_log'] = MagicMock()
# sys.modules['exporter.ar_log.log_exporter'] = MagicMock()
# sys.modules['exporter.ar_trace'] = MagicMock()
# sys.modules['exporter.ar_trace.trace_exporter'] = MagicMock()
# sys.modules['exporter.public'] = MagicMock()
# sys.modules['exporter.public.client'] = MagicMock()
# sys.modules['exporter.public.public'] = MagicMock()

# Mock llmadapter, the private LLM adapter.
llmadapter_mock = MagicMock()
sys.modules['llmadapter'] = llmadapter_mock
sys.modules['llmadapter.llms'] = MagicMock()
sys.modules['llmadapter.llms.llm_factory'] = MagicMock()
sys.modules['llmadapter.schema'] = MagicMock()
# Mock the commonly used Message class.
llmadapter_mock.schema.HumanMessage = MagicMock
llmadapter_mock.schema.AIMessage = MagicMock

# Mock func_timeout
func_timeout_mock = MagicMock()
func_timeout_mock.func_timeout = MagicMock(side_effect=lambda timeout, func, args=(), kwargs=None: func(*args, **(kwargs or {})))
func_timeout_mock.FunctionTimedOut = Exception
sys.modules['func_timeout'] = func_timeout_mock

# Mock tiktoken, used for token counting.
tiktoken_mock = MagicMock()
tiktoken_mock.get_encoding = MagicMock(return_value=MagicMock())
# Mock the encode method on the encoding object.
mock_encoding = MagicMock()
mock_encoding.encode = MagicMock(return_value=[1, 2, 3, 4, 5])  # Return a fake token list.
tiktoken_mock.get_encoding.return_value = mock_encoding
sys.modules['tiktoken'] = tiktoken_mock

# Mock confluent_kafka, the Kafka client.
confluent_kafka_mock = MagicMock()
# Mock Producer
mock_producer = MagicMock()
mock_producer.produce = MagicMock()
mock_producer.poll = MagicMock()
mock_producer.flush = MagicMock()
confluent_kafka_mock.Producer = MagicMock(return_value=mock_producer)
# Mock Consumer
mock_consumer = MagicMock()
mock_consumer.subscribe = MagicMock()
mock_consumer.poll = MagicMock(return_value=None)
mock_consumer.close = MagicMock()
confluent_kafka_mock.Consumer = MagicMock(return_value=mock_consumer)
# Mock Admin-related classes.
confluent_kafka_admin_mock = MagicMock()
confluent_kafka_admin_mock.AdminClient = MagicMock()
confluent_kafka_admin_mock.NewTopic = MagicMock()
sys.modules['confluent_kafka'] = confluent_kafka_mock
sys.modules['confluent_kafka.admin'] = confluent_kafka_admin_mock

import pytest
import asyncio
from typing import Dict, Any


@pytest.fixture(scope="session")
def event_loop():
    """Test event loop."""
    loop = asyncio.get_event_loop_policy().new_event_loop()
    yield loop
    loop.close()


@pytest.fixture
def mock_redis():
    """Test mock redis."""
    redis_mock = AsyncMock()
    redis_mock.get_str = AsyncMock(return_value=None)
    redis_mock.set_str = AsyncMock(return_value=True)
    redis_mock.delete_str = AsyncMock(return_value=True)
    return redis_mock


@pytest.fixture
def mock_db_connection():
    """Test mock db connection."""
    connection = Mock()
    cursor = Mock()
    cursor.execute = Mock()
    cursor.fetchall = Mock(return_value=[])
    cursor.fetchone = Mock(return_value=None)
    return connection, cursor


@pytest.fixture
def mock_user_info():
    """Test mock user info."""
    return "test_user_id", "zh", "user"


@pytest.fixture
def mock_llm_model_data():
    """Test mock llm model data."""
    return [{
        "f_model_id": "123456789012345678",
        "f_model_name": "test_model",
        "f_model_series": "openai",
        "f_model": "gpt-3.5-turbo",
        "f_model_config": '{"api_url": "http://test.com", "api_model": "gpt-3.5-turbo", "api_key": "test_key"}',
        "f_max_model_len": 4096,
        "f_model_parameters": 1000000,
        "f_model_type": "llm",
        "f_quota": False,
        "f_create_by": "user1",
        "f_update_by": "user2",
        "f_create_time": "2024-01-01 00:00:00",
        "f_update_time": "2024-01-02 00:00:00"
    }]


@pytest.fixture
def mock_small_model_data():
    """Test mock small model data."""
    return [{
        "f_model_id": "123456789012345679",
        "f_model_name": "test_embedding",
        "f_model_type": "embedding",
        "f_model_config": '{"api_url": "http://test.com", "api_model": "embedding-model", "api_key": "test_key"}',
        "f_adapter": False,
        "f_adapter_code": "",
        "f_create_by": "user1",
        "f_update_by": "user2",
        "f_create_time": "2024-01-01 00:00:00",
        "f_update_time": "2024-01-02 00:00:00"
    }]


@pytest.fixture
def mock_request():
    """Test mock request."""
    request = Mock()
    request.headers = {
        "Authorization": "Bearer test_token",
        "x-account-id": "test_user",
        "x-account-type": "user",
        "x-func-module": "test"
    }
    request.url = Mock()
    request.url.path = "/api/v1/test"
    return request


@pytest.fixture
def mock_aiohttp_response():
    """Test mock aiohttp response."""
    response = AsyncMock()
    response.status = 200
    response.text = AsyncMock(return_value='{"active": true, "sub": "user123", "client_id": "user123"}')
    return response


@pytest.fixture(autouse=True)
def reset_mocks():
    """Test reset mocks."""
    yield
    # Cleanup after tests.

