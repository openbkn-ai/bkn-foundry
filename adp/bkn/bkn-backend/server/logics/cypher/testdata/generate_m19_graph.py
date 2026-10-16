# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Independent, deliberately bounded CREATE-fixture decoder for M19 graph tests.

It decodes source fixtures only. It never evaluates a query or derives a result.
Unsupported preparation is recorded for review rather than silently approximated.
"""
import re
import json
from generate_m19 import LiteralReader, literal, table_after, cypher_code


class FixtureUnsupported(ValueError):
    pass


class GraphReader(LiteralReader):
    def __init__(self, text):
        # Keep quoted strings intact while removing comments from fixture code.
        strings_or_comments = r"'(?:\\.|''|[^'\\])*'|\"(?:\\.|\"\"|[^\"\\])*\"|//[^\n]*|/\*.*?\*/"
        super().__init__(re.sub(strings_or_comments, lambda m: ' ' if m[0].startswith(('//', '/*')) else m[0], text, flags=re.S))
        self.nodes, self.edges, self.variables = [], [], {}

    def peek(self):
        return self.items[self.index][1] if self.index < len(self.items) else None

    def expect(self, text):
        if self.take()[1].upper() != text:
            raise FixtureUnsupported('unsupported preparation syntax')

    def node(self):
        self.expect('(')
        name, labels, props = None, [], {}
        if self.peek() not in (':', '{', ')'):
            kind, name = self.take()
            if kind != 'name':
                raise FixtureUnsupported('escaped fixture names require another adapter')
        while self.peek() == ':':
            self.take()
            kind, label = self.take()
            if kind != 'name':
                raise FixtureUnsupported('unsupported label')
            labels.append(label)
        if self.peek() == '{':
            props = self.read()
        self.expect(')')
        if name in self.variables:
            if labels or props:
                raise FixtureUnsupported('redeclared CREATE variable')
            return self.variables[name]
        if len(labels) > 1:
            raise FixtureUnsupported('multiple independent labels')
        if any(isinstance(v, (list, dict)) for v in props.values()):
            raise FixtureUnsupported('non-scalar source properties require a value adapter')
        value = len(self.nodes)
        self.nodes.append({'identity': str(value), 'type': labels[0] if labels else '__tck_node', 'properties': props})
        if name:
            self.variables[name] = value
        return value

    def graph(self):
        while self.peek() is not None:
            self.expect('CREATE')
            while True:
                if self.peek() != '(':
                    kind, _ = self.take()
                    if kind != 'name':
                        raise FixtureUnsupported('unsupported named fixture path')
                    self.expect('=')
                left = self.node()
                while self.peek() in ('-', '<'):
                    incoming = self.peek() == '<'
                    if incoming:
                        self.take()
                    self.expect('-')
                    self.expect('[')
                    if self.peek() != ':':
                        kind, _ = self.take()
                        if kind != 'name':
                            raise FixtureUnsupported('unsupported relationship variable')
                    self.expect(':')
                    kind, typ = self.take()
                    if kind != 'name' or self.peek() != ']':
                        raise FixtureUnsupported('relationship attributes or missing type')
                    self.expect(']')
                    self.expect('-')
                    if not incoming:
                        self.expect('>')
                    right = self.node()
                    source, target = (right, left) if incoming else (left, right)
                    self.edges.append({'type': typ, 'source': source, 'target': target})
                    left = right
                if self.peek() != ',':
                    break
                self.take()
            if self.peek() == ';':
                self.take()
                if self.peek() is not None:
                    raise FixtureUnsupported('multiple statements require explicit variable scopes')
        return {'nodes': self.nodes, 'edges': self.edges}


def graph_fixture(item, expanded):
    query = item.get('query', '')
    if not query or item['contract_applicability']['status'] != 'requires_adapter_review':
        return None
    code = cypher_code(query)
    if re.search(r'\b(?:CREATE|MERGE|DELETE|SET|REMOVE|CALL)\b', code, re.I):
        return None
    fixture = dict(item)
    try:
        params = table_after(expanded, r'(?:And|Given) parameters are:')
        fixture['parameters'] = {row[0]: literal(row[1]) for row in params} if params else {}
        # A negative query still needs exact type/phase comparison in its own adapter.
        if 'should be raised' in expanded:
            return None
        result = table_after(expanded, r'Then the result should be')
        if not result:
            return None
        fixture['columns'] = result[0]
        fixture['expected'] = [dict(zip(result[0], [literal(cell) for cell in row])) for row in result[1:]]
        fixture['ordered'] = 'Then the result should be, in order:' in expanded
        fixture['ignore_list_order'] = 'ignoring list order' in expanded or 'ignoring element order for lists' in expanded
        setup = re.findall(r'(?:Given|And) having executed:\s+"""(.*?)"""', expanded, re.S)
        if len(setup) > 1:
            raise FixtureUnsupported('multiple preparation statements require explicit scope handling')
        if not re.search(r'(?m)^\s*Given an empty graph\s*$', expanded):
            raise FixtureUnsupported('unspecified initial graph')
        fixture['graph'] = GraphReader(setup[0]).graph() if setup else {'nodes': [], 'edges': []}
        json.dumps(fixture, allow_nan=False)
        fixture['adapter_status'] = 'candidate'
    except (ValueError, KeyError, IndexError, AssertionError, SyntaxError) as error:
        fixture['adapter_status'] = 'requires_fixture_adapter'
        fixture['adapter_detail'] = str(error)
    return fixture
