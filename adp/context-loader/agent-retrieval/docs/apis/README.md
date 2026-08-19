# agent-retrieval API documentation

**The OpenAPI documentation for the external interface (`/api/agent-retrieval/v1`) has been moved to the top-level documentation center:**
[`docs/api/context-loader/`](../../../../../docs/api/context-loader/).

According to the "document placement" rules in [`rules/CONTRIBUTING.md`](../../../../../rules/CONTRIBUTING.md),
OpenAPI documents for all services live under the top-level `docs/api/` directory and are no longer kept under each module's own `docs/` directory.
Update external API documentation there, and do not add new external-interface YAML files in this directory.

The remaining files in this directory are drafts for the **internal interface (`/api/agent-retrieval/in/v1`)**.
They authenticate with `X-Account-ID` / `X-Account-Type` headers instead of tokens, are not published externally, and are not included in the documentation site:

| Directory | Contents |
|---|---|
| `api_private/` | Request/response definitions for internal endpoints |
| `api_public/` | Historical external drafts, replaced by the documentation center and kept only for comparison |
