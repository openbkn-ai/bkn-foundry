#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

chart_dir="${1:-charts}"
disabled_rendered="$(helm template bkn-agent "${chart_dir}" --set publicIngress.enabled=false)"
default_rendered="$(helm template bkn-agent "${chart_dir}")"
admin_rendered="$(helm template bkn-agent "${chart_dir}" \
    --set publicIngress.adminAgentsEnabled=true)"

expected_paths=$'/api/bkn-agent/v1/metric-design/executions\n/api/bkn-agent/v1/executions\n/api/bkn-agent/v1/me/agents\n/api/bkn-agent/v1/networks'
admin_path='/api/bkn-agent/v1/admin/agents'

rendered_paths() {
    awk '$1 == "-" && $2 == "path:" { print $3 }' <<<"$1"
}

if grep -qF 'kind: Ingress' <<<"${disabled_rendered}"; then
    echo 'Public ingress must be disabled when explicitly turned off' >&2
    exit 1
fi
if [[ "$(rendered_paths "${default_rendered}")" != "${expected_paths}" ]]; then
    echo 'Default public ingress must contain only the Studio execution and setting routes' >&2
    exit 1
fi
if [[ "$(rendered_paths "${admin_rendered}")" != "${expected_paths}"$'\n'"${admin_path}" ]]; then
    echo 'Admin ingress must add only the built-in Agent management route' >&2
    exit 1
fi

echo 'bkn-agent public ingress route scope: PASS'
