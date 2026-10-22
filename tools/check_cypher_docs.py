# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Check that every place describing run_cypher states the same rules.

run_cypher is described in eight places: the two manuals, the two API
definitions and four Context Loader texts agents read. A rule changed in some
of them and not the others has reached agents wrong twice. Each fact below
names a rule as a pattern per language; every place must match it.

Add a fact when a rule is added or changes; this script does not judge
wording, only that the rule is stated everywhere.
"""

from __future__ import annotations

import json
import re
import sys
from dataclasses import dataclass
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
SCHEMAS = "adp/context-loader/agent-retrieval/server/driveradapters/mcp/schemas"


@dataclass(frozen=True)
class Place:
    path: str
    language: str
    # For a JSON file, the top-level key holding run_cypher's texts.
    key: str | None = None


PLACES = (
    Place("help/zh/manual/cypher.md", "zh"),
    Place("help/en/manual/cypher.md", "en"),
    Place("docs/api/bkn/cypher-queries.yaml", "en"),
    Place("docs/api/context-loader/cypher.yaml", "zh"),
    Place(f"{SCHEMAS}/run_cypher.json", "zh"),
    Place(f"{SCHEMAS}/tools_meta.json", "zh", "run_cypher"),
    Place(f"{SCHEMAS}/locales/en-US/tools_meta.json", "en", "run_cypher"),
    Place(f"{SCHEMAS}/locales/en-US/schema_descriptions.json", "en", "run_cypher"),
)


@dataclass(frozen=True)
class Fact:
    name: str
    zh: str
    en: str


FACTS_FILE = REPOSITORY_ROOT / "tools/cypher_doc_facts.json"


def load_facts(path: Path = FACTS_FILE) -> tuple[Fact, ...]:
    """The rules to check, kept in JSON beside this script."""
    data = json.loads(path.read_text(encoding="utf-8"))
    return tuple(Fact(item["name"], item["zh"], item["en"]) for item in data["facts"])


FACTS = load_facts()


def strings(value: object) -> list[str]:
    """Every string inside a JSON value."""
    if isinstance(value, str):
        return [value]
    if isinstance(value, dict):
        return [s for item in value.values() for s in strings(item)]
    if isinstance(value, list):
        return [s for item in value for s in strings(item)]
    return []


def read(place: Place, root: Path) -> str:
    text = (root / place.path).read_text(encoding="utf-8")
    if not place.path.endswith(".json"):
        return text
    value = json.loads(text)
    if place.key is not None:
        value = value[place.key]
    return "\n".join(strings(value))


def missing(root: Path = REPOSITORY_ROOT) -> list[str]:
    """The facts each place does not state, as readable lines."""
    problems = []
    for place in PLACES:
        text = read(place, root)
        for fact in FACTS:
            pattern = fact.zh if place.language == "zh" else fact.en
            if not re.search(pattern, text, re.IGNORECASE | re.DOTALL):
                problems.append(f"{place.path}: does not state {fact.name!r} (pattern {pattern!r})")
    return problems


def main() -> int:
    problems = missing()
    for problem in problems:
        print(problem)
    if problems:
        print(
            f"\n{len(problems)} gap(s). A run_cypher rule must be stated in every place listed "
            "in tools/check_cypher_docs.py; update the ones named above."
        )
        return 1
    print(f"run_cypher texts agree: {len(FACTS)} facts in {len(PLACES)} places.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
