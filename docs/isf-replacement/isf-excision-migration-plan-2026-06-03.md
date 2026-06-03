# 剔除 ISF —— 迁移计划 (ADR, 2026-06-03)

> 分支 `feat/isf-replacement`。本文是 settle 后的权威决策,**部分取代** `isf-replacement-landing-design` / `isf-contract-freeze` 里"复刻全部契约"的旧框架(见各处 NOTE)。

## 0. 目标

把 **ISF 服务系列**(authentication / authorization / user-management / eacp / sharemgnt / oauth2-ui / policy-management / audit-log / isfweb / authentication-jwt / hydra-fork)整体**剔除**,换成 **上游 hydra + 一个自研 bkn-safe**。总纲:**简化**(11 服务 → 2)。

剔掉 ISF 与重设计接口是**两个独立轴**:换实现(必须)≠ 改契约(可选,按层定)。

## 1. bkn-safe 职责(3 件)

1. **用户管理** —— 自建目录(GORM 表:users/departments/groups/roles/memberships),+ **LDAP 连接器**(go-ldap,登录时联邦,"轻"方案);重度 IAM(完整 IdP/多协议/SCIM)以后再说。
2. **权限(authz)** —— **Casbin**(redo,干净 API)。
3. **认证** —— login/consent/device 验证页 + 自有用户库验密码(bcrypt)+ 注入 introspect claims。**token 由 hydra 签**,bkn-safe 不是 token 引擎。

## 2. 选型(定)

| 件 | 选 | 不选 / 备注 |
|---|---|---|
| token 引擎 | **上游 ORY Hydra v26.2.0**(不 fork) | 已验;CalVer;device flow 在 v26.x |
| hydra DB | **PostgreSQL**(独立小库) | MariaDB 任何版本装不了上游 hydra(`CAST AS JSON`,MDEV-26448);PG 是 hydra 一等后端 + 金仓 PG 系利于未来信创;信创延后,仅 hydra 这块隔离 |
| bkn-safe ORM/DB | **GORM + proton-rds driver** | 信创 driver 级免费;绝不 pop |
| authz 引擎 | **casbin/v2 + gorm-adapter** | `keyMatch` 非 keyMatch2(`:` 越权 bug);effect 只 allow |
| 密码 | **x/crypto/bcrypt** | |
| hydra 对接 | **ory/hydra-client-go/v2** | |
| 外部用户 | **go-ldap**(轻) | 重度 IAM(Keycloak/Kratos/Zitadel/Casdoor)否决:加服务/重置 hydra/不合目录模型/信创风险 |
| bkn-safe 依赖 kweaver-go-lib | **零** | bkn-safe 是生产方,用上游库 |

## 3. 按层迁移策略(核心)

剔 ISF 的爆炸半径**按层不均**,分层处理:

| 层 | 调用方 | 风险 | 策略 |
|---|---|---|---|
| **introspect / token** | **8 服务**(kweaver-go-lib/hydra,鉴权中间件,每请求) | 🔴 高 | **保兼容** —— hydra 签 token,bkn-safe 注 ext claims(`visitor_type="realname"` 等)。应用零改。不重设计。 |
| **authz** | 5 服务(bkn/vega/dataflow/exec-factory/DA,各自 drivenadapter) | 🟡 中 | **全新做** —— Casbin 干净 API,改 5 服务适配层 + 迁 policy 数据。弃 ISF authz 怪癖。 |
| **user-mgmt** | ~4 服务(bkn/vega/flow/DA,各自 client) | 🟡 中 | **倾向全新做**(求简),亦可保契约;改调用方适配层。最终定见 §6。 |
| **eacp / anyshare auth** | 仅 flow-automation(+ 密码登录耦合) | 🟡 中 | **切断** —— bkn-safe 自有库验密码,不调 eacp;flow-automation 的 anyshare 文档 ACL 随 anyshare 决策(待定 #2)。 |

> 应用的 logger/audit/rest 仍用 kweaver-go-lib(织得深,不值得拔)。introspect 是否现代化为 JWT 本地验签(去 introspect 往返 + panic 解析,但改 8 服务)= **OPEN**,默认先走保兼容。

## 4. 逐服务切换顺序(增量,不大爆炸)

每个服务的 ISF 调用都集中在**一个适配文件**(drivenadapter/middleware)→ 改动有界。顺序按耦合面从小到大:

1. **bkn-safe 上线 + 标准 hydra**(已有 dev 栈),seed 角色(role.json,保 UUID)。
2. **authz 先切**(只 5 服务,孤立适配):exec-factory → DA → dataflow → vega → bkn。每个切完**影子比对**(bkn-safe Casbin vs ISF authorization,同请求 diff 判定)再 flip。
3. **user-mgmt 切**(~4 服务):同样适配层替换 + 比对。
4. **introspect 端点**指向 bkn-safe/hydra(保兼容,理论零改,仅改 endpoint 配置)。
5. **eacp/anyshare**:切断密码登录依赖;flow-automation anyshare 文档功能按 #2 决策处理。
6. **对账无差** → 下线 ISF 11 服务。

## 5. 风险兜底

- 调用点集中在适配层 → 每服务改 1 文件。
- 逐服务切 + **影子比对**(冻结契约 + 已抓 golden = 回归预言机)。
- **角色 UUID 保号** → 零数据/代码迁移。
- 都是自有服务 + 有测试,非第三方黑盒。
- 失败回滚:endpoint 配置切回 ISF(并行期保留)。

## 6. 决策（2026-06-03 已拍）

1. **user-mgmt:全新接口**(不复刻 ISF 13 端点;改 ~4 调用方适配器)。
2. **introspect:保兼容**(应用零改,仅改 endpoint 配置指向新 hydra)。JWT 本地验签现代化 = 后续可选优化(差「1 个 hydra 配置 + 重写 8 服务验签中间件 + 接受 exp 撤销」)。
3. **anyshare:彻底去掉**(待产品确认无人用文档库能力后执行)。评估:flow-automation 的 eacp(`perm2/set|get`)+ authentication-jwt **是为 33 个 `@anyshare/*` dataflow 动作服务的**,非独立 auth。含义:① **bkn-safe 不实现 eacp/jwt**,auth 替换不被阻塞;② anyshare 动作删除是独立的 **flow-automation 清理任务**(删 `pkg/actions/anyshare_*.go`、`drivenadapters/{eacp,anyshare,doc,doc_share,authentication}.go`、actionMap 33 项),**删前需产品确认无 dataflow 在用**。
4. **bkn-safe 定位:独立可选组件**(沿 ISF 定位,不占 Core 预算)。
5. **hydra DB:PostgreSQL**(MariaDB 永不支持 CAST AS JSON);信创延后。
6. **重度 IAM / 多协议联邦**:以后再说。

## 7. 已完成(Phase 0/1)

- 契约冻结 + 可执行 contract test(introspect 真实 lib v1.0.5、authz Casbin 等价、抓到 keyMatch2 越权 bug)、user-mgmt 调用侧契约、角色 UUID seed。
- 标准 hydra v26.2.0 + PostgreSQL dev 栈 + bkn-safe on MariaDB(VM,持久,smoke + e2e PASS:client_credentials introspect + device flow)。
- ISF 真实 golden 取证(VM):introspect user token 真值(`visitor_type=realname`、`udid=""`)、operation-check、resource-operation;发现 resource-filter/list、v2/names 仅内网(public 404)。详见 `contracts/golden/_capture-notes.md`。
