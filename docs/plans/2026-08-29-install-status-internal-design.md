# Install Status Internal Service Mapping Design

## Context

The install-status refresher merges live Deployment and StatefulSet readiness
into the publish-time service-health snapshot. Service entries are keyed by
Service name, while workload entries are keyed by Helm release name. The
existing merge recognizes the legacy `-svc` Service suffix only.

`agent-observability-internal` selects the `agent-observability` workload. It
therefore retains a stale install-time health value after the workload recovers,
because its key is not resolved to the workload key.

## Decision

Treat `-internal` as a Service alias suffix in the same fallback lookup used
for `-svc`. The lookup order remains:

1. exact Service name;
2. name with `-svc` removed;
3. name with `-internal` removed.

If no matching workload exists, preserve the publish-time value as today.

## Scope and Safety

Only pod-sourced `serviceHealth` entries are affected. HTTP-sourced entries
remain unmodified because the refresher does not re-probe application health.
The fix does not alter Kubernetes workloads, Services, probes, or deployment
configuration.

## Verification

A focused shell regression test supplies an `agent-observability-internal`
snapshot with stale `1/2` readiness and a live `agent-observability` workload
with `1/1` readiness. It asserts the merged entry becomes `1/1` and `up`, and
also protects the existing `-svc` fallback and unmatched-entry behavior.
