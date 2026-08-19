#!/usr/bin/env python
"""Export the frozen docs/api/bkn-agent/bkn-agent.yaml contract (#212).

Run from infra/bkn-agent with ``python scripts/export_openapi.py``. After an API
change, rerun this script and commit the spec diff or test_contract.py will fail.
"""
import json
import sys
from pathlib import Path

import yaml

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app.main import app  # noqa: E402

OUT = Path(__file__).resolve().parents[3] / "docs" / "api" / "bkn-agent" / "bkn-agent.yaml"


def main() -> None:
    spec = json.loads(json.dumps(app.openapi()))
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(yaml.safe_dump(spec, allow_unicode=True, sort_keys=False), encoding="utf-8")
    print(f"wrote {OUT} (openapi {spec['openapi']}, version {spec['info']['version']})")


if __name__ == "__main__":
    main()
