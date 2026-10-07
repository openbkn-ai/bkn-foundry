# Managed operation lease reuse

Goal: remove the repeated interaction read on consecutive managed tool calls without weakening Core admission or receipt settlement.

Core renews lease expiry/version on ensure, but does not change token/epoch. Reuse only credentials received from Core, scoped to the authenticated owner, conversation and interaction, in each LifecycleClient. Bound the cache to 256 entries and one minute, never beyond the expiry returned by Core. Cache misses use the existing authorized GET. Core remains authoritative for active status, ownership, expiry, fencing, operation idempotency and execution disposition. Cache hits never grant permission to execute locally.

A cached lease rejected with terminal_conflict is evicted and re-read once before retrying ensure with the same operation key and input. Other API errors evict it without retry. Successful interaction responses seed or invalidate entries, including start and finish. Concurrent requests use a mutex only for cache access, never network I/O; duplicate misses are allowed. There is no distributed cache, new endpoint, new dependency, or change to artifact durability.

Artifact acknowledgements are drained up to 4 KiB under the existing request deadline, enabling HTTP connection reuse without retrying a committed write when the acknowledgement read fails. Artifact writes remain synchronous.

## Implementation / verification

- [x] Add a failing regression asserting start → ensure skips GET, and consecutive ensures reuse an authorized lease.
- [x] Implement bounded, identity-scoped lease reuse in `server/infra/bkntrace`.
- [x] Cover identity separation, expiry, terminal/fenced responses, idempotent retry and concurrent access.
- [x] Run focused tests with race detection, module tests, vet and build.
- [x] Submit PR and write actual scope/results back to #1691. Artifact HTTP persistence is retained and remains a separate issue scope.

Validation: full module tests, race tests for the trace client, vet, default/ee_dev builds and default/ee_dev MCP/extension tests passed. The initial all-package run under parallel compilation hit two existing two-second Python SSE test deadlines; both passed in the subsequent full run with GOMAXPROCS=4. No Kind/Studio deployment was performed for this client-only optimization.
