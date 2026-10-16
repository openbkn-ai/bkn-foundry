# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import unittest
from generate_m19_graph import GraphReader, graph_fixture


class GraphFixtureTests(unittest.TestCase):
    def test_variable_identity_and_reverse_direction(self):
        graph = GraphReader("CREATE (a:A {n: 1}), (b:A {n: 1}) CREATE (a)<-[:R]-(b)").graph()
        self.assertEqual(len(graph['nodes']), 2)
        self.assertNotEqual(graph['nodes'][0]['identity'], graph['nodes'][1]['identity'])
        self.assertEqual(graph['edges'], [{'type': 'R', 'source': 1, 'target': 0}])

    def test_comments_do_not_destroy_quoted_urls(self):
        graph = GraphReader("/* preparation */ CREATE (:Page {url:'https://example.test/a//b'}) // comment").graph()
        self.assertEqual(graph['nodes'][0]['properties']['url'], 'https://example.test/a//b')

    def test_unknown_graph_is_never_assumed_empty(self):
        item = {'query': 'MATCH (n) RETURN count(n) AS count', 'contract_applicability': {'status': 'requires_adapter_review'}}
        fixture = graph_fixture(item, 'Given the binary-tree-1 graph\nThen the result should be:\n | count |\n | 0 |\n')
        self.assertEqual(fixture['adapter_status'], 'requires_fixture_adapter')
        self.assertNotIn('graph', fixture)

    def test_expected_values_are_not_evaluated_from_the_query(self):
        item = {'query': 'MATCH (n) RETURN count(n) AS count', 'contract_applicability': {'status': 'requires_adapter_review'}}
        fixture = graph_fixture(item, 'Given an empty graph\nThen the result should be:\n | count |\n | 99 |\n')
        self.assertEqual(fixture['expected'], [{'count': 99}])
        self.assertEqual(fixture['graph'], {'nodes': [], 'edges': []})

    def test_semicolon_does_not_extend_variable_scope(self):
        with self.assertRaises(ValueError):
            GraphReader('CREATE (a:A); CREATE (a)-[:R]->(:A)').graph()


if __name__ == '__main__':
    unittest.main()
