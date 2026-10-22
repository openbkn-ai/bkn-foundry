# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Tests for tools/check_cypher_docs.py."""

from __future__ import annotations

import json
import shutil
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import check_cypher_docs as check  # noqa: E402


class CheckCypherDocsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.root = Path(tempfile.mkdtemp())
        for place in check.PLACES:
            target = self.root / place.path
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy(check.REPOSITORY_ROOT / place.path, target)

    def tearDown(self) -> None:
        shutil.rmtree(self.root)

    def test_the_repository_agrees(self) -> None:
        self.assertEqual(check.missing(), [])

    def test_a_rule_missing_from_one_place_is_named(self) -> None:
        manual = self.root / "help/en/manual/cypher.md"
        manual.write_text(manual.read_text(encoding="utf-8").replace("!=", "not-equal"), encoding="utf-8")
        self.assertEqual(
            [line.split(":")[0] for line in check.missing(self.root)],
            ["help/en/manual/cypher.md"],
        )

    def test_only_run_cypher_texts_count_in_shared_files(self) -> None:
        # A rule stated for another tool does not satisfy run_cypher.
        path = self.root / f"{check.SCHEMAS}/tools_meta.json"
        texts = json.loads(path.read_text(encoding="utf-8"))
        texts["run_cypher"] = {"description": "nothing"}
        texts["other_tool"] = {"description": "!= 1000 10000"}
        path.write_text(json.dumps(texts, ensure_ascii=False), encoding="utf-8")
        named = {line.split(":")[0] for line in check.missing(self.root)}
        self.assertEqual(named, {f"{check.SCHEMAS}/tools_meta.json"})


if __name__ == "__main__":
    unittest.main()
