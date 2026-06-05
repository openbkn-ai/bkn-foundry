# CLI device-login e2e handoff — bkn-safe + hydra (VM 10.211.55.4, 2026-06-05)

For the CLI/SDK agent. The auth/login layer is live and externally reachable.
Service-side authz enforcement is intentionally OFF for now (see Limits), so this
round verifies the **login + token + introspect** path end to end.

## Connection params

- Gateway base (TLS, self-signed — use `-k` / `--insecure`): `https://10.211.55.4`
- OIDC discovery: `https://10.211.55.4/.well-known/openid-configuration`
  (issuer = `https://10.211.55.4`)
- Device client_id: **`openbkn-sdk`** (public, `token_endpoint_auth_method=none`,
  grants `device_code`+`refresh_token`, scope `openid offline`). NOT `kweaver-cli`.
- Endpoints (all proxied at the gateway → bkn-safe-hydra-public:4444):
  - device authorization: `POST https://10.211.55.4/oauth2/device/auth`
    body `client_id=openbkn-sdk&scope=openid offline`
  - user verification page (browser): `https://10.211.55.4/device`
    (hydra returns `verification_uri` / `verification_uri_complete` pointing here)
  - token poll: `POST https://10.211.55.4/oauth2/token`
    body `grant_type=urn:ietf:params:oauth:grant-type:device_code&device_code=<dc>&client_id=openbkn-sdk`
  - userinfo: `https://10.211.55.4/userinfo`

Other registered clients (FYI): `ci-runner` (client_credentials), `openbkn-studio`
(authorization_code+PKCE, public; redirect_uri is a placeholder until frontend redone).

## What to verify this round

1. Discovery returns issuer `https://10.211.55.4` + a `device_authorization_endpoint`.
2. `device/auth` with `openbkn-sdk` returns `device_code` + `user_code` + `verification_uri`.
3. Browser opens `verification_uri`, lands on bkn-safe login → consent (the device/login/
   consent pages are served by bkn-safe at `/login` `/consent` `/device`).
4. After approval, token poll returns an `access_token` (+ `refresh_token`).
5. Introspect (admin-side, not public) shows `active=true` and the **ext claims**
   `visitor_type / login_ip / udid / account_type / client_type` on the user token —
   bkn-safe injects these at consent (`auth/provider.go ExtClaims` → `session.SetAccessToken`).
   These five must be present (the services' kweaver-go-lib has no nil-check).

## Limits (do NOT test this round)

- **Service API authz is OFF.** All 6 backend services run `AUTH_ENABLED=false`, so
  calling e.g. `/api/vega-backend/v1/catalogs` with a user token will NOT exercise
  introspect/authz — it returns 200 regardless. Enabling enforcement is blocked on
  the S2S issue below.
- A `client_credentials` token (e.g. `ci-runner`) has NO ext claims (skips consent),
  so it 502s the services' lib. Use a real user (device) token, not client_credentials,
  for anything that hits a service.

## S2S internal-call authz — root-caused + fixed (was the blocker)

Enabling `AUTH_ENABLED=true` on a service that is also an internal callee surfaced a
403 → panic on tokenless service-to-service calls. Reproduced: vega auth ON →
bkn-backend startup `Init BKN Dataset` → `GetCatalogByID` (bkn→vega HTTP, no token) →
vega authz denies 403 → bkn-backend `panic` (`logics.Init` / `main.go:68`).

**Root cause (NOT a logic change vs ISF):** the internal `/in/v1` route always
skipped token introspection but still ran resource authz (`FilterResources`). For
tokenless S2S, the caller falls back to the admin account `266c6a42-6131-4d62-8f39-853e7093701c`
(type `user`) via `x-account-id` headers — the single fallback identity used by ALL
services (bkn-backend, agent-retrieval, operator-integration; vega/ontology-query are
callees or pass through the user). Under ISF this admin UUID was bound to the
super-admin role by the external UserManagement/Authorization seed, so authz passed.
bkn-safe's seed had the super-admin role + its wildcard `*:* → *` grant but never bound
the admin UUID to it.

**Fix:** seed the missing role binding `266c6a42… → 超级管理员 (7dcfcc9c-ad02-11e8-aa06-000c29358ad6)`
at bkn-safe startup — `bkn-safe/server/internal/seed/data/role-bindings.json` +
`seedRoleBindings` in `seed.go` (idempotent `AssignRole`). The wildcard grant then
covers `view_detail` on `catalog:adp_bkn_catalog` and every other internal resource,
so `FilterResources` passes for S2S calls. This replicates ISF's super-admin grant;
the enforcement logic, `/in/v1` route split, and tokenless header mechanism are
unchanged. With this, `AUTH_ENABLED=true` can be turned on for end-user enforcement.

**Verified live on VM (2026-06-05):** rebuilt bkn-safe (`bkn-safe:newseed`) with the
role-binding seed, redeployed, then flipped `AUTH_ENABLED=true` on vega-backend +
bkn-backend. bkn-backend startup `Init BKN Dataset Start → Catalog adp_bkn_catalog
found → Init BKN Dataset Success → Server Started` — no 403, no panic. Casbin
grouping policy confirmed in mariadb `safe.casbin_rule`: `g, 266c6a42…, 7dcfcc9c…`.
The other 4 services remain `AUTH_ENABLED=false` (not yet flipped); the same single
fallback identity covers them, so enabling them should need no further authz seed.

**Note:** the VM deploy uses a locally-built dev image (`bkn-safe:newseed`); the
seed change still needs to be baked into the published `ghcr.io/openbkn-ai/bkn-safe`
image (rebuild from this branch) before it's permanent across redeploys.
