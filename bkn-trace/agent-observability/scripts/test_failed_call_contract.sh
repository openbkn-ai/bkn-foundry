#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
contract_file="$(mktemp "${TMPDIR:-/tmp}/bkn-failed-call-contract.XXXXXX")"
trap 'rm -f "$contract_file"' EXIT
export BKN_TRACE_FAILED_CALL_CONTRACT="$contract_file"
(cd "$repo_root/adp/context-loader/agent-retrieval" && go test ./server/driveradapters/mcp -run '^TestLifecycleMiddlewareFinalizesRealAdapterFailures$' -count=1)
(cd "$repo_root/bkn-trace/agent-observability" && go test -tags=integration ./src/domain/service/evidencesvc -run '^TestFailedCallProducerToLedgerIntegrity$' -count=1 -v)
