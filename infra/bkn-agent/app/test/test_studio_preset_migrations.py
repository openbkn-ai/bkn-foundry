"""Validate both installation paths using the shared migrator's real selector."""

import json
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
MIGRATIONS = ROOT / "migrations/bkn-agent/mariadb"
DELTA = MIGRATIONS / "0.2.1/01-agent-preset-bootstrap.sql"


def test_shared_migrator_selects_fresh_snapshot_and_upgrade_delta():
    script = """
import json
import logging
import sys
from server.config.models import AppConfig, RDSConfig
from server.migrate.script_selector import ScriptSelector

config = AppConfig(
    rds=RDSConfig('unused', 3306, 'unused', '', 'mariadb', 'internal'),
    repo_path=sys.argv[1],
)
selector = ScriptSelector(config, logging.getLogger('test'))
print(json.dumps({
    'fresh': selector.find_init_sql('bkn-agent'),
    'upgrade': selector.select_upgrade_scripts('bkn-agent', '0.2.0'),
}))
"""
    result = subprocess.run(
        [sys.executable, "-c", script, str(ROOT / "migrations")],
        cwd=ROOT / "data-migrator",
        capture_output=True,
        text=True,
        check=True,
    )
    selected = json.loads(result.stdout)
    assert selected["fresh"] == [str(MIGRATIONS / "0.2.1/init.sql"), "0.2.1"]
    assert selected["upgrade"] == [[["0.2.1", [str(DELTA)]]], "0.2.1", True]


def test_fresh_snapshot_preserves_previous_schema_and_adds_preset_tables():
    previous = (MIGRATIONS / "0.2.0/init.sql").read_text()
    current = (MIGRATIONS / "0.2.1/init.sql").read_text()
    # Preserve existing tables, checkpoint schema and legacy seed content.
    assert current.startswith(previous.rstrip() + "\n")
    assert current[len(previous.rstrip()) :].strip() == DELTA.read_text().strip()
    for name in (
        "t_agent_preset_version",
        "t_agent_preset_installation",
        "t_agent_scenario_binding",
    ):
        assert f"create table if not exists {name}" in current
