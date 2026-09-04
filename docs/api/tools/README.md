# API Contract Inspection Tool

`api_contract_diff.py` calls running services and compares every observed field
with the documented HTTP 200 response schema.

## Problem addressed

`make api-docs-lint` proves that documentation is internally consistent: YAML
is valid and references resolve. It cannot prove that documentation matches the
implementation. Live calls reveal issues such as:

- a documented string that is actually an `{id, type, name}` object;
- a misspelled field such as `updator` instead of `updater`;
- an object documented where the service returns an array;
- response fields that the service returns but the documentation omits.

Static code inspection also misses handler composition across layers and the
runtime effects of `omitempty`.

## Usage

```bash
# Inspect the tokenless internal surface on the development VM
make api-contract-diff

# Or invoke the tool directly
python3 docs/api/tools/api_contract_diff.py \
  --spec-dir docs/api --face in \
  --exec-mode kubectl --ssh parallels@10.211.55.4 \
  --account-id <admin-uuid> \
  --out report.md --json-out report.json
```

Override Makefile variables to select another environment:

```bash
make api-contract-diff CONTRACT_SSH=root@14.103.77.23 CONTRACT_ACCOUNT_ID=<uuid>
```

Use a token to inspect the documented external surface:

```bash
python3 docs/api/tools/api_contract_diff.py --spec-dir docs/api \
  --face ex --token "$(openbkn auth token)" \
  --exec-mode kubectl --ssh parallels@10.211.55.4 --out report.md
```

Exit codes are 0 for no gaps, 1 for gaps, and 2 for execution failure.

Remote execution is bounded. SSH uses `BatchMode` and `ConnectTimeout=10`, and
the outer limit allows 20 seconds per request plus 60 seconds for connection
setup. An unresponsive SSH target or pod fails rather than hanging indefinitely.

## Read-only guarantee

- GET requests are allowed.
- Read-only POST operations using `x-http-method-override: GET` require `--include-query-post`.
- Read-only POST operations declared with `x-contract-probe.readonly: true` require `--include-probe-post`.
- A GET with `x-contract-probe` always uses the batched path. GET itself is already allowed; the annotation controls batching and parameter discovery.
- PUT and DELETE are never sent. POST is sent only through one of the two explicit read-only mechanisms above.

## Probing query operations implemented as POST

Some services, including every public context-loader operation, implement
queries as POST without an override header. Declare safe probe behavior through
`x-contract-probe` on the OpenAPI operation:

```yaml
  /kn/list_knowledge_networks:
    post:
      operationId: listKnowledgeNetworks
      x-contract-probe:
        readonly: true                    # Explicitly guarantees no side effects
        order: 1                          # Lower batches run first
        body: {limit: 3}                  # Request body template
        provides: {cl_kn_id: entries[0].id}   # Value for a later batch
  /kn/get_kn_detail:
    post:
      x-contract-probe:
        readonly: true
        order: 2
        body: {kn_id: '{cl_kn_id}'}
        provides: {cl_ot_id: object_types[0].id}
```

| Field | Meaning |
|---|---|
| `readonly` | Must be explicitly `true`. This is the only switch; the tool never guesses whether a POST is safe. On GET it affects batching, not eligibility |
| `order` | Execution batch, default 1. Lower batches finish and publish `provides` values before higher batches start |
| `body` / `query` | Request-body or query template. Values may reference discovered parameters with `{name}` |
| `provides` | `{parameter: value path}`, for example `entries[0].id`. If the path has no value, dependent operations are marked as missing probe parameters rather than called with invalid input |

Rules for annotations:

1. Never annotate side-effecting operations such as `execute_action`; no annotation means the tool never sends the request.
2. Prefix discovered parameter names with the module, such as `cl_kn_id`, to avoid collisions with generic discovery names such as `kn_id`, `ot_id`, and `id`.
3. Keep the annotation beside the endpoint so interface changes and probe changes are reviewed together.
4. Annotate GET only when batching is required to provide an ID to a later operation or to supply fixed query parameters. Ordinary GET requests already run concurrently and cannot consume another request's result from the same batch.

If discovery cannot supply a path parameter, `fill_path` falls back to the
parameter's OpenAPI `example`, `default`, or `enum`. This supports stable values
such as the sole `template_type` value `python`. A fictitious example ID is
still sent and commonly returns an empty list; the report classifies this as an
empty response sample rather than verified behavior.

Required query parameters use one shared `with_required_query` rule. Explicit
probe values win; otherwise the schema supplies a valid value. An operation is
skipped when no value can be synthesized.

## How it works

1. Parse `docs/api/**.yaml`, resolve cross-file `$ref` plus `oneOf`, `anyOf`, and `allOf`, and flatten the HTTP 200 schema into field paths with type and requiredness.
2. Call list APIs to discover scoped path parameters such as `kn_id`, `ot_id`, and `log_id`, then use them in detail APIs without leaking generic IDs across catalogs, resources, or build tasks.
3. Send real requests and flatten response JSON into equivalent field paths.
4. Compare the two sets and report results by severity.

## Reading a report

| Finding | Meaning | Action |
|---|---|---|
| **Type mismatch** | Documented and actual types differ | Fix it; generated clients may fail to parse the response |
| **Required in documentation but absent** | A field on a required chain was not returned | Determine whether the documentation or implementation is wrong |
| **Returned but undocumented** | The service returned a field absent from the contract | Document it so callers know it exists |
| **Optional field not observed** | An optional field was absent in this sample | Not necessarily a defect; `omitempty` or the selected view may omit it |

An additional “empty response sample” section prevents “zero gaps” from being
misread as verification. Array element fields cannot be observed when a list is
empty. Exclude such operations from completed-comparison counts until an
environment with suitable data is inspected.

`--json-out` writes a machine-readable report for CI assertions and
cross-version comparison.

## Known limitations

- Only read-only APIs are covered.
- Only observed fields can be verified; empty collection element schemas remain unobservable.
- Results depend on environment data. Detail operations are skipped when no corresponding resource exists.
- Internal and external surfaces differ. `auth-resources`, `connector-types`, and BKN `/resources` exist only externally and require a token.
- Operations requiring real discovery values are skipped when values such as `at_id` cannot be found; they are neither gaps nor verified.
- Schema traversal stops at `MAX_DEPTH=7` and breaks recursive reference cycles such as `condition.sub_conditions`.

## Why there is no scheduled workflow

The tool must reach a real environment, while public runners cannot access the
private network. A scheduled workflow requires a self-hosted runner or bastion
plus repository secrets for the environment and credentials. Those prerequisites
do not exist yet, so inspection remains manual.
