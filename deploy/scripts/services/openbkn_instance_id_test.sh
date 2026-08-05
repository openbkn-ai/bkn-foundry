#!/usr/bin/env bash
# _bkn_safe_instance_id / _bkn_safe_derive_instance_id 的行为测试（无需集群）。
#
# 这个身份决定授权指纹：算错一次，已激活的证就失效（#508）。所以四件事必须钉住：
# 现值粘性、DMI 占位值降级到 machineID、多节点确定性、以及"函数的 stdout 就是
# 身份值"——告警混进 stdout 会被命令替换吞进 helm --set。
set -uo pipefail

ONE_FAILED=0
PASS=0
fail() { echo "FAIL: $*"; ONE_FAILED=1; }
ok() { PASS=$((PASS + 1)); }
check() {
    if [[ "$2" == "$3" ]]; then ok; else fail "$1: got[$2] want[$3]"; fi
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# shellcheck source=../services/openbkn.sh
source "${SCRIPT_DIR}/scripts/services/openbkn.sh"

log_warn() { echo "[WARN] $*"; }

# common.sh 不在测试里 source：补齐 _openbkn_release_extra_sets 走到的另一半
# （初始密码分支）所需的最小依赖。
CONFIG_YAML_PATH="${CONFIG_YAML_PATH:-/dev/null}"
config_yaml_top_field() { printf ''; }

# 桩 kubectl：SCENARIO 决定 configmap 现值与各 node 字段的返回。
# 节点行故意乱序给出，用来验证排序发生在我们这边而不是靠 kubectl --sort-by。
kubectl() {
    local args="$*"
    case "${SCENARIO}" in
        secret_absent)
            return 1
            ;;
        secret_same_owner)
            [[ "${args}" == *"-o jsonpath="* ]] && {
                printf 'agent-observability|openbkn|Helm'
                return 0
            }
            [[ "${args}" == *"get secret bkn-trace-evidence-ingest"* ]] && return 0
            ;;
        secret_unowned)
            [[ "${args}" == *"-o jsonpath="* ]] && {
                printf '||'
                return 0
            }
            [[ "${args}" == *"get secret bkn-trace-evidence-ingest"* ]] && return 0
            ;;
        secret_other_owner)
            [[ "${args}" == *"-o jsonpath="* ]] && {
                printf 'shared-secrets|openbkn|Helm'
                return 0
            }
            [[ "${args}" == *"get secret bkn-trace-evidence-ingest"* ]] && return 0
            ;;
        normal)
            [[ "${args}" == *configmap* ]] && return 1
            [[ "${args}" == *systemUUID* ]] && {
                printf 'node-b 22222222-2222-2222-2222-222222222222\n'
                printf 'node-a 11111111-1111-1111-1111-111111111111\n'
                return 0
            }
            ;;
        dmi_placeholder)
            [[ "${args}" == *configmap* ]] && return 1
            [[ "${args}" == *systemUUID* ]] && {
                printf 'node-a 03000200-0400-0500-0006-000700080009\n'
                return 0
            }
            [[ "${args}" == *machineID* ]] && {
                printf 'node-a 9f2c1d3ea4b64e2f8c7d5a1b0e6f3c9d\n'
                return 0
            }
            ;;
        partial_report)
            # 名字最小的节点没上报字段（只有 name 一列），要跳过而不是判空。
            [[ "${args}" == *configmap* ]] && return 1
            [[ "${args}" == *systemUUID* ]] && {
                printf 'node-a \n'
                printf 'node-b 22222222-2222-2222-2222-222222222222\n'
                return 0
            }
            ;;
        sticky)
            [[ "${args}" == *configmap* ]] && { printf 'OLD-IDENTITY-1111'; return 0; }
            [[ "${args}" == *systemUUID* ]] && { printf 'node-a NEW-NODE-2222\n'; return 0; }
            ;;
        nothing)
            return 1
            ;;
    esac
    return 1
}

# 取值：只收 stdout，告警必须走 stderr
resolve() { SCENARIO="$1" _bkn_safe_instance_id openbkn 2>/dev/null; }

# 正常集群：取 systemUUID，多节点按节点名排序取最小者（node-a），与桩的返回顺序无关
check "normal-picks-lowest-node" "$(resolve normal)" "11111111-1111-1111-1111-111111111111"

# DMI 是厂商占位值：跳过，降级取 machineID
check "dmi-placeholder-falls-back" "$(resolve dmi_placeholder)" "9f2c1d3ea4b64e2f8c7d5a1b0e6f3c9d"

# 排序最靠前的节点没上报：跳过它，取下一个真有值的
check "skips-node-without-value" "$(resolve partial_report)" "22222222-2222-2222-2222-222222222222"

# 集群里已有身份：粘住不动，哪怕现在能推导出别的值
check "existing-wins" "$(resolve sticky)" "OLD-IDENTITY-1111"

# 什么都取不到：输出空（调用方据此告警并跳过 --set，绝不编一个值）
check "nothing-derivable-is-empty" "$(resolve nothing)" ""

# 换节点的告警走 stderr —— 若写到 stdout 就会被命令替换吞进身份值
stdout_only="$(SCENARIO=sticky _bkn_safe_instance_id openbkn 2>/dev/null)"
stderr_only="$(SCENARIO=sticky _bkn_safe_instance_id openbkn 2>&1 >/dev/null)"
check "warning-not-on-stdout" "${stdout_only}" "OLD-IDENTITY-1111"
if [[ "${stderr_only}" == *"NEW-NODE-2222"* ]]; then ok; else
    fail "warning-on-stderr: got[${stderr_only}]"
fi

# 身份走 --set-string：全数字的 machineID 被 helm 当数字重排就会改掉指纹
SCENARIO=normal _openbkn_release_extra_sets bkn-safe openbkn >/dev/null 2>&1
check "identity-uses-set-string" \
    "${CORE_RELEASE_EXTRA_SET_STRINGS[*]:-}" \
    "config.license.instanceId=11111111-1111-1111-1111-111111111111"
if [[ "${CORE_RELEASE_EXTRA_SETS[*]:-}" == *"instanceId"* ]]; then
    fail "identity-must-not-use-plain-set: got[${CORE_RELEASE_EXTRA_SETS[*]:-}]"
else
    ok
fi

trace_secret_create_setting() {
    SCENARIO="$1" _openbkn_release_extra_sets agent-observability openbkn >/dev/null 2>&1
    printf '%s' "${CORE_RELEASE_EXTRA_SETS[*]:-}"
}

if [[ "$(trace_secret_create_setting secret_absent)" == *"evidence.ingestAuth.createSecret=true"* ]]; then ok; else
    fail "trace-secret-absent-must-be-created"
fi
if [[ "$(trace_secret_create_setting secret_same_owner)" == *"evidence.ingestAuth.createSecret=true"* ]]; then ok; else
    fail "trace-secret-current-release-must-remain-managed"
fi
if [[ "$(trace_secret_create_setting secret_unowned)" == *"evidence.ingestAuth.createSecret=false"* ]]; then ok; else
    fail "trace-secret-unowned-must-not-be-adopted"
fi
if [[ "$(trace_secret_create_setting secret_other_owner)" == *"evidence.ingestAuth.createSecret=false"* ]]; then ok; else
    fail "trace-secret-other-release-must-not-be-adopted"
fi

if [[ "${ONE_FAILED}" -eq 0 ]]; then
    echo "openbkn_instance_id_test: all ${PASS} checks passed"
    exit 0
fi
echo "openbkn_instance_id_test: FAILED"
exit 1
