"""Offline regression checks for API documentation drift.

Run: python -m unittest discover -s docs/api/tools -p 'test_*.py' -v
Requires PyYAML, like api_contract_diff.py; no service startup or network calls.
"""
import ast
import json
import re
import unittest
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[3]
API = ROOT / 'docs/api'
METHODS = {'get', 'post', 'put', 'patch', 'delete', 'head', 'options'}


def load(path):
    return yaml.safe_load((API / path).read_text())


def documented_routes(module):
    routes = set()
    for path in (API / module).glob('*.yaml'):
        for route, item in yaml.safe_load(path.read_text())['paths'].items():
            routes.update((method, route) for method in item if method in METHODS)
    return routes


def python_routes(folder):
    routes = set()
    for path in folder.glob('*.py'):
        if path.name == 'private_route.py':
            continue
        for node in ast.walk(ast.parse(path.read_text())):
            if not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                continue
            for decorator in node.decorator_list:
                if not isinstance(decorator, ast.Call) or not isinstance(decorator.func, ast.Attribute):
                    continue
                if decorator.func.attr not in METHODS or not decorator.args:
                    continue
                if any(k.arg == 'include_in_schema' and isinstance(k.value, ast.Constant)
                       and k.value.value is False for k in decorator.keywords):
                    continue
                routes.add((decorator.func.attr, decorator.args[0].value))
    return routes


class DocumentationContracts(unittest.TestCase):
    def test_model_factory_public_routes(self):
        for module in ('mf-model-manager', 'mf-model-api'):
            with self.subTest(module=module):
                self.assertEqual(python_routes(ROOT / 'infra' / module / 'app/routers'),
                                 documented_routes(module))

    def test_capabilities_lab_routes(self):
        source = (ROOT / 'adp/execution-factory/operator-integration/server/'
                  'capabilitieslab/handler/capabilities.go').read_text()
        expected = set()
        for group, method, path in re.findall(
                r'(group|cap)\.(GET|POST|PUT|PATCH|DELETE)\("([^"]*)", h\.', source):
            path = ('/capabilities/:id' if group == 'cap' else '') + path
            expected.add((method.lower(), re.sub(r':(\w+)', r'{\1}', path)))
        spec = load('execution-factory/capabilities-lab.yaml')
        actual = {(m, p) for p, item in spec['paths'].items() for m in item}
        self.assertEqual(expected, actual)

    def test_producer_outbox_routes_and_shared_contract(self):
        for module in ('bkn', 'ontology-query'):
            spec = load(module + '/trace-outbox.yaml')
            self.assertEqual(set(spec['paths']), {'/trace/outbox', '/trace/outbox/{outbox_id}',
                             '/trace/outbox/{outbox_id}/retry', '/trace/outbox/{outbox_id}/abandon'})
        schemas = load('_shared/trace-outbox.yaml')['components']['schemas']
        self.assertNotIn('last_error_code', schemas['Summary']['required'])
        self.assertIn('envelope', schemas['Detail']['required'])

    def test_object_detail_omission(self):
        source = (ROOT / 'adp/bkn/bkn-backend/server/interfaces/object_type.go').read_text()
        self.assertIn('json:"data_properties,omitempty"', source)
        schema = load('bkn/object-type.yaml')['components']['schemas']['ObjectTypeDetail']
        self.assertNotIn('data_properties', schema['required'])

    def test_sandbox_request_contract(self):
        schemas = load('sandbox-control-plane/sandbox-control-plane.yaml')['components']['schemas']
        self.assertNotIn('template_id', schemas['CreateSessionRequest'].get('required', []))
        self.assertIn({'type': 'null'}, schemas['CreateSessionRequest']['properties']['template_id']['anyOf'])
        self.assertIn('env_vars', schemas['ExecuteCodeRequest']['properties'])
        timeout = schemas['InstallSessionDependenciesRequest']['properties']['install_timeout']
        self.assertEqual((timeout['default'], timeout['minimum'], timeout['maximum']), (300, 30, 1800))

    def test_sandbox_copies_stay_synchronized(self):
        spec = load('sandbox-control-plane/sandbox-control-plane.yaml')
        native = ROOT / 'infra/sandbox/docs/api/rest'
        self.assertEqual(spec, json.loads((native / 'sandbox-openapi.json').read_text()))
        sync = yaml.safe_load((native / 'execute-sync-openapi.yaml').read_text())
        for path, operation in sync['paths'].items():
            self.assertEqual(spec['paths'][path], operation)
        for name, schema in sync['components']['schemas'].items():
            self.assertEqual(spec['components']['schemas'][name], schema)

    def test_executor_acknowledgement_matches_handler(self):
        source = ROOT / 'infra/sandbox/runtime/executor/interfaces/http/rest.py'
        tree = ast.parse(source.read_text())
        handler = next(n for n in ast.walk(tree) if isinstance(n, ast.AsyncFunctionDef)
                       and n.name == 'execute_endpoint')
        # The ID is dynamic; fixed status/message are literal dictionary entries.
        response = next(n.value for n in handler.body if isinstance(n, ast.Return))
        literals = {k.value: v.value for k, v in zip(response.keys, response.values)
                    if isinstance(v, ast.Constant)}
        spec = load('sandbox-executor/sandbox-executor.yaml')
        schema = spec['paths']['/execute']['post']['responses']['200']['content']['application/json']['schema']
        for key in ('status', 'message'):
            self.assertEqual(schema['properties'][key]['enum'], [literals[key]])
        self.assertNotIn('/info', spec['paths'])
        self.assertIn('/internal/session-config/sync', spec['paths'])

    def test_audit_cursor_envelopes_are_distinct(self):
        vega = load('vega/operation-audit.yaml')['components']['schemas']['Page']['properties']
        execution = load('execution-factory/operation-audit.yaml')['components']['schemas']['Page']['properties']
        self.assertIn('next_before_time', vega)
        self.assertNotIn('next_before_time', execution)


if __name__ == '__main__':
    unittest.main()
