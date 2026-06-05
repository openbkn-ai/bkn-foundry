# bkn-safe — Phase 1 dev stack (upstream hydra + MySQL)

Local/VM dev stack standing up **upstream ORY Hydra `v26.2.0` unmodified** on a
sidecar **MySQL 8**, to validate the token contract against a real hydra and
exercise the device-code flow. Per the §11.1 decision (default = upstream hydra
+ standard MySQL, xinchuang deferred). NOT a production deploy.

## Run (on the VM, not the local Mac)

```bash
scp -r bkn-safe/dev parallels@10.211.55.4:~/bkn-safe-dev
ssh parallels@10.211.55.4 'cd ~/bkn-safe-dev && docker compose up -d && ./seed-clients.sh && ./validate.sh'
```

Ports: hydra public `4444`, admin `4445`, MySQL `13306`.

## What it validates (smoke PASS 2026-06-03)

1. **client_credentials → introspect (app contract).** `ci-runner` gets a token;
   introspect returns `active:true`, `sub==client_id==ci-runner` → the lib parses
   it as an **app** visitor (no `ext` needed). This is the only token path a bare
   hydra can produce without the bkn-safe UI.
2. **device authorization (RFC 8628).** `openbkn` `POST /oauth2/device/auth`
   returns `device_code` + `user_code` + `verification_uri[_complete]` + `interval`.

## Hard-won gotchas (corrected against the design docs)

- **Use `v26.2.0`, not `v2.3.0`.** ORY switched to CalVer after v2.3.0. `v2.3.0`
  has NO device flow — no `hydra_oauth2_device_auth_codes` table, `/oauth2/device/auth`
  404s. Device flow is in v26.x.
- **MySQL, not MariaDB.** hydra's `20220513..._string_slice_json.mysql.up.sql`
  migration fails on MariaDB with `Error 1064` (MariaDB's JSON type is a longtext
  alias). hydra officially supports MySQL/PostgreSQL/CockroachDB.

## NOT covered here (Phase 2 = bkn-safe)

The **user-type** token path needs bkn-safe's login/consent/device-verification
UI. hydra delegates login & consent to bkn-safe; the required user-type introspect
ext claims (`visitor_type,login_ip,udid,account_type,client_type` — or the lib
panics) are injected by bkn-safe at consent-accept. `URLS_LOGIN/CONSENT/DEVICE_VERIFICATION`
point at `127.0.0.1:3000` as a placeholder for that service.
