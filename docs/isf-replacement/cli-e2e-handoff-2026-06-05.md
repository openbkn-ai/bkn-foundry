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

## Known blocker for service-side authz (separate task — S2S)

Enabling `AUTH_ENABLED=true` on a service that is also an internal callee breaks
tokenless service-to-service calls. Reproduced: vega auth ON → bkn-backend startup
`Init BKN Dataset` → `GetCatalogByID` (bkn→vega HTTP, no token) → vega authz denies
403 → bkn-backend `panic` (`logics.Init` / `main.go:68`). Needs a S2S auth strategy
(service/client_credentials token propagation, or an internal-port/internal-subject
exemption) before end-user enforcement can be turned on. Tracked as deferred.
