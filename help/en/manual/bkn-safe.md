# 🔐 BKN Safe

## 📖 Overview

**BKN Safe** is the mandatory **cross-cutting security layer**: unified **identity**, **permissions**, **policies**, and **audit** across data access, model output, and tool invocation. It may integrate with OAuth2/OIDC stacks such as Hydra.

**Related modules:** All subsystems that accept `Authorization` headers; [VEGA Engine](vega.md) is a primary consumer.

## 🛡️ Administrator commands: `openbkn admin`

BKN Safe's day-to-day **management surface** — users, organizations, roles, models (`llm` / `small-model`), audit — is handled through the **`openbkn admin`** subcommand of the same `openbkn` CLI. There is **no separate admin package** — admin ships with `@openbkn/bkn-sdk` and is reached via `openbkn admin ...`, sharing the same login/session as the end-user `openbkn` CLI shown below on this page.

```bash
openbkn admin org tree                              # list departments
openbkn admin user create --login alice             # initial password generated + returned once (initial_password); forced change at first sign-in
openbkn admin user assign-role <userId> <roleId>
openbkn admin user reset-password -u alice          # admin reset
openbkn admin role list
openbkn admin audit list --user alice --start 2026-04-01 --end 2026-04-30
```

> Full command list: see [Install — Administrator commands after installation (`openbkn admin`)](../install.md#-administrator-commands-after-installation-openbkn-admin).
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

### Configuration

```bash
# Show current configuration (server and user)
openbkn config show
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

const catalogs = await bkn.call('/api/vega-backend/v1/catalogs', { method: 'GET' });
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

```
