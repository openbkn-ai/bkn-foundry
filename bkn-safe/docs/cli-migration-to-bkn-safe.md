# openbkn admin CLI → bkn-safe migration spec

Handoff for the **kweaver-admin / openbkn CLI** agent. ISF is retired; the CLI's
`admin` group must move from the four ISF base paths to bkn-safe's API.

## 0. Ground rules

- **One gateway base URL** (unchanged). Drop all four ISF prefixes
  (`/api/user-management/v1`, `/api/authorization/v1`, `/isfweb/api/ShareMgnt`,
  `/api/eacp/v1`). Replace with the bkn-safe paths below.
- **Two surfaces:**
  - **Admin** — everything the CLI `admin` group does — lives under
    `/api/safe/v1/admin/*` and is **token-gated**. `api-client.ts request()`
    already attaches `Authorization: Bearer <token>` (line ~98), so no per-call
    change is needed — but the logged-in identity **must be an admin**.
  - **Internal** — `/api/safe/v1/authz/*`, `/api/safe/v1/directory/*` — tokenless,
    ClusterIP, service-to-service. The CLI generally should NOT call these; use the
    `/admin` reads instead. (They are not exposed at the gateway.)
- **Auth gate semantics:** no/invalid token → `401 {"error":"..."}`; valid token
  whose subject is not an admin → `403 {"error":"not authorized for admin
  operations"}`. Surface both clearly ("login required" / "admin privileges
  required"). "Admin" = super-admin today (the seeded super-admin user, or any
  accessor granted `safe_admin/manage`).
- **Remove ISF-isms:** thrift envelopes (`Usrm_*`), RSA password encryption
  (`encryptModifyPwd`), the PATCH/thrift fallbacks, and `resolveCurrentUserId` /
  JWT-decode-for-caller-id (admin endpoints take identity from the bearer token;
  no `callerUserId` in any body). `x-business-domain` header is ignored by
  bkn-safe — harmless to keep or drop.
- **Response shape deltas (apply everywhere):** `entries` → `users`/`roles`/
  `departments`; `total_count` → `total`; department `parent_deps[]` → single
  `parent_id`.

## 1. Endpoint map (api-client.ts methods)

### Users

| CLI method | New call | Request | Response |
| --- | --- | --- | --- |
| `listUsers` | `GET /api/safe/v1/admin/users?search=&offset=&limit=` | — | `{ users:[{id,account,name,email,enabled,account_type}], total }` |
| `findUserByAccount` | `GET /api/safe/v1/admin/users?account=<login>` | — | `{ users:[u]\|[], total }` — empty list (200) on miss, **not** 404 |
| `getUser` | `GET /api/safe/v1/admin/users/:id` | — | `{ id,account,name,email,telephone,enabled,account_type, roles:[roleId...], departments:[deptId...] }` (404 if missing) |
| `createUser` | `POST /api/safe/v1/admin/users` | `{ id?, account, name, email, password, account_type }` (plaintext password) | `201 { id }` (account unique; dup → 500). Server sets must-change-password. |
| `updateUser` | `PUT /api/safe/v1/admin/users/:id` | `{ name?, email?, telephone?, enabled?, account_type? }` (only present fields; snake_case) | `204` / 404 |
| `deleteUser` | `DELETE /api/safe/v1/admin/users/:id` | — | `204` / 404 (purges memberships + role bindings) |
| `setUserPassword` | `PUT /api/safe/v1/admin/users/:id/password` | `{ password }` (**plaintext — drop RSA encrypt**) | `204` |
| `getUserRoles` | `GET /api/safe/v1/admin/role-bindings?accessor_id=<userId>` | — | `{ role_ids:[...] }` — **ids only**; enrich names via `GET /admin/roles` |
| `assignRole` | `POST /api/safe/v1/admin/role-bindings` | `{ accessor_id, role_id }` | `204` |
| `revokeRole` | `DELETE /api/safe/v1/admin/role-bindings` | `{ accessor_id, role_id }` | `204` |

### Departments (org)

| CLI method | New call | Request | Response |
| --- | --- | --- | --- |
| `listOrgs` / `searchDepartments` | `GET /api/safe/v1/admin/departments?search=&offset=&limit=` | — | `{ departments:[{id,name,parent_id,type,created_at}], total }` |
| `searchDepartmentsAll` | paginate the above | — | flatten pages |
| `org tree` | `GET /api/safe/v1/admin/departments` (flat) | — | build the tree client-side from `parent_id` |
| drill-down children | `GET /api/safe/v1/admin/departments?parent_id=<id>` (`""`=roots) | — | `{ departments:[children], total }` |
| `getOrg` | `GET /api/safe/v1/admin/departments/:id` | — | `{ id,name,parent_id,type,created_at }` (404) |
| `createOrg` | `POST /api/safe/v1/admin/departments` | `{ id?, name, parent_id, type }` | `201 { id }` |
| `updateOrg` | `PUT /api/safe/v1/admin/departments/:id` | `{ name?, parent_id?, type? }` | `204` / 404 |
| `deleteOrg` | `DELETE /api/safe/v1/admin/departments/:id` | — | `204`; **409** if it has children or members (surface "department not empty"); 404 |
| `getOrgMembers` | `GET /api/safe/v1/admin/departments/:id/members` | — | `{ users:[{id,account,name,email,enabled,account_type}], total }` (direct members) |

### Roles

| CLI method | New call | Request | Response |
| --- | --- | --- | --- |
| `listRoles` | `GET /api/safe/v1/admin/roles?source=<system\|business\|custom>` | — | `{ roles:[{id,name,description,source,built_in}] }` (returns **all**; no server paging/keyword — filter client-side) |
| `getRole` | `GET /api/safe/v1/admin/roles/:id` | — | `{ id,name,description,source,built_in, members:[accessorId...], permissions:[{resource:{type,id},operations:[...]}] }` (404) |
| `getRoleMembers` | `GET /api/safe/v1/admin/roles/:id/members` | — | `{ accessor_ids:[...] }` — **ids only**; resolve names via `POST /directory/names` or `GET /admin/users` |
| `role add-member` | `POST /api/safe/v1/admin/role-bindings` | `{ accessor_id, role_id }` | `204` |
| `role remove-member` | `DELETE /api/safe/v1/admin/role-bindings` | `{ accessor_id, role_id }` | `204` |
| **new:** `role create` | `POST /api/safe/v1/admin/roles` | `{ id?, name, description }` (source forced `custom`) | `201 { id }` |
| **new:** `role update` | `PUT /api/safe/v1/admin/roles/:id` | `{ name?, description? }` | `204`; **403** if built-in (system/business) |
| **new:** `role delete` | `DELETE /api/safe/v1/admin/roles/:id` | — | `204`; **403** built-in (purges bindings+grants) |
| **new:** `role grant/revoke perm` | `POST\|DELETE /api/safe/v1/admin/roles/:id/permissions` | `{ resource:{type,id}, operations:[...] }` (`id:"*"` = whole type) | `204`; 403 built-in |

### Audit

| CLI method | Status |
| --- | --- |
| `listAuditLogs` / `audit list` | **No bkn-safe endpoint** — login-log/EACP is intentionally retired (no standalone audit service per bkn-safe DESIGN). Make the command return a clear `not available on bkn-safe` message (non-zero exit), not a 503/404. |

## 2. Behavioral notes the CLI must handle

1. **IDs vs names.** `getUserRoles` and `getRoleMembers` now return bare IDs. To
   render names: cache `GET /admin/roles` (id→name) for roles; use
   `POST /api/safe/v1/directory/names {user_ids,department_ids,...}` or
   `GET /admin/users?account=` for accessor names.
2. **Built-in roles are immutable.** `source` ∈ {system, business} → `built_in:true`;
   update/delete/permission-edit return `403`. Only `custom` roles are mutable.
   The 3 business-admin UUIDs (應用/數據/AI 管理員) and super-admin are built-in.
3. **Department delete is non-cascade** → `409` when non-empty; the CLI should tell
   the user to move/remove children+members first.
4. **Pagination caps:** users `limit` default 50 / max 500; departments default
   100 / max 1000; roles return all (no paging).
5. **No `role` query param** anywhere (ISF required it on user/dept search) — drop it.
6. **account_type** values: `other|id_card|app|contactor` (app/contactor are User
   rows, same as ISF semantics).

## 3. Tests to update (kweaver-admin)

- `src/lib/__tests__/resolve-refs.test.ts` — re-point `findUserByAccount` mock to
  `GET /api/safe/v1/admin/users?account=` (`{users,total}` shape) and `listRoles`
  to `GET /api/safe/v1/admin/roles` (`{roles}` shape).
- Any command snapshot tests reading `entries`/`total_count`/`parent_deps` — update
  to `users`/`roles`/`departments`/`total`/`parent_id`.

## 4. Server status / where to test

- Server code: branch `feat/isf-replacement` (bkn-safe). Admin API + auth +
  user-list/dept-members/find-by-account all committed.
- Endpoints live on the validation VM **after the `adminv2` image is deployed**
  (deploy pending). Until then the VM runs `adminauth`, which has the admin API
  and gate but **not** `GET /admin/users` (list), `/admin/departments/:id[/members]`,
  or `?account=` — those return 404 until redeploy.
- Full HTTP reference: `bkn-safe/docs/API.md` (管理 API section).
- To get an admin token for manual testing: log in as the seeded super-admin via
  the normal openbkn login/device flow (the CLI already stores the token).
