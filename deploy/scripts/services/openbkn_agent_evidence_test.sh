#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${SCRIPT_DIR}/scripts/services/openbkn.sh"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT
export PATH="${test_dir}:${PATH}"
export AGENT_TEST_SECRET_STATE=ready
export AGENT_TEST_SECRET_CALLS="${test_dir}/secret-calls"
cat >"${test_dir}/kubectl" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${AGENT_TEST_SECRET_CALLS}"
case "${AGENT_TEST_SECRET_STATE}" in
  missing) exit 1 ;;
  empty-key) printf '{"data":{"username":"dGVzdA==","password":""}}' ;;
  *) printf '{"data":{"username":"dGVzdA==","password":"dGVzdA=="}}' ;;
esac
SH
chmod +x "${test_dir}/kubectl"
log_info() { :; }
log_error() { :; }
_openbkn_drop_literal_env_now_from_secret() { :; }
_openbkn_adopt_unowned_resources() { return 1; }
test_installed=false
old_values='{}'
helm() {
  case "$1" in
    list) [[ "${test_installed}" != true ]] || printf 'bkn-agent\n' ;;
    get) printf '%s' "${old_values}" ;;
    install) command helm "$@" >"${test_dir}/render.json"; cat "${test_dir}/render.json" ;;
    upgrade) printf '%s\n' upgrade >>"${test_dir}/upgrades" ;;
    *) return 1 ;;
  esac
}
chart="${SCRIPT_DIR}/../infra/bkn-agent/charts"
args=(upgrade --install bkn-agent "${chart}" --namespace openbkn)
_openbkn_trace_kafka_brokers() { printf 'profile-kafka:9092'; }
OPENBKN_TRACE_ADMISSION_POLICY_URL=http://custom-internal:8081/policy
_openbkn_release_extra_sets bkn-agent openbkn
_openbkn_helm_upgrade_release bkn-agent openbkn "${args[@]}"
python3 - "${test_dir}/render.json" <<'PY'
import json, sys
p = json.load(open(sys.argv[1]))['config']['observability']['evidencePublisher']
assert p['enabled'] is True and p['brokers'] == 'profile-kafka:9092'
assert p['traceAdmission']['policyURL'] == 'http://custom-internal:8081/policy'
PY

# Upgrade keeps actual publisher choices, not obsolete unrelated chart defaults.
test_installed=true
old_values='{"observability":{"evidencePublisher":{"enabled":false,"brokers":"old-kafka:9092","queueMaxRecords":12,"password":"never-copy"}},"unrelated":"never-copy"}'
rm -f "${test_dir}/secret-calls"
export AGENT_TEST_SECRET_STATE=missing
_openbkn_helm_upgrade_release bkn-agent openbkn "${args[@]}"
[[ ! -f "${test_dir}/secret-calls" ]]
python3 - "${test_dir}/render.json" <<'PY'
import json, sys
c = json.load(open(sys.argv[1]))['config']
p = c['observability']['evidencePublisher']
assert p['enabled'] is False and p['brokers'] == 'old-kafka:9092'
assert p['queueMaxRecords'] == 12 and 'password' not in p and 'unrelated' not in c
PY

# Explicit current configuration overrides both installed values and profile defaults.
export AGENT_TEST_SECRET_STATE=ready
printf '{"observability":{"evidencePublisher":{"enabled":true,"brokers":"custom:9092","credentialsSecretName":"custom-secret","traceAdmission":{"policyURL":"http://explicit:8081/policy"}}}}' >"${test_dir}/custom.json"
_openbkn_helm_upgrade_release bkn-agent openbkn "${args[@]}" -f "${test_dir}/custom.json"
grep -q 'secret custom-secret' "${test_dir}/secret-calls"
python3 - "${test_dir}/render.json" <<'PY'
import json, sys
p = json.load(open(sys.argv[1]))['config']['observability']['evidencePublisher']
assert p['enabled'] is True and p['brokers'] == 'custom:9092'
assert p['traceAdmission']['policyURL'] == 'http://explicit:8081/policy'
PY
export AGENT_TEST_SECRET_STATE=missing
_openbkn_helm_upgrade_release bkn-agent openbkn "${args[@]}" -f "${test_dir}/custom.json" --set observability.evidencePublisher.enabled=false
for state in missing empty-key; do
  export AGENT_TEST_SECRET_STATE="${state}"
  if _openbkn_helm_upgrade_release bkn-agent openbkn "${args[@]}" -f "${test_dir}/custom.json" 2>/dev/null; then
    echo "enabled publisher must reject ${state} Kafka Secret before rollout" >&2
    exit 1
  fi
done
export AGENT_TEST_SECRET_STATE=ready
if _openbkn_helm_upgrade_release bkn-agent openbkn "${args[@]}" -f "${test_dir}/custom.json" --set observability.evidencePublisher.traceAdmission.policyURL= 2>/dev/null; then
  echo 'enabled publisher must reject an empty internal control endpoint' >&2
  exit 1
fi
[[ "$(wc -l <"${test_dir}/upgrades")" -eq 4 ]]
echo 'Agent Evidence defaults, upgrades and Secret preflight checks passed'
