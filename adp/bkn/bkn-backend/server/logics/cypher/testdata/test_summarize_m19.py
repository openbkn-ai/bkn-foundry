# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import unittest
from generate_m19 import SHA256
from summarize_m19 import summarize


class CoverageIntegrityTests(unittest.TestCase):
    def setUp(self):
        self.inventory = {'sha256': SHA256, 'cases': [{'source': 'test.feature', 'scenario': 'case', 'example': 1, 'classification': 'requires_adapter', 'contract_applicability': {'status': 'requires_adapter_review'}}]}
        self.report = {'SHA256': SHA256, 'Cases': [{'Source': 'test.feature', 'Scenario': 'case', 'Example': 1, 'Status': 'unsupported'}]}

    def test_missing_and_unsupported_are_not_passes(self):
        self.assertEqual(summarize(self.inventory, [])['counts'], {'not_executed': 1})
        self.assertEqual(summarize(self.inventory, [self.report])['counts'], {'unsupported': 1})

    def test_reports_must_be_disjoint(self):
        with self.assertRaises(ValueError):
            summarize(self.inventory, [self.report, self.report])

    def test_unrecognized_source_cannot_be_certified(self):
        self.report['SHA256'] = 'different archive'
        with self.assertRaises(ValueError):
            summarize(self.inventory, [self.report])

    def test_exclusions_cannot_hide_adapter_results(self):
        self.inventory['cases'][0]['contract_applicability'] = {'status': 'not_applicable', 'reason': 'native model'}
        with self.assertRaises(ValueError):
            summarize(self.inventory, [self.report])


if __name__ == '__main__':
    unittest.main()
