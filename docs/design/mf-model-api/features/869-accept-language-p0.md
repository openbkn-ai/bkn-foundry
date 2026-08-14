---
issue: "#869"
branch: "feat/869-mf-model-api-locale"
module: "mf-model-api"
status: "draft"
author: "@rongwei-liu"
created: "2026-08-14"
pr: ""
---

# MF Model API Accept-Language P0

Status: Draft, awaiting acceptance approval

Issue: #869

## Goal

Make `mf-model-api` conform to the platform P0 locale contract without changing
its API machine contracts, model-provider configuration, or database schema.

## Scope

- Negotiate `zh-CN` or `en-US` from `Accept-Language` once per HTTP request.
- Store the result in request-local context and use it for all downstream
  platform requests.
- Localize human-readable error fields only. Keep HTTP status, error codes,
  JSON field names, OpenAI-compatible error structure, and SSE event data
  stable.
- Set `Content-Language` only for localized error representations.
- Apply `Cache-Control: private, no-cache` to business API responses and
  preserve stricter existing directives such as `no-store`.
- Keep health endpoints language-neutral.

## Non-goals

- User or tenant language preferences.
- Localized model output, prompts, tool payloads, or provider responses.
- Locale persistence across asynchronous workloads.
- MCP protocol or session changes.
- Database, provider-key, deployment, or chart changes.

## Request and Response Flow

1. A new ASGI locale middleware parses `Accept-Language` with the shared P0
   rules: `zh-CN` and `en-US` are supported; `zh` and `zh-Hans` map to
   `zh-CN`, `en-*` uses the English base match, and only the legacy alias
   `zh_CN` is accepted before strict BCP 47 validation. `q=0` excludes a
   range; the resolved value is one canonical locale.
2. The middleware writes `effective_locale` to request state and a ContextVar
   before authentication runs. The value is immutable for the request.
3. Authentication and controllers read the frozen locale. Platform-internal
   `aiohttp` calls use `Accept-Language: <effective_locale>` unconditionally;
   they never forward the original multi-value header.
4. JSON error responses are localized at the middleware boundary. Model Factory
   envelopes localize `description`, `detail`, and `solution`; OpenAI
   compatibility errors localize only `error.message`. Their codes and shapes
   remain unchanged.
5. Platform-owned SSE errors are built with a stable Model Factory error code
   and obtain their human-readable message from the frozen ContextVar before
   the error frame is emitted. Provider error frames remain unchanged.
6. The middleware adds `Content-Language` to localized errors and merges the
   private cache policy. It forwards non-JSON and `text/event-stream` response
   starts immediately, so no SSE header waits for a model token. Successful
   JSON can also stream immediately; business JSON errors are buffered until
   their final body chunk before localization.

## Middleware Ordering

`LocaleResponseMiddleware` is registered outside the existing authentication
middleware. This makes the resolved locale available to authentication failures
and lets the response layer cover controller JSON responses, OpenAI errors, and
SSE headers. Health paths bypass locale and cache changes.

## Error Catalog

Add an `app/commons/locale.py` resolver and locale catalogs modeled on the
validated `mf-model-manager` implementation, adapted for MF Model API codes.
Only platform-owned codes present in the catalog are localized. Unknown or
provider-owned OpenAI errors remain unchanged and do not receive
`Content-Language`; existing English controller messages are retained rather
than overwritten. FastAPI's exact default JSON 404 and 405 responses are
converted into stable `HTTP_404` and `HTTP_405` platform errors.

## Internal Calls

Apply the shared header helper to BKN Safe, Hydra, User Management, Permission
Manager, and BKN Trace calls. Do not add platform locale headers to external
model-provider requests.

## Verification

- Table-driven language resolver cases, including `en-GB`, `zh-Hans`, `q=0`,
  wildcard, invalid q values, accepted `zh_CN`, and rejected `zh_Hans`.
- TestClient tests for authenticated errors, OpenAI error bodies, health paths,
  `Content-Language`, and cache merging.
- ASGI tests proving an SSE `http.response.start` reaches the client before the
  first body chunk, while platform-generated SSE error frames carry localized
  messages and stable codes.
- ASGI and FastAPI `BaseHTTPMiddleware` tests proving a chunked JSON error is
  localized only after its final body chunk, with the expected headers and
  OpenAI-compatible error shape.
- Regression tests proving provider OpenAI errors are byte-for-byte unchanged,
  and framework default 404/405 responses follow the documented error shapes.
- Mocked `aiohttp` tests verifying every platform-internal call receives one
  canonical `Accept-Language` value.
- OpenAPI assertions for request and localized-error response headers.
