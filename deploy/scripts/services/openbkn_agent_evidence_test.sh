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
    # Effective values must include a historical chart's default disabled state.
    get) [[ " $* " == *" --all "* ]] || return 1; printf '%s' "${old_values}" ;;
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

# Upgrade preserves capture choices, but uses current installer connections.
test_installed=true
old_values='{"observability":{"evidencePublisher":{"enabled":false,"brokers":"old-kafka:9092","credentialsSecretName":"old-secret","traceAdmission":{"configurationURL":"http://agent-observability:8080/api/agent-observability/v1/trace-evidence-configuration"},"queueMaxRecords":12,"password":"never-copy"}},"unrelated":"never-copy"}'
rm -f "${test_dir}/secret-calls"
export AGENT_TEST_SECRET_STATE=missing
_openbkn_helm_upgrade_release bkn-agent openbkn "${args[@]}"
[[ ! -f "${test_dir}/secret-calls" ]]
python3 - "${test_dir}/render.json" <<'PY'
import json, sys
c = json.load(open(sys.argv[1]))['config']
p = c['observability']['evidencePublisher']
assert p['enabled'] is False and p['brokers'] == 'profile-kafka:9092'
assert p['credentialsSecretName'] != 'old-secret'
assert ':8081/' in p['traceAdmission']['configurationURL']
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
# Reject values which otherwise cause the runtime to disable capture after rollout.
for invalid in brokers=kafka brokers=kafka:0 brokers=kafka:65536 queueMaxRecords=0 queueMaxRecords=1.5 queueMaxBytes=1073741825 maxRecordBytes=1048577 maxAgeS=0 maxAgeS=NaN maxAttempts=11 retryBackoffMs=-1 retryBackoffMs=60001 drainTimeoutS=0 drainTimeoutS=61; do
  if _openbkn_helm_upgrade_release bkn-agent openbkn "${args[@]}" -f "${test_dir}/custom.json" --set "observability.evidencePublisher.${invalid}" 2>/dev/null; then
    echo "enabled publisher must reject invalid ${invalid%%=*} before rollout" >&2
    exit 1
  fi
done
[[ "$(wc -l <"${test_dir}/upgrades")" -eq 4 ]]
printf 'null' | python3 "${SCRIPT_DIR}/scripts/lib/agent_evidence.py" preserve | python3 -c 'import json,sys; assert json.load(sys.stdin) == {}'
echo 'Agent Evidence defaults, upgrades and Secret preflight checks passed'
