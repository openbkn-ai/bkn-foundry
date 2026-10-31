#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

chart_dir="${1:-charts}"
rendered="$(helm template bkn-agent "${chart_dir}")"

legacy="$(helm template bkn-agent "${chart_dir}" \
    --set provenanceBootstrap.auth.createSecret=true \
    --set provenanceBootstrap.auth.existingSecret=obsolete-secret \
    --set provenanceBootstrap.auth.secretKey=custom-key)"
for manifest in "${rendered}" "${legacy}"; do
    if grep -Eq 'provenance-bootstrap|BKN_PROVENANCE_BOOTSTRAP_TOKEN|obsolete-secret' <<<"${manifest}"; then
        echo "retired provenance account credential must not render" >&2
        exit 1
    fi
done

echo "bkn-agent without provenance account credential: PASS"
