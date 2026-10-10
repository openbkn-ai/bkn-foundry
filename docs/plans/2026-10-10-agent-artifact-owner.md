# Agent artifact owner completion

8081 acceptance after #2081 accepted Agent evidence into Ledger, but the Agent
question artifact body lacked application/effective subject attribution. Core
artifact ingress persists those existing contract fields from JSON, not headers.

Use the same authenticated Context Loader lifecycle owner already retained by
Agent to populate question and result artifact body metadata. Preserve account
fields, content/hash, caller authentication, and existing trace/account fallback.
No new Core header inference, privileges, migration, or historical artifact edits.

- [x] Reproduce the body attribution gap with failing user/service tests.
- [x] Add the two existing fields in the common artifact builder.
- [x] Full Agent tests (316 passed) and independent review (no blocking findings).
- [ ] Official image deployment and fresh task/chat Ledger/artifact acceptance.

The separate Core opaque envelope projection failure is tracked independently.
Old failed acceptance records remain unchanged and do not count as recovery.
