<!--
Copyright openbkn.ai

Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
-->

# Knowledge-network authorization contract

This document contains only the authorization invariants shared by bkn-safe,
bkn-backend, ontology-query, context-loader, and execution-factory. It is not an
API reference or a design document.

Concrete paths, methods, fields, status codes, and error envelopes are defined
by the service OpenAPI files:

- [bkn-safe authorization](bkn-safe/authorization.yaml)
- [BKN](bkn/)
- [ontology-query](ontology-query/ontology-query.yaml)
- [execution-factory](execution-factory/)

Detailed design background and decision history belong in
[bkn-docs](https://github.com/openbkn-ai/bkn-docs/issues/92).

## Resource references

Authorization decisions use seven resource types. A knowledge network uses its
business ID directly; every child uses the owning knowledge-network ID and its
business ID joined by `/`.

| Business resource | Resource type | Authorization ID example |
| --- | --- | --- |
| Knowledge network | `knowledge_network` | `supply_chain` |
| Concept group | `concept_group` | `supply_chain/core_concepts` |
| Object type | `object_type` | `supply_chain/purchase_order` |
| Relation type | `relation_type` | `supply_chain/order_supplier` |
| Action type | `action_type` | `supply_chain/approve_order` |
| Metric | `metric` | `supply_chain/on_time_rate` |
| Risk type | `risk_type` | `supply_chain/delivery_delay` |

The two parts of a child ID are business IDs. Do not substitute a database row
ID, a parent-only knowledge-network ID, or a branch-qualified ID. Authorization
always evaluates the published `main` model.

## Operations and inheritance

Each child has one `ResourceParent` edge to its knowledge network. bkn-safe
applies the following one-hop parent operation mapping:

| Child operation | Parent knowledge-network operation |
| --- | --- |
| `view_detail` | `view_detail` |
| `query_data` | `query_data` |
| `modify` | `modify` |
| `delete` | `modify` |
| `authorize` | `authorize` |
| `task_manage` | `task_manage` |
| `action_type.execute` | no inheritance; direct action-type grant required |

`action_type:*:execute` is invalid. Execution permission is granted only on a
concrete `action_type:{kn_id}/{action_type_id}` resource.

## Cross-service enforcement

| Entry point | Required decision |
| --- | --- |
| Knowledge-network or child detail | `view_detail` on the canonical resource |
| List or search | build trusted candidates, filter by `view_detail`, then compute total and pagination |
| Create knowledge network | `knowledge_network:*:create` |
| Modify child | `modify` on the canonical child |
| Delete child | `delete` on every target; a batch is all-or-nothing |
| Query object, relation, metric, or action data | `query_data` on every dependency resolved from the published model |
| Submit or invoke an action | action-type `execute` AND execution-resource `execute` AND all data dependencies `query_data` |
| Manage action schedules or tasks | `task_manage` on the applicable action type or knowledge network |

An action execution stores the authenticated execution subject. The worker
rechecks that same subject immediately before the external call. Automatic
retries retain the original subject; a manual rerun creates a new execution
for the current caller. Caller-supplied body fields do not replace the
authenticated subject.

## Collection and batch consistency

Lists and searches build their trusted candidate set first, apply authorization,
and only then calculate total and pagination. This prevents unauthorized rows
from affecting result counts or page boundaries.

The shared resource-filtering contract has no fixed resource-count,
operation-count, or matrix-cell hard cap and does not promise a fixed-size
payload rejection threshold. Callers may split trusted candidates into
dynamically sized chunks for operational reasons. A timeout, failed chunk, or
malformed response fails the whole business request; callers must not return a
partial list or query result. An omitted candidate is a normal denial because
the filter returns only authorized resources.

The default caller chunk size is an implementation tuning value, not an API
limit. It must not be used to reject an otherwise valid business request.

## Subjects and failure behavior

`AUTH_ENABLED` is the only remaining authorization gate. When it is `true`, all
checks in this contract are enforced; there are no module-specific child,
query-data, or action-execution rollout switches. An environment running with
authentication disabled does not provide these fine-grained authorization
guarantees.

Public services derive the subject from their authenticated request. Trusted
internal calls propagate that subject through their service-to-service identity
context. Caller-controlled payload fields never replace it. Missing, disabled,
unknown, or unsupported subjects are denied before data access or external
execution.

Authorization is fail-closed across service boundaries. A timeout, transport
failure, malformed response, incomplete dependency set, or indeterminate
decision produces no data and invokes no external target. Each service reports
that failure using its own OpenAPI error contract.

## Rollout prerequisites

The module-specific PEP rollout switches have been removed. Before upgrading an
existing environment that runs with authentication enabled, follow the
[migration guide](../../adp/bkn/bkn-backend/script/migrate_kn_authz/README.md)
and validate the result.
Run the dry-run first while the affected services are stopped and both databases
are backed up. Any validation error or non-zero migration exit blocks the
upgrade; do not route traffic to the upgraded services with partial authorization
data. Safe reconstruction is transactional; branch normalization is a separate
BKN transaction. The script is idempotent, so investigate the report and rerun
it after a failure.
When authentication is enabled, every participating service must have a valid
bkn-safe connection before it receives traffic. Exact configuration keys and
startup validation are documented by the owning service.
