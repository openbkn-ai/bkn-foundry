# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
import copy
import unittest
from core_history import plan_core_history
from test_core_history import fixture
from snapshot import strict_loads, canonical
from core_history_batches import core_history_import_batches, HISTORY_CORE_FIELDS


class HistoricalCoreBatchTests(unittest.TestCase):
    def plan(self):
        result = plan_core_history(fixture())["core"]
        owner = result["conversations"][0]["owner"]
        result["conversations"].append(
            dict(result["conversations"][0], conversation_id="empty-conv")
        )
        result["interactions"].append(
            dict(result["interactions"][0], interaction_id="empty-round", ordinal=8)
        )
        result["idempotency_records"] = [
            dict(
                scope="create",
                external_conversation_key="empty",
                idempotency_key="key-empty",
                request_hash="h",
                resource_type="conversation",
                resource_id="empty-conv",
                owner=owner,
                created_at="2026-09-01T00:00:00Z",
            ),
            dict(
                scope="start",
                external_conversation_key="thread",
                idempotency_key="key-round",
                request_hash="h",
                resource_type="interaction",
                resource_id="empty-round",
                owner=owner,
                created_at="2026-09-01T00:00:00Z",
            ),
        ]
        return result

    def test_preserves_empty_conversation_interaction_and_all_seven_categories(self):
        plan = self.plan()
        batches = list(core_history_import_batches(plan, max_interactions=1))
        self.assertGreater(len(batches), 1)
        bodies = [strict_loads(b) for b in batches]
        for key in HISTORY_CORE_FIELDS:
            actual = {canonical(r) for b in bodies for r in b[key]}
            expected = {canonical(r) for r in plan[key]}
            self.assertEqual(actual, expected, key)
        for body in bodies:
            conv = {r["conversation_id"] for r in body["conversations"]}
            ints = {r["interaction_id"] for r in body["interactions"]}
            ops = {r["operation_id"] for r in body["operations"]}
            receipts = {r["receipt_id"] for r in body["receipts"]}
            self.assertTrue(
                all(r["conversation_id"] in conv for r in body["interactions"])
            )
            self.assertTrue(
                all(r["interaction_id"] in ints for r in body["operations"])
            )
            self.assertTrue(all(r["operation_id"] in ops for r in body["receipts"]))
            self.assertTrue(
                all(r["receipt_id"] in receipts for r in body["call_facts"])
            )
            self.assertTrue(
                all(r["interaction_id"] in ints for r in body["assembly_revisions"])
            )

    def test_encoded_bytes_bound_handles_multibyte_and_deterministic_repeat(self):
        plan = self.plan()
        plan["conversations"][0]["agent_name"] = "供应链 Agent 🧭"
        first = list(core_history_import_batches(plan, max_interactions=1))
        limit = max(map(len, first))
        batches = list(
            core_history_import_batches(plan, max_bytes=limit, max_interactions=1)
        )
        self.assertTrue(all(len(b) <= limit for b in batches))
        self.assertEqual(
            batches,
            list(
                core_history_import_batches(
                    copy.deepcopy(plan), max_bytes=limit, max_interactions=1
                )
            ),
        )
        with self.assertRaises(ValueError):
            list(core_history_import_batches(plan, max_bytes=100))

    def test_orphans_and_duplicate_source_identity_fail_before_yield(self):
        for field, change in [
            ("operations", {"interaction_id": "missing"}),
            ("call_facts", {"receipt_id": "missing"}),
            ("call_facts", {"attempt": 99}),
        ]:
            plan = self.plan()
            plan[field][0].update(change)
            with self.subTest(field=field, change=change), self.assertRaises(
                ValueError
            ):
                list(core_history_import_batches(plan))
        plan = self.plan()
        plan["conversations"].append(dict(plan["conversations"][0], status="expired"))
        with self.assertRaises(ValueError):
            list(core_history_import_batches(plan))
