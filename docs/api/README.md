# 📚 API Documentation

This directory contains the OpenAPI documentation for bkn-foundry services. YAML is the common publication format. Handwritten modules treat YAML as the source of truth, while generated modules treat source annotations as authoritative. Tooling renders interactive HTML from the YAML files.

## 👀 Viewing the documentation

- **Online (recommended):** After changes are merged into `main`, CI publishes versioned, interactive, module-grouped documentation to **GitHub Pages**, including search, collapsible sections, examples, and authentication guidance.
- **Generate interactive HTML locally:**

  ```bash
  npm install          # First run: install @redocly/cli and other documentation tools
  make api-docs-html   # Render to _generated/html/, then open index.html
  ```

## 🔑 Calling the APIs

APIs require authentication through `Authorization: Bearer <token>`. Obtain a token with one of these methods: **1. CLI login** (`openbkn auth login`, which stores the token in `~/.bkn/` and sends it automatically); **2. AppKey** (`POST /api/safe/v1/me/api-keys` issues a `bak_` key for automation); **3. device authorization flow** (an integrated application directs the user to sign in through `POST /oauth2/device/auth` without registering a client). The authentication section on the generated documentation home page contains complete examples.

## 🗂️ Modules

The order below matches the card groups on the documentation home page. It is controlled by `MODULES` in the repository [Makefile](../../Makefile).

| Module | Directory | Coverage |
|---|---|---|
| 🟦 bkn-backend | [`bkn/`](bkn/) | Business knowledge networks: object types, relation types, action types, concept groups, metrics, and import/export. **Complete** |
| 🟫 context-loader | [`context-loader/`](context-loader/) | Agent context entry points: schema retrieval, instance and subgraph queries, logical properties, action execution, Skill retrieval, direct data access, and MCP. **Complete external surface**; internal `/in/v1` APIs are excluded |
| 🟩 ontology-query | [`ontology-query/`](ontology-query/) | Ontology queries, semantic retrieval, action execution, and logs. **Complete** |
| 🟨 vega-backend | [`vega/`](vega/) | Data observability: catalogs, resources, connectors, build tasks, discovery tasks, and raw queries. **Complete** |
| 🟩 execution-factory | [`execution-factory/`](execution-factory/) | Execution factory: functions, sandbox observability, import/export, operators, MCP, toolboxes, and Skills. **Complete public surface** (89 endpoints). Only Ingress-exposed `/v1` APIs are included; the tokenless `internal-v1` surface is intentionally excluded, and `/api/capabilities-lab/v1` is not documented yet |
| 🟧 mf-model-manager | [`mf-model-manager/`](mf-model-manager/) | Model factory. **Partial**: currently covers large-model connection testing, default model settings, and usage overview. Small models, quotas, prompts, and other APIs are not yet documented |
| 🟥 agent-observability | [`agent-observability/`](agent-observability/) | BKN Trace: managed session lifecycle, business evidence, technical traces, and snapshots. Generated from Go annotations; **do not edit the YAML directly**. **Complete** |
| 🟪 bkn-agent | [`bkn-agent/`](bkn-agent/) | Agent runtime: agent CRUD, conversations, tasks, prompts, and import/export. **Complete** |

### Unpublished modules

These directories remain in the repository but are not published on the site. They are registered in `MODULES_UNPUBLISHED` in the Makefile.

| Directory | Reason |
| --- | --- |
| [`bkn-safe/`](bkn-safe/) | Contains the self-service read surface and a cluster-internal authorization contract. Both are linted but are not published as general external integration APIs |
| [`observability/`](observability/) | Contains only `observability.json`; it has no publishable YAML and would render as an empty group |

> Every new module directory must be registered in either `MODULES` or `MODULES_UNPUBLISHED`. The `make api-docs-*` targets fail otherwise, preventing documentation from being added silently without appearing on the site.

### ⚠️ `/api/ontology-manager/v1` is a legacy alias

bkn-backend registers both `/api/bkn-backend/v1` and `/api/ontology-manager/v1` as equivalent external routes. The same applies to their internal `in/v1` surfaces. The latter was retained for compatibility during the monorepo refactor in #111 and is still exposed by the Helm Ingress.

**The canonical prefix is `/api/bkn-backend/v1`**, and this documentation uses only that prefix:

- all in-repository service calls use `/api/bkn-backend/v1`; none use the alias;
- the alias remains available to avoid breaking existing clients and can be removed after external usage has been ruled out.

## 🔗 Shared definitions

`_shared/` centralizes schemas reused across modules. Module YAML files reference them with `$ref` instead of embedding copies.

| File | Content |
|---|---|
| [`_shared/errors.yaml`](_shared/errors.yaml) | Common Go service error envelope (`rest.BaseError`: `error_code / description / solution / error_link / error_details`). Reference: `$ref: '../_shared/errors.yaml#/components/schemas/Error'` |
| [`_shared/auth.yaml`](_shared/auth.yaml) | Authentication schemes (OAuth2 client credentials and `bak_` AppKey). Reference: `$ref: '../_shared/auth.yaml#/components/securitySchemes/OAuth2'` |

> ⚠️ mf-model uses FastAPI and has a different error envelope (`code / detail / link`). If documented, it must use a separate `errors-fastapi.yaml` rather than being forced into the common schema.

The resource IDs, operation inheritance, execution subjects, batch behavior, and failure boundaries shared by bkn-safe, BKN, ontology-query, context-loader, and execution-factory are defined in the [knowledge-network authorization contract](knowledge-network-authorization.md). Concrete methods, request fields, and response fields remain authoritative in the corresponding module YAML.

## 🛠️ Rendering pipeline

Everything under `_generated/` is generated output. It is not committed and must not be edited manually.

```bash
npm install            # Install @redocly/cli and widdershins from the root package.json
make api-docs-lint     # Validate OpenAPI YAML and resolve references
make api-docs-html     # Render interactive HTML to _generated/html/
make api-docs          # Optionally render Markdown to _generated/*.md
```

- **CI:** [`.github/workflows/ci-docs-api.yml`](../../.github/workflows/ci-docs-api.yml). Pull requests that touch `docs/api/**` run lint. Pushes to `main` render HTML and publish it to **GitHub Pages**. Repository Settings → Pages must use “GitHub Actions” as the source.
- **Lint configuration:** [`.redocly.yaml`](../../.redocly.yaml). All `$ref` values must resolve. Existing example and description issues are warnings and should be cleaned up when the owning module is updated.

## ✍️ Conventions

See the documentation placement rules in [`rules/CONTRIBUTING.md`](../../rules/CONTRIBUTING.md). Key points:

- Add or update API documentation in the relevant module `*.yaml`, with one resource per YAML file.
- `agent-observability` is generated. Update its Go annotations and run `make -C bkn-trace/agent-observability gen-swag`; never edit the published YAML directly. `check-swag` verifies that runtime JSON, Go documentation, and published YAML remain synchronized.
- Reference shared errors and authentication from `_shared/`; do not copy them.
- The legacy `adp/docs/api/` location contains only [`MOVED.md`](../../adp/docs/api/MOVED.md) and must not receive new files.
