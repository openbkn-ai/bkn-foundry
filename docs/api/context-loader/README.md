# Context Loader API Documentation

> OpenAPI 3.0.3 definitions for the Context Loader HTTP API, whose service name
> is `agent-retrieval`. This is the unified entry point for agents to obtain
> business context: schema exploration, instance retrieval, logical-property
> evaluation, action retrieval and execution, Skill retrieval, direct data
> access, and an equivalent MCP tool surface.

## File index

| File | Topic | Endpoints under `/api/agent-retrieval/v1` |
|---|---|---|
| [schema-search.yaml](schema-search.yaml) | Schema retrieval | `POST /kn/search_schema`, `POST /kn/kn_search` |
| [kn-explore.yaml](kn-explore.yaml) | Knowledge-network exploration | `POST /kn/list_knowledge_networks`, `POST /kn/get_kn_detail`, `POST /kn/get_object_types`, `POST /kn/get_relation_types` |
| [object-instance.yaml](object-instance.yaml) | Object-instance queries | `POST /kn/query_object_instance` |
| [instance-subgraph.yaml](instance-subgraph.yaml) | Instance-subgraph queries | `POST /kn/query_instance_subgraph` |
| [cypher.yaml](cypher.yaml) | Cypher queries | `POST /kn/run_cypher` |
| [logic-property.yaml](logic-property.yaml) | Logical-property evaluation and metric queries | `POST /kn/logic-property-resolver`, `POST /kn/query_metric` |
| [action.yaml](action.yaml) | Action retrieval and execution | `POST /kn/get_action_info`, `POST /kn/execute_action`, `POST /kn/get_action_execution`, `POST /kn/list_action_executions` |
| [skill.yaml](skill.yaml) | Skill reading and execution | `POST /kn/list_skills`, `POST /kn/get_skill_content`, `POST /kn/read_skill_file`, `POST /kn/execute_skill` |
| [tool.yaml](tool.yaml) | Mounted-capability retrieval and tool execution | `POST /kn/search_capabilities`, `POST /kn/execute_tool` |
| [data-access.yaml](data-access.yaml) | Direct data access | `POST /kn/list_resources`, `POST /kn/describe_resource`, `POST /kn/run_sql` |
| [mcp.yaml](mcp.yaml) | MCP service | `GET /mcp/info`, `POST /mcp` |

## Typical flow

```text
list_knowledge_networks  → discover kn_id
search_schema            → find relevant object, relation, action, and metric types
get_object_types         → inspect physical property columns, allowed operators, and related_metrics
query_object_instance    → retrieve instances and read the primary key from _instance_identity
  ├→ logic-property-resolver → evaluate metric or operator logical properties
  ├→ get_action_info → execute_action → get_action_execution → complete an action flow
  └→ search_capabilities    → rank every kind the network mounted in one space
       ├→ capability_type=skill → get_skill_content → read_skill_file → execute_skill
       └→ capability_type=function | mcp_tool → execute_tool → run one as the calling principal
```

For modeled metrics, prefer the ontology contract rather than rebuilding the
definition with `run_sql`:

```text
search_schema / get_kn_detail  → identify an object type; related_metric_count must be positive
get_object_types               → select a metric from related_metrics
  ├→ logic-property-resolver   → instance-level metrics and bound logical properties
  └→ query_metric              → type-level metrics or unbound logical properties, using MetricDefinition
```

The Skill surface also has a knowledge-network-independent entry point:
`list_skills` lists published Skills directly, followed by the same
`get_skill_content` → `read_skill_file` → `execute_skill` flow.

To bypass the ontology and access data directly, use
`list_resources` → `describe_resource` → `run_sql`.

Multi-hop retrieval across object types: `search_schema` → `run_cypher`. Labels are object types, relationship types are relation types, and properties are logical property names, so neither `resource_id` nor physical column names are needed. Fall back to `run_sql` only for what the Cypher subset cannot express.

## Managed calls and ad-hoc calls

Every business operation runs in one of two modes, chosen by whether the request
names a managed interaction in `bkn_context`.

| | Managed | Ad hoc |
|---|---|---|
| How it is chosen | `bkn_context` carries a `conversation_id` and an `interaction_id` | `bkn_context` is absent or empty |
| What is recorded | an Operation under that interaction, a receipt on the response, and evidence for what was read | nothing |
| What it depends on | Trace Core, in addition to the downstream the operation queries | only that downstream |
| Where it is available | every surface | the REST routes under `/kn/` |

**The MCP surface requires it.** An MCP business tool refuses a call with no
managed interaction, so an agent starts with `bkn_start_interaction`, passes the
two ids on every later call, and closes with `bkn_finish_interaction`. That is
the right default there: MCP is where agents call, and an agent turn is exactly
what an Interaction records.

**The REST surface admits both.** A `/kn/` request with no `bkn_context` runs ad
hoc and never touches Trace Core. A request that names one is managed and is
recorded like any other. The exception is `/mcp/proxy/.../call`, which is an
agent calling a tool under another name, so the managed context stays mandatory
there whatever the transport.

Note that only two shapes count as ad hoc: no `bkn_context` at all, and an empty
one. A `bkn_context` holding one id and not the other, or only
`parent_operation_id`, or a misspelt field, is a caller wiring the context up and
getting it wrong, and is rejected rather than quietly downgraded.

### Which one to use

Use a managed call when the answer has to enter the evidence chain: an agent turn
someone may audit afterwards, anything that executes an action, and any reading a
later decision will be justified by. The cost is a dependency on Trace Core and
the round trips that go with it.

An ad-hoc call is the right choice for everything else, and the callers of the
REST surface usually are everything else: Studio answering a click, an operator
at the CLI, one service asking another for a schema. The cost is explicit — no
Operation, no receipt, no evidence — so a question whose answer nobody will have
to justify pays nothing for provenance it will not use.

Two things follow that are worth stating, because both have been got wrong:

- **Do not mint a conversation and an interaction just to satisfy the guard.**
  Single-operation records dilute the concept rather than document anything. If
  the call does not belong to an agent turn, make it ad hoc.
- **Do not retry a managed call through a Trace Core outage when the answer never
  needed provenance.** Route it to the REST equivalent instead. A statistic a
  skill computes on the way to an answer is usually of this kind. See #1691 for
  what the managed path costs per call.

## Conventions

- **OpenAPI version:** 3.0.3.
- **Authentication:** `Authorization: Bearer <token>` with either an OAuth access token or a self-issued AppKey with the `bak_` prefix. The service derives the account from the credential; callers do not send `x-account-id` or `x-account-type`.
- **All operations use POST:** This includes query-like operations and operations without a request body, such as `list_knowledge_networks`.
- **Response format:** Every operation accepts `?response_format=toon` and returns `application/toon`, which represents homogeneous arrays as tables to reduce token usage. The default is `json`.
- **Error envelope:** This service does not use `comm-go/rest.BaseError`. It uses `code`, `description`, `solution`, `link`, and `details`, as defined by [`ErrorAgentRetrieval`](../_shared/errors.yaml). Downstream error bodies are passed through unchanged and commonly use `error_code` and `error_details`. Locally generated codes start with `Public.*` or `agentRetrieval.*`.
- **Internal APIs:** Most operations have a corresponding `/api/agent-retrieval/in/v1/...` route with the same request and response structure. External routes forward the original bearer token to Execution Factory. Internal routes derive the visitor from trusted `X-Account-ID` and `X-Account-Type` headers and use Execution Factory's `/internal-v1/caller/...` face, which performs the same caller-level permission checks without substituting a service identity. A request with neither a bearer token nor a trusted caller identity returns 401. Arbitrary function-code execution is the exception: it has no caller-scoped internal equivalent and returns 403 internally. This documentation covers only external APIs; `driveradapters/rest_private_handler.go` is authoritative for internal routing.
- **Do not construct instance identities:** Values required by `_instance_identities` must come from `_instance_identity` in `query_object_instance` or `query_instance_subgraph` results.
- **Object types define allowed operators:** Use the object type's `condition_operations`, returned by `get_object_types`. The client supplies this declaration when modeling the network and it is stored without server-side validation; ontology-query makes the final decision.

## Contract inspection coverage

All operations in this module use POST, while the inspection tool sends GET by
default. Read-only operations therefore use `x-contract-probe`; see
[tools/README.md](../tools/README.md).

```bash
make api-contract-diff CONTRACT_FACE=ex CONTRACT_SSH=root@<host> \
     CONTRACT_ARGS="--include-probe-post --token $TOKEN"
```

Sixteen of the 26 operations are probed. The remaining operations require
manual review because their response structures have not been verified against
a running environment:

| Endpoint | Why it is not probed |
|---|---|
| `execute_action` | Has side effects and starts a real action execution |
| `logic-property-resolver` | Has side effects and requires a real instance identity |
| `query_metric` | Requires a modeled metric whose `metric_id` cannot be synthesized |
| `run_sql` | Requires meaningful SQL for a concrete resource |
| `run_cypher` | Requires a meaningful statement over concrete object and relation types |
| `POST /mcp` | Uses JSON-RPC session semantics rather than an ordinary request/response contract |
| `execute_skill` | Has side effects and executes a command in the sandbox |
| `get_skill_content` | Requires the `skill_id` of a published Skill |
| `read_skill_file` | Also requires an existing `rel_path` inside the Skill package |

Among the probed operations, `get_action_info` and `get_action_execution`
require action types or execution records in the environment. Without them, the
report marks the operation as missing probe parameters or returns 404, so it
remains unverified. `search_capabilities` returns an empty list plus `message`
when the network has mounted nothing. `list_skills`
normally returns an empty list plus `message` when no Skill is published.
