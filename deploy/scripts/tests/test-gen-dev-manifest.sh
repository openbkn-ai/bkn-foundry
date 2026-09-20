#!/usr/bin/env bash
# Focused regression tests for --latest main-build and branch-build tag recognition.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

python3 - "${script_dir}/gen-dev-manifest.sh" <<'PY'
import ast
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

# Execute the resolver function from the embedded Python source so the test
# covers its actual release-branch behavior without calling GHCR.
embedded = re.search(r"ORG=.*?python3 - <<'PY'\n(.*)\nPY\n", source, re.DOTALL)
assert embedded, "embedded Python resolver not found"
tree = ast.parse(embedded.group(1))
function = next(
    node for node in tree.body
    if isinstance(node, ast.FunctionDef) and node.name == "newest_branch_build"
)
namespace = {"re": re}
exec(compile(ast.fix_missing_locations(ast.Module(body=[function], type_ignores=[])), "<resolver>", "exec"), namespace)
newest_branch_build = namespace["newest_branch_build"]

tags = [
    "0.1.5-release-0.1.5.20260918003916.sha1643ce0",
    "0.1.5-release-0.1.5.20260919003916.sha2aa3ce0",
    "0.1.5-release.20260920003916.sha3bb4ce0",
]
assert newest_branch_build(tags, "release-0.1.5") == tags[1]
assert newest_branch_build(tags, "patch-0.1.6") is None
PY
