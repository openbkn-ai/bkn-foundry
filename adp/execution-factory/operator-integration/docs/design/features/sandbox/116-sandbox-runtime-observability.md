---
issue: "#116"
branch: "feature/116-sandbox-runtime-observability"
module: "operator-integration"
status: "draft"
created: "2026-07-01"
---

# Feature #116: Sandbox Runtime Observability Entry

## Goal

Execution Factory Lab needs a runtime management entry that helps platform
administrators diagnose function, Skill, MCP, and operator debugging failures.
The first phase is read-only observability. It must answer:

- is the sandbox control plane reachable;
- is the session pool saturated;
- which sessions are failed or abnormal;
- what runtime, template, resource limits, dependencies, and recent error
  summary belong to a session.

## Product Boundary

This change implements backend private management APIs in
`operator-integration`. The Studio frontend is not in this repository, so the
actual navigation entry should be added by the frontend project later:

`Execution Factory Lab -> Runtime Management`

The page can display `Sandbox Runtime` as the technical subtitle, but the user
facing navigation should prefer "Runtime Management".

## API Design

The private route base is:

`/api/agent-operator-integration/internal-v1/sandbox`

Read-only endpoints:

- `GET /health`
- `GET /pool`
- `GET /sessions`
- `GET /sessions/{id}`

The frontend must not call sandbox-control-plane directly. `operator-integration`
is the boundary for auth context, future audit, field shaping, and redaction.

## UX Contract

The frontend should render:

- top status cards for control plane health, pool usage, running tasks, and
  failed sessions;
- a session list with status, source, runtime, template, resource limit,
  dependency status, create/update/last-active time, and recent error summary;
- a details drawer with workspace, runtime node, pod, dependency lists, package
  index, resource limits, and sanitized diagnostics.

The first phase intentionally does not expose full stdout/stderr, terminate,
delete, cleanup, prewarm, or dependency reinstall actions. The response exposes
`governance_actions_available=false` and
`full_stdout_stderr_available=false` so the UI can avoid showing action affordances.

## Compatibility

The change is additive. Existing function execution, Skill execution, MCP
execution, and sandbox session pool behavior are unchanged.

## Acceptance Criteria

- [x] Private API exposes sandbox control plane health.
- [x] Private API exposes session pool waterline and resource configuration.
- [x] Private API lists sandbox sessions with status/runtime/source filters.
- [x] Private API returns session detail with dependencies and sanitized error
      summary.
- [x] No write operation is exposed by the new sandbox management handler.
- [x] Frontend integration boundary is documented because Studio is outside this
      repository.

## Future Work

- Add Studio frontend navigation and pages.
- Link function / Skill / operator debug failures to session id or task id.
- Implement controlled governance actions in #117 with permission checks,
  confirmation UX, and audit logs.
