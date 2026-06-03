# ISF 轻量替换 —— 落地设计

> 日期：2026-05-25
> 目标：用「hydra + 一个自研 auth-service」替换 ISF（11 release），脱离 anyshare 重组件、轻量化部署，**应用零改契约**，新增 OAuth Device Code 能力
> 前置分析：`reports/adp-auth-permission-isf-analysis-2026-05-22.md`、`reports/isf-interface-inventory-compat-2026-05-25.md`

---

## 1. 目标架构

```
                       ┌──────────────┐
  CLI/设备/CI ──────▶  │   hydra      │  token 签发/内省（OAuth2/OIDC，含 device code）
                       │  (v2.2+)     │
                       └──────┬───────┘
                              │ login/consent + introspect
                       ┌──────▼───────────────────────────┐
  9 个 Kowell 应用  ──▶│        auth-service (新)           │
   (零改契约)          │  · login/consent provider + 验证页 │
                       │  · 用户/组织目录 (user-mgmt 契约)  │
                       │  · 鉴权：内嵌 Casbin (authz 契约)  │
                       │  · 角色 seed (role.json，保 UUID)  │
                       └───────────────────────────────────┘

服务数：hydra + auth-service = 2（ISF 11 → 2，净减 9；Casbin 是库不算进程）
```

**保留**：hydra（token，标准 OAuth2，且 device code 需要它）。
**自研合并**：authentication + authorization + user-management + sharemgnt + oauth2-ui 的**核心能力** → 一个 auth-service。
**弃**：eacp / authentication-jwt（anyshare 文档体系，仅 flow-automation 用，随 anyshare 决策）、policy-management / audit-log（用 `go-lib/audit`）、isfweb（微前端按需）。

---

## 2. 服务边界（auth-service 做什么 / 不做什么）

**做**：
- OAuth2 login/consent provider（对接 hydra 的 login & consent flow）
- device code 验证页（输 user_code + 登录 + 批准）
- 用户/组织目录：实现 user-management 的 13 个查询端点
- 鉴权：实现 authorization 的 6+1 端点，内部用 Casbin 求值
- 角色定义 seed（role.json 9 角色，保 UUID）+ 用户↔角色绑定
- service account 管理（CI 用，发 client_id/secret）

**不做**（划清边界，避免重蹈 ISF 之重）：
- anyshare 文档 ACL（eacp）、组织架构深度权限
- obligation / resource-type 层级 / condition / deny / 过期（Kowell 零使用）
- 独立审计服务（用 `go-lib/audit`）

---

## 3. 对外契约映射（ISF 端点 → 新实现）

### 3.1 token（hydra，保留）
| 应用调用 | 实现 |
|---|---|
| `VerifyToken` / `Introspect`（lib `hydra` & `rest.Hydra` 两套抽象） | hydra `/oauth2/introspect`；lib 仅改 endpoint，`Visitor{ID,Type,TokenID}` 字段不变 |

### 3.2 authorization（`/api/authorization/v1/*` → Casbin）
| 端点 | Casbin 实现 |
|---|---|
| `POST /operation-check` | `enforcer.Enforce(sub, obj, act)` |
| `POST /policy` | `AddPolicy` / `AddGroupingPolicy` |
| `DELETE /policy/` + `POST /policy-delete`（双形态都支持） | `RemovePolicy` / `RemoveFilteredPolicy` |
| `POST /resource-filter` | `GetImplicitResourcesForUser` 过滤 |
| `POST /resource-operation` | 遍历 user 对 resource 的 act |
| `POST /resource-list` | 列 user 可访问 obj |
| `/resource_type/` | 静态资源类型登记（少量） |

### 3.3 user-management（13 端点，自建目录）
`/v1/users/`、`/v1/apps`、`/v1/names`、`/v2/names`、`/v1/emails`、`/v1/departments[/]`、`/v1/internal-groups[/]`、`/v1/internal-group-members/`、`/v1/group-members`、`/v1/search-org` —— 读为主，自建用户/组织表 + 查询 API。

**兼容硬约束**：
1. 角色 UUID 保号：数据 `00990824-`、AI `3fb94948-`、应用 `1572fb82-`（DA `inner_role.go` 等硬编码）。
2. operation-check / policy 请求响应 schema 逐字一致（含 `policy-delete` vs `DELETE /policy` 漂移）。
3. hydra `Visitor` 字段映射不变；两套抽象都喂饱。

---

## 4. Casbin model 设计

```ini
# model.conf — RBAC + 资源实例，accessor 分 user/app
[request_definition]
r = sub, obj, act              # sub=accessorID, obj="type:id", act=operation
[policy_definition]
p = sub, obj, act              # sub 可为 userID / roleID / appID
[role_definition]
g = _, _                       # 用户/应用 → 角色 (保 UUID)
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub) && keyMatch2(r.obj, p.obj) && r.act == p.act
```
- **建对象授权**（`CreateResources`）：`AddPolicy(creatorID, "pipeline:<id>", op...)`
- **角色级权限**（DA `GrantAgentUsePmsForAppAdmin`）：`AddPolicy("1572fb82-...", "agent:*", "use")` + `g(userID, "1572fb82-...")`
- **资源类型 `*`**（`RESOURCE_ID_ALL`）：`keyMatch2` 支持 `agent:*`
- policy 存储：Casbin adapter → 共享 DB（MariaDB/Postgres），auth-service 单写

> Kowell 不用 deny / condition / obligation，故 effect 仅 allow，无需 priority/ABAC matcher。

---

## 5. 认证 grant 设计（device code / client_credentials 为首选，password 保留为过渡）

> 决策（2026-05-25）：**暂保留用户名/密码登录**。删除 password 改动面大（CLI + 所有自动化脚本 + onboard 全要换），风险高。device code 与 client_credentials 作为**新增首选**，不强制下线 password。

| 主体 | 场景 | 首选 grant | 保留 fallback |
|---|---|---|---|
| 普通用户（kweaver CLI） | 无头/远程，人操作 | **device code** | password 登录 |
| 运维（kweaver-admin） | 客户无头服务器，人操作 | **device code** | password 登录 |
| CI / 自动化 | 无人值守 | **client_credentials** | password 登录 |

**password 登录怎么承接（关键澄清）**：
- 今天的 `kweaver auth login --http-signin -u -p` **不是** hydra 的 ROPC password grant（ORY Hydra 已移除 ROPC）；是 ISF `authentication` 服务的**自定义 HTTP 登录**端点。
- 保留它 = **auth-service 继续提供这个 HTTP 登录端点**（承接现状，非新增），登录后驱动 hydra login/consent 自动 accept → 发可内省 token。
- 即 password 是「auth-service 自有登录路径」，与 hydra 的标准 grant（device/client_credentials）并存。

**安全要点**：
- CI 的 `client_secret` 进 K8s Secret / Vault，禁明文/进 repo。
- client_credentials 拿到 **app 身份**（`accessor.type=app`）→ Casbin 给 service account 配角色/策略。
- device：user_code 高熵 + 轮询限速 + 过期（hydra 默认处理）。
- password 保留期：建议加开关（`AUTH_PASSWORD_LOGIN_ENABLED`），后续可单独评估下线，不阻塞本次替换。

---

## 6. Device Code 验证页设计

### 6.1 流程（RFC 8628）
```
1. CLI:   POST hydra /oauth2/device/auth (client_id, scope)
          ← device_code, user_code(如 WDJB-MJHT), verification_uri, verification_uri_complete, interval, expires_in
2. CLI:   显示 "打开 https://<host>/device，输入 WDJB-MJHT"（或直接给 verification_uri_complete 二维码/链接）
3. 用户:  浏览器开 verification_uri → auth-service 验证页
4. 验证页: ① 输/确认 user_code ② 跳 hydra login → auth-service login provider 验身份
          ③ hydra consent → auth-service consent（展示 scope）④ 批准
5. CLI:   按 interval 轮询 POST hydra /oauth2/token
          (grant_type=urn:ietf:params:oauth:grant-type:device_code, device_code)
          ← 未批准: authorization_pending / slow_down；已批准: access_token(+refresh)
```

### 6.2 验证页（auth-service 前端）3 个页面
| 页 | 路由 | 内容 |
|---|---|---|
| **输码页** | `GET /device` | user_code 输入框（verification_uri_complete 进来则预填）→ 提交校验 |
| **登录页** | hydra login challenge → `/login` | 账号密码/SSO 验身份（这是唯一保留密码的地方：**人在浏览器登录**，非 CLI 传密码）→ accept login |
| **批准页** | hydra consent challenge → `/consent` | 展示「设备请求登录 + scope」→ 批准/拒绝 → accept/reject consent |

成功页：「设备已授权，可关闭本页，返回终端」。

### 6.3 关键点
- 无头服务器**零 web**：只出站调 hydra device/token 端点；验证页由中心 auth-service 托管（走 ingress）。
- `verification_uri_complete`（含 user_code）→ CLI 可打印可点链接 / 生成二维码，省去手输。
- 登录页的密码校验 = **浏览器内人工登录**，与「CLI 去 password」不矛盾（去的是 CLI 直传密码）。

---

## 7. hydra fork 与升级风险（v2.1.1 → v2.2+）

> 风险等级修正（2026-05-25）：经查 fork 机制，由「🔴 高」**下修为「🟡 中」**——见 §7.2。

### 7.0 机制：为何 hydra 不能像应用那样「import driver 即可」
关键在 DB 抽象层级不同：

| | 应用（oss-gateway/DA） | hydra |
|---|---|---|
| ORM | **GORM**（吃 `database/sql` driver，连接级） | **gobuffalo/pop**（吃 **dialect**，方言级） |
| 接信创 | `import proton-rds-sdk-go/driver` 即可 ✅ | 光给 driver 不行，pop 按 **dialect 名**分发 SQL/migration/quoting ❌ |

→ proton-rds-sdk-go/driver 只是 `database/sql` 驱动（把信创 DB 伪装成 MySQL 线协议）。GORM 拿来即用；pop 还需一个**注册的方言**。这就是 fork 写 `proton_gobuffalo_pop/dialect_protonrds.go` 的原因——它是**一整个 pop 方言**，不是 import driver 就完事。

**但该方言很薄**：`dialect_protonrds.go` 本质包了一层 MySQL（`import go-sql-driver/mysql`、backtick 引号、复用 `commonDialect`）——信创 DB 在 wire 层已被 proton 伪装成 MySQL，方言只是让 pop 认它。

### 7.1 现状：isf 的 hydra 不是干净上游，是信创 fork
| 改动 | 证据 | 影响 |
|---|---|---|
| **DB 层换 proton-rds-sdk** | go.mod `replace github.com/kweaver-ai/proton-rds-sdk-go => ./proton-rds-sdk-go` | 持久层深改 |
| **Kingbase（人大金仓）方言** | `proton_gobuffalo_pop/dialect_protonrds.go`、`proton-rds-sdk-go/driver/kingbase/` | 信创 DB 支持 |
| **fork ORY x 层** | `proton_ory_x/dbal`、`proton_ory_x/sqlcon` | 改了 ORY 的 DB 抽象 |
| **persister 定制** | `persistence/sql/persister_oauth2.go`（删除逻辑改，引 aishu confluence） | OAuth2 持久定制 |
| **版本** | v2.1.1（2023），migration `20230508...init` | 无 device flow 表 |

### 7.2 风险（🟡 中，非高）
- 升级 = 把 **几个 fork 文件**（`dialect_protonrds.go` + `proton_ory_x` + `persister_oauth2` 改动）**rebase 到 v2.2**，不是重写。
- v2.2 新增的 **device_code migration 是 MySQL 风格 DDL**，proton-rds 方言本就是 mysql-flavored → 大概率直接跑通，**不用为信创单独写 device 表 migration**。
- 难点 = rebase 那几个文件 + 验 device migration 在信创 DB（达梦/金仓）实跑通 + 验 v2.1→v2.2 的 OAuth 表 migration 在信创方言无碍。需 fork 属主参与，但工作量可控。

### 7.3 信创是产品级需求（已查证 2026-05-25）
kweaver **全线已支持信创**，统一靠 `proton-rds-sdk-go/driver` 把信创 DB 伪装成 MySQL 线协议，应用用 gorm mysql 方言：

| 处 | 信创支持 | 证据 |
|---|---|---|
| oss-gateway-backend | **DM8(达梦) / KDB9(金仓 KingbaseES V9) / MySQL** | `internal/database/helper.go:22-30` + import `proton-rds-sdk-go/driver` |
| infra/sandbox | 达梦 DM8 + mariadb 两套 migration | `migrations/dm8/`、`migrations/mariadb/` |
| decision-agent/agent-factory | proton-rds 驱动 | `infra/common/db.go:12` |
| mf-model-manager / mf-model-api | DB 类型配置 | charts values |

→ **信创不能简单丢**（达梦 + 金仓都是产品需求）。但分两层，成本差异大：

| 组件 | 信创成本 | 原因 |
|---|---|---|
| **新 auth-service** | 🟢 低 | **用 GORM**（像 oss-gateway/DA）：import `proton-rds-sdk-go/driver` + gorm mysql 方言 + **Casbin gorm adapter** → 达梦/金仓 driver 级自动覆盖，零方言活 |
| **hydra** | 🟡 中 | 用 gobuffalo/pop（非 gorm），需 pop **方言**（§7.0）；升 v2.2 = rebase 薄方言 + 几个文件，device migration 多为 mysql DDL 可复用 |

> **设计约束**：新 auth-service **必须用 GORM，不要选 pop** —— pop 会引入方言成本（hydra 的坑）；GORM 吃 driver 级，信创零额外活。

**缓解（取决于合规口径）**：
- 若信创合规要求**所有库**落信创 → hydra 必须 rebase fork（贵）。
- 若 hydra OAuth 表可放**旁路 MariaDB** → 直接用上游 ORY Hydra v2.2+，丢 hydra fork（省，device code 白嫖），仅 auth-service 走信创。
- → **最高优先待决策：信创合规是否强制 hydra 也用信创 DB？**（§11.1）

---

## 8. 角色 seed
启动灌 `role.json` 9 角色（6 system + 3 business），**UUID 保号**。business 三角色权限（agent 资源类型）按 DA `InitPermission` 现逻辑迁成 Casbin policy。

---

## 9. 分阶段迁移

0. **摸契约 + 定 DB**：固化 introspect/authz 6 端点/user-mgmt 13 端点 schema + contract test；定信创与否（决定 hydra 路线）。
1. **hydra 就位**：上游 v2.2（标准 DB）或 rebase fork（信创）；注册 client（device + client_credentials）。
2. **auth-service MVP**：login/consent provider + device 验证页 + Casbin 鉴权（6 端点）+ role seed。
3. **user 目录**：13 端点 + 用户/组织表 + service account 管理。
4. **CLI 改造**：`kweaver`/`kweaver-admin` 加 device flow；CI 加 client_credentials；**password 保留为 fallback**（加 `AUTH_PASSWORD_LOGIN_ENABLED` 开关，不下线）。
5. **逐服务切**：`AUTH_PROVIDER`/endpoint 指向新栈，与 ISF **影子比对**鉴权结果。
6. **退役 ISF**：对账无差异后下线 11 release。

---

## 10. 验收 / 失败条件

**验收**：
- [ ] 同操作，新栈与 ISF 鉴权结果逐条一致（影子比对）
- [ ] introspect/operation-check/policy 请求响应 schema 逐字兼容（含 policy-delete 双形态）
- [ ] 角色 UUID 保号，DA 等硬编码引用零改
- [ ] device code：无头服务器 CLI 全流程通（kweaver + kweaver-admin）
- [ ] CI client_credentials 通；password 登录 fallback 保留可用
- [ ] 9 应用编译/测试通过，契约零改

**失败回滚（任一）**：
- 任一服务鉴权结果偏差 / token 校验不兼容 / 角色 ID 对接断裂
- hydra 升级致 token 签发或 introspect 行为变化
- device code 轮询/批准异常

---

## 11. 待决策

**已决（2026-06-03）—— DB / hydra 架构：**
- **§11.1 信创 vs hydra DB：定了。** 默认部署 = **上游 ORY Hydra（最新 2.3.0）原封不动 + 外挂标准 MariaDB**，不 fork（怕版本漂移）。hydra 只存 OAuth2 协议元数据（client/jwk/token/code/flow/device，**无业务数据**），信创**仅 hydra 这块延后**且隔离。真有信创需求时：先试路线 A（上游 hydra `dialect=mysql` 直连信创 MySQL 兼容模式），不行才单独评估 fork。device flow 用上游 2.3.0 自带表 `hydra_oauth2_device_auth_codes`，免自写 migration。
- **bkn-safe 自研服务：GORM + `proton-rds-sdk-go` driver，day 1 就用**（同 oss-gateway 模式）。proton driver 在 `database/sql` 层把达梦/金仓伪装 MySQL → bkn-safe 零 DB 方言代码、与全栈一致、信创免费（**不延后**）。硬规则：用 `database/sql` 生态（GORM 或裸 sql），**绝不用 gobuffalo/pop**（pop 按方言名分发 = hydra 那个 fork 坑的根源）。
- 护栏：hydra 的 MariaDB 严格隔离（独立 DB/schema，无他者依赖）；DSN 配置化（换库不动镜像）。

**未决（原 #1 已决，下列保留）：**
2. anyshare 集成（eacp / authentication-jwt，仅 flow-automation）是否保留 → 决定这 2 块替不替。
3. flow-automation 的 `pkg/ecron` 私货 auth + `IsDataAdmin` 角色名硬判断的迁移方式。
4. auth-service 归属：算 Core 服务（占预算）还是独立可选组件（建议后者，沿 ISF 定位）。

**已决（2026-05-25）：**
- 无头 CLI：运维 + CI **都有**。
- device code：`kweaver` + `kweaver-admin` **都给**。
- password：**保留为 fallback**（删除改动面大）；grant 分工 人→device/password、CI→client_credentials/password。
- 信创：产品级需求（达梦+金仓），新 auth-service 走 proton-rds 成本低；hydra 是难点（§7.3）。
