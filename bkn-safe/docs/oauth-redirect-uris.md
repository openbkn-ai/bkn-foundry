# OAuth2 login-client redirect_uris

The login clients (`openbkn-studio`, `openbkn-cli`, `openbkn-sdk`) live in **Hydra's**
store, not in bkn-safe's database. Their `redirect_uris` are the only URLs Hydra
will redirect a browser back to after login; a mismatch is the
`invalid_request ... 'redirect_uri' ... does not match ... pre-registered redirect urls`
error.

There are three ways to manage them. Use the lowest one that fits.

## 1. Standard dev port (no work)

The chart already registers `http://localhost:8000/studio/callback`
(`clientSeed.extraWebRedirectUris` default). If every frontend dev runs their local
studio on **`localhost:8000`**, login works out of the box — no script, no redeploy.
Standardize on this port and most redirect management disappears.

## 2. Permanent / production addresses → chart values + redeploy

Source of truth is the chart (in git). This survives every upgrade.

- The gateway callback (`https://<accessAddress.host>/studio/callback`) is **derived
  automatically** from `accessAddress` — nothing to add for the server-side studio.
- Any extra permanent address goes in `charts/bkn-safe/values.yaml`:

  ```yaml
  clientSeed:
    extraWebRedirectUris:
      - "http://localhost:8000/studio/callback"
      - "https://studio.example.com/studio/callback"
  ```

- Apply with `helm upgrade` — the post-upgrade seed job re-registers the clients.
  No image rebuild (the redirect logic is Helm templating, not Go).

## 3. Temporary / dev addresses → `deploy/scripts/bkn-redirect.sh`

For a one-off (e.g. a dev on a non-standard port hitting a remote install) without a
redeploy. Calls the bkn-safe admin API (gateway-exposed, RequireAdmin, audited).

```bash
openbkn auth login https://10.211.55.4   # must be a super-admin session
export BKN_HOST=https://10.211.55.4
deploy/scripts/bkn-redirect.sh add http://localhost:5173/studio/callback
deploy/scripts/bkn-redirect.sh list
deploy/scripts/bkn-redirect.sh del http://localhost:5173/studio/callback
```

> **Ephemeral.** A `helm upgrade` re-seeds clients from chart values and wipes
> anything added this way. For anything that must stick, use option 2. Requires a
> super-admin token, so this is an ops / team-lead tool, not per-developer
> self-service (widening who can edit redirect_uris is a security boundary change).

### Admin API used by the script

```http
GET    /api/safe/v1/admin/clients/:id/redirect-uris
POST   /api/safe/v1/admin/clients/:id/redirect-uris   {"redirect_uri":"..."}
DELETE /api/safe/v1/admin/clients/:id/redirect-uris   {"redirect_uri":"..."}
```

`:id` is restricted to the first-party clients (`openbkn-studio` / `-cli` / `-sdk`).
`redirect_uri` must be an absolute `http(s)` URL with a host and no wildcard/fragment.

## Which callback URL?

`redirect_uri` = the address the **browser** is on + `/studio/callback`:

- Studio served behind the gateway → `https://<gateway-host>/studio/callback`
  (VM: `https://10.211.55.4/studio/callback`).
- Local dev server → `http://localhost:<port>/studio/callback`.

Use the public/gateway host, not an internal service name. Port 443 is omitted.
