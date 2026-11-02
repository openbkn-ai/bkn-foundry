# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Validate the document extractor's additive seed without inference or live SQL."""

import json

import pytest

from app.public_auth import BUILTIN_AGENT_IDS
from app.test import test_shared_agent_seeds as seeds

AGENT = "bkn-project-document-extraction"
SEED = seeds.MIGRATIONS / "0.2.1/39-seed-bkn-project-document-extraction.sql"


def isolated_database():
    db = seeds.database()
    seeds.apply(db, seeds.MIGRATIONS / "0.2.1/init.sql")
    for table, column in (
        ("t_agent_version", "f_agent_id"),
        ("t_agent", "f_agent_id"),
        ("t_agent_prompt_version", "f_prompt_id"),
        ("t_agent_prompt", "f_prompt_id"),
    ):
        db.execute(
            f"DELETE FROM {table} WHERE {column}=?",
            (AGENT if column == "f_agent_id" else AGENT + "-prompt",),
        )
    return db


def test_document_extractor_is_builtin_tool_free_bilingual_and_idempotent():
    with isolated_database() as db:
        seeds.apply(db, SEED)
        item = seeds.item_from_db(db, AGENT)
        assert AGENT in BUILTIN_AGENT_IDS
        assert item["spec"]["mode"] == "task"
        assert item["spec"]["status"] == "published"
        assert item["spec"]["tools"] == item["spec"]["skills"] == []
        assert item["spec"]["limits"] == {
            "max_turns": 1,
            "max_tool_calls": 0,
            "timeout_s": 300,
            "max_output_tokens": 16384,
        }
        row = db.execute(
            "SELECT * FROM t_agent_prompt_version WHERE f_prompt_id=?",
            (AGENT + "-prompt",),
        ).fetchone()
        localized = json.loads(row["f_localized_content"])
        assert row["f_source_language"] == "en-US"
        assert localized["en-US"] == row["f_content"]
        assert set(localized) == {"en-US", "zh-CN"}
        for rule in (
            "one supplied locked file",
            "human-confirmed",
            "uniquely occurring quote",
            "business_number",
            "server derives",
            "independent claims",
            "co-occurrence",
            "Do not fabricate system records",
            "never claim visual facts",
        ):
            assert rule in row["f_content"]
        before = seeds.snapshot(db)
        seeds.apply(db, SEED)
        assert seeds.snapshot(db) == before


@pytest.mark.parametrize("completed", [1, 2, 3])
def test_document_extractor_seed_recovers_partial_initialization(completed):
    with isolated_database() as db, isolated_database() as expected:
        seeds.apply(expected, SEED)
        seeds.apply(db, SEED, limit=completed)
        seeds.apply(db, SEED)
        assert seeds.snapshot(db) == seeds.snapshot(expected)


def test_document_extractor_preserves_customized_agent_and_prompt():
    with isolated_database() as db:
        seeds.apply(db, SEED)
        db.execute("UPDATE t_agent SET f_name='Custom extractor' WHERE f_agent_id=?", (AGENT,))
        db.execute(
            "UPDATE t_agent_prompt_version SET f_content='Custom rules' WHERE f_prompt_id=?",
            (AGENT + "-prompt",),
        )
        before = seeds.snapshot(db)
        seeds.apply(db, SEED)
        assert seeds.snapshot(db) == before
