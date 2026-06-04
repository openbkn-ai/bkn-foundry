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

### POST /role-bindings — 绑定用户/应用到角色
请求:`{ "accessor_id":"u1", "role_id":"1572fb82-526f-11f0-bde6-e674ec8dde71" }`
响应:`204 No Content`

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
请求(任意子集):`{ "user_ids":["u1"], "department_ids":["d1"], "group_ids":["g1"] }`
响应(未知 id 省略,不报错):
```json
{ "user_names":[{"id":"u1","name":"Alice"}],
  "department_names":[{"id":"d1","name":"研发部"}],
  "group_names":[] }
```

### GET /departments?parent_id=`<id>` — 列部门(空=根)
响应:`[ {"id":"d1","name":"研发部","parent_id":"","type":"department",...} ]`

### GET /groups/:id/members — 组成员
响应:`{ "user_ids":["u1","u2"] }`

### POST /search-org — 哪些用户在 scope 部门子树内
请求:`{ "user_ids":["u1","u2"], "scope":["d1"] }`
响应:`{ "user_ids":["u1"] }`

### POST /users — 建本地用户(bcrypt)
请求:`{ "account":"alice","name":"Alice","email":"","password":"<pwd>","account_type":"other" }`
响应:`201 { "id":"<uuid>" }`(account 唯一,重复 → 500)

### PUT /users/:id/password — 重置密码
请求:`{ "password":"<new>" }`
响应:`204 No Content`

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
