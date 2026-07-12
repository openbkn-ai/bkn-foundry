#!/usr/bin/env python
"""导出冻结契约 docs/api/agent-runtime.yaml（#212 spec 先行流程）。

用法（infra/agent-runtime 下）：python scripts/export_openapi.py
改 API 后必须重跑本脚本并将 spec diff 一并提交，否则 test_contract.py 红。
"""
import json
import sys
from pathlib import Path

import yaml

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app.main import app  # noqa: E402

OUT = Path(__file__).resolve().parents[3] / "docs" / "api" / "agent-runtime.yaml"


def main() -> None:
    spec = json.loads(json.dumps(app.openapi()))
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(yaml.safe_dump(spec, allow_unicode=True, sort_keys=False), encoding="utf-8")
    print(f"wrote {OUT} (openapi {spec['openapi']}, version {spec['info']['version']})")


if __name__ == "__main__":
    main()
