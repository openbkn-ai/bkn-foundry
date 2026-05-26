# ADP / Decision-Agent 认证授权（ISF 接入）现状分析与重构评估

> 日期：2026-05-22
> 范围：`adp/`、`decision-agent/`、`infra/`
> 主题：各微服务如何接入 ISF（Information Security Fabric）做认证（Hydra Token）与授权（authorization 策略），重复度评估，重构方向

---

## 1. 背景：ISF 是什么

ISF = **Information Security Fabric**，KWeaver 的安全/认证底座，**可选组件**（文档里 Info Security Fabric / Internal Security Framework 命名混用，实体一个）。由 Helm 安装的一组服务构成（见 `deploy/release-manifests/0.7.0/isf.yaml`）：

| 组件 | 职责 |
|---|---|
| **hydra** | OAuth2 Provider：**签发 / 存储 / 内省 / 刷新 token**（token 唯一来源，库 `hydra_v2`） |
| **authentication** | 验证用户身份（OAuth2 login/consent provider；会话、票据、短信验证码） |
| **oauth2-ui** | 登录 / 授权页面 |
| **authorization** | token 之后的权限策略决策（`/operation-check`、`/policy`） |
| user-management / sharemgnt / policy-management / eacp / audit-log | 用户、组织、策略、审计 |
| isfweb / isfwebthrift | 微前端组件（由 DIP 挂载） |

架构约束（`rules/ARCHITECTURE.md`）：ISF 是 Core 「无 UI」原则的唯一例外（可出微前端，但必须 DIP 挂载）；ISF 可选，禁用不得阻止系统启动。

### 认证 vs 授权分工
- **Hydra** 只管 token，不认证用户身份；按 OAuth2 标准把 login/consent 委托给 `authentication` + `oauth2-ui`。
- 应用侧只做 **introspect 校验**（不签发），再调 `authorization` 做 **operation-check** 鉴权。

---

## 2. 角色与权限的初始化位置

| 层 | 位置 | 说明 |
|---|---|---|
| 角色/权限**表结构** | `deploy/scripts/sql/{0.4.0,0.5.0}/isf/01-init-database.sql` | `t_role`、`t_user_role_relation`、`t_org_perm`、`t_acs_custom_perm`、hydra OAuth scope 等 |
| 表结构注入时机 | ≤0.5.0 `init_isf_database()`；**≥0.6.0 改由 `isf-data-migrator` chart（pre-stage）** | 0.6.0+ 后 repo 不再管 schema/seed |
| 默认业务角色（数据/AI/应用管理员）的**数据 seed** | **不在本仓库** | repo SQL 全文无 `INSERT INTO t_role`；由 ISF 服务（sharemgnt/authorization）自举，或 isf-data-migrator chart |
| 角色**分配给用户** | `deploy/scripts/lib/onboard_isf_test_user.sh` 运行时调 `kweaver-admin role list` + `user assign-role` | onboard 不创建角色定义，只把已存在角色绑到 `test` 用户 |

唯一在 repo SQL 里的角色相关 seed：`t_log_scope_strategy`（audit-log 的 `sec_admin`/审计管理员日志查看策略）。

---

## 3. 运行时鉴权链路

```
用户 → oauth2-ui 登录 → authentication 验身份 → Hydra 签发 token
     → 用户带 Bearer token 调应用 API
     → 应用 verifyOAuth → Hydra Admin introspect → Visitor{ID, Type}
     → 业务 CheckPermission → authorization 服务 /operation-check → {result: bool}
```

应用统一权限模型 = `Accessor`(谁) + `Resource`(什么) + `Operations`(干啥)。应用代码**看不到角色**：`user↔角色↔权限`映射 + 角色优先级全在 authorization 服务内部算，应用只提交 `accessor.ID + resource + operation` 收 bool。Accessor 类型分 `user`（实名）/ `app`（应用账户）。

### 权限注册（策略下发）
**无集中的「权限点 / 操作类型注册」**——resource type、operation 全是各服务编译进去的常量；authorization 接受任意 type/op 字符串，无需预注册目录。

注册粒度 = **每实例、运行时**：业务对象创建后紧接着调 `CreateResources(resource, ops)` → 取 ctx 创建者 → 组 `PermissionPolicy{accessor=创建者, allow=ops}` → `POST /policy`。即「谁建给谁全套操作权」。删对象 → `DeleteResources` 清策略。

| 服务 | 注册调用点 | 资源类型 |
|---|---|---|
| dataflow/pipeline | `logics/pipeline/pipeline_service.go:162` | `stream_data_pipeline` |
| vega | `logics/catalog/catalog_service.go:177`、`logics/resource/resource_service.go:176` | catalog / resource |
| bkn | `logics/knowledge_network/...`、`logics/ontology_init.go` | 知识网络 |

> 注意区分：bkn/execution-factory 调的 `vega CreateResource`（`VBA.CreateResource` / `vegaClient.CreateResource`）是把对象登记成 **vega 数据资源/catalog**，**不是** ISF authorization 策略，仅同名易混。

---

## 4. 现状全景：4 套实现并存

| 服务 | Hydra 抽象 | 认证开关 | permission | 分层风格 |
|---|---|---|---|---|
| **bkn-backend** | `go-lib/hydra` | `AUTH_ENABLED` | operation-check（`permission_access` ~380 行） | Pattern A |
| **vega-backend** | `go-lib/hydra` | `AUTH_ENABLED` | operation-check（与 bkn 94% 同） | Pattern A |
| **dataflow/pipeline-mgmt** | `go-lib/hydra` | `AUTH_ENABLED` | operation-check（同上） | Pattern A |
| **bkn/ontology-query** | `go-lib/hydra` | `AUTH_ENABLED` | 无 permission | Pattern A（缺 permission） |
| **context-loader** | `go-lib/hydra` 直连 | `AUTH_ENABLED` | 无（#250 已删 user_mgmt 死代码） | Pattern B（middleware） |
| **execution-factory** | `go-lib/hydra` 直连 | `AUTH_ENABLED` | authorization.go | Pattern B |
| **dataflow/flow-automation** | `go-lib/hydra` 直连（`pkg/ecron` 另起一套） | 自有 | authorization.go | Pattern B + ecron 私货 |
| **decision-agent/agent-factory** | **`go-lib/rest.Hydra`**（≠ hydra 包） | **Mock 开关**（`SwitchFields.Mock.MockHydra`） | `authzhttp/judge_single_check` 自写 | Pattern C（capimiddleware） |
| **infra/oss-gateway-backend** | 无 | — | — | 不接 ISF |
| **infra/mf-model-manager, mf-model-api** | 无 | — | — | 不接 ISF |

- **Pattern A**（重，DDD 分层）：`interfaces/{auth_access,auth_service,permission_access,permission_service}` + `logics/auth/{hydra,noop}` + `logics/permission/{impl,noop}` + `drivenadapters/{auth,permission}` + `common/setting.go::GetAuthEnabled`。
- **Pattern B**（轻，中间件直连）：`driveradapters/middleware.go::middlewareIntrospectVerify` + `drivenadapters/hydra.go`，无 Service 抽象。
- **Pattern C**（DA）：`capimiddleware/verify_oauth.go` + `authzhttp/judge_single_check.go`，用 `rest.Hydra` 抽象 + Mock 注入假身份（`mocked_user_id`）。

---

## 5. 重复度实测

- `permission_access.go`：bkn vs vega **仅差 22 行 / ~380（≈94% 相同）**，差异全是 observability 库（`oteltrace/otellog` vs `o11y`）+ import 路径 + 几行 logger。vega vs dataflow 同理。→ **三份纯复制**。
- `func GetAuthEnabled()`：**6 个服务各定义一遍**，逐字复制。
- `ADMIN_ACCOUNT_ID` 硬编码常量、resource/operation 枚举、Noop 实现：各服务各抄。
- 粗估 **>1500 行近重复**。

---

## 6. 仓库边界（决定重构落点）

| 事实 | 含义 |
|---|---|
| **无 `go.work`** | 模块间不联编，互不可见 |
| 每服务**独立 go module**（6+ 个各自 go.mod） | 跨模块共享必须靠「发布」或 `replace` |
| module 名 **3 套乱象**：`bkn-backend`/`vega-backend`/`flow-stream-data-pipeline`（裸名）vs `github.com/kweaver-ai/adp/...` vs `github.com/kweaver-ai/kweaver-core/adp/...` | 裸名无法被别的模块 import——这些服务设计上就没打算互相引用 |
| `kweaver-go-lib` 是**外部已发布 module**，按版本 pin，**版本已漂移**（bkn/vega `v1.0.4` vs dataflow `v1.0.5`） | 唯一现成跨模块共享通道；但「升 lib ≠ 自动生效」，各服务各自升版本 |

**结论**：仓内建 `pkg/authz` 并非「零摩擦」（无 go.work + 裸 module 名 → 同样要 replace 或发版）。**真正干净的共享点 = `kweaver-go-lib`**，代价是版本漂移治理。

### lib 自身的债
即使抽 lib，先有两个抽象之争待收敛：
- adp 用 `kweaver-go-lib/hydra`（`hydra.Hydra` / `hydra.Visitor`）
- DA 用 `kweaver-go-lib/rest`（`rest.Hydra` / `rest.Visitor` / `rest.TokenIntrospectInfo`）

两套开关语义也不同：adp 的 `AUTH_ENABLED=false` 是「匿名放行」；DA 的 Mock 是「假装某用户」（测试用途）。

---

## 7. 可抽性判断

| 部件 | 重复度 | 可抽 | 落点 |
|---|---|---|---|
| `permission_access`（operation-check / policy CRUD） | 94% | ✅ 强 | lib `authz.Client` |
| `CreateResources`/`DeleteResources`/`FilterResources` 组 policy 逻辑 | 高 | ✅ | lib `authz` |
| `GetAuthEnabled` + AUTH_ENABLED 工厂 + Noop | 100% | ✅ 强 | lib（一并治版本漂移） |
| token 提取 / introspect 中间件 | A/B 不一致 | ⚠️ 需先归一 | Pattern B 的 `getToken` 多支持 query token，需确认是否保留 |
| resource type / operation 枚举 | 0%（各业务不同） | ❌ 留服务 | 业务语义，不进 lib |

---

## 8. 重构方向（建议，待决策）

### 推荐路径：先收 Pattern A 三兄弟（最高 ROI、最低风险）
bkn-backend / vega-backend / dataflow-pipeline 范式一致、开关一致、94% 重复。把 auth+permission 抽进 `kweaver-go-lib/authz`，三服务退化成「注册资源类型 + 一行接中间件 + 调 Check」。

分阶段（遵守「>3 文件拆任务」）：
1. **Phase 0** 冻结契约：固化 operation-check/policy/introspect 的 struct + contract test；抽 `GetAuthEnabled` 单一实现。
2. **Phase 1** lib 落地：`auth.Middleware`、`authz.Client`、`authz.Noop`、统一类型；`sync.Once` 单例不变。
3. **Phase 2** 服务逐个迁移（每服务一 PR）：dataflow → vega → bkn → ontology-query → 再收 Pattern B 三个。
4. **Phase 3** 收尾：删各服务 `interfaces/permission_access`、mock；统一 `ADMIN_ACCOUNT_ID`、错误码；Pattern B middleware 归一。

### 验收清单
- [ ] `AUTH_ENABLED=true`：各服务外部 API 行为与改造前逐字一致（introspect + operation-check 正常）
- [ ] `AUTH_ENABLED=false`：无 Hydra 也能起，Noop 放行 + fullOps
- [ ] 内网路由 `/in/v1` 不受影响（本就走 Header）
- [ ] `permission_access.go` 等重复文件删除，全仓 `func GetAuthEnabled` 归零
- [ ] 各服务编译通过 + 现有测试全绿 + lib 覆盖两开关路径单测

### 失败条件（任一即回滚）
- 任一服务 introspect/operation-check 的线上请求体/响应解析变化
- Noop 模式给出非 fullOps 或漏放行
- `sync.Once` 语义破坏导致 AUTH_ENABLED 运行期可变
- 跨仓库：服务升 lib 版本时 contract test 不过

---

## 9. 待决策项

1. **重构主目标**：抽共享库（保守、Phase 0+1，未解耦服务直接用）/ 全量统一删重复（4 阶段）/ 只统一开关逻辑。
2. **共享落点**：先确认仓库边界结论已知 → 倾向直接进 `kweaver-go-lib`（仓内 module 裸名无法互引）。
3. **lib `hydra` vs `rest` 抽象**收敛到哪个？（需拉 kweaver-go-lib 源码看差异）
4. **DA 的 Mock 开关**是否纳入统一，还是当独立测试机制保留？
5. **重构范围**：先 Pattern A 三兄弟（推荐），还是连 Pattern B/C 一起。
6. **observability 库分裂**（`oteltrace/otellog` vs `o11y`）是迁移半途还是有意——抽 lib 前需统一。

---

## 附：关键文件索引

- ISF 部署：`deploy/scripts/services/isf.sh`、`deploy/release-manifests/0.7.0/isf.yaml`
- 角色 SQL：`deploy/scripts/sql/0.5.0/isf/01-init-database.sql`
- onboard 角色分配：`deploy/scripts/lib/onboard_isf_test_user.sh`
- 权限模型（Pattern A 代表）：
  - `adp/dataflow/flow-stream-data-pipeline/server/pipeline-mgmt/interfaces/permission_access.go`
  - `.../drivenadapters/permission/permission_access.go`
  - `.../logics/permission/permission_service_impl.go`
- 解耦设计：`adp/docs/design/bkn/features/auth/auth-decouple-design.md`、`adp/context-loader/agent-retrieval/docs/prd/issue-250-contextloader-isf-decouple-prd.md`
- DA 模式：`decision-agent/agent-backend/agent-factory/src/infra/common/capimiddleware/verify_oauth.go`、`.../drivenadapter/httpaccess/authzhttp/judge_single_check.go`
