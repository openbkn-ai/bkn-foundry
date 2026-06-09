# Live ISF golden capture (2026-06-03, VM 10.211.55.4 ns openbkn)

Token via `kweaver token` (user `1b43bd4a-5f74-11f1-862f-c27a60c422ae`) on the Mac;
endpoints hit through the VM ingress with `kweaver call` (NODE_TLS_REJECT_UNAUTHORIZED=0
for the self-signed cert). introspect via hydra-admin port-forward on the VM.

## Captured (real)
- `introspect-user.json` — real user token introspect. **PRIZE.**
- `operation-check.json` — `{"result": true}` (matches frozen).
- `resource-operation.json` — `[{"id":"probe","operation":["use","mgnt_built_in_agent"]}]` (array form confirmed).

## Discrepancies vs frozen contract (ACT ON THESE)
1. **introspect `ext.visitor_type` = "realname"**, not "user". Frozen golden user.json used "user".
   Both map to VisitorType_User in the lib (visitorTypeMap), so neither panics — but ISF's real
   value is "realname". bkn-safe should emit "realname". Update contracts/introspect/user.json.
2. **`ext.udid` = ""** (present but empty) in the real token. Empty string is a valid string
   (lib reads `.(string)` — no panic). Confirms udid can be blank.
3. **resource-operation op order differs** from the dip-poc golden (["use","mgnt_built_in_agent"]
   here vs ["mgnt_built_in_agent","use"] before) → order is NOT stable; consumers must treat the
   op list as a SET. (Our §4 Casbin test already sorts — good.)
4. real introspect also carries `token_use:"access_token"`, `iss`, `aud:[]`, `nbf` — extra fields
   the lib ignores (it only reads active/sub/scope/client_id/ext.*). Harmless.

## Endpoints that 404 / need internal route (not via public ingress)
- `/api/authorization/v1/resource-filter` → 404 (public). Confirms §2.0: internal-only route.
- `/api/authorization/v1/resource-list` → 404 (public). Internal-only.
- `/api/user-management/v2/names` → 404 (public). Likely internal-only too (callers use private host).
- `/api/user-management/v1/users/{id}/{fields}` → 400 when fields include "roles"
  (`{"cause":"invalid type","detail":{"params":"role"},"code":400000000}`) — field-name/parse
  detail to pin down; exact flow field list is name,parent_deps,csf_level,roles,email,telephone,
  enabled,custom_attr. NEEDS internal route + correct field probing (port-forward *-private svc).

## TODO (next capture pass, via *-private services port-forwarded on the VM)
- resource-filter / resource-list golden (authorization-private :30920)
- user-management v2/names + v1/users golden (user-management-private :30980)
- policy write (POST /policy) + policy-delete shapes (careful: avoid corrupting state)
