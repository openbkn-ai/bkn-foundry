#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
collector_chart="$root_dir/otelcol-contribute-chart/charts/otelcol-contrib"
agent_chart="$root_dir/agent-observability/charts/agent-observability"
collector_rendered="$(mktemp)"
agent_rendered="$(mktemp)"
trap 'rm -f "$collector_rendered" "$agent_rendered"' EXIT

helm template trace-admission "$collector_chart" --set traceAdmission.workloadIdentity=spiffe://cluster-a/ns/openbkn/sa/otelcol >"$collector_rendered"

helm template agent-observability "$agent_chart" >"$agent_rendered"

python3 - "$collector_rendered" "$agent_rendered" <<'PY'
import sys
import yaml

collector = [doc for doc in yaml.safe_load_all(open(sys.argv[1], encoding="utf-8")) if doc and doc.get("kind") == "Deployment"][0]
agent = [doc for doc in yaml.safe_load_all(open(sys.argv[2], encoding="utf-8")) if doc and doc.get("kind") == "Deployment"][0]

def env(deployment):
    return {item["name"]: item.get("value") for item in deployment["spec"]["template"]["spec"]["containers"][0]["env"] if "value" in item}

collector_env = env(collector)
assert collector_env["TRACE_ADMISSION_WORKLOAD_IDENTITY"] == "spiffe://cluster-a/ns/openbkn/sa/otelcol"
assert "/internal/trace-evidence/configuration" in collector_env["TRACE_ADMISSION_CONFIGURATION_URL"]
for deployment in (collector, agent):
    names = {item["name"] for item in deployment["spec"]["template"]["spec"]["containers"][0]["env"]}
    assert "TRACE_ADMISSION_CLIENT_SECRET" not in names
    assert "TRACE_ADMISSION_CURRENT_PUBLIC_KEY" not in names
    assert "BKN_TRACE_CAPTURE_POLICY_SIGNING_KEY" not in names

PY

echo "trace admission identity wiring: PASS"
