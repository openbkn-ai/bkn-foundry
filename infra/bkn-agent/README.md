# bkn-agent

The platform's built-in Agent runtime (Epic #202; design: `docs/design/bkn-agent/features/202-bkn-agent.md`).

Legacy management and service-call APIs remain internal. Version 0.2.1 adds user execution APIs with independent credential verification; do not expose the entire service or legacy API through the gateway. By default, the chart creates a scoped Ingress for Studio access through the unified gateway, exposing `/api/bkn-agent/v1/metric-design/executions`, `/api/bkn-agent/v1/executions` (including result recovery), `/api/bkn-agent/v1/me/agents` and `/api/bkn-agent/v1/networks` (the latter currently contains only Agent-setting handlers). Set `publicIngress.enabled=false` for internal-only installations. Agent settings require a verified account, a supported public Agent ID, and the relevant Agent or knowledge-network permission. The built-in Agent administration page additionally needs `publicIngress.adminAgentsEnabled=true`, which exposes only the `/api/bkn-agent/v1/admin/agents` subtree: reads require an authenticated account and writes require `admin-agent:manage`. Neither `/api/bkn-agent/in/v1` nor other legacy management APIs are exposed. The Ingress disables proxy buffering for SSE and sets a 180-second read timeout.

- Engine: LangGraph (Python)
- Model: mf-model-api (OpenAI-compatible, cluster-internal `/api/private`; an empty model uses the platform default)
- Tools: the Execution Factory toolbox is the unified tool plane. Context Loader tools are available only when declared; execution goes through operator-integration with identity headers forwarded. External MCP endpoints require explicit `type: "mcp"` references.
- Skills: Execution Factory internal-v1 (`/skill/content` injection and progressive `/skill/files/read` access)
- Storage: shared `openbkn` database, `agent_`-prefixed tables; migrations in `migrations/bkn-agent/`

## Run locally

```bash
pip install --require-hashes -r requirements.lock
uvicorn main:app --port 30800
```

See `app/config.py` for key environment variables (`RDS*`, `MF_MODEL_API_PRIVATE_BASE`, `OPERATOR_INTEGRATION_BASE`, and `CHECKPOINTER_BACKEND`).

## 0.2.1 LangGraph dependency baseline (#2174 / #537)

The runtime lock pins LangGraph 1.2.15 and the complete dependency trees for LangChain, the checkpoint/MySQL saver, MCP adapters, and observability integrations. `requirements.txt` still declares acceptable ranges. Docker installs from `requirements.lock`; CI installs from that runtime lock and the constrained `test-requirements.lock`. Both verify distribution hashes. Test dependencies do not enter the runtime image.

```bash
pip install --require-hashes -r test-requirements.lock
OTEL_ENABLED=false python -m pytest -q
```

For an explicit upgrade, generate the two coordinated locks, review version and hash differences, and then test. Do not re-resolve dependencies during the build:

```bash
uv pip compile requirements.txt --python-version 3.12 --universal --generate-hashes --output-file requirements.lock
uv pip compile requirements.txt test-requirements.txt --constraint requirements.lock --python-version 3.12 --universal --generate-hashes --output-file test-requirements.lock
```

`fixtures/langgraph/checkpoint-1.2.9.json` was captured in an isolated LangGraph 1.2.9 environment and records the other dependency versions used during capture. The compatibility test reads this checkpoint under the new baseline and continues a real Agent conversation. It verifies library-level compatibility, not every historical production checkpoint or MariaDB. `scripts/capture_langgraph_checkpoint.py` uses only an in-memory saver, a fake model, and local tools; it makes no external calls.

For rollback, restore `requirements.txt`, both locks, and their adapters from the same verified commit, then rebuild. Do not delete historical checkpoints. Do not perform a production upgrade or rollback before recovery compatibility has been accepted against a real database.

## Trace Evidence installation and upgrade

Fresh Helm installations enable `observability.evidencePublisher.enabled` by default. Before enabling it, provide Kafka brokers and an existing Secret (default: `bkn-trace-evidence-kafka`) with nonempty `username` and `password`. The standard installer follows the platform's existing Kafka credential preparation flow, validates the runtime ranges of brokers, queue, and retry settings before installing Agent, and performs a read-only check of the final reference. Operators of standalone Helm installations must prepare these references beforehand. Deployments without Kafka should explicitly set `observability.evidencePublisher.enabled=false`; this renders neither Kafka nor admission configuration and does not check the Kafka Secret. Artifact upload has separate credentials.

The standard installer's override order is: chart/install defaults → installed collection switch and queue parameters → current config file → CLI `--set`. An upgrade does not automatically enable a previously disabled publisher; set it to true explicitly in persistent configuration. Connection addresses and Secret references are resolved from the current installation configuration rather than old installer-enforced defaults (including the retired port 8080 config endpoint). Store custom connections in the current config file or supply them explicitly through installation environment variables or CLI options. Only supported collection switches and queue parameters are retained; inline credentials and legacy internal OAuth/signature configuration are not. Do not use blanket `--reuse-values`.

Internal policy, configuration, heartbeat, and ACK use the existing `agent-observability-internal:8081` endpoint; they do not need an internal OAuth or signature Secret. Starting the publisher does not mean admission is enabled or events have been persisted: central policy, budgets, Kafka consumption, and Ledger/projections each retain their own contracts. A runtime Kafka/control initialization failure records the error type but allows Agent to start; restart after correcting persistent configuration. It does not fabricate events, bypass admission, or claim complete authentication records. Budgets and consumer settings are managed by their respective deployment configurations.

**No tools by default:** `agent.tools` is the complete tool set. Zero declarations mean zero tools. To use tools, explicitly declare `type: "toolbox"`, `"mcp"`, or `"agent"` references in the Agent definition. A failed toolbox reference is an error, not a silent fallback. The built-in `read_skill_file` tool is attached only when a skill is declared or another tool is already installed; it cannot create a tools node on its own.

## API

The contract is frozen in `docs/api/bkn-agent/bkn-agent.yaml` (OpenAPI 3.1, #212). API changes start with the specification: update implementation routes/models, run `python scripts/export_openapi.py` to regenerate the spec, and use `app/test/test_contract.py` to enforce parity.

`/api/bkn-agent/v1/` provides Agent CRUD, `POST /chat` (SSE), `POST /run` plus `GET /tasks/{id}`, `POST /invoke/{agent_id}` (synchronous; responds only at a terminal state), `GET /threads/{id}` (conversation history), Prompt management and caller overrides (`/prompts`, `/agents/{id}/prompt`), and import/export (`/export`, `/import`). Import/export includes the Agent definition and current Prompt version, preserves the original ID through idempotent upsert, records the same name under a different ID as a failure without aborting, and warns about missing cross-environment references.

## Context budget on the public execution surface

Public execution (`/executions`) applies the merged Agent `limits` to what each model call sees. It changes only the copy sent to the model; stored conversation history retains complete tool results. The defaults in `app/core/agent_settings.py` are:

| Field | Default | Meaning |
| --- | --- | --- |
| `data_result_chars` | 12000 (previously 8000) | Maximum characters in one data-tool result; truncate excess and tell the model to request fewer columns instead of paginating |
| `schema_result_chars` | 24000 | Maximum characters in one schema/discovery-tool result |
| `keep_tool_results` | 3 | Keep at least the latest three tool results for the current question; retain older results while within the `keep_tool_results × data_result_chars` budget (previously only the latest three were kept) |
| `summarize_after_tokens` | 60000 | Summarize earlier questions after the conversation exceeds this token count; do not summarize the current question |

Removed results become placeholders that identify the original call and size. Repeated read-only calls within the same question, such as `run_cypher` and `query_object_instance`, reuse an earlier successful result instead of calling the backend again.

## Execution Factory registration

There is none. Published Agents used to be registered automatically in the Execution Factory toolbox (#212); that behavior has been removed. Agents are exposed only through this service's own `/api/bkn-agent/v1` API, with no duplicate tool description in the factory. To attach one Agent to another, use a `type: agent` entry in `tool_refs` rather than the factory.

## Built-in Agent default data (#2170)

Built-in Agents use fixed IDs. Their configuration, shared Prompts, and Prompt history live in existing database tables. `core-data-migrator` installs all default data; there is no JSON config package, extra bootstrap table, or application startup Job, and the former `deploy/` directory has been removed. Administrators edit shared configuration through the existing management API. Initialization does not overwrite existing Agents.

The 0.2.1 fresh-install and upgrade paths are:

- Fresh installation runs `migrations/bkn-agent/mariadb/0.2.1/init.sql`, retaining the original ten tables and semantic-understanding definitions while adding the Agent settings/version tables, Studio Smart QA, Cypher generation, Object Metric Designer, and two business-provenance Agents.
- Upgrade runs the numbered scripts from `01-seed-studio-smart-qa.sql` through `07-seed-object-metric-designer.sql` in order. The Agent seed scripts add only missing defaults; `03-upgrade-business-provenance-defaults.sql` restores official default fixes missed by direct upgrades from an older EE version without changing table schema. Scripts `04-agent-settings.sql` and `05-agent-versions.sql` add the settings and version tables.
- Existing fixed-ID Agents retain their model, tools, status, Prompt reference, and history. The business-provenance optimizer moves from a 180-second to a 600-second limit only when the original management principal is still the last updater and the entire `limits` value matches the old default. It adds a new Prompt version only for an exact match to the published 0.1.4/0.1.5 defaults maintained by that principal and without custom variables. Administrator edits, rollbacks, and conflicting versions remain untouched. Shared Prompt edits use the existing version-management capability; personal override tables and old APIs are not removed here.
- New Agents are written last, so a compatible incomplete default Prompt can be completed on retry. Prompt or name conflicts fail rather than taking over customer configuration. The migrator commits statements individually; it does not promise a cross-table transaction rollback, and a conflict may leave an unreferenced default Prompt row.

Studio Smart QA has fixed ID `studio_smart_qa`. It starts as a draft with an empty model that inherits the platform default and declares the full Context Loader MCP. `runtime.contextLoaderMcpUrl` / `CONTEXT_LOADER_MCP_URL` selects the full `/mcp/` or compact `/mcp-compact/` endpoint. See #2173 below for the public user entry point, network scope, and permission checks. No placeholder basic-data QA Agent is created.

Cypher generation has fixed ID `cypher_generation`. `06-seed-cypher-generation.sql` installs missing defaults; it also starts as a draft with an empty model. It loads only `get_kn_detail`, `search_schema`, `get_object_types`, `get_relation_types`, `search_instance`, and `run_cypher` from the full Context Loader. It writes a complete query with `RETURN` and `LIMIT` using real property names, runs the same query through `run_cypher`, and returns only a query that passed that trial. Prefer nodes, relations, or virtual edges; return scalars when the user asks only for statistics. The caller runs the Cypher code block unchanged.

Object Metric Designer has fixed ID `object_metric_designer`. `07-seed-object-metric-designer.sql` installs missing defaults; it starts as a draft with an empty model and no tools or skills. Only the restricted `/api/bkn-agent/v1/metric-design/executions` entry point can provide the current form and authorized candidates and receive a server-validated field-level proposal. The Agent does not save, publish, or query metric instance data. Follow-up turns are bound to the real user, knowledge network, object type, metric type, and draft version. An administrator must configure the model and publish the Agent before use.

For phase two, Studio calls the existing metric APIs for validation and trial calculation only after an explicit user action. The Agent receives at most a limited `diagnostic` for the current form revision (status, issues, warnings, value type/unit, row count, and duration). It receives no instance identifiers or detail values, and it does not treat client-reported diagnostics as proof of server-side correctness. Trial calculation retains the existing `query_data` path and its underlying data permissions, timeouts, and result limits.

The two business-provenance Agents retain their original fixed IDs, owner, and tool declarations. Missing defaults install the current Prompt and limits; old defaults are repaired only under the conditions above. SQL registration does not create an EE management identity or open license-controlled EE entry points. `python -m app.bootstrap.business_provenance` remains as a compatibility command for deployment scripts: it only verifies the management principal, Agent ownership, and shared Prompt; it no longer calls `/import` or overwrites administrator changes. The old JSON-file environment override is unsupported and fails if set explicitly; manage shared configuration through the management API. The old `scripts/import-business-provenance-optimizer.sh` script has been removed.

Migration execution, production configuration, and deployment still require Owner confirmation. This change was not migrated against a real database locally. If a test environment has already run an older PR's three-table 0.2.1 script, do not automatically drop tables or fabricate migration records. The migrator will not rerun numbered scripts already marked installed; inspect that environment's version and script progress separately before arranging a supplemental migration.

The complete design lives in the [simplified bkn-docs plan](https://github.com/openbkn-ai/bkn-docs/blob/main/docs/foundry/bkn-agent/design/issue-2151-builtin-agent-registration.md).

### Lifecycle owner attribution

For Context Loader interactions, `bkn_start_interaction` returns response-only
`owner` metadata derived from its authenticated account context after Core accepts
Start. Agent preserves that owner with the interaction for Kafka evidence and
private artifact ingress. Question and result artifact bodies also carry the
existing `application_principal_id` and `effective_subject_id` fields from that
owner, because Core persists artifact attribution from the body. Without a
lifecycle owner they retain the trace identity/account fallback used by evidence.
It does not replace caller authentication, tool headers,
or model-visible parameters. Older Context Loader responses without owner still
load tools, but log a coverage warning and cannot establish corrected evidence
attribution. Upgrade Context Loader together with Agent for this contract.

## Public user execution API (#2173)

`POST /api/bkn-agent/v1/executions` returns SSE; `GET /api/bkn-agent/v1/executions/{run_id}` reads run status and the committed result; `POST /api/bkn-agent/v1/executions/{run_id}/cancel` requests cancellation. All three require `Authorization: Bearer ...`. OAuth credentials are validated through Hydra, and `bak_` AppKeys through bkn-safe. External account headers are not accepted as identity. Dependency addresses are configured with `runtime.hydraAdminBase` / `HYDRA_ADMIN_BASE` and `runtime.bknSafeBase` / `BKN_SAFE_BASE`; the original caller credential is forwarded downstream.

The API serves `studio_smart_qa` and `cypher_generation`. Before use, an administrator must complete shared configuration and publish the Agent, and the caller must have that Agent's `use` permission and the knowledge network's `view_detail` permission. Downstream services continue to enforce actual data and action permissions. This API does not automatically publish an Agent, grant permissions, or modify defaults.

```json
{
  "agent_id": "studio_smart_qa",
  "message": "Analyze orders in the current network",
  "client_request_id": "request-unique-01",
  "context": {"kn_id": "network-01"}
}
```

Include the first turn's `thread_id` from `meta` on follow-up turns; use a new thread when switching networks. Requests cannot override the model, tools, Skill, or Prompt. The new entry point always uses the shared Prompt; the old API's personal and request overrides remain compatible.

SSE events are `meta/token/tool_started/tool_finished/usage/result/terminal`, with a `run_id` and monotonically increasing sequence number. Tool events reference a real call ID; parameters and results are size-limited and redacted. The result is written to the existing `t_agent_task` before a successful terminal event is sent; GET can recover the committed result. Missing actual usage is explicitly marked unavailable. See the [OpenAPI contract](../../docs/api/bkn-agent/bkn-agent.yaml) for endpoint and error details.

The full Context Loader MCP remains registered through the existing full/compact endpoint. Network scope is checked during execution. Tools without a trustworthy network constraint, such as arbitrary code execution, cross-Agent calls, or other unsupported types, are unavailable rather than silently allowed. This delivery does not imply that graph-exploration tools, canvas results, or frontend migration are complete.

The same account type, account ID, and `client_request_id` never execute twice. Reusing an ID with different input or while a run is active returns 409; retrying after a terminal state returns the stored result. Cancellation returns the actual committed state, so a later GET may be needed to await completion. Disconnects stop execution, and process interruption follows the old task recovery path to mark failure. SSE replay, background continuation after disconnect, and multi-replica coordination are not promised. The chart remains single-replica. No database tables or columns are added.
