# bkn-safe 设计文档

> ISF 替换的自研认证/鉴权/用户管理服务(代码 `safe`)。配合上游 ORY Hydra。
> 背景与决策见 [`../../docs/isf-replacement/README.md`](../../docs/isf-replacement/README.md)。

## 1. 定位与边界

bkn-safe 是**独立可选组件**(沿 ISF 定位,不占 Core 预算),承担三件事:

1. **认证(authentication)** —— hydra 的 login/consent/device 提供方:验密码(自有库 bcrypt)、跑 OAuth login/consent/device 流程、在 consent 时注入 introspect 的 ext claims。
2. **鉴权(authorization)** —— Casbin RBAC + 资源实例,判定"谁能对什么做什么"。
3. **用户管理(directory)** —— 用户/部门/组/角色目录 + 名称解析 + 搜索 + LDAP 联邦。

**不做**:token 签发(hydra 做)、anyshare 文档 ACL(eacp,已剔除)、ISF ABAC 的 deny/condition/obligation/层级(Kowell 不用)。

## 2. 系统上下文

```
 CLI/设备/CI ─┐                       ┌── hydra (上游 v26.2.0, PostgreSQL) ── 签发/内省 token
              ├─ OAuth2/OIDC ────────┤        admin :4445(内网) public :4444 + device flow
 浏览器 ──────┘   login/consent       └── bkn-safe (GORM + proton-rds → MariaDB)
                  redirect                    认证 provider + Casbin 鉴权 + 用户目录 + LDAP
 9 应用 ──── introspect(打 hydra-admin)/ authz(打 bkn-safe)/ directory(打 bkn-safe)
```

- **token**:hydra 签发;应用打 hydra-admin `/admin/oauth2/introspect` 校验(保兼容,不变)。
- **认证**:hydra 把 login/consent/device 重定向到 bkn-safe 的页面;bkn-safe 验密码后驱动 hydra accept。
- **鉴权/目录**:应用打 bkn-safe 的 `/api/safe/v1/*`(内网 ClusterIP)。

## 3. 组件(代码结构)

```
server/  (module bkn-safe, go1.25)
  config/        环境变量配置(SAFE_*)
  internal/
    model/       GORM 领域模型(User/Role/Department/Group/ResourceType/Operation + 关系)
    database/    proton-rds driver + GORM(mysql 方言)+ AutoMigrate
    authz/       Casbin 引擎(model + gorm-adapter)+ Check/AllowedOps/Grant/AssignRole
    seed/        启动集中 seed(roles.json + catalog.json + grants.json,内置 embed)
    auth/        userstore(bcrypt) + hydra 客户端 + login/consent 编排(provider)+ LDAP 连接器
    directory/   目录查询服务(用户/部门/组/名称/search-org)
    httpapi/     gin 路由:health + authz API + directory API + user-write + provider 页
  cmd/authz-shadow/   ISF↔bkn-safe 影子比对 CLI(离线批量 diff)
  contract/           契约测试(introspect 过真实 lib + Casbin 等价)
  dev/                dev 栈(compose:postgres+mariadb+hydra+safe)+ 验证脚本
  charts/bkn-safe/    helm chart(+ bundledDeps 开关)
```

## 4. 数据模型(model 包)

| 表 | 说明 |
|---|---|
| `users` | 身份(account 登录名、name、email、enabled、source=local/ldap、account_type、password_hash bcrypt) |
| `roles` | 角色(**UUID 保号**,source=system/business)— seed 灌 9 个 |
| `departments` / `user_departments` | 组织树(parent_id)+ 用户↔部门 |
| `groups` / `group_members` | 组 + 成员 |
| `resource_types` / `operations` | 资源类型 + 操作目录(seed 灌)|
| `casbin_rule` | Casbin policy(gorm-adapter 管):p=角色/用户→资源→操作,g=用户→角色 |

信创:全程 GORM + `proton-rds` driver(达梦/金仓/MySQL 在 driver 层透明),**零方言代码,绝不用 pop**。

## 5. 鉴权模型(authz 包)

Casbin model(RBAC + 资源实例):

```ini
[request_definition] r = sub, obj, act          # sub=accessorID, obj="type:id", act=operation
[policy_definition]  p = sub, obj, act
[role_definition]    g = _, _                    # user/app → role(UUID 保号)
[policy_effect]      e = some(where (p.eft == allow))
[matchers] m = g(r.sub, p.sub) && keyMatch(r.obj, p.obj) && (p.act == "*" || r.act == p.act)
```

- **`keyMatch` 不用 keyMatch2**:`type:id` 的 `:` 会被 keyMatch2 当通配 → `pipeline:p1` 越权命中 `pipeline:p2`(提权)。keyMatch 只认 `*`。
- **act 通配 `*`**:超级管理员 `(*, "*", "*")` 能干一切。
- **只 allow**:Kowell 不用 deny/condition/obligation。
- **角色级授权**:`AddPolicy(roleUUID, "agent:*", "use")` + `g(user, roleUUID)`。
- **逐对象授权**(创建者):`AddPolicy(userID, "pipeline:p1", "read")`。
- policy 存共享 DB(gorm-adapter),bkn-safe 单写。

## 6. 集中 seed(seed 包)

取代 ISF 的散点初始化(authz 服务启动 seed + 各模块 HTTP 注册 resource_type + DA InitPermission)→ **bkn-safe 启动一处幂等灌**:

- `roles.json` — 9 角色(6 system + 3 business),UUID 保号。
- `catalog.json` — 13 资源类型 + 操作(从各服务代码扒)。
- `grants.json` — 角色→资源→操作:3 业务角色管各自域(全 ops)、超级管理员通配、其余 4 system 角色保号不授权(可后续经授权 API 激活)。

角色↔资源域见 [`../../docs/isf-replacement/contracts/authz-catalog.md`](../../docs/isf-replacement/contracts/authz-catalog.md)。

## 7. 认证流程(auth 包)

hydra 委托 login/consent/device 给 bkn-safe:

```
1. 应用/CLI 发起 OAuth → hydra → 302 到 bkn-safe GET /login?login_challenge=
2. 用户提交账密 → POST /login → userstore.Verify(bcrypt;或 LDAP 联邦)→ hydra accept login → 回 hydra
3. hydra → 302 到 GET /consent?consent_challenge= → bkn-safe 渲染显式同意页(展示请求方 client + scope 清单)→ 用户点同意 → POST /consent(decision=allow)→ 注入 ext claims → hydra accept consent(拒绝则 reject)
4. hydra → 回 redirect_uri + code → 应用换 token
5. 应用 introspect token → hydra 返回含 ext 的响应
```

**ext claims(§1 最硬契约)**:consent accept 时把 `{visitor_type:realname, login_ip, udid:"", account_type, client_type:web}` 注入 hydra session.access_token → hydra introspect 时冒在 `ext` 字段。旧 lib 无 nil 检查,缺字段 panic → 这 5 个必须齐(`ExtClaims` 保证)。

**device flow**:`/device` 收 user_code → 调 hydra accept user code → 走 login/consent。
**LDAP(轻)**:`SAFE_LDAP_URL` 配了则 local→LDAP 链式认证,LDAP 成功 provision 本地用户(source=ldap)。

## 8. 配置(env)

见 [`../README.md`](../README.md#配置环境变量)。关键:`SAFE_DB_*`(proton/MariaDB)、`SAFE_HYDRA_ADMIN_URL/PUBLIC_URL`、`SAFE_LDAP_*`、`SAFE_SEED_ON_START`。

## 9. 部署

- **helm**:`charts/bkn-safe`,`bundledDeps` 开关(非生产自带 postgres+hydra+mariadb;生产关掉指真实服务)。只 login/consent/device 走 ingress,authz/directory 内网 ClusterIP。
- **dev**:`dev/docker-compose.yml`(hydra/PG + safe/MariaDB)。

## 10. 测试与等价性

| 层 | 验什么 |
|---|---|
| `go test ./...` | 单元:authz/seed/auth/directory/httpapi |
| `seed/matrix_test.go` | **全角色×资源等价矩阵 + 无泄漏** |
| `contract/` | introspect 过真实 lib v1.0.5 + Casbin 复现 ISF golden |
| `dev/validate-safe-api.sh` | 全 API 功能(19 项) |
| `dev/validate-e2e.sh` | 完整 OAuth 登录流 + introspect ext |
| `cmd/authz-shadow` | ISF↔bkn-safe 离线 diff |

## 11. 迁移与切换(Phase 6)

各调用方服务加 bkn-safe authz 适配器 + `AUTHZ_PROVIDER` 开关(isf 默认 / shadow / bkn-safe),逐服务切 + 影子比对,**随时翻 env 回退**。详见 [`../../docs/isf-replacement/isf-excision-migration-plan-2026-06-03.md`](../../docs/isf-replacement/isf-excision-migration-plan-2026-06-03.md)。
