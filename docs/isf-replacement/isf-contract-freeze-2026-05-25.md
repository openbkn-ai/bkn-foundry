# ISF 替换 —— 契约冻结 Spec（Phase 0）

> 日期：2026-05-25　分支：`feat/isf-replacement`
> 目的：钉死「新 auth-service / hydra 必须满足的对外契约」，作为 contract test + 上线影子比对的基准。**不被信创决策（§11.1）阻塞，先行。**
> 上游：`reports/isf-replacement-landing-design-2026-05-25.md`

---

## 1. token introspect 契约（hydra `/admin/oauth2/introspect`）⚠️ 最硬

lib `kweaver-go-lib/hydra.Introspect` 打 ORY 标准 `POST {hydraAdmin}/admin/oauth2/introspect`（form `token=<t>`），按下表解析响应。**新 hydra（含其 login/consent provider = auth-service）必须让 introspect 返回这些字段，否则 lib 类型断言 panic（无 nil 检查）。**

| lib 字段 | 来源 claim | 规则 |
|---|---|---|
| `Active` | `active` (bool) | false 直接返回 |
| `VisitorID` | `sub` (string) | |
| `Scope` | `scope` (string) | |
| `ClientID` | `client_id` (string) | |
| `VisitorTyp` | sub==client_id → `app`；否则 `ext.visitor_type` | 取值 `realname`/`user`/`anonymous`/`app` |
| `LoginIP` | `ext.login_ip` (string) | **仅 VisitorTyp=user 时读** |
| `Udid` | `ext.udid` (string) | 仅 user |
| `AccountTyp` | `ext.account_type` (string) | 仅 user，取值 `other`/`id_card` |
| `ClientTyp` | `ext.client_type` (string) | 仅 user，取值见 ClientType 枚举（windows/ios/android/harmony/mac_os/web/...） |

**硬约束（DoD）**：
- [ ] auth-service 作为 consent provider，session 注入 `ext = {visitor_type, login_ip, udid, account_type, client_type}`，5 个字段对 user 类型必须齐全（否则 lib panic）。
- [ ] app 类型（client_credentials）：sub==client_id，`ext.*` 可缺（lib 走 app 分支不读 ext）。
- [ ] anonymous：VisitorTyp=anonymous，ClientTyp 默认 web。
- [ ] 契约 test：构造 user/app/anonymous 三类 token，断言 introspect 响应可被 lib 正确解析为 `TokenIntrospectInfo`。

> 注：DA 用 `rest.Hydra`（早期 lib 版本，Hydra 类型在 rest 包），与 adp 的 `hydra` 包**异源**。两套 introspect 客户端都打同一 hydra，需**分别**跑契约 test。

---

## 2. authorization 契约（`/api/authorization/v1/*`）

实际只用 RBAC 子集（Deny/Condition/ExpiresAt/obligation 全空，见落地设计 §4）。下表请求结构源自 isf Authorization 自带的 **JSON Schema 校验文件**（`driveradapters/jsonschema/policy_calc/*.json`、`.../policy/*.json`），权威。

### 2.0 路由内外网分组（决定 public ingress 可见性）

isf Authorization 注册两组（`driveradapters/policy_calc_rest_handler.go:83-97`、`policy_rest_handler.go:87-97`）：

| 路由 | 内网组 | public 组（ingress 暴露） |
|---|---|---|
| operation-check | ✅ check | ✅ checkPublic |
| resource-operation | ✅ | ✅ public |
| resource-type-operation | — | ✅ public |
| **resource-list / resource-filter** | ✅ **仅内网** | ❌ |
| policy（create/get/set/delete）| createPrivate / deletePrivate | create / get / set / delete / resource-policy |

> ⚠️ resource-filter/resource-list **只在内网路由** → 公网打 404（之前 dip-poc 404 真因，非版本旧）。

### 2.1 端点契约表

| 端点 | 方法 | 请求（JSON Schema 权威，required 加粗） | 响应 | Casbin |
|---|---|---|---|---|
| `/operation-check` | POST | **accessor**{**id**,**type**∈user/app}, **resource**{**id**,**type**,name?}, **operation**[str], **method** | `{result:bool}`（实测）| `Enforce(id,"type:id",op)` |
| `/resource-operation` | POST | **accessor**, **resources**[{id,type}], **operation**[], **method**(+allow_operation) | `[{id,operation:[...]}]`（实测,**数组**）| 遍历 ops |
| `/resource-filter` | POST(内网) | **accessor**, **resources**[], **operation**[], **method** | 同构数组（未实测）| 过滤+`Enforce` |
| `/resource-list` | POST(内网) | **accessor**, **resource**, **operation**, **method**, include | 资源列表 | 列可访问 obj |
| `/policy` | POST | **数组**[{**accessor**{**id**,**type**∈**user/department/group/role/app**}, **resource**{**id**,**name**,**type**}, **operation**{**allow**:[{id}],**deny**:[]}}] | 2xx | `AddPolicy`/`AddGroupingPolicy` |
| `/policy-delete`（内网）| POST | **method**, **resources**[] | 2xx | `RemoveFilteredPolicy` |
| `DELETE /policy/:ids` / `PUT /policy/:ids` / `GET /policy` / `GET /resource-policy` | — | 按 id 增删改查 | — | 同上 |

**关键**：
- `accessor.type` 枚举 = **user / department / group / role / app** —— 策略可直接绑**角色/部门/组**（DA 给 app_admin 授权即 `type=role`）。→ Casbin 需多段 `g` 表达 user→role、user→dept、user→group 归属。
- `method` 字段所有 policy_calc 端点**必填**（值=被代理真实 HTTP 方法）。
- `operation`（create policy）= `{allow:[{id}], deny:[]}`，二者 required；kweaver 实测 deny 恒空。

**DoD**：
- [x] 抓现 ISF 对 operation-check / resource-operation 的真实 req/resp（见 §2.1，环境 dip-poc.aishu.cn，`kweaver call` 注入 token）。
- [ ] policy / policy-delete（写）golden —— 避免在 POC 写脏数据，待隔离环境或从 isf/Authorization 源码补。
- [ ] resource-filter / resource-list —— dip-poc 此版**未暴露（404）**，待在有这两端点的部署抓，或从源码补。
- [ ] `policy-delete`（exec-factory）与 `DELETE /policy/`（Pattern A）**双形态都实现**。
- [ ] Casbin model（见 §4）对每端点行为等价，golden 比对一致。

### 2.1 实测 golden（2026-05-25，dip-poc.aishu.cn，user f6ae435c）

**通用**：请求头自动注入 `Authorization: Bearer <token>` + `token` + `x-business-domain: bd_public`。

**operation-check**（POST `/api/authorization/v1/operation-check`）
```
req:  {"accessor":{"type":"user","id":"<uid>"},"resource":{"type":"agent","id":"probe"},"operation":["use"],"method":"GET"}
resp: 200 {"result": true}
err:  缺 method → 400 {"code":"Public.BadRequest","description":"(root): method is required"}
err:  调用方角色不符 → 403 {"code":"Public.Forbidden","description":"Unsupported user role type"}
```

**resource-operation**（POST `/api/authorization/v1/resource-operation`）
```
req:  {"accessor":{"type":"user","id":"<uid>"},"resources":[{"type":"agent","id":"probe"}],"operation":["use"],"allow_operation":true,"method":"GET"}
resp: 200 [{"id":"probe","operation":["mgnt_built_in_agent","use"]}]
```
⚠️ **响应是 JSON 数组 `[{id, operation:[...]}]`，不是 map** —— lib 侧 `GetResourcesOperations`/`FilterResources` 返回 `map[string]...`，故客户端做 array→map（按 id 键）转换。**新服务必须返回数组形态。**

> `method` 是必填字段（值为被代理的真实 HTTP 方法，如 GET），所有 authz 端点都要带。

---

## 3. user-management 契约（13 端点，目录查询）

```
/v1/users/  /v1/apps  /v1/apps/  /v1/names  /v2/names  /v1/emails
/v1/departments[/]  /v1/internal-groups[/]  /v1/internal-group-members/
/v1/group-members  /v1/search-org
```
**DoD**：
- [ ] 抓每端点真实 req/resp（TODO：字段级 schema 待从实流量/ISF UserManagement 源码补全）。
- [ ] 标注哪些是读、哪些是写（替换优先实现读）。

---

## 4. Casbin model（验证等价 RBAC 子集）

```ini
[request_definition]
r = sub, obj, act              # sub=accessorID, obj="type:id", act=operation
[policy_definition]
p = sub, obj, act
[role_definition]
g = _, _                       # user/app → role(UUID)
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub) && keyMatch2(r.obj, p.obj) && r.act == p.act
```
映射：建对象授权=`AddPolicy(creator,"pipeline:<id>",op...)`；角色授权（DA app_admin）=`AddPolicy("1572fb82-...","agent:*","use")`+`g(user,"1572fb82-...")`；`RESOURCE_ID_ALL "*"`→`keyMatch2`。

**DoD**：
- [ ] 用 §2 golden 报文驱动 Casbin，断言 operation-check/filter/list 结果与 ISF 一致。
- [ ] 确认无需 deny/condition/obligation（已证 kweaver 不用）。

---

## 5. 角色 UUID（保号清单）

| UUID | 角色 | source |
|---|---|---|
| 7dcfcc9c-ad02-11e8-... | 超级管理员 | system |
| d2bd2082-ad03-11e8-... | 系统管理员 | system |
| d8998f72-ad03-11e8-... | 安全管理员 | system |
| def246f2-ad03-11e8-... | 审计管理员 | system |
| e63e1c88-ad03-11e8-... | 组织管理员 | system |
| f06ac18e-ad03-11e8-... | 组织审计员 | system |
| 00990824-4bf7-11f0-... | 数据管理员 | business |
| 3fb94948-5169-11f0-... | AI管理员 | business |
| **1572fb82-526f-11f0-bde6-e674ec8dde71** | **应用管理员** | business（DA `inner_role.go` 硬编码） |

源：`/Users/cx/Work/kweaver-ai/isf/Authorization/driveradapters/init_data/role.json`。**DoD**：新 auth-service seed 沿用同 UUID。

---

## 6. 审计契约（MQ 解耦，低影响）

应用经 `kweaver-go-lib/audit` 发 Kafka topic `AUDIT_TOPIC`（`AuditLog` 结构）。**替换 = 换消费者，应用零改**。`AuditOperator` 由 `audit.TransforOperator(hydra.Visitor)` 转换 → 依赖 §1 的 Visitor 字段。

**DoD**：[ ] 新栈提供 `AUDIT_TOPIC` 消费者（或保留 audit-log）。

---

## 7. 信创/国密（挂 §11.1 决策）
- token/密码加密若需国密 → 用 `kweaver-go-lib/crypto/haitai`（海泰 HSM，SM 算法）。
- hydra DB：信创合规口径决定上游 v2.2 直用 vs rebase fork。**本 spec 不阻塞，待决策。**

---

## 8. Phase 0 待办
- [ ] §1 introspect ext claim 契约 test（user/app/anonymous）
- [ ] §2 抓 authorization 7 端点 golden 报文
- [ ] §3 补 user-management 字段级 schema
- [ ] §4 Casbin model 跑 golden 验等价
- [ ] §5 角色 UUID seed
- [ ] 升级 §11.1 信创合规决策（并行）
