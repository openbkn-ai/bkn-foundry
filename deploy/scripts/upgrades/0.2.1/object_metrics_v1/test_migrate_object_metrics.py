import importlib.util
import os
from pathlib import Path
import sys
import types
import unittest
from unittest import mock


sys.modules.setdefault("pymysql", types.ModuleType("pymysql"))
MODULE_PATH = Path(__file__).with_name("migrate_object_metrics.py")
SPEC = importlib.util.spec_from_file_location("migrate_object_metrics", MODULE_PATH)
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


def legacy_metric(**overrides):
    row = {
        "f_id": "metric-1",
        "f_name": "Revenue",
        "f_comment": "",
        "f_unit_type": "currencyUnit",
        "f_unit": "CNY",
        "f_metric_type": "atomic",
        "f_scope_type": "object_type",
        "f_scope_ref": "orders",
        "f_calculation_formula": '{"aggregation":{"aggr":"sum","property":"amount"}}',
        "f_analysis_dimensions": "[]",
        "f_time_dimension": None,
        "f_create_time": 0,
        "f_update_time": 0,
    }
    row.update(overrides)
    return row


class MigrationTranslationTest(unittest.TestCase):
    def test_maps_current_time_dimension_property(self):
        decision = MODULE.translate(legacy_metric(
            f_time_dimension='{"property":"paid_at","default_range_policy":"none"}',
        ))

        self.assertEqual(decision.action, "migrate")
        self.assertEqual(decision.definition["query_capabilities"]["time_dimensions"], [{
            "object_type_id": "orders",
            "property_name": "paid_at",
        }])

    def test_maps_legacy_time_dimension_field(self):
        decision = MODULE.translate(legacy_metric(f_time_dimension='{"field":"paid_at"}'))

        self.assertEqual(decision.action, "migrate")
        self.assertEqual(
            decision.definition["query_capabilities"]["time_dimensions"][0]["property_name"],
            "paid_at",
        )

    def test_requires_manual_review_for_default_range_policy(self):
        decision = MODULE.translate(legacy_metric(
            f_time_dimension='{"property":"paid_at","default_range_policy":"last_1h"}',
        ))

        self.assertEqual(decision.action, "manual_review")

    def test_does_not_invent_decimal_rounding(self):
        decision = MODULE.translate(legacy_metric())

        value = decision.definition["result_semantics"]["value"]
        self.assertNotIn("precision", value)
        self.assertNotIn("scale", value)
        self.assertNotIn("rounding_mode", value)

    def test_defaults_to_openbkn_database(self):
        with mock.patch.object(sys, "argv", ["migrate_object_metrics.py"]), mock.patch.dict(
            os.environ, {}, clear=True
        ):
            self.assertEqual(MODULE.arguments().database, "openbkn")


if __name__ == "__main__":
    unittest.main()
