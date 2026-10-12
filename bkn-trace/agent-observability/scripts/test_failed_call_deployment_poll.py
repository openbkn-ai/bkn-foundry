# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

import unittest
from unittest import mock

import test_failed_call_deployment as deployment


class DeploymentPollingTest(unittest.TestCase):
    def test_waits_for_listing_and_integrity_to_converge(self):
        ready = {"interaction_id": "int-1", "current_record_integrity": {"status": "complete"}}
        pending = {"interaction_id": "int-1", "current_record_integrity": {"status": "pending"}}
        with mock.patch.object(deployment, "request", side_effect=[
            ({"entries": []}, {}),
            ({"entries": [pending]}, {}),
            ({"entries": [ready]}, {}),
        ]) as request, mock.patch.object(deployment.time, "sleep") as sleep:
            result = deployment.poll(lambda: deployment.completed_interaction(
                "http://core.test", {}, "conv-1", "int-1"))
        self.assertEqual(result, ready)
        self.assertEqual(request.call_count, 3)
        self.assertEqual(sleep.call_count, 2)

    def test_malformed_response_is_not_retried_as_visibility_delay(self):
        with mock.patch.object(deployment, "request", return_value=({}, {})) as request:
            with self.assertRaises(KeyError):
                deployment.poll(lambda: deployment.completed_interaction(
                    "http://core.test", {}, "conv-1", "int-1"))
        self.assertEqual(request.call_count, 1)


if __name__ == "__main__":
    unittest.main()
