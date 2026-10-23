# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Opt-in migration acceptance; creates and drops only isolated test databases."""

import json
import os
import uuid

import pymysql
import pytest

from app.test.test_shared_agent_seeds import CLAIM_LOCALES, MIGRATIONS, statements


@pytest.mark.skipif(not os.getenv("BKN_AGENT_TEST_MARIADB_HOST"), reason="isolated MariaDB not configured")
@pytest.mark.parametrize("upgrade", ["fresh", "0.2.0", "0.2.1"])
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
        if upgrade == "0.2.0":
            apply(MIGRATIONS / "0.2.0/init.sql")
            for path in sorted((MIGRATIONS / "0.2.1").glob("[0-9][0-9]-*.sql")):
                apply(path)
        elif upgrade == "0.2.1":
            # Reconstruct the already-installed 021 schema, without the new delta.
            old_init = (MIGRATIONS / "0.2.1/init.sql").read_text().removesuffix(CLAIM_LOCALES.read_text())
            import tempfile
            from pathlib import Path
            with tempfile.TemporaryDirectory() as tmp:
                old_path = Path(tmp) / "init.sql"
                old_path.write_text(old_init)
                apply(old_path)
            with db.cursor() as cursor:
                with pytest.raises(pymysql.err.OperationalError, match="Unknown column"):
                    cursor.execute("SELECT f_localized_content FROM t_agent_prompt_version LIMIT 0")
            # Existing 021 does not auto-discover the delta: operator applies it.
            apply(CLAIM_LOCALES)
        else:
            apply(MIGRATIONS / "0.2.1/init.sql")
        with db.cursor() as cursor:
            cursor.execute("SELECT f_localized_content FROM t_agent_prompt_version LIMIT 0")
            assert cursor.fetchall() == ()
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
    finally:
        with db.cursor() as cursor:
            cursor.execute(f"DROP DATABASE IF EXISTS `{name}`")
        db.close()
