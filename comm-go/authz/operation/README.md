# Authorization operation contract

This package is the single source of truth for published authorization operation wire values. Go callers use typed constants, for example `operation.QueryData`; a service may retain an old local name only as an alias to this constant during migration.

`All` is a vocabulary, not an authorization grant. In particular, bkn-safe must keep its resource catalog, Community whitelist, prerequisites, and parent-operation mappings explicit and reviewed by resource; none may be inferred from this package.

Non-Go consumers use the same wire values without inventing a second vocabulary:

- bkn-safe JSON seed files retain explicit resource-to-operation references and validate every reference with `operation.Known` after upgrading to the release containing this package.
- Python clients expose generated or hand-imported aliases from this published vocabulary; they do not define new wire values.
- OpenAPI schemas expose operation fields as an enum generated from this package's published list, while resource-specific endpoint constraints remain explicit schemas.

Bare operation strings are permitted only in protocol serialization assertions, historical migration fixtures, and documentation examples. Production authorization decisions, catalog definitions, and formal API schemas use this contract or generated artifacts.

To add an operation, add a constant and its immutable wire value, update the stability test, then explicitly add it to every intended resource catalog/bundle/mapping in a later compatible change. Deprecation retains the old wire value until all consumers and persisted authorization data have been migrated. A rename is a new operation plus a compatibility migration; never mutate a published wire value.
