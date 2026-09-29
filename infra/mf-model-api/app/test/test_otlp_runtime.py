import unittest
from unittest.mock import patch

from app.core.config import _positive_int_env
from app.utils.observability.observability import init_observability
from app.utils.observability.observability_setting import ObservabilitySetting, ServerInfo


class TestOTLPRuntime(unittest.TestCase):
    def test_empty_batch_setting_uses_bounded_default(self):
        with patch.dict("os.environ", {"TRACE_MAX_EXPORT_BATCH_SIZE": ""}):
            self.assertEqual(_positive_int_env("TRACE_MAX_EXPORT_BATCH_SIZE", 512), 512)

    def test_trace_initialization_failure_is_fail_open(self):
        setting = ObservabilitySetting()
        setting.log.log_enabled = False
        setting.trace.trace_enabled = True
        with patch(
            "app.utils.observability.observability.init_trace_provider",
            side_effect=ValueError("invalid batch settings"),
        ):
            init_observability(ServerInfo(), setting)
