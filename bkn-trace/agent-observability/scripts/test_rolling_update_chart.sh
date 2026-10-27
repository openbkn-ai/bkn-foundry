#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
set -euo pipefail
chart_dir="${1:-charts/agent-observability}"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
for replicas in 1 3; do
  for kube_version in 1.23.0 1.30.0 1.37.0; do
    helm template trace "${chart_dir}" --set replicaCount="${replicas}" \
        --kube-version "${kube_version}" \
        --show-only templates/deployment.yaml >"${work}/deployment.yaml"
    python3 - "${work}/deployment.yaml" "${replicas}" "${kube_version}" <<'PY'
import sys
import re

rendered = open(sys.argv[1]).read()
assert re.search(r'^  replicas: ' + sys.argv[2] + r'$', rendered, re.M)
assert ('  strategy:\n    type: RollingUpdate\n    rollingUpdate:\n'
        '      maxUnavailable: 0\n      maxSurge: 1\n') in rendered, (
    'Trace must explicitly retain old replicas until one replacement is ready')
assert re.search(r'readinessProbe:\s+httpGet:\s+path: /health/ready', rendered)
assert re.search(r'livenessProbe:\s+httpGet:\s+path: /health/live', rendered)
assert 'terminationGracePeriodSeconds: 40' in rendered
assert ('preStop:\n              sleep:\n                seconds: 5' in rendered) == (int(sys.argv[3].split('.')[1]) >= 30)
PY
  done
done
echo 'PASS: bounded rolling surge and readiness for single/multiple Trace replicas'
