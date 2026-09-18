#!/usr/bin/env bash
# Focused regression test for --latest main-build tag recognition.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

python3 - "${script_dir}/gen-dev-manifest.sh" <<'PY'
import re
import sys

source = open(sys.argv[1]).read()
match = re.search(r"MAIN_BUILD=re\.compile\(r'([^']+)'\)", source)
assert match, "MAIN_BUILD pattern not found"
pattern = re.compile(match.group(1))

assert pattern.fullmatch("0.1.5-main.20260918003916.sha1643ce0")
assert not pattern.fullmatch(
    "0.1.5-feature-1382-map-enum-string-main.20260908180557.sha12df9ec"
)
assert not pattern.fullmatch("0.1.5-fix-main.20260918003916.sha1643ce0")
PY
