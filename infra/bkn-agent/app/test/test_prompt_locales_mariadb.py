# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Opt-in migration acceptance; creates and drops only isolated test databases."""

import json
import os
import uuid
from pathlib import Path

import pymysql
import pytest

from app.public_auth import BUILTIN_AGENT_IDS
from app.test.test_builtin_prompt_locales import LEGACY_SMART_QA
from app.test.test_shared_agent_seeds import BUILTIN_LOCALES, CLAIM_LOCALES, PROMPT_INDEX, MIGRATIONS, REQUIREMENTS, statements, selected_migrations


@pytest.mark.skipif(not os.getenv("BKN_AGENT_TEST_MARIADB_HOST"), reason="isolated MariaDB not configured")
@pytest.mark.parametrize("upgrade", ["fresh", "0.2.0"])
def test_claim_locale_migration_on_mariadb(upgrade):
    db = pymysql.connect(
        host=os.environ["BKN_AGENT_TEST_MARIADB_HOST"],
        port=int(os.getenv("BKN_AGENT_TEST_MARIADB_PORT", "3306")),
        user=os.getenv("BKN_AGENT_TEST_MARIADB_USER", "root"),
        password=os.getenv("BKN_AGENT_TEST_MARIADB_PASSWORD", ""),
        charset="utf8mb4", autocommit=True,
    )
    name = "bkn_agent_locale_test_" + uuid.uuid4().hex
    def apply(path):
        for statement in statements(path):
            if not statement.lower().startswith("use "):
                with db.cursor() as cursor:
                    cursor.execute(statement)
    try:
        with db.cursor() as cursor:
            cursor.execute(f"CREATE DATABASE `{name}` CHARACTER SET utf8mb4")
            cursor.execute(f"USE `{name}`")
        def saved_rows():
            with db.cursor() as cursor:
                result = []
                for table in ("t_agent", "t_agent_version", "t_agent_prompt", "t_agent_prompt_version"):
                    cursor.execute(f"SELECT * FROM {table} ORDER BY 1, 2")
                    result.append(cursor.fetchall())
                return result
        fresh, selected = selected_migrations(upgrade if upgrade != "fresh" else "0.0.0")
        if upgrade == "fresh":
            assert fresh[1] == "0.2.1"
            apply(Path(fresh[0]))
        else:
            apply(MIGRATIONS / upgrade / "init.sql")
            with db.cursor() as cursor:
                with pytest.raises(pymysql.err.OperationalError, match="Unknown column"):
                    cursor.execute("SELECT f_localized_content FROM t_agent_prompt_version LIMIT 0")
                with pytest.raises(pymysql.err.OperationalError, match="doesn.t exist"):
                    cursor.execute("SELECT f_agent_id FROM t_agent FORCE INDEX (ix_agent_prompt_id_agent_id) LIMIT 0")
            assert selected[1:] == ["0.2.1", True]
            for _, paths in selected[0]:
                for path in paths:
                    apply(Path(path))
        with db.cursor() as cursor:
            cursor.execute("SELECT f_localized_content FROM t_agent_prompt_version LIMIT 0")
            assert cursor.fetchall() == ()
        with db.cursor() as cursor:
            cursor.execute("SELECT f_source_language FROM t_agent_prompt_version LIMIT 0")
        before_locales_rerun = saved_rows()
        apply(BUILTIN_LOCALES)
        apply(BUILTIN_LOCALES)
        apply(REQUIREMENTS)
        apply(REQUIREMENTS)
        assert saved_rows() == before_locales_rerun
        with db.cursor() as cursor:
            cursor.execute("SELECT a.f_agent_id, v.f_content, v.f_localized_content, p.f_current_version FROM t_agent a "
                           "JOIN t_agent_prompt p ON p.f_prompt_id=a.f_prompt_id "
                           "JOIN t_agent_prompt_version v ON v.f_prompt_id=p.f_prompt_id "
                           "AND v.f_version=p.f_current_version")
            rows = cursor.fetchall()
            assert len(rows) == len(BUILTIN_AGENT_IDS)
            for agent_id, canonical, variants, current in rows:
                cursor.execute("SELECT f_prompt_version FROM t_agent_version WHERE f_agent_id=%s AND f_initial=1", (agent_id,))
                initial = cursor.fetchone()
                if variants is not None:
                    variants = json.loads(variants)
                    assert variants.get("zh-CN", canonical) != variants.get("en-US", canonical), agent_id
                else:
                    assert not initial or initial[0] != current, agent_id
            # Preserve main's explicit official v6 upgrade and translations.
            cursor.execute("SELECT f_current_version FROM t_agent_prompt WHERE f_prompt_id='resource-semantic-understanding-prompt'")
            assert cursor.fetchone() == (6,)
        before_index_rerun = saved_rows()
        apply(PROMPT_INDEX)
        apply(PROMPT_INDEX)
        assert saved_rows() == before_index_rerun
        with db.cursor() as cursor:
            cursor.execute("SELECT COLUMN_NAME, NON_UNIQUE, INDEX_TYPE FROM information_schema.STATISTICS "
                           "WHERE TABLE_SCHEMA=%s AND TABLE_NAME='t_agent' "
                           "AND INDEX_NAME='ix_agent_prompt_id_agent_id' ORDER BY SEQ_IN_INDEX", (name,))
            assert cursor.fetchall() == (("f_prompt_id", 1, "BTREE"), ("f_agent_id", 1, "BTREE"))
        query = "SELECT f_content, f_localized_content FROM t_agent_prompt_version WHERE f_prompt_id='business_provenance_claim_attribution_prompt' AND f_version=1"
        with db.cursor() as cursor:
            cursor.execute(query)
            canonical, localized = cursor.fetchone()
            assert canonical.startswith("你是 OpenBKN")
            assert json.loads(localized)["en-US"].startswith("You are the OpenBKN")
        # Runtime ORM/imports can store JSON null instead of SQL NULL.
        with db.cursor() as cursor:
            cursor.execute("UPDATE t_agent_prompt_version SET f_vars_schema='null', f_localized_content=NULL WHERE f_prompt_id='business_provenance_claim_attribution_prompt'")
        apply(CLAIM_LOCALES)
        with db.cursor() as cursor:
            cursor.execute(query)
            assert cursor.fetchone() == (canonical, localized)
        apply(CLAIM_LOCALES)
        with db.cursor() as cursor:
            cursor.execute(query)
            assert cursor.fetchone() == (canonical, localized)
            cursor.execute("UPDATE t_agent_prompt_version SET f_content='Custom admin content', f_localized_content=NULL WHERE f_prompt_id='business_provenance_claim_attribution_prompt'")
            cursor.execute("INSERT INTO t_agent_prompt_override VALUES ('business_provenance_claim_attribution','test-user','Custom user content',1)")
        apply(CLAIM_LOCALES)
        with db.cursor() as cursor:
            cursor.execute(query)
            assert cursor.fetchone() == ("Custom admin content", None)
            cursor.execute("SELECT f_content FROM t_agent_prompt_override WHERE f_account_id='test-user'")
            assert cursor.fetchone()[0] == "Custom user content"
        # Existing Initial configurations can reference an older official Prompt body.
        with db.cursor() as cursor:
            for version, creator in ((103, "266c6a42-6131-4d62-8f39-853e7093701c"), (104, "administrator"), (105, "266c6a42-6131-4d62-8f39-853e7093701c")):
                cursor.execute("INSERT INTO t_agent_prompt_version (f_prompt_id,f_version,f_content,f_create_user,f_create_time) VALUES (%s,%s,%s,%s,%s)", ("studio_smart_qa_prompt", version, LEGACY_SMART_QA, creator, 1))
            cursor.execute("UPDATE t_agent_prompt SET f_current_version=103 WHERE f_prompt_id='studio_smart_qa_prompt'")
            cursor.execute("UPDATE t_agent_version SET f_prompt_version=103 WHERE f_agent_id='studio_smart_qa' AND f_initial=1")
        before_legacy = saved_rows()
        apply(BUILTIN_LOCALES)
        with db.cursor() as cursor:
            cursor.execute("SELECT f_version,f_content,f_localized_content FROM t_agent_prompt_version WHERE f_prompt_id='studio_smart_qa_prompt' AND f_version IN (103,104,105) ORDER BY f_version")
            official, custom, non_initial = cursor.fetchall()
            assert official[1] == LEGACY_SMART_QA
            assert json.loads(official[2])["en-US"].startswith("You are a business knowledge network Q&A assistant")
            assert custom == (104, LEGACY_SMART_QA, None)
            assert non_initial == (105, LEGACY_SMART_QA, None)
        after_legacy = saved_rows()
        assert before_legacy[:3] == after_legacy[:3]
        apply(BUILTIN_LOCALES)
        assert saved_rows() == after_legacy
    finally:
        with db.cursor() as cursor:
            cursor.execute(f"DROP DATABASE IF EXISTS `{name}`")
        db.close()
