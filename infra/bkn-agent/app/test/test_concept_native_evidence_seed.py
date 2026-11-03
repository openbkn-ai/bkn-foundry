# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Native concept citations select verified fragments rather than copied quotes."""

import json

import pytest

from app.test.test_shared_agent_seeds import (
    CONCEPT_AGENT, CONCEPT_BUDGET, CONCEPT_SEED, MIGRATIONS,
    apply, concept_seed_database, database, item_from_db, snapshot,
)

NATIVE_EVIDENCE = MIGRATIONS / "0.2.1/40-concept-native-evidence.sql"


def test_native_concept_upgrade_keeps_history_and_replays_without_changes():
    with concept_seed_database() as db:
        apply(db, CONCEPT_SEED)
        apply(db, CONCEPT_BUDGET)
        old = [dict(row) for row in db.execute("SELECT * FROM t_agent_version WHERE f_agent_id=? ORDER BY f_version", (CONCEPT_AGENT,))]
        apply(db, NATIVE_EVIDENCE)
        rows = [dict(row) for row in db.execute("SELECT * FROM t_agent_version WHERE f_agent_id=? ORDER BY f_version", (CONCEPT_AGENT,))]
        assert len(rows) == 3
        assert (rows[2]["f_version"], rows[2]["f_prompt_version"], rows[2]["f_current"], rows[2]["f_initial"]) == (3, 2, 1, 1)
        for previous, retained in zip(old, rows):
            assert all(retained[key] == value for key, value in previous.items() if key not in {"f_current", "f_initial"})
            assert not retained["f_current"] and not retained["f_initial"]
        prompt = db.execute("SELECT * FROM t_agent_prompt_version WHERE f_prompt_id=? AND f_version=2", (CONCEPT_AGENT+"-prompt",)).fetchone()
        locales = json.loads(prompt["f_localized_content"])
        assert locales["en-US"] == prompt["f_content"]
        assert set(locales) == {"zh-CN", "en-US"}
        assert "sample_id" in prompt["f_content"] and "only" in prompt["f_content"]
        assert "Never output quote" in prompt["f_content"]
        assert item_from_db(db, CONCEPT_AGENT)["spec"]["limits"]["timeout_s"] == 300
        before = snapshot(db)
        apply(db, NATIVE_EVIDENCE)
        assert snapshot(db) == before


@pytest.mark.parametrize("custom", ["body", "translation", "prompt_head", "model", "limits", "tools", "skills", "agent_version", "operator_prompt_version"])
def test_native_concept_upgrade_preserves_customized_configuration(custom):
    with concept_seed_database() as db:
        apply(db, CONCEPT_SEED)
        apply(db, CONCEPT_BUDGET)
        if custom == "body":
            db.execute("UPDATE t_agent_prompt_version SET f_content='operator prompt' WHERE f_prompt_id=?", (CONCEPT_AGENT+"-prompt",))
        elif custom == "translation":
            db.execute("UPDATE t_agent_prompt_version SET f_localized_content='{}' WHERE f_prompt_id=?", (CONCEPT_AGENT+"-prompt",))
        elif custom == "prompt_head":
            db.execute("UPDATE t_agent_prompt SET f_update_user='operator' WHERE f_prompt_id=?", (CONCEPT_AGENT+"-prompt",))
        elif custom == "agent_version":
            db.execute("INSERT INTO t_agent_version SELECT f_agent_id,3,f_model,f_tools,f_limits,f_prompt_version,f_changes,0,0,0,'operator',1000 FROM t_agent_version WHERE f_agent_id=? AND f_version=2", (CONCEPT_AGENT,))
        elif custom == "operator_prompt_version":
            db.execute("INSERT INTO t_agent_prompt_version SELECT f_prompt_id,2,f_content,f_vars_schema,'operator',1000,f_source_language,f_localized_content FROM t_agent_prompt_version WHERE f_prompt_id=? AND f_version=1", (CONCEPT_AGENT+"-prompt",))
        else:
            field, value = {"model":("f_model", "custom-model"), "limits":("f_limits", '{"timeout_s":90}'), "tools":("f_tools", '[{"type":"custom"}]'), "skills":("f_skills", '[{"id":"custom"}]')}[custom]
            db.execute(f"UPDATE t_agent SET {field}=? WHERE f_agent_id=?", (value, CONCEPT_AGENT))
        before = snapshot(db)
        apply(db, NATIVE_EVIDENCE)
        assert snapshot(db) == before


def test_native_concept_fresh_snapshot_is_current_and_migration_is_noop():
    with database() as db:
        apply(db, MIGRATIONS / "0.2.1/init.sql")
        current = db.execute("SELECT * FROM t_agent_version WHERE f_agent_id=? AND f_current=1", (CONCEPT_AGENT,)).fetchone()
        assert (current["f_version"], current["f_prompt_version"]) == (3, 2)
        before = snapshot(db)
        apply(db, NATIVE_EVIDENCE)
        assert snapshot(db) == before


@pytest.mark.parametrize("limit", range(1, 9))
def test_native_concept_upgrade_recovers_compatible_partial_seed(limit):
    with concept_seed_database() as db, concept_seed_database() as expected:
        for connection in (db, expected):
            apply(connection, CONCEPT_SEED)
            apply(connection, CONCEPT_BUDGET)
        apply(expected, NATIVE_EVIDENCE)
        apply(db, NATIVE_EVIDENCE, limit=limit)
        apply(db, NATIVE_EVIDENCE)
        assert snapshot(db) == snapshot(expected)


def test_native_concept_upgrade_preserves_explicit_operator_rollback():
    with concept_seed_database() as db:
        apply(db, CONCEPT_SEED)
        apply(db, CONCEPT_BUDGET)
        apply(db, NATIVE_EVIDENCE)
        db.execute("UPDATE t_agent_version SET f_current=(f_version=2) WHERE f_agent_id=?", (CONCEPT_AGENT,))
        db.execute("UPDATE t_agent_prompt SET f_current_version=1,f_update_user='operator',f_update_time=5000 WHERE f_prompt_id=?", (CONCEPT_AGENT+"-prompt",))
        before = snapshot(db)
        apply(db, NATIVE_EVIDENCE)
        assert snapshot(db) == before
