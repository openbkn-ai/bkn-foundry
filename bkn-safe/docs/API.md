# bkn-safe API 文档

> base：服务监听 `:3000`(`SAFE_HTTP_ADDR`)。
> 两类接口:**provider 页**(login/consent/device,浏览器经 hydra 重定向,走 ingress);**内部 API**(`/api/safe/v1/*`,服务间 ClusterIP 调,JSON)。
> 实现:`server/internal/httpapi/{auth,authz,directory,useradmin}.go`。

## 约定
- 内部 API 均 `Content-Type: application/json`。
- 成功:200 带 body,或 204 无 body(写操作)。
- 失败:400 `{ "error": "<bind error>" }`(参数);404 `{ "error": "<msg>" }`;500 `{ "error": "<msg>" }`。

---

## 健康

| 方法 | 路径 | 响应 |
|---|---|---|
| GET | `/health/ready` | `{"status":"ok"}` |
| GET | `/health/alive` | `{"status":"ok"}` |

---

## 认证 provider 页(hydra 重定向到此)

> hydra 配置 `URLS_LOGIN/CONSENT/DEVICE_VERIFICATION` 指向这些路径。浏览器流,非 JSON API。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/login?login_challenge=<c>` | 渲染登录页(账号/密码,带样式) |
| POST | `/login` | 表单 `login_challenge,account,password` → 验密码(自有库 bcrypt/LDAP)→ accept login → **302** 回 hydra;失败 401 |
| GET | `/consent?consent_challenge=<c>` | 渲染**显式同意页**:展示请求方 client + requested scope 清单 + 同意/拒绝(**200 HTML**,不再自动放行) |
| POST | `/consent` | 表单 `consent_challenge,decision=allow\|deny`。allow → 授予 scope + **注入 ext claims** → 302;deny → reject → 302 |
| GET | `/device?device_challenge=<c>&user_code=<u>` | 渲染设备授权页:**醒目展示 user_code** 供核对 + 警告语;`user_code` 可由 `verification_uri_complete` 预填 |
| POST | `/device` | 表单 `device_challenge,user_code` → accept user code → **302**(进登录/同意流) |

introspect 的 ext claims(consent 注入,user 类型 5 字段必齐):
`{ visitor_type:"realname", login_ip, udid:"", account_type:"other|id_card", client_type:"web" }`

---

## 鉴权 API `/api/safe/v1/authz`

### POST /check — 单点判定
请求:
```json
{ "accessor_id": "<user/app/role id>", "resource": { "type": "agent", "id": "probe" }, "operation": "use" }
```
响应:`{ "allowed": true }`
```bash
curl -X POST $SAFE/api/safe/v1/authz/check -H 'Content-Type: application/json' \
  -d '{"accessor_id":"u1","resource":{"type":"agent","id":"probe"},"operation":"use"}'
```

### POST /operations — 可做哪些操作
请求:`{ "accessor_id":"u1", "resource":{"type":"agent","id":"probe"} }`
响应(候选取自该类型目录,返回允许的子集,**无序,当 set**):
```json
{ "operations": ["use","mgnt_built_in_agent"] }
```

### POST /policies — 授予(逐对象,创建者模式)
请求:`{ "accessor_id":"u1", "resource":{"type":"pipeline","id":"p1"}, "operations":["read","update"] }`
响应:`204 No Content`

### DELETE /policies — 删除某资源实例的全部策略(资源删除时)
请求:`{ "resource":{"type":"pipeline","id":"p1"} }`
响应:`204 No Content`

### GET /resources — 列访问者对某类型有某操作权限的资源实例 ID(含角色继承)
查询:`?accessor_id=u1&resource_type=data_flow&operation=list`
响应:`{ "ids":["d1:default","d2:default"] }`
> 仅枚举具体实例;类型级 `*` 授权(超管/数据管理员)不在内,调用方用 is-admin 短路另行处理。ID 原样返回(bkn-safe 对调用方的 id 编码透明)。flow-automation 的 ListResource 用。

### GET /policies — 列某资源实例上各访问者的授权(谁能做什么)
查询:`?resource_type=agent&resource_id=a1`(`resource_id` 可空)
响应:`{ "entries":[ { "accessor_id":"u1", "resource":{"type":"agent","id":"a1"}, "operations":["use","modify"] } ] }`
> 按访问者分组;bkn-safe 无过期/condition,条目视为永不过期、allow-only。DA 的 ListPolicy(All) 用。

> 角色枚举/绑定、用户与部门写操作已迁至 **管理 API `/api/safe/v1/admin`**(需鉴权,见下)。本节为内部服务间无鉴权接口(ClusterIP)。
> 资源类型/操作全集见 `docs/isf-replacement/contracts/authz-catalog.md`。

---

## 用户目录 API `/api/safe/v1/directory`

### GET /users/:id — 用户详情
响应:
```json
{ "id":"u1","account":"alice","name":"Alice","email":"a@x.com","telephone":"",
  "enabled":true,"account_type":"other","roles":["..."],"departments":["d1"] }
```
未找到 → 404 `{"error":"user not found"}`。

### POST /names — id→名称解析(按类型)
请求(任意子集):`{ "user_ids":["u1"], "app_ids":["a1"], "contactor_ids":["c1"], "department_ids":["d1"], "group_ids":["g1"] }`
响应(未知 id 省略,不报错):
```json
{ "user_names":[{"id":"u1","name":"Alice"}],
  "app_names":[{"id":"a1","name":"服务应用"}],
  "contactor_names":[],
  "department_names":[{"id":"d1","name":"研发部"}],
  "group_names":[] }
```
> 应用账户(app)/联系人(contactor)是 `account_type` 不同的 User 行,按 id 在 users 表解析。对接 ISF `/v1`(5 数组)与 `/v2`(user+app)names。

### GET /departments?parent_id=`<id>` — 列部门(空=根)
响应:`[ {"id":"d1","name":"研发部","parent_id":"","type":"department",...} ]`

### GET /groups/:id/members — 组成员
响应:`{ "user_ids":["u1","u2"] }`

### POST /search-org — 哪些用户在 scope 部门子树内
请求:`{ "user_ids":["u1","u2"], "scope":["d1"] }`
响应:`{ "user_ids":["u1"] }`

> 用户/部门写操作、角色管理已迁至 **管理 API**(见下)。本节为内部服务间无鉴权读接口(ClusterIP)。

---

## 管理 API `/api/safe/v1/admin`(需鉴权,网关暴露)

面向 openbkn admin CLI / web 控制台。**每个请求需 `Authorization: Bearer <hydra access token>`**;中间件 `RequireAdmin` 先 introspect token 取 subject,再用 casbin 判超管(`CanAdmin`)。`401` 缺/无效 token,`403` 非管理员。当前仅超级管理员通过;放开给系统管理员等 = 给该角色授予 `safe_admin/manage`。

### 用户

- `GET /users?search=&offset=&limit=` — 列表/搜索(account/name 子串)→ `{ "users":[{id,account,name,email,enabled,account_type}], "total" }`。
- `GET /users?account=<login>` — 按登录名精确查 → `{ "users":[u], "total":1 }`,无匹配 → `{ "users":[], "total":0 }`(200)。
- `GET /users/:id` — 用户详情(含 roles+departments)。未找到 → 404。
- `POST /users` — 建本地用户(bcrypt)。`{ "account","name","email","password","account_type" }` → `201 { "id" }`(account 唯一,重复 → 500)。
- `PUT /users/:id` — 改 `name/email/telephone/enabled/account_type`(指针字段,只动传入的;`account`/密码另有专门接口)。未找到 → 404。
- `DELETE /users/:id` — 删用户 + 清部门/组成员 + casbin 绑定与直授。未找到 → 404。
- `PUT /users/:id/password` — 管理员重置密码 `{ "password" }` → 204(置 MustChangePassword)。

### 部门

- `GET /departments?parent_id=` — 列部门(同内部 GET,经鉴权暴露)。
- `POST /departments` — 建部门 `{ "id?","name","parent_id","type" }` → `201 { "id" }`。
- `PUT /departments/:id` — 改 `name/parent_id/type`(子集)。未找到 → 404。
- `DELETE /departments/:id` — 删空部门;有子部门或成员 → 409;未找到 → 404。

### 角色绑定

- `POST /role-bindings` — 绑定 `{ "accessor_id","role_id" }` → 204。
- `GET /role-bindings?accessor_id=` — 列访问者已绑角色 `{ "role_ids":[...] }`(对接 ISF accessor_roles)。缺 accessor_id → 400。
- `DELETE /role-bindings` — 解绑 `{ "accessor_id","role_id" }` → 204(幂等)。

### 角色目录

- `GET /roles?source=<system|business|custom>` — `{ "roles":[ {id,name,description,source,built_in} ] }`。
- `GET /roles/:id` — 详情含成员+权限:`{ ...,"members":["u1"],"permissions":[{"resource":{"type","id"},"operations":[...]}] }`。未找到 → 404。
- `GET /roles/:id/members` — `{ "accessor_ids":[...] }`。
- `POST /roles` — 建自定义角色(source 强制 custom)`{ "id?","name","description" }` → `201 { "id" }`。
- `PUT /roles/:id` — 改名/描述(仅 custom)。内建 → 403。
- `DELETE /roles/:id` — 删自定义角色 + 清绑定与权限。内建 → 403。
- `POST /roles/:id/permissions` — 授权 `{ "resource":{"type","id"},"operations":[...] }`(id `*`=整类,仅 custom)。内建 → 403。
- `DELETE /roles/:id/permissions` — 撤权(同上,仅 custom)。内建 → 403。

> 内建角色(system/business)只读:UUID 被 DA/flow-automation 硬引用,权限矩阵归 seed `grants.json`;runtime 仅允许 custom 角色增删改。`audit list` 类登录日志按设计不提供(无独立审计服务)。

---

## 自助 API `/api/safe/v1/me`(需鉴权,网关暴露)

面向前端/CLI 的"我能做什么"读取。**每个请求需 `Authorization: Bearer <hydra access token>`**;中间件 `RequireUser` 仅认证(introspect token 取 subject),不做管理员判定——任何登录访问者都可读**自己的**数据(accessor id 取自 token,不可由调用方指定)。

### GET /permissions — 当前访问者的全量权限列表

含角色继承的全部授权,按资源对象分组、操作去重;类型级授权 id 为 `*`(超管通配为 `type:"*", id:""`)。

```json
{ "is_admin": false,
  "permissions": [
    { "resource": { "type": "agent", "id": "*" }, "operations": ["use"] },
    { "resource": { "type": "kn", "id": "kn-1" }, "operations": ["view"] } ] }
```

> 前端用它做菜单/按钮显隐,仅是 UX;后端每个请求仍必须走 `/authz/check` 强制鉴权。

---

## 角色 UUID(保号)

| 角色 | UUID | source |
|---|---|---|
| 超级管理员 | 7dcfcc9c-ad02-11e8-… | system(通配授权) |
| 系统/安全/审计/组织管理/组织审计 | d2bd2082/d8998f72/def246f2/e63e1c88/f06ac18e-… | system(保号未授权) |
| 数据管理员 | 00990824-4bf7-11f0-… | business |
| AI管理员 | 3fb94948-5169-11f0-… | business |
| 应用管理员 | 1572fb82-526f-11f0-… | business |

---

## 备注
- token 校验**不在 bkn-safe**:应用打 hydra-admin `/admin/oauth2/introspect`(保兼容)。
- authz/directory 为内网接口,不经 public ingress。
- 设计见 [`DESIGN.md`](DESIGN.md);服务指南见 [`../README.md`](../README.md)。
