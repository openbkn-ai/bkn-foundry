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
functions = [
    node for node in tree.body
    if isinstance(node, ast.FunctionDef)
    and node.name in {"sanitize", "branch_channel", "newest_branch_build"}
]
namespace = {"re": re}
exec(compile(ast.fix_missing_locations(ast.Module(body=functions, type_ignores=[])), "<resolver>", "exec"), namespace)
newest_branch_build = namespace["newest_branch_build"]
branch_channel = namespace["branch_channel"]

assert branch_channel("release/0.1.5") == ("release", "0.1.5")
assert branch_channel("main") == ("main", None)
assert branch_channel("feature/my-work") == ("feature-my-work", None)

tags = [
    "0.1.5-release.20260918003916.sha1643ce0",
    "0.1.5-release.20260919003916.sha2aa3ce0",
    "0.1.4-release.20260920003916.sha3bb4ce0",
    "0.1.5-release-0.1.5.20260919013916.sha4cc5ce0",
]
assert newest_branch_build(tags, "release", "0.1.5") == tags[3]
assert newest_branch_build(tags, "release", "0.1.4") == tags[2]
assert newest_branch_build(tags[:3], "release", "0.1.5") == tags[1]
assert newest_branch_build(tags, "feature-unrelated") is None
legacy = "0.1.5-release.sha1643ce0"
assert newest_branch_build([legacy], "release", "0.1.5") == legacy
assert newest_branch_build([legacy, "0.1.5-release.sha2aa3ce0"], "release", "0.1.5") is None
PY

test_bin="$(mktemp -d)"
trap 'rm -rf "${test_bin}"' EXIT
cat > "${test_bin}/git" <<'SH'
#!/usr/bin/env bash
if [[ "$1 $2 $3 $4" == "symbolic-ref --quiet --short HEAD" && -n "${TEST_GIT_BRANCH:-}" ]]; then
    printf '%s\n' "${TEST_GIT_BRANCH}"
    exit 0
fi
exit 1
SH
chmod +x "${test_bin}/git"

assert_latest_branch() {
    local branch="$1" expected="$2"
    local output status
    set +e
    output="$(PATH="${test_bin}:${PATH}" TEST_GIT_BRANCH="${branch}" bash "${script_dir}/gen-dev-manifest.sh" --latest --template=/missing 2>&1)"
    status=$?
    set -e
    [[ ${status} -ne 0 ]] || { echo "--latest unexpectedly succeeded for ${branch}" >&2; exit 1; }
    [[ "${output}" == *"${expected}"* ]] || { echo "unexpected --latest output for ${branch}: ${output}" >&2; exit 1; }
}

assert_latest_branch "main" "current branch is main; resolving newest main builds"
assert_latest_branch "release/0.1.5" "current branch is release/0.1.5; resolving its newest builds"
assert_latest_branch "feature/my-work" "only supports main or release/X.Y.Z"
