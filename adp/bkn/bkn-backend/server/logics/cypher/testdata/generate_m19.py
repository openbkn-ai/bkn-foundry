# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Export the pinned M19 inventory and independently decoded tableless fixtures.

Usage: python3 generate_m19.py /path/to/tck-M19.zip
The archive is an input artifact, never a product or test-time network dependency.
"""
import ast
import hashlib
import json
from pathlib import Path
import re
import sys
import zipfile

SHA256 = "93d3ea6dcf386a64754bb561936a7035c3b429f81b9bc091b809061b4ea0d9bd"


def cells(line):
    text = line.strip()[1:-1]
    result, current = [], ""
    index = 0
    while index < len(text):
        char = text[index]
        if char == "\\" and index + 1 < len(text) and text[index + 1] in "\\|n":
            index += 1
            current += "\n" if text[index] == "n" else text[index]
        elif char == "|":
            result.append(current.strip())
            current = ""
        else:
            current += char
        index += 1
    result.append(current.strip())
    return result


def table_after(text, marker):
    match = re.search(marker + r"[^\n]*\n((?:[ \t]*\|[^\n]*\n?)+)", text)
    return [cells(line) for line in match.group(1).splitlines()] if match else None


class LiteralReader:
    """Read TCK scalar/list/map values without using the implementation under test."""
    tokens = re.compile(r"\s*(?:(?P<string>'(?:\\.|[^'\\])*'|\"(?:\\.|[^\"\\])*\")|(?P<number>[-+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][-+]?[0-9]+)?)|(?P<name>[A-Za-z_][A-Za-z_0-9]*)|(?P<symbol>[^\s]))", re.S)

    def __init__(self, text):
        self.items = [(m.lastgroup, m.group(m.lastgroup)) for m in self.tokens.finditer(text)]
        self.index = 0

    def take(self):
        item = self.items[self.index]
        self.index += 1
        return item

    def read(self):
        kind, token = self.take()
        if kind == "string":
            return ast.literal_eval(token)
        if kind == "number":
            return float(token) if any(c in token for c in ".eE") else int(token)
        if kind == "name":
            return {"null": None, "true": True, "false": False}[token]
        if token == "[":
            result = []
            if self.items[self.index][1] != "]":
                while True:
                    result.append(self.read())
                    if self.items[self.index][1] == "]":
                        break
                    assert self.take()[1] == ","
            assert self.take()[1] == "]"
            return result
        if token == "{":
            result = {}
            if self.items[self.index][1] != "}":
                while True:
                    kind, key = self.take()
                    if kind == "string":
                        key = ast.literal_eval(key)
                    assert self.take()[1] == ":"
                    result[key] = self.read()
                    if self.items[self.index][1] == "}":
                        break
                    assert self.take()[1] == ","
            assert self.take()[1] == "}"
            return result
        raise ValueError("value requires a graph or temporal adapter")


def literal(text):
    reader = LiteralReader(text)
    result = reader.read()
    assert reader.index == len(reader.items)
    return result


def cypher_code(text):
    # Preserve only code for keyword/model-contract classification. Words in
    # strings, comments and escaped identifiers are not language constructs.
    return re.sub(r"'(?:\\.|''|[^'\\])*'|\"(?:\\.|\"\"|[^\"\\])*\"|`(?:``|[^`])*`|//[^\n]*|/\*.*?\*/", " ", text, flags=re.S)


def contract_applicability(query, expanded):
    """Only prove exclusions from explicit fixture syntax; never infer a pass."""
    setup = re.findall(r'(?:Given|And) having executed:\s+"""(.*?)"""', expanded, re.S)
    setup = [cypher_code(statement) for statement in setup]
    query = cypher_code(query)
    text = "\n".join(setup + [query])
    if re.search(r"\b(?:CREATE|MERGE|DELETE|SET|REMOVE)\b", query, re.I):
        return {"status": "outside_read_only_scope", "reason": "query_writes_graph"}
    if "should be raised" not in expanded:
        if re.search(r"\(\s*(?:[A-Za-z_]\w*)?\s*:\s*[A-Za-z_]\w*\s*:\s*[A-Za-z_]\w*", text):
            return {"status": "not_applicable", "reason": "multiple_independent_node_labels"}
        if re.search(r"-\[[^]\n]*\{", "\n".join(setup)):
            return {"status": "not_applicable", "reason": "relationship_owned_properties"}
        edges = re.findall(r"\(([A-Za-z_]\w*)\)\s*-\[\s*(?:[A-Za-z_]\w*)?\s*:\s*([A-Za-z_]\w*)\s*\]\s*->\s*\(([A-Za-z_]\w*)\)", "\n".join(setup))
        if len(edges) != len(set(edges)):
            return {"status": "not_applicable", "reason": "parallel_relationship_instances"}
    return {"status": "requires_adapter_review"}


def main():
    raw = Path(sys.argv[1]).read_bytes()
    assert hashlib.sha256(raw).hexdigest() == SHA256, "unexpected M19 archive"
    provenance = {
        "source": "https://s3.amazonaws.com/artifacts.opencypher.org/M19/tck-M19.zip",
        "sha256": SHA256,
        "license": "Apache-2.0; Copyright (c) 2015-2022 Neo Technology / openCypher contributors",
        "comparison": "Result values and row multiplicity; not a full TCK error-taxonomy or connector certification.",
    }
    inventory, fixtures = [], []
    declarations = 0
    with zipfile.ZipFile(sys.argv[1]) as archive:
        for source in sorted(archive.namelist()):
            if not source.endswith(".feature"):
                continue
            feature = archive.read(source).decode()
            blocks = re.split(r"(?=^  Scenario(?: Outline)?:)", feature, flags=re.M)[1:]
            for block in blocks:
                declarations += 1
                scenario = block.splitlines()[0].strip()
                examples = []
                for match in re.finditer(r"Examples:[^\n]*\n((?:[ \t]*\|[^\n]*\n?)+)", block):
                    table = [cells(line) for line in match.group(1).splitlines()]
                    examples.extend(dict(zip(table[0], row)) for row in table[1:])
                if "Scenario Outline:" not in scenario:
                    examples = [{}]
                for example_index, example in enumerate(examples):
                    expanded = block
                    for key, value in example.items():
                        expanded = expanded.replace("<" + key + ">", value)
                    item = {"source": source, "scenario": scenario, "example": example_index + 1}
                    queries = re.findall(r"(?:When|And) executing (?:query|control query):\s+\"\"\"(.*?)\"\"\"", expanded, re.S)
                    if len(queries) != 1:
                        item["classification"] = "requires_multi_step_adapter"
                    else:
                        query = queries[0].strip()
                        item["query"] = query
                        if not re.match(r"(?:RETURN|WITH|UNWIND)\b", query, re.I) or re.search(r"\b(?:MATCH|CREATE|MERGE|DELETE|SET|REMOVE|CALL)\b", query, re.I):
                            item["classification"] = "requires_graph_or_write_adapter"
                        else:
                            try:
                                params = table_after(expanded, r"(?:And|Given) parameters are:")
                                parameters = {row[0]: literal(row[1]) for row in params} if params else {}
                                error = re.search(r"Then (?:a|an) (\w+) should be raised at (compile time|runtime):\s*(\w+)", expanded)
                                result = table_after(expanded, r"Then the result should be")
                                fixture = dict(item, query=query, parameters=parameters)
                                if error:
                                    fixture["expected_error"] = {"type": error.group(1), "phase": error.group(2), "detail": error.group(3)}
                                elif result:
                                    fixture["columns"] = result[0]
                                    fixture["expected"] = [dict(zip(result[0], [literal(cell) for cell in row])) for row in result[1:]]
                                    fixture["ignore_list_order"] = ("ignoring list order" in expanded or "ignoring element order for lists" in expanded)
                                    fixture["ordered"] = "Then the result should be, in order:" in expanded
                                else:
                                    raise ValueError("unrecognized expectation")
                                json.dumps(fixture, allow_nan=False)
                                fixtures.append(fixture)
                                item["classification"] = "tableless_fixture"
                            except (AssertionError, IndexError, KeyError, SyntaxError, ValueError):
                                item["classification"] = "requires_value_or_expectation_adapter"
                    item["contract_applicability"] = contract_applicability(item.get("query", ""), expanded)
                    if item["classification"] == "tableless_fixture":
                        item["contract_applicability"] = {"status": "tableless_fixture"}
                    inventory.append(item)
    out = Path(__file__).parent
    summary = {"scenario_declarations": declarations, "expanded_scenarios": len(inventory), "tableless_fixtures": len(fixtures)}
    (out / "tck_m19_inventory.json").write_text(json.dumps(dict(provenance, summary=summary, cases=inventory), ensure_ascii=False, indent=2) + "\n")
    (out / "tck_m19_tableless.json").write_text(json.dumps(dict(provenance, cases=fixtures), ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(summary))


if __name__ == "__main__":
    main()
