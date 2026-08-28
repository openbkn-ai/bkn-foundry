# Configurable BKN Trace Interaction Capacity

## Decision

Make the per-Interaction capacity limits deployment configuration owned by BKN Trace Core. The service defaults are 256 Operations, 32 Claims, and 4,096 observed evidence references. Clients cannot provide or override these values.

## Rationale

The existing limits are enforced as constants in `sessionsvc`, while the chart and process configuration cannot tune them. Operations and evidence references scale with tool activity; Claims are curated conclusion statements and should retain the current cap. This preserves the existing 16 evidence-reference-per-Operation budget while allowing a longer tool-use turn.

## Configuration contract

- Helm values: `core.capacity.maxOperationsPerInteraction`, `maxClaimsPerInteraction`, and `maxEvidenceRefsPerInteraction`.
- Environment variables: `BKN_TRACE_CORE_MAX_OPERATIONS_PER_INTERACTION`, `BKN_TRACE_CORE_MAX_CLAIMS_PER_INTERACTION`, and `BKN_TRACE_CORE_MAX_EVIDENCE_REFS_PER_INTERACTION`.
- Missing values use the documented defaults. Invalid, zero, or negative explicit values reject startup. The values are read at process start and require a rollout to change.
- The Core validates the relationship `maxClaimsPerInteraction <= maxOperationsPerInteraction` and `maxEvidenceRefsPerInteraction >= maxOperationsPerInteraction`; invalid deployment configuration prevents startup rather than silently weakening a configured guardrail.

## Compatibility and verification

No API, schema, or migration changes are required. Tests cover defaults, valid environment overrides, invalid configuration rejection, service enforcement of all three configured limits, and Helm rendering. The capacity baseline is updated to the new defaults.
