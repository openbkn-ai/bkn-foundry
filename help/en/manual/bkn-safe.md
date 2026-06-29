# 🔐 BKN Safe

## 📖 Overview

**BKN Safe** is the **cross-cutting security layer**: unified **identity**, **permissions**, **policies**, and **audit** across data access, model output, and tool invocation. In full installs it may integrate with OAuth2/OIDC stacks (e.g. Hydra) and business-domain services.

With **`--minimum` install**, many auth components are disabled for a simpler lab setup — APIs may not require tokens. For production, enable the full auth profile per the deployment and security guide bundled with your release.

**Related modules:** All subsystems that accept `Authorization` headers; [VEGA Engine](vega.md) is a primary consumer.

## 🛡️ Administrator commands: `openbkn admin`

In a **full install** (with `auth.enabled=true` and `businessDomain.enabled=true`), BKN Safe's day-to-day **management surface** — users, organizations, roles, models (`llm` / `small-model`), audit — is handled through the **`openbkn admin`** subcommand of the same `openbkn` CLI. There is **no separate admin package** — admin ships with `@openbkn/bkn-sdk` and is reached via `openbkn admin ...`, sharing the same login/session as the end-user `openbkn` CLI shown below on this page.

```bash
openbkn admin org tree                              # list departments
openbkn admin user create --login alice             # default password 123456, forced change at first sign-in
openbkn admin user assign-role <userId> <roleId>
openbkn admin user reset-password -u alice          # admin reset
openbkn admin role list
openbkn admin audit list --user alice --start 2026-04-01 --end 2026-04-30
```

> Full command list and `--minimum` install caveats: see [Install — Administrator commands after a full install (`openbkn admin`)](../install.md#-administrator-commands-after-a-full-install-openbkn-admin).
>
> Respect the **separation-of-duties** built-in accounts (`system`, `admin`, `security`, `audit`) — operators should use individual accounts, not the shared `admin`, for traceable audit logs.

## 💻 CLI

### Authentication — Login

```bash
# Basic login (opens browser for OAuth flow)
openbkn auth login https://<access-address>

# Skip TLS certificate verification (self-signed certs)
openbkn auth login https://<access-address> -k

# Save the connection with an alias for easy switching
openbkn auth login https://<access-address> --alias prod -k

# Login with no auth (for --minimum installs where auth is disabled)
openbkn auth login https://<access-address> --no-auth

# Login with username/password directly (non-interactive)
openbkn auth login https://<access-address> -u <username> -p <password> -k

# Login via HTTP sign-in explicitly (no browser, no Node/Chromium needed)
openbkn auth login https://<access-address> -u <username> -p <password> --http-signin -k

# Headless interactive login: CLI prints an OAuth URL — open it on any
# device with a browser, then paste the full callback URL (or auth code) back
openbkn auth login https://<access-address> --no-browser -k
```

### Session Management

```bash
# List all saved server connections
openbkn auth list

# Switch to a different saved connection
openbkn auth use prod

# List users in the current server
openbkn auth users

# Switch to a different user on the current server
openbkn auth switch <user_id>

# Show the current authenticated identity
openbkn auth whoami

# Show connection status and token expiry
openbkn auth status
```

**`auth whoami` and no-auth**: `whoami` requires an `id_token` from OAuth login. If you used **`auth login … --no-auth`** or the platform has authentication disabled, the CLI is in **no-auth** mode and `whoami` will error with no `id_token` — **expected**. Use `auth status` to confirm; do not treat it as a failed login.

```bash
# Export the current token (for use in scripts or curl)
openbkn auth export

# Logout from the current server
openbkn auth logout

# Delete a saved connection entirely
openbkn auth delete <alias>
```

### Multi-Account Workflow

```bash
# 1. Login to multiple environments
openbkn auth login https://dev.openbkn.example.com --alias dev -k
openbkn auth login https://staging.openbkn.example.com --alias staging -k
openbkn auth login https://prod.openbkn.example.com --alias prod -k -u <username> -p <password>

# 2. List all connections
openbkn auth list
# Output:
#   * dev     https://dev.openbkn.example.com     (active)
#     staging https://staging.openbkn.example.com
#     prod    https://prod.openbkn.example.com

# 3. Switch between environments
openbkn auth use staging
openbkn auth whoami
# → user: admin@staging.openbkn.example.com

openbkn auth use prod
openbkn auth status
# → server: https://prod.openbkn.example.com
# → user: admin
# → token expires: 2026-04-14T22:30:00Z

# 4. Call a protected API (after auth use prod — same session as CLI)
curl -sk "https://prod.openbkn.example.com/api/vega-backend/v1/catalogs" \
  -H "Authorization: Bearer $(openbkn token)"

# 5. Cleanup
openbkn auth logout           # logout from active connection
openbkn auth delete staging   # remove a saved connection
```

### Configuration and Business Domain

```bash
# Show current configuration (server, user, business domain)
openbkn config show

# List available business domains
openbkn config list-bd

# Set the active business domain
openbkn config set-bd <bd_uuid>
```

**`config list-bd` / `config set-bd` and minimal installs**: **`--minimum` / minimal installs do not ship** the **business-domain management service** these two subcommands call, so **`list-bd` / `set-bd` are unavailable** (e.g. `list-bd` returns **404**) — deployment choice, not a CLI bug. A default domain still exists; use `config show`. On a **full install**, use `list-bd` / `set-bd` to list or switch domains; if that still fails, check routing or whether the service is deployed.

### Business Domain Priority

Some resources are scoped to a business domain. If queries return empty results, verify you are in the correct domain:

```bash
# 1. Check current domain
openbkn config show
# → bd: bd_public (default)

# 2. List available domains
openbkn config list-bd
# →  bd_public    (default)
#    bd-sales     Sales Division
#    bd-finance   Finance Division

# 3. Switch to the correct domain
openbkn config set-bd bd-sales

# 4. Retry your query
openbkn bkn list
openbkn agent list
```

---

## TypeScript SDK

Interactive login (browser PKCE / headless OAuth) is a CLI concern — run
`openbkn auth login` first. The library resolves credentials explicitly: pass a
token to `createClient`, or let it read the CLI session from `~/.bkn/`. Session
state is available through the standalone `auth` namespace.

```typescript
import { createClient, auth } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<access-address>', token: process.env.BKN_TOKEN });

// Inspect the current session (from ~/.bkn/ or the attached token)
const status = auth.status();
console.log('platform:', status.baseUrl, 'hasToken:', status.hasToken, 'expired:', status.expired);

const me = auth.whoami();
console.log(me.userId, me.username);

// List available business domains (no typed helper — use the generic passthrough)
const domains = await bkn.call('/api/bkn-backend/v1/business-domains', { method: 'GET' });
console.log('business domains:', domains);

// Scope subsequent requests to a business domain
const scoped = createClient({
  baseUrl: 'https://<access-address>',
  token: process.env.BKN_TOKEN,
  businessDomain: 'bd-sales',
});
const catalogs = await scoped.call('/api/vega-backend/v1/catalogs', { method: 'GET' });
console.log('catalogs:', catalogs);
```

---

## curl

```bash
# Discover OpenID configuration
curl -sk "https://<access-address>/.well-known/openid-configuration"

# Get an access token via OAuth2 password grant
curl -sk -X POST "https://<access-address>/oauth2/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=password&username=admin&password=secretpass&client_id=openbkn-sdk&scope=openid"

# Get an access token via client credentials
curl -sk -X POST "https://<access-address>/oauth2/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=client_credentials&client_id=<client_id>&client_secret=<client_secret>&scope=openid"

# Verify a token (introspection)
curl -sk -X POST "https://<access-address>/oauth2/introspect" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "token=<access-token>"

# Get current user info
curl -sk "https://<access-address>/userinfo" \
  -H "Authorization: Bearer $(openbkn token)"

# Use the token to call a protected API
curl -sk "https://<access-address>/api/vega-backend/v1/catalogs" \
  -H "Authorization: Bearer $(openbkn token)"

# List business domains
curl -sk "https://<access-address>/api/bkn-backend/v1/business-domains" \
  -H "Authorization: Bearer $(openbkn token)"

# Set business domain header for scoped requests
curl -sk "https://<access-address>/api/vega-backend/v1/catalogs" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "X-Business-Domain: bd-sales"
```
