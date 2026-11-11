#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

set -euo pipefail
chart_dir="$(cd "$(dirname "$0")/../helm/agent-retrieval" && pwd)"
render() { helm template license-test "$chart_dir" --show-only templates/deployment.yaml "$@"; }
assert_community() {
    if [[ "$1" == *'name: BKN_SAFE_URL'* ]]; then
        echo 'An unconfigured community deployment must not receive a licence hub' >&2
        exit 1
    fi
}
assert_community "$(render)"
assert_community "$(render --set licenseHub=null)"
assert_community "$(render --set-string bknSafe.url=http://platform-default:3000)"
assert_community "$(render --set-string mcpExtensionManifestURL=http://provider/tools)"
for hub in http://bkn-safe:3000 https://custom-hub.example:3443/licensing; do
    deployment="$(render --set-string "licenseHub.url=${hub}")"
    actual="$(awk '/name: BKN_SAFE_URL$/ {getline; sub(/^[[:space:]]+/, ""); print}' <<<"${deployment}")"
    [[ "${actual}" == "value: \"${hub}\"" ]] || { echo 'The configured licence hub was not rendered exactly once' >&2; exit 1; }
done
echo 'licence hub chart regression checks passed'
