# Dependencies

1. This service handles large-model and small-model API calls. It shares the same base image and database with `mf-model-manager`.

## Error Contract for the OpenAI-Compatible Surface

`/v1/chat/completions`, including the public route and the S2S `private` route, is declared as OpenAI-compatible.
Callers such as `@ai-sdk/openai-compatible`, `openai-python`, and LangChain parse responses with
`union(chunkSchema, errorSchema)`: either the top level contains `choices`, or the top level contains `error`.
Therefore, every failure exit on this route, including SSE frames and JSON bodies, must use this shape:

```json
{"error": {"message": "...", "type": "...", "param": null, "code": "..."}}
```

Rules:

- **Do not wrap with an envelope.** Model Factory's own `{code, description, detail, solution, link}`
  shape matches neither side. Clients throw `TypeValidationError` and expose the raw body to end users (#620).
- **Pass through compliant upstream errors as-is.** If the upstream returns `{"error": {...}}`, forward it directly.
  Do not stringify the JSON and put it into another field, because that forces callers to run `JSON.parse` twice.
- **Status codes follow upstream semantics, but dependency-side auth codes are not passed through.** Pass through
  4xx codes that describe a problem with the caller's current request itself, namely 400/408/413/422/429.
  Upstream 401/403/404 describe authentication between this service and the model provider. Passing them through
  would make callers interpret the result as their own credential or permission failure. This service's own 403
  means "no execute permission for this model"; dependency-side 401/403/404 are collapsed to `502`, with the real
  cause preserved in `error.type`. 5xx errors are collapsed to `503`, connection failures are `502`, and rate
  limiting or unavailability carries `Retry-After`. Mapping and classification are centralized in
  `app/utils/openai_error.py`.
- **Do not leak unknown upstream body shapes.** When none of the known fields, such as `error.message`, `message`,
  or `detail`, match, return fixed wording and write the original text only to logs. Provider 5xx responses often
  echo the full request, internal trace IDs, and gateway node names. Internal exceptions follow the same rule:
  `str(e)` must not enter `error.message`.
- **For streaming, send an error frame before closing the stream.** If an error occurs after the SSE stream has
  started, send one `data: {"error": {...}}` frame and then terminate. Do not place the error where a chunk should
  be. Note that `EventSourceResponse` flushes response headers before the generator runs, so streaming HTTP status
  is always 200 and errors can only be expressed by error frames.
- **Retry transient errors first.** Upstream 429/502/503/504 use backoff retries through `sleep_before_retry`;
  report the error only after retries are exhausted. 4xx parameter errors are not retried.

The compatibility surface also covers the framework layer. Request bodies rejected by pydantic use FastAPI's
`RequestValidationError` handler and are converted by path into OpenAI error bodies in `_is_openai_compat` inside
`app/routers/__init__.py`. That handler is shared by the whole service. Small-model and model-management endpoints
are not compatibility surfaces and must continue to use the envelope shape, so do not apply this conversion globally.

Internal envelopes, including parameter validation, permission, and quota errors, are converted by
`llm_controller.envelope_error_response()` into the shape above before leaving the service. The original `code`
lands in OpenAI's `code` field, preserving the machine-readable identity.

Regression tests are in `app/test/test_openai_error.py` and `app/test/test_llm_error_contract.py`.

## Logging Discipline

When calling third-party models, request headers carry the provider `api_key`, and the request body carries the
user's full conversation content. If both are written into logs as-is, they leave this service through the log
collection pipeline, whose read-permission model is separate from credential management and business-data
management (#636).

All request context written to logs must go through `app/utils/log_redact.py`:

- `safe_headers(headers)` masks `Authorization`, `api-key`, `Cookie`, and similar fields. It preserves the `Bearer`
  scheme prefix and the first four characters, which is enough to identify which key was used but not enough to use
  the key. Other headers are preserved as-is.
- `request_digest(params)` compresses the request body into model, stream, message count, character count, role
  sequence, and sampling parameters. It contains no `content`.
- `messages_digest(messages)` is the shorthand used when only `messages` is available.
- `safe_url(url)` keeps only scheme, host, and path, and removes the entire query. Baidu OAuth puts
  `client_secret` in the query string, and `OtherClient.api_url` is freely configured by administrators.

**Successful paths follow the same rule.** `BaiduTianchenClient` previously logged full `messages` at INFO on every
call, which leaked more aggressively than the error-only paths. It now logs only the digest.

To reproduce issues, use the model and parameters in the digest together with the caller's own trace. Do not replay
the user's original text from logs. See `app/test/test_log_redact.py` for regression coverage, including a source
scan assertion that prevents leak points from being reintroduced.
