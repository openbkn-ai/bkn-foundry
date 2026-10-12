# Offline Historical Span Codec

This independent module uses the actual OpenSearch exporter v0.148.0 public
Factory and official pdata v1.54.0 JSON parser. The exporter writes only to an
in-process loopback HTTP bulk capture sink. There is no configurable network
destination, database connection, or production index writer.

Build with Go 1.25 or newer:

```sh
go test ./...
go vet ./...
go build -o /private/tmp/bkn-historical-span-codec .
```

The command reads NDJSON from stdin and writes one JSON response per input line.
Input has exactly `document` (an OTLP JSON object using camelCase fields) and
`source_deployment` (a nonempty deployment identifier). Success returns
`accepted: true`, `reason: official_ss4o_encoded`, and `spans`, sorted by
traceId/spanId. Each span contains the complete exporter `payload` and `sidecar`.
Rejected records return `accepted: false`, a bounded reason, and an empty array.
The caller must retain its original input for rejected records.

The sidecar retains the entire typed source document, its original span JSON
pointer, unknown noncritical fields, deployment, codec version, and hashes.
`source_sha256` hashes the exact document bytes. `native_sha256` hashes Go's
deterministic JSON encoding of the corrected payload; it is not a Ledger hash
or a JCS claim. Fields absent from SS4O, including resource schema/counts and
flags, remain in the typed source rather than being invented as native fields.

The pinned exporter uses its default dataset `default` and namespace `namespace`.
These generate `attributes.data_stream` and must match the caller's frozen
contract; this initial command does not accept arbitrary collector configuration.
An original data_stream attribute is retained in the source document if the
exporter overrides it. Queue and retry are disabled, and index-name time
suffixes are disabled at the capture endpoint.

The encoder's wall-clock observedTimestamp on zero-Unix-second span events is
removed. Nonzero original event nanoseconds, including subsecond epoch times,
become the original event @timestamp; unset zero timestamps remain absent.
The exporter's top-level zero @timestamp is preserved; it is not an occurrence
time. Identity/time errors, duplicate IDs/keys, ambiguous AnyValues and unknown
identity/time/scope fields are rejected. No hand-written Span DTO is used.
Span startTimeUnixNano/endTimeUnixNano must both be explicitly present as
unsigned integer strings within uint64 range. Missing endpoints are rejected,
not replaced by epoch zero; explicitly recorded zero remains distinct.

Acceptance proves offline format encoding only. Target mapping, business scope,
record identity conflicts, source permissions, admission, native API visibility,
and release qualification remain the parent converter's mandatory checks.
