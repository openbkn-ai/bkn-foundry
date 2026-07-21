#!/usr/bin/env bash
# _core_uninstall_retired_releases 的行为测试（无需集群）。
# 与 deploy/scripts/lib/preflight_checks_test.sh 同风格：source 真实脚本、mock
# 外部命令、断言计数。
#
# 覆盖历史 release 退役逻辑的四个不变量：
#   1. 清单里存在的 release 都会被 uninstall
#   2. 不存在的 release 跳过（幂等——重复 install/upgrade 不重复卸载）
#   3. 清单全不存在时一个都不卸
#   4. 单个 uninstall 失败不中断，继续处理其余项并告警
set -uo pipefail

ONE_FAILED=0
PASS=0
fail() { echo "FAIL: $*"; ONE_FAILED=1; }
ok() { PASS=$((PASS + 1)); }
check() {
    if [[ "$2" == "$3" ]]; then ok; else fail "$1: got[$2] want[$3]"; fi
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# core.sh 顶层只有变量赋值与函数定义，source 不触发任何执行。
# shellcheck source=../services/core.sh
source "${SCRIPT_DIR}/scripts/services/core.sh"

# --- mock 外部依赖 ------------------------------------------------------------
# 日志：静音，避免污染测试输出。
log_info() { :; }
log_warn() { :; }

# helm：由测试用例通过环境变量驱动。
#   HELM_EXISTS         —— 空格分隔，视为“已安装”的 release（helm status 返回 0）
#   HELM_UNINSTALL_FAIL —— 空格分隔，其 uninstall 返回非 0
#   CALLS_FILE          —— 记录 uninstall 调用顺序（用文件而非变量，跨命令替换子 shell 可见）
HELM_EXISTS=""
HELM_UNINSTALL_FAIL=""
CALLS_FILE="$(mktemp)"
helm() {
    local sub="$1"
    shift
    local rel="$1"
    case "${sub}" in
        status)
            [[ " ${HELM_EXISTS} " == *" ${rel} "* ]] && return 0 || return 1
            ;;
        uninstall)
            echo "${rel}" >>"${CALLS_FILE}"
            if [[ " ${HELM_UNINSTALL_FAIL} " == *" ${rel} "* ]]; then
                echo "simulated uninstall failure"
                return 1
            fi
            return 0
            ;;
    esac
}

# 用可控清单覆盖真实退役清单，避免测试跟随 capabilities-lab 之类真实条目漂移。
_CORE_RETIRED_RELEASES=("svc-a|reason a" "svc-b|reason b|含额外竖线")

calls() { tr '\n' ' ' <"${CALLS_FILE}" | sed 's/ $//'; }
reset() { : >"${CALLS_FILE}"; }

# --- 用例1：两个都存在 → 都被 uninstall ---
HELM_EXISTS="svc-a svc-b"
HELM_UNINSTALL_FAIL=""
reset
_core_uninstall_retired_releases "ns" >/dev/null 2>&1
check "both-exist-both-uninstalled" "$(calls)" "svc-a svc-b"

# --- 用例2：只有 svc-a 存在 → 只卸 svc-a（幂等：不存在的跳过）---
HELM_EXISTS="svc-a"
HELM_UNINSTALL_FAIL=""
reset
_core_uninstall_retired_releases "ns" >/dev/null 2>&1
check "only-existing-uninstalled" "$(calls)" "svc-a"

# --- 用例3：都不存在 → 一个都不卸 ---
HELM_EXISTS=""
HELM_UNINSTALL_FAIL=""
reset
_core_uninstall_retired_releases "ns" >/dev/null 2>&1
check "none-exist-none-uninstalled" "$(calls)" ""

# --- 用例4：svc-a 卸载失败 → 不中断，继续卸 svc-b ---
HELM_EXISTS="svc-a svc-b"
HELM_UNINSTALL_FAIL="svc-a"
reset
_core_uninstall_retired_releases "ns"
rc=$?
check "failure-does-not-abort-rc" "${rc}" "0"
check "failure-continues-to-next" "$(calls)" "svc-a svc-b"

# --- 用例5：reason 含额外竖线 → release 名不被截断 ---
# svc-b 的条目是 "svc-b|reason b|含额外竖线"，${entry%%|*} 应取到 "svc-b"。
HELM_EXISTS="svc-b"
HELM_UNINSTALL_FAIL=""
reset
_core_uninstall_retired_releases "ns" >/dev/null 2>&1
check "reason-with-pipe-parses-release" "$(calls)" "svc-b"

rm -f "${CALLS_FILE}"

if [[ "${ONE_FAILED}" -eq 0 ]]; then
    echo "core_retire_test: all ${PASS} checks passed"
    exit 0
fi
echo "core_retire_test: FAILED"
exit 1
