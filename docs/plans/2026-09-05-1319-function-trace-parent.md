# Function Trace parent propagation (#1319)

## Design

Preserve the lifecycle Guard's persisted execute_tool operation as the parent of Function reads. Carry it separately from the derived downstream bkn-operation-id through an internal bkn-parent-operation-id header, then the per-execution BKN_PARENT_OPERATION_ID environment variable. Both sandbox_sdk.bkn and bkn_osdk must put it in bkn_context.parent_operation_id. Each child still obtains its own operation and receipt from the existing Guard. No new operation layer or storage schema is needed.

A header containing a derived downstream ID is not a substitute for a persisted parent. Do not change business Function inputs, results, kn_id, names, or historical data. Clear the per-execution parent when absent. Direct execution continues to use its explicit envelope and does not infer parentage from arbitrary headers.

## Implementation plan

- [x] Reproduce missing parent in the published-tool client and both Python SDK paths with failing tests.
- [x] Add parent header to the Context Loader published-tool adapter and capture/forward it in Execution Factory.
- [x] Set/reset the parent environment variable in Function execution; cover managed and direct paths.
- [x] Consume the variable in the legacy SDK and Python OSDK, verifying multiple calls, subsequent invocations, and root calls.
- [x] Verify Trace Guard / Core already accept and persist the supplied parent; add topology coverage where missing.
- [x] Run targeted and module tests, lint/build, and review the complete diff (baseline lint findings noted below).

## Delivery

Requires rebuilding Context Loader, Execution Factory and sandbox SDK images. The Python OSDK change is in the companion [bkn-sdk PR #96](https://github.com/openbkn-ai/bkn-sdk/pull/96); the sandbox image dependency is pinned to its published commit `3d99c6b3d8faec780e728d6c57be381200a1ae18`. Merge the companion SDK change before this PR. Do not claim a live fix until deployment integration is verified. No deployment has been performed.

## Verification (2026-09-05)

- Context Loader: `GOCACHE=/tmp/bkn-1319-go-cache make test` passed, 26 tested packages / 831 top-level tests. The subsequently added `TestFunctionReadsPreserveParentThroughLifecycle` passed separately.
- Execution Factory: `GOCACHE=/tmp/bkn-1319-go-cache make test` passed, 34 tested packages / 242 top-level tests. The subsequently added `TestExecuteToolCapturesFunctionParentFromRequest` passed separately.
- Both Go modules: `go build ./...` passed. Full golangci-lint reports existing main findings; `golangci-lint run --new-from-rev=HEAD` passes for both modules.
- Executor: isolated environment `python -m pytest tests/unit/ -q`: 324 passed, including a red/green regression for explicit turn override.
- Python OSDK: 412 passed, 32 live tests skipped; 43 traced-path tests rerun after formatting. Ruff and mypy passed (46 source files). Wheel/sdist built; wheel installed and parent-context behavior checked outside the source directory.
- Independent review identified legacy environment-parent leakage across an explicit turn override; reproduced with a failing test and fixed by matching the environment conversation/interaction before inheriting the parent.

## Remaining integration check

- [x] Advance `infra/sandbox/images/templates/executor/bkn-requirements.txt` from `00e128e` to published SDK commit `3d99c6b3d8faec780e728d6c57be381200a1ae18`.
- [ ] Merge the companion SDK PR and rebuild the images. If its final commit changes during review, update the pin before merging this PR.
- [ ] Deploy reviewed artifacts and run two Functions with multiple internal reads plus a direct query; inspect operation and operation_call_fact rows for persisted parent links and distinct child IDs. No live deployment or database mutation was performed here.
