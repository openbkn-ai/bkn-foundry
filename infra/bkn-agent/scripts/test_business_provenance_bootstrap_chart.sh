#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

chart_dir="${1:-charts}"
rendered="$(helm template bkn-agent "${chart_dir}")"

for required in \
    'name: "bkn-agent-provenance-bootstrap"' \
    'name: BKN_PROVENANCE_BOOTSTRAP_TOKEN' \
    'key: "token"'; do
    grep -q "${required}" <<<"${rendered}" || {
        echo "bkn-agent bootstrap credential contract missing: ${required}" >&2
        exit 1
    }
done

echo "bkn-agent provenance bootstrap credential contract: PASS"
