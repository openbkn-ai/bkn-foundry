<!--
Copyright openbkn.ai

Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
-->

# Knowledge-network authorization contract

This document describes the authorization contract shared by bkn-safe,
bkn-backend, ontology-query, context-loader, and execution-factory. The
runtime OpenAPI files remain authoritative for individual request and response
schemas.

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
always evaluates the published `main` model. Business IDs must be non-empty,
must not contain `/` or `*`, and must not have leading or trailing whitespace.

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

## Business entry points

| Entry point | Required decision |
| --- | --- |
| Knowledge-network or child detail | `view_detail` on the canonical resource |
| List or search | build trusted candidates, filter by `view_detail`, then compute total and pagination |
| Create knowledge network | `knowledge_network:*:create` |
| Import a new knowledge network | `knowledge_network:*:create` once for the transaction; nested child creates reuse that precheck |
| Import with overwrite | `knowledge_network:*:create`, then the existing knowledge network and affected children must pass their normal update checks |
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

## Batch filtering

`POST /api/safe/v1/authz/resource-filter` has no fixed resource-count,
operation-count, or matrix-cell hard cap and does not promise a fixed-size
`413` response. Callers may split trusted candidates into dynamically sized
chunks for operational reasons. A timeout, failed chunk, missing `resources`
field, duplicate row, or unknown resource echoed by a response makes the whole
business request fail; callers must not return a partial list or partial query
result. Because this API returns only authorized resources, an omitted
candidate is a normal denial rather than a malformed response.

The default caller chunk size is an implementation tuning value, not an API
limit. It must not be used to reject an otherwise valid business request.

## Subjects and failure behavior

`AUTH_ENABLED` is the only remaining authorization gate. When it is `true`, all
checks in this contract are enforced; there are no module-specific child,
query-data, or action-execution rollout switches. An environment running with
authentication disabled does not provide these fine-grained authorization
guarantees.

Public APIs derive the subject from a valid OAuth bearer token or AppKey.
Internal APIs accept only the trusted service-to-service `x-account-id` and
`x-account-type` context. Missing, empty, disabled, unknown, or unsupported
subjects are denied before data access or external execution.

| Condition | Business behavior |
| --- | --- |
| Credential missing or invalid | `401` on public APIs |
| Authenticated subject lacks a required operation | `403` |
| Requested business resource does not exist | `404` only after the authoritative business service confirms absence |
| bkn-safe timeout, transport failure, invalid response, or incomplete decision | fail closed; no partial result or external execution |
| BKN authorization dependency failure | `500` with the BKN error envelope |
| ontology-query authorization dependency failure | `503` with the ontology-query error envelope |
| execution-factory authorization denial or decision failure | `403` with the execution-factory error envelope |

Do not turn a `403` into `404`, `missing`, an empty enrichment, or a schema-only
success. A disabled account is not an anonymous caller and receives no
permissions.

## Correct and incorrect requests

Correct single-resource decision:

```json
{
  "accessor_id": "user-a",
  "resource": {"type": "object_type", "id": "supply_chain/purchase_order"},
  "operation": "query_data"
}
```

Correct mixed-resource batch filter:

```json
{
  "accessor_id": "user-a",
  "resources": [
    {"type": "object_type", "id": "supply_chain/purchase_order"},
    {"type": "metric", "id": "supply_chain/on_time_rate"}
  ],
  "visibility_operations": ["view_detail"],
  "candidate_operations": ["view_detail", "query_data", "modify", "delete", "authorize"]
}
```

Incorrect child reference (parent-only fallback):

```json
{
  "accessor_id": "user-a",
  "resource": {"type": "object_type", "id": "supply_chain"},
  "operation": "query_data"
}
```

Incorrect operation and forbidden wildcard execution grant:

```json
{
  "accessor_id": "user-a",
  "resource": {"type": "action_type", "id": "*"},
  "operations": ["data_query", "execute"]
}
```

Use `query_data`, not `data_query`, and grant `execute` to one concrete action
type.

## Rollout prerequisites

The module-specific PEP rollout switches have been removed. Before upgrading an
existing environment that runs with authentication enabled, run the migration
documented in `adp/bkn/bkn-backend/script/migrate_kn_authz/README.md` and validate
the result.
Run the dry-run first while the affected services are stopped and both databases
are backed up. Any validation error or non-zero migration exit blocks the
upgrade; do not route traffic to the upgraded services with partial authorization
data. Safe reconstruction is transactional; branch normalization is a separate
BKN transaction. The script is idempotent, so investigate the report and rerun
it after a failure.
The deployment must provide a valid bkn-safe base URL. When authentication is
enabled, BKN and execution-factory stop at startup if the URL is missing or is
not an absolute HTTP(S) service URL.

Implementation tracking: [#1241](https://github.com/openbkn-ai/bkn-foundry/issues/1241),
contract synchronization: [#1249](https://github.com/openbkn-ai/bkn-foundry/issues/1249),
and end-to-end validation: [#1239](https://github.com/openbkn-ai/bkn-foundry/issues/1239).
