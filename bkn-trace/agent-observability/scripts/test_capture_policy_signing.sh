#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

chart_dir="${1:-charts/agent-observability}"
error_file="$(mktemp)"
trap 'rm -f "${error_file}"' EXIT

if helm template agent-observability "${chart_dir}" --kube-version 1.23.0 >/dev/null 2>"${error_file}"; then
    echo "render without capture policy signing Secret must fail" >&2
    exit 1
fi
if ! grep -Fq 'core.capturePolicySigning.existingSecret is required' "${error_file}"; then
    echo "missing signing Secret failed for an unexpected reason" >&2
    cat "${error_file}" >&2
    exit 1
fi

rendered="$(helm template agent-observability "${chart_dir}" --kube-version 1.23.0 \
    --set core.capturePolicySigning.existingSecret=trace-capture-policy-signing \
    --show-only templates/deployment.yaml)"
grep -Fq 'name: BKN_TRACE_CAPTURE_POLICY_SIGNING_KEY' <<<"${rendered}" || {
    echo "signing key environment variable is missing" >&2
    exit 1
}
grep -A5 -F 'name: BKN_TRACE_CAPTURE_POLICY_SIGNING_KEY' <<<"${rendered}" | grep -Fq 'name: "trace-capture-policy-signing"' || {
    echo "signing key must use the supplied external Secret" >&2
    exit 1
}
grep -A5 -F 'name: BKN_TRACE_CAPTURE_POLICY_SIGNING_KEY' <<<"${rendered}" | grep -Fq 'key: "private-key"' || {
    echo "signing key must use the configured private-key field" >&2
    exit 1
}

echo "capture policy signing Secret chart contract: PASS"
