# CLI device-login e2e handoff — bkn-safe + hydra (VM 10.211.55.4, 2026-06-05)

For the CLI/SDK agent. The auth/login layer is live and externally reachable.
Service-side authz enforcement is now ON (all 5 services `AUTH_ENABLED=true`, see
Limits + S2S), so beyond the **login + token + introspect** path, service API calls
now enforce ext claims + resource authz.

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

- **Service API authz is now ON** (changed 2026-06-05). All 5 auth-gated services
  (vega-backend, bkn-backend, ontology-query, agent-retrieval,
  agent-operator-integration) run `AUTH_ENABLED=true` — calling e.g.
  `/api/vega-backend/v1/catalogs` now exercises introspect + resource authz, so a
  user token must carry the ext claims (below) and the account must be authorized
  for the resource. See the S2S section for how internal calls pass authz.
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

## SDK CLI implementation — add device-code login (`@openbkn/bkn-sdk`)

The CLI (`/Users/cx/Work/kowell/bkn-sdk`) today implements only
`authorization_code` (PKCE browser + headless password) in `src/auth/oauth.ts` —
**no device-code flow**. Add it so the `openbkn-sdk` device client above is usable.

### Exact params to wire in

- Base: `https://10.211.55.4` (self-signed → login with `-k/--insecure`; the CLI
  then sets `NODE_TLS_REJECT_UNAUTHORIZED=0`, so `fetch` accepts the cert).
- Device `client_id`: **`openbkn-sdk`** (default; not `openbkn`, not `kweaver-cli`).
- scope: `openid offline` (exact — do NOT add `all`, the client isn't granted it).
- Device authorization: `POST /oauth2/device/auth`, body
  `client_id=openbkn-sdk&scope=openid offline`.
- Token poll: `POST /oauth2/token`, body
  `grant_type=urn:ietf:params:oauth:grant-type:device_code&device_code=<dc>&client_id=openbkn-sdk`.
- Verification page hydra returns → `https://10.211.55.4/device` (open
  `verification_uri_complete` for the user; it carries the `user_code`).
- Identity check after login: `GET /userinfo`. Discovery (optional, to read the
  endpoints instead of hardcoding): `/.well-known/openid-configuration`.

### Changes

1. `src/auth/oauth.ts`: add `deviceLogin(baseUrl, {clientId, scope, onPrompt})`
   implementing RFC 8628 — POST `/oauth2/device/auth` → call `onPrompt` with
   `user_code`/`verification_uri[_complete]` → poll `/oauth2/token` honoring
   `interval`, `authorization_pending` (keep polling), `slow_down` (+5s),
   `access_denied`/`expired_token` (fail). Reuse existing `mapToken` +
   `normalizeBaseUrl`; throw `InputError` on user-facing failures. Default
   `clientId='openbkn-sdk'`, `scope='openid offline'`. Also `export openBrowser`.
2. `src/commands/auth.ts`: in `login`, make device the default when no
   `-u`/`--token` (add `--browser` to force PKCE). `onPrompt` prints
   `user_code` + `verification_uri` to stderr and opens `verification_uri_complete`.
   Watch the commander `--no-browser` vs new `--browser` naming collision.
3. Public client → token poll sends only `client_id`, no secret (matches
   `token_endpoint_auth_method=none`).

### Self-test (against this VM)

```bash
node dist/cli.js auth login https://10.211.55.4 -k     # default device, client_id=openbkn-sdk
# terminal prints user_code + verification_uri → authorize in browser
node dist/cli.js auth whoami https://10.211.55.4 -k    # hits /userinfo
```

Stay within "Limits" above: authz is ON now, so service calls (`openbkn bkn list`,
etc.) require the device (user) token's ext claims — they will fail with a
`client_credentials` token. Use the device login token.

## Forced password change — what the CLI must (and need not) do

bkn-safe now seeds a built-in admin (`admin` / initial `openbkn`) and forces a
password change on first login for that admin, for any admin-created local user,
and after an admin password reset (`MustChangePassword`). Two paths matter to the
CLI:

### Device login — the forced change is browser-side; CLI needs no new logic

In the device flow the user authenticates in the browser. If a change is required,
bkn-safe renders its `/change-password` page after `/login` (carrying the
`login_challenge`) and only accepts the hydra login once the new password is set —
so the token the CLI eventually polls is already post-change. The CLI therefore
does NOT implement forced-change handling for device login; it just keeps polling
`/oauth2/token`:

- honor `authorization_pending` (keep polling), `slow_down` (+5s);
- use a generous overall timeout (a forced change takes the user longer — follow
  the device-auth `expires_in`, on the order of minutes, not seconds).

There is NO `must_change_password` claim in the token (the change happens before
the token is issued), so the CLI cannot — and need not — read it from
token/introspect.

### Self-service change — a `bkn auth change-password` command (no browser)

For users who have an initial / admin-reset password and want to change it from
the terminal (no browser), call the self-service endpoint:

`POST /api/safe/v1/auth/change-password`

```json
{"account": "<account>", "old_password": "<old>", "new_password": "<new>"}
```

- Prompt for **account** (the login column — e.g. `admin`, NOT the display name),
  old password, and new password (twice).
- Responses: `204` success · `401` wrong account/old password (or disabled) ·
  `400` new == old.
- Do NOT add a client-side strength rule — the server defers strength checks too.
- This path is plain credential change (no hydra challenge); after it succeeds the
  user logs in normally via device flow.

### Limitation

In the device flow the CLI never sees the password, so it cannot auto-detect "user
is on the initial password". Either document that users run
`bkn auth change-password` first, or — only if the CLI later adds a password/headless
login mode — surface the must-change state on that path.
