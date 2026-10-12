# Agent lifecycle owner propagation implementation plan

**Goal:** Preserve the authenticated lifecycle owner when Agent publishes evidence for a Context Loader interaction.

**Architecture:** Return a response-only `owner` from the authenticated context already used for Core lifecycle headers, after Core successfully authorizes Start. Keep this tuple with the runtime session and interaction, independently of incoming authentication and outbound tool headers. Reuse it for evidence batches and private artifact ingress. Core ownership validation remains unchanged.

**Tech Stack:** Go MCP lifecycle facade; Python Agent ContextVars; existing Kafka/Core/OpenSearch deployment.

## Design and alternatives

020 requires exact owner and scope matching in Ledger. The real 8081 interaction used application `openbkn-sdk`, while Agent's account fallback emitted the user ID. Returning authenticated owner metadata is additive and requires no extra network request. Re-introspection would introduce a second identity lookup; parsing the opaque bearer or overriding Ledger ownership would violate the existing trust boundary. Neither is needed.

Owner is not a lifecycle input, model parameter, authorization header override, or permission grant. Successful Start proves the same authenticated tuple was accepted by Core. Older lifecycle servers without owner retain existing behavior with a visible coverage warning; they are not claimed to provide corrected attribution. No historical event or receipt is rewritten.

## Execution

- [x] Add failing MCP regression: response owner equals authenticated Core request headers even when caller supplies misleading owner hints; keep Core errors and artifact fail-open behavior.
- [x] Add failing Python regression: session parses owner from the same lifecycle result as IDs; evidence event/artifact owner matches it; authenticated outbound headers remain unchanged; reset restores previous interaction.
- [x] Implement shared Go trusted-owner derivation and additive output schema, then runtime-only Python propagation in task and chat paths.
- [x] Run Context Loader CI commands and Agent full unit tests; review source and security boundary independently.
- [ ] Publish the reviewed Foundry change and EE retrieval dependency update; replace only the existing 8081 workloads, retaining live settings and credentials.
- [ ] Invoke a new innocuous input with the existing OAuth user; verify event consumption, owner equality, artifact ownership, lifecycle closure and exact OpenSearch projection. Preserve Audit/config/401 checks and report untested permission roles separately.

## Verification evidence

2026-10-10: observed the new MCP test fail for missing response owner and Python tests fail for missing session/interaction owner. After the change, Agent full suite: 312 passed. Context Loader default and ee_dev builds, selected tests, vet and bkntrace race tests passed. Independent review caught structured/text precedence; corrected it and added conflicting text, missing/invalid owner, account/service derivation, actual Core request headers, task and chat wiring tests. Authentication context and Core ownership guard are unchanged.
