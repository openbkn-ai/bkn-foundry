"""契约漂移守卫（#212）：docs/api/bkn-agent.yaml 与实现不一致视为 bug。"""
import json
from pathlib import Path

import yaml

from app.main import app

SPEC_PATH = Path(__file__).resolve().parents[4] / "docs" / "api" / "bkn-agent.yaml"


def test_frozen_spec_matches_app():
    frozen = yaml.safe_load(SPEC_PATH.read_text(encoding="utf-8"))
    live = json.loads(json.dumps(app.openapi()))
    assert live == frozen, (
        "契约漂移：spec 先行——先改 docs/api/bkn-agent.yaml 评审，"
        "实现对齐后运行 `python scripts/export_openapi.py` 重新导出提交。"
    )


def test_spec_is_openapi_31():
    frozen = yaml.safe_load(SPEC_PATH.read_text(encoding="utf-8"))
    assert frozen["openapi"].startswith("3.1")
    assert frozen["info"]["title"] == "bkn-agent"
