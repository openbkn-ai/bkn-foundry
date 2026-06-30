# BKN Foundry Architecture Design Specification (ARCHITECTURE)

[中文](ARCHITECTURE.zh.md) | English

This document defines the BKN Foundry architecture rules. **For day-to-day work, read Sections 1–2**. The appendix is only for terminology and copy-paste examples.

## 1. Architecture Rules (MUST)

### 1.1 Layers and dependencies

- **Foundry (no UI)**: Foundry must not include UI/Web Console/Portal/BFF. It only exposes **APIs/SDKs** and admin APIs.
- **Product dependency**: Products call Foundry over its Public APIs. No reverse dependency — Foundry must not depend on products.
- **Component optionality**: Capability modules are optional by default and must support enable/disable; consumers must degrade gracefully when a component is disabled (see Section 2).

```mermaid
flowchart LR
  Product[Product] --> Foundry[Foundry]
```

### 1.2 Backend services (no page-scoped BFF)

- **Allowed**: product-domain services with a real domain model / transactions / rules / data ownership.
- **Forbidden**:
  - A backend that only stitches/forwards/maps fields for a single page or view (page-scoped BFF)
  - Splitting a backend microservice per micro-app
- **Call paths**: consumers call **Foundry Public APIs** directly, or via a unified API Gateway for auth pass-through and necessary protocol adaptation. A gateway must **not** evolve into “one BFF per page/module”.

Before adding a backend service, answer:

- Does it have persistent domain data and consistency/transaction needs?
- Does it require server-side permission/compliance logic that cannot reuse platform capabilities?
- Does it have a long-lived evolving domain model (not temporary stitching)?
- Is it reused by multiple products and not part of Foundry?

If all answers are “no” → do not add a new service.

### 1.3 API rules (Foundry Public APIs must be backward compatible)

- **API tiers**: Public / Internal / Experimental
  - Cross-component dependencies are only allowed on Public; Internal/Experimental must not be depended on across components.
- **Foundry backward compatibility**:
  - Foundry Public APIs **must be backward compatible** (no breaking changes within the same major version)
  - Any Foundry endpoint called by products must be treated as Public API
- **HTTP versioning**: URL major only (`/api/v1` → `/api/v2`)
  - Within the same major: only add optional fields (with default semantics), add new endpoints, extend enums (clients tolerate unknown values)
  - Breaking changes: only by introducing `/api/v2` and providing a deprecation window (e.g., 2 releases or 90 days)
- **Contract**:
  - HTTP: OpenAPI 3.1 (unified error model + pagination/filter/sort)
  - Skill: Claude Skills (tool/function calling). Must declare auth/tenant/audit and input/output schemas.

- **Compatibility definition (MUST)**:
  - **Request/Input compatibility**: older clients/callers must still work when fields are missing or use older values; do not change optional fields to required.
  - **Response/Output compatibility**: adding new fields is allowed; do not remove/rename existing fields; callers must ignore unknown fields.
  - **Behavior compatibility**: semantics must remain stable; no “same name, different meaning”.

- **Skills must be compatible too (Claude Skills)**:
  - If a Skill is used by products, treat it as **Public** and keep it backward compatible.
  - **Stable `name`**: once published, `name` must not change (renaming means a new Skill).
  - **Schema compatibility**:
    - `input_schema`: only add optional fields / extend enums (callers tolerate unknown values). Do not delete fields or change optional to required.
    - `output_schema`: only add fields. Do not delete/rename existing fields.
  - **Breaking changes**: only via a new `version` (and a new `name` if needed) with a deprecation window.
- **Change requirements**: API changes require ADR + OpenAPI diff (breaking detection) + contract tests (critical endpoints)

### 1.4 Service budget (MUST)

- **Foundry**: backend microservices **< 5**

Counting rules:

- Count: independently deployable/scalable backend services with their own runtime and release cadence (Foundry services)
- Do not count: DB/cache/message infrastructure; local-only mocks

Minimum enforcement:

- When adding/splitting a backend service, update the “service inventory” and provide the Foundry service count in the PR description
- CI must include an automated count check (exceeding budget requires explicit exemption)

Exemption (must be recorded):

- Only short-term exemptions are allowed, with a consolidation plan and timeline

## 2. Mandatory checklist (MUST)

- **Dependency direction**: products call Foundry; no reverse dependency
- **Foundry has no UI**: no React/Vue/static assets/routes/Web Console in Foundry repos
- **Optional components**: disabling optional components must not prevent the system from starting; consumers degrade gracefully
- **APIs**: OpenAPI updated + breaking detection passed + deprecation/migration notes + contract tests
- **Backend additions**: no page-scoped BFF; any new service must pass the questions in 1.2
- **Backends**: do not add a backend microservice per micro-app or per page; new backends must be product-domain services
- **Budget**: Foundry < 5; service inventory and counts updated

---

## Appendix: terminology and examples (optional)

### A.1 Terminology (extended)

- **Page-scoped backend / page-scoped BFF**: a backend serving only one page/route/view; primarily used for stitching/forwarding/permission filtering for that page.

### A.2 OpenAPI example (minimal snippet)

```yaml
openapi: 3.1.0
info:
  title: Knowledge Query Public API
  version: 1.2.0
paths:
  /api/v1/knowledge/queries:
    get:
      summary: List queries
      parameters:
        - in: query
          name: page
          schema: { type: integer, minimum: 1, default: 1 }
      responses:
        "200":
          description: OK
        "401":
          description: Unauthorized
        "500":
          description: Internal Server Error
```

### A.3 Skill example (Claude Skills / tool calling)

```yaml
---
name: knowledge.query
version: 1.0.0
stability: public
description: "Query the knowledge network and return structured results."

auth:
  required: true
  scopes:
    - knowledge:read
tenant:
  required: true
audit:
  required: true

runtime:
  timeout_ms: 15000

io:
  input_schema:
    type: object
    additionalProperties: false
    properties:
      question:
        type: string
        minLength: 1
    required: ["question"]
  output_schema:
    type: object
    additionalProperties: false
    properties:
      answer: { type: string }
      requestId: { type: string }
    required: ["answer", "requestId"]
---
```
