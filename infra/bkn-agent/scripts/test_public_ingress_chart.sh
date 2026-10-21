#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

chart_dir="${1:-charts}"
default_rendered="$(helm template bkn-agent "${chart_dir}" --set publicIngress.enabled=true)"
admin_rendered="$(helm template bkn-agent "${chart_dir}" \
    --set publicIngress.enabled=true --set publicIngress.adminAgentsEnabled=true)"

execution_path='path: /api/bkn-agent/v1/executions'
admin_path='path: /api/bkn-agent/v1/admin/agents'

grep -qF "${execution_path}" <<<"${default_rendered}"
if grep -qF "${admin_path}" <<<"${default_rendered}"; then
    echo 'Built-in Agent management route must be opt-in' >&2
    exit 1
fi
grep -qF "${execution_path}" <<<"${admin_rendered}"
grep -qF "${admin_path}" <<<"${admin_rendered}"
if grep -qE 'path: /api/bkn-agent/(in/v1|v1/?$)' <<<"${admin_rendered}"; then
    echo 'Public ingress must not expose internal or blanket BKN Agent routes' >&2
    exit 1
fi

echo 'bkn-agent public ingress route scope: PASS'
