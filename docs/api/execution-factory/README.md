# Execution Factory API Documentation

> OpenAPI 3.0.3 definitions for the Execution Factory HTTP API, whose service
> name is `agent-operator-integration`. Platform capabilities—code, operators,
> toolboxes, MCP servers, and Skills—are registered, debugged, published, and
> executed through this service.

## File index

| File | Topic | Endpoints under `/api/agent-operator-integration/v1` |
|---|---|---|
| [function.yaml](function.yaml) | Functions | `POST /function/execute`, `POST /function/infer-schema`, `GET /function/dependencies`, `GET /function/dependency-versions/{package_name}`, `GET /template/{template_type}`, `POST /ai_generate/function/{type}`, `GET /ai_generate/prompt/{type}` |
| [sandbox.yaml](sandbox.yaml) | Sandbox observability | `GET /sandbox/health`, `GET /sandbox/pool`, `GET /sandbox/sessions`, `GET /sandbox/sessions/{id}` |
| [impex.yaml](impex.yaml) | Import/export | `GET /impex/export/{type}/{id}`, `POST /impex/import/{type}` |
| [operator.yaml](operator.yaml) | Operators | Registration, editing, update, list, detail, batch names, status, deletion, debugging, history, market, and category: 14 operations |
| [mcp.yaml](mcp.yaml) | MCP | Probe, CRUD, status, tool debugging, three market operations, proxy tool listing and invocation, and three exposed operations: 16 operations |
| [toolbox.yaml](toolbox.yaml) | Toolboxes | Toolbox CRUD and status, tool CRUD and enablement, debug and proxy calls, operator conversion, OpenAPI capability packages, and four market operations: 21 operations |
| [skill.yaml](skill.yaml) | Skills | Registration, list, detail, metadata and package updates, publishing and history, two market operations, three consumer reads, three management reads, execution, and five index-build operations: 25 operations |

**All 89 public operations are documented.**

## Run a function end to end

**1. Read the template.** The entry function must be named `handler`.

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$BASE/api/agent-operator-integration/v1/template/python"
```

**2. Inspect installed packages.** Packages in this list can be imported
directly and do not need to be declared in `dependencies`.

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$BASE/api/agent-operator-integration/v1/function/dependencies"
```

**3. Execute the function.** `event` is the only input. The `handler` return
value becomes `result`, while printed output becomes `stdout`.

```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  "$BASE/api/agent-operator-integration/v1/function/execute" -d '{
    "code": "from typing import Dict, Any\n\ndef handler(event: Dict[str, Any]) -> Any:\n    name = event.get(\"name\", \"world\")\n    print(f\"greeting {name}\")\n    return {\"message\": f\"Hello, {name}\"}\n",
    "event": {"name": "BKN"}
  }'
```

```json
{
  "stdout": "greeting BKN\n",
  "stderr": "",
  "result": { "message": "Hello, BKN" },
  "metrics": {
    "duration_ms": 71.97115616872907,
    "cpu_time_ms": 3.9721400000019003,
    "peak_memory_mb": null,
    "io_read_bytes": null,
    "io_write_bytes": null
  },
  "exit_code": 0,
  "execution_time_ms": 0,
  "artifacts": [],
  "session_id": "sess_aoi_0"
}
```

> This is a real response from a test environment. `peak_memory_mb` and the I/O
> counters depend on sandbox runtime support and are null when unavailable.

**4. Add a third-party package.** Look up a version first, then declare the
dependency. The first execution is slower while the package is installed.

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$BASE/api/agent-operator-integration/v1/function/dependency-versions/requests?python_version=3.10"
```

```jsonc
// Add these fields to the execute request
"dependencies": [{ "name": "requests", "version": "2.32.3" }],
"dependencies_url": "https://pypi.tuna.tsinghua.edu.cn/simple/"   // Use a private mirror when required
```

**5. Generate code or metadata.** A large model can generate the function, or
infer parameter definitions from existing code.

```bash
# Generate code from a description
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  "$BASE/api/agent-operator-integration/v1/ai_generate/function/python_function_generator" \
  -d '{"query": "Write a function that accepts a list of orders and returns the total amount and order count"}'

# Infer input and output definitions from code
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  "$BASE/api/agent-operator-integration/v1/ai_generate/function/metadata_param_generator" \
  -d '{"code": "def handler(event):\n    return {}\n"}'
```

## Common pitfalls

- **The entry function must be named `handler`** with the signature `handler(event: Dict[str, Any]) -> Any`. A different name does not necessarily produce a missing-entry error; behavior may simply be incorrect.
- **A code exception still returns HTTP 200.** Use `exit_code`—zero means success—and `stderr` to determine the execution result.
- **Public `timeout` values are seconds.** The internal `POST /internal-v1/function/exec/{version}` operation uses milliseconds.

## Conventions

- **OpenAPI version:** 3.0.3.
- **Authentication:** `Authorization: Bearer <token>` with an OAuth access token or a self-issued AppKey with the `bak_` prefix.
- **Authorization:** Function execution requires `execute` on the operator. AI generation requires `create`. Check role grants after a 403 response.
- **Error envelope:** This service does not use `comm-go/rest.BaseError`. It uses `code`, `description`, `solution`, `link`, and `details`, as defined by [`ErrorCompact`](../_shared/errors.yaml), which is also used by context-loader.
- **Internal APIs:** `/api/agent-operator-integration/internal-v1` is the internal surface and includes operations such as `POST /function/exec/{version}`, which executes a registered function version with a millisecond timeout. This documentation excludes internal APIs.
- **Capabilities surface:** `/api/capabilities-lab/v1` is another route group merged from the former capabilities-lab service. It is exposed through Ingress but has different paths and semantics and is not yet documented here.
- **All `*_time` fields are nanoseconds:** Operator, MCP, toolbox, and Skill timestamps use `time.Now().UnixNano()`, for example `1784880971306127803`. Parsing them as milliseconds produces a date near 1970. Sandbox-observability fields ending in `*_at`, including `created_at`, `last_used_at`, and `checked_at`, are RFC3339 strings.
- **Contract inspection:** Read-only GET operations are probed by default and need no annotation. The 13 `x-contract-probe` annotations in this module are only for operations that must run in batches. List operations use `provides` to feed values such as `box_id`, `skill_id`, and `mcp_id` into detail operations in later batches. See [tools/README.md](../tools/README.md).

## Publication scope

Only the Ingress-exposed public surface at
`/api/agent-operator-integration/v1` is documented. This is the surface reached
by browsers and Studio. The `internal-v1` surface is intentionally not exposed
through Ingress. It does not validate tokens and derives identity from the
caller-supplied `X-Account-ID` header. Exposing it would make roughly 40 write
operations reachable from outside the cluster without credentials; see the
chart values comments and #326. Do not describe the internal surface in public
documentation.

## Coverage boundary

All 89 operations on the public `/api/agent-operator-integration/v1` surface are
documented: 7 function, 4 sandbox-observability, 2 import/export, 14 operator,
16 MCP, 21 toolbox, and 25 Skill operations.

> The operation total was corrected twice: 89 → 90 after the MCP
> `Any /mcp/app/{mcp_id}/mcp` route was missed because the route-extraction
> expression did not count `Any`, and 90 → 91 after
> `POST /function/infer-schema` was missed because an outdated handler file was
> inspected. The third change was a removal rather than a correction: 91 → 89
> when `POST /operator/intcomp` and `POST /tool-box/intcomp` were removed with
> the built-in component registration mechanism. The current total has been
> checked line by line against `RegisterPublic` and cross-checked against live
> access logs.

### Two levels of verification

- **Routes and publication scope:** Every `RegisterPublic` route was checked; all 89 are present.
- **Field-level contracts:** Only operations exercised against a running environment are considered verified. Other schemas were derived from Go types and require manual review when changed.

The historical draft under
`adp/execution-factory/operator-integration/docs/apis/` can provide context but
has drifted from the implementation. A similar context-loader draft contained
three observed errors, so drafts are not authoritative.

### Live verification coverage

> The numbers in this section come from an inspection performed when the total
> was still 91, before the two intcomp operations were removed. Those operations
> were unprobed writes, so only the total changed from 91 to 89 and the write
> count from 42 to 40. Other categories are unaffected; the original numbers
> remain here to preserve the inspection record.

The contract inspection ran against development VM `parallels@10.211.55.4`
using image `0.1.3-main.20260730112246.sha185a9c2`. Thirty of 91 operations
completed field-level comparison with no gaps. Four returned empty arrays, so
only 26 operations had every field observed.

```bash
make api-contract-diff CONTRACT_FACE=ex CONTRACT_SSH=parallels@10.211.55.4 \
     CONTRACT_ARGS="--include-probe-post --token $TOKEN"
```

| Category | Count | Notes |
|---|---|---|
| Field-level comparison completed | 30 | No gaps. Four responses were empty (`function/dependencies`, `operator/info/list`, `operator/market`, `skills/index/build`), leaving 112 fields under empty arrays unobserved. Do not treat their zero-gap result as full verification |
| Writes or operations not marked read-only | 42 | Registration, editing, deletion, publication, and execution are intentionally not sent |
| HTTP 200 without a JSON body | 7 | Deletion, status changes, SSE streams, and `.adp` import have no 200 response schema to compare |
| Missing path parameters | 7 | The environment lacked data such as operator versions or MCP tool names |
| Environment-dependent failure | 5 | An unreachable MCP proxy (503), proxy MCP without a connection address (400), two non-JSON Skill package downloads, and a required `tool_name` for `/tool-box/market/tools` that could not be synthesized |

### Observed production-like usage

Access logs from test environment `14.103.77.23` were compared over three days.
Thirty-one of the then-current 91 operations were observed. This does not imply
that the others are unused; writes, market operations, history, and index
rebuilds are naturally less frequent in a test environment. Example hot paths
were `GET /tool-box/list` with 265 calls, `GET /skills` with 87,
`GET /operator/category` with 74, and `GET /mcp/list` with 67.

Another 40 paths in the logs belonged to `internal-v1`, used inside the cluster
by bkn-agent and context-loader through ClusterIP for tool proxying and
`function/exec`. They bypass Ingress and remain outside this documentation.
The other excluded surface is `/api/capabilities-lab/v1`.
