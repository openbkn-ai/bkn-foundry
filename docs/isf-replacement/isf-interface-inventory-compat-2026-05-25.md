# Kowell 依赖的 ISF 接口清单 与 兼容性评估

> 日期：2026-05-25
> 范围：`adp/`、`decision-agent/`（infra 不接 ISF）
> 目的：盘清 Kowell 各服务调用了 ISF 的哪些接口，评估「自研轻量服务能否保证兼容（应用零改）」

---

## 0. 结论先行

- Kowell 对 ISF 的依赖面 = **3 个核心契约 + 2 个 anyshare 耦合接口**。
- **核心 3 块兼容可行**（自研服务保契约，应用零改）：hydra token 内省、authorization 7 端点（实际只用 RBAC 子集）、user-management 目录查询。
- **anyshare 耦合 2 块**（eacp 文档权限、authentication/jwt）**仅 flow-automation 使用**，且绑定 anyshare 文档体系；是否需要替换取决于 anyshare 集成是否保留。
- 鉴权实测只用「accessor + resource(type+id) + allow operation」纯 RBAC，**Casbin 足以承接**（ISF 自研 ABAC 的 obligation/层级/condition/deny/过期，Kowell 零使用）。

---

## 1. ISF 依赖面总览

| ISF 服务 | 契约面 | 调用方 | 兼容难度 | 性质 |
|---|---|---|---|---|
| **hydra**（token） | `VerifyToken`/`Introspect`（lib `hydra`/`rest.Hydra`） | bkn、vega、dataflow-pipeline、ontology-query、context-loader、exec-factory、flow-automation、DA | 🟢 低 | 标准 OAuth2，保留或换 OIDC |
| **authorization** | 7 端点 `/api/authorization/v1/*` | bkn、vega、dataflow-pipeline、exec-factory、DA | 🟡 中 | 实际只用 RBAC 子集 → Casbin |
| **user-management** | 13 端点 `/api/user-management/v1\|v2/*` | bkn、vega、dataflow、exec-factory、DA | 🟡 中 | 用户/组织目录查询，读为主 |
| **eacp** | `perm1/check`、`perm2/set`、`perm2/get` | **仅 flow-automation** | 🔴 高（anyshare） | anyshare 文档 ACL，非核心鉴权 |
| **authentication** | `/v1/jwt`、`/v1/access-token-perm/app/` | **仅 flow-automation** | 🔴 高（anyshare） | 为 anyshare API 签 JWT |

> 注：`GetToken`（713 处）是 flow-automation 本地的 anyshare token 策略（`logics/auth/auth.go`、`pkg/mod/token_strategy.go`），**非 ISF 调用**，不计入依赖面。

---

## 2. 逐接口清单

### 2.1 hydra（token 内省）
经 `kweaver-go-lib/hydra`（adp Pattern A/B）与 `kweaver-go-lib/rest`（DA）两套抽象：

| 方法 | 次数 | 用途 |
|---|---|---|
| `VerifyToken(ctx, c)` | 136 | 从请求取 Bearer，内省，返回 `Visitor{ID, Type, TokenID}` |
| `Introspect(ctx, token)` | 127 | token 内省，返回 `TokenIntrospectInfo` |
| `GetLanguage(c)` | 7 | 取语言（i18n） |

**兼容方案**：保留 hydra（Go server）或换任意支持 RFC7662 内省 / JWT 验签的 OIDC；lib 客户端只改 endpoint，`Visitor` 字段映射保持即可。两套抽象（`hydra` vs `rest`）需都满足（最终都打 hydra admin）。

### 2.2 authorization（`/api/authorization/v1/*`，7 端点）

| 端点 | 用途 | Pattern A 对应方法 |
|---|---|---|
| `POST /operation-check` | 单点鉴权 → `{result: bool}` | `CheckPermission` |
| `POST /policy` | 建策略（建对象时给创建者授权） | `CreateResources` |
| `DELETE /policy/`（A） vs `POST /policy-delete`（exec-factory） | 删策略 | `DeleteResources` |
| `POST /resource-filter` | 过滤用户可见资源 | `FilterResources` |
| `POST /resource-operation` | 查资源可做操作 | `GetResourcesOperations` |
| `POST /resource-list` | 列资源 | DA/exec-factory |
| `/resource_type/` | 资源类型 | 少量 |

**实测使用的 policy 特性（极简）**：

| 特性 | 用了吗 | 证据 |
|---|---|---|
| `Allow` operations | ✅ | 核心 |
| `Deny` | ❌ 恒空 `[]` | `permission_service_impl.go`、`perm_policy.go:287` |
| `Condition` | ❌ 恒空 `""` | `perm_policy.go:289` |
| `ExpiresAt` | ❌ 恒空 `""` | `perm_policy.go:290` |
| `obligation` / `resource_type_hierarchy` | ❌ 零使用 | 全仓 grep 无 |

→ 实际模型 = 纯 RBAC/ACL：「accessor 对 resource(type+id) 有哪些 allow op」。**Casbin RBAC model 直接表达**。

> ⚠️ 兼容注意：`policy-delete`（exec-factory `POST /v1/policy-delete`）与 Pattern A 的 `DELETE /policy/` **有漂移**，替换服务两种都要支持。

### 2.3 user-management（`/api/user-management/*`，13 端点）
用户/组织目录查询，读为主：

```
/v1/users/            /v1/apps  /v1/apps/
/v1/names  /v2/names  /v1/emails
/v1/departments  /v1/departments/
/v1/internal-groups  /v1/internal-groups/  /v1/internal-group-members/
/v1/group-members  /v1/search-org
```
**兼容方案**：替换服务需实现这组目录查询（用户名/邮箱/部门/组/应用账户）。多为读接口，自建用户表 + 这层查询 API 即可。

### 2.4 eacp（anyshare 文档 ACL，仅 flow-automation）
```
/api/eacp/v1/perm1/check   doc.go:293
/api/eacp/v1/perm2/set     doc_share.go:98,120
/api/eacp/v1/perm2/get     doc_share.go:139
```
**anyshare 文档分享权限**，非核心 ABAC。绑 anyshare 体系。若 anyshare 集成保留则需 anyshare 提供；若移除 anyshare，则随之消失。

### 2.5 authentication（仅 flow-automation）
```
/api/authentication/v1/jwt                  authentication.go:67（为 user_id 签 JWT）
/api/authentication/v1/access-token-perm/app/
```
为访问 anyshare API 签发 JWT。同样绑 anyshare 集成。

---

## 3. 兼容性评估（能否应用零改）

| 契约块 | 兼容可行？ | 替换实现 | 工作量 |
|---|---|---|---|
| hydra 内省 | ✅ | 保留 hydra / 换 OIDC，lib 改 endpoint | 🟢 低 |
| authorization 7 端点 | ✅ | Casbin 包一层适配，保 6+1 端点契约 + 角色 UUID | 🟡 中 |
| user-management 13 端点 | ✅ | 自建目录服务，实现查询 API | 🟡 中 |
| eacp 3 端点 | ⚠️ 取决于 anyshare | 保留 anyshare 或随 anyshare 移除 | 视范围 |
| authentication/jwt | ⚠️ 取决于 anyshare | 同上 | 视范围 |

**关键约束（兼容必须满足）**：
1. **角色 UUID 保号**：`role.json` 9 角色，尤其业务三角色（数据 `00990824-`、AI `3fb94948-`、应用 `1572fb82-`）——DA `inner_role.go` 等硬编码引用，必须沿用同 ID。
2. **operation-check / policy 的请求/响应 schema 逐字一致**（含 `policy-delete` vs `DELETE /policy` 双形态）。
3. **hydra `Visitor` 字段**（ID/Type/TokenID）映射不变。
4. **两套 hydra 抽象**（`hydra` 与 `rest.Hydra`）都要喂饱。

**结论**：核心 3 块（hydra + authorization + user-management）**可保证兼容、应用零改**，replacement 是「契约兼容的单体服务（依赖 hydra + 内嵌 Casbin + 用户目录）」。anyshare 耦合 2 块（eacp、authentication/jwt）**仅 flow-automation**，是否替换取决于 anyshare 集成边界——建议单独决策，不纳入核心 auth 替换范围。

---

## 4. 调用方 × 契约 矩阵

| 服务 | hydra | authorization | user-mgmt | eacp | auth/jwt |
|---|---|---|---|---|---|
| bkn-backend | ✅ | ✅ | ✅ | — | — |
| vega-backend | ✅ | ✅ | ✅ | — | — |
| dataflow/pipeline-mgmt | ✅ | ✅ | — | — | — |
| bkn/ontology-query | ✅ | — | — | — | — |
| context-loader | ✅ | — | — | — | — |
| execution-factory | ✅ | ✅ | ✅ | — | — |
| dataflow/flow-automation | ✅ | ✅ | ✅ | ✅ | ✅ |
| decision-agent/agent-factory | ✅ | ✅ | ✅ | — | — |
| infra/*（oss-gateway, mf-model-*） | — | — | — | — | — |

**flow-automation 是最大兼容负担**：唯一用 eacp + authentication/jwt（anyshare 耦合），且自带 `pkg/ecron` 一套 auth + `IsDataAdmin` 角色名硬判断。

---

## 5. 新增能力：OAuth Device Code（RFC 8628，无头服务器认证）

> ISF 现状不具备此能力，是替换方案要新增的需求。

### 5.1 场景
无头（无浏览器）服务器上跑 CLI 登录：服务器本身无 web，用户在自己电脑浏览器完成验证。

```
无头服务器:  kweaver auth login   → 显示「打开 https://<host>/device 输入码 WDJB-MJHT」
用户笔记本:  浏览器开该 URL → 登录 + 批准
服务器 CLI:  轮询 token 端点 → 拿 token，登录完成
```
关键：**服务器无需 web，只需用户在别处有浏览器**。验证页由中心化部署托管（走 ingress），无头服务器纯出站轮询，零额外端口/web。

### 5.2 替换现状
今天 CLI 登录为密码式（onboard）：`kweaver auth login <url> -u test -p '<pwd>' --http-signin -k` —— 密码进命令行/脚本，不安全、不适合无头自动化。device code 更优：无密码、token 短时、批准可审计。

### 5.3 hydra 版本结论
| | 状态 |
|---|---|
| isf 当前 hydra | **v2.1.1**（go 1.20），无 `device_authorization`/`device_code` → **不支持** |
| ORY Hydra **v2.2.0+** | 原生支持 RFC 8628（device 端点 + verification URI + 轮询） |

→ **必须升级 hydra 到 v2.2.0+**（验 isf 对 hydra 的 fork 改动是否影响升级 + DB migration）。

### 5.4 需要做的
| 件 | 要求 |
|---|---|
| hydra | 升 v2.2.0+，启用 device grant |
| verification 页 | 中心托管 web：输 user_code + 登录 + 批准 → 归 auth-service（login/consent provider） |
| CLI 改造 | `kweaver`/`kweaver-admin` 加 device flow：`POST /oauth2/device/auth` → 显示 code+URL → 轮询 `POST /oauth2/token`（`grant_type=urn:ietf:params:oauth:grant-type:device_code`） |
| client 注册 | hydra 注册 public client，允许 device grant |

### 5.5 对整体方案的约束（关键）
device code 需求**锁定 token 层 = 保留 hydra（升级版）**；**自签 JWT 路线出局**（无 device 流，需自研完整 grant：user_code 熵、轮询限速、CSRF、过期 —— 安全天坑）。验证页本就要做（login/consent），device code 仅多一个「输 user_code」入口，增量小。

### 5.6 含 device code 的最终全景
```
token       = hydra v2.2+（authcode + password + device_code grant）
身份/验证页  = auth-service（login/consent + device verification UI）
鉴权        = auth-service 内嵌 Casbin（保 authorization 契约 + 角色 UUID）
CLI         = kweaver / kweaver-admin 加 device flow
服务数      = hydra + auth-service = 2（仍比 ISF 11 个少 9）
```

### 5.7 已确认决策（2026-05-25）
1. **谁跑无头 CLI**：运维（客户服务器 `kweaver-admin`）+ CI/自动化，**两者都有**。
2. **device code 覆盖范围**：`kweaver`（普通用户）+ `kweaver-admin`（运维），**两个都给**。
3. **password fallback**：**保留**（删除改动面大，作过渡 fallback；见落地设计 §5）。grant 分工：人 → device code（首选）/ password（fallback）；CI → client_credentials（首选）/ password（fallback）。

---

## 6. 关联文档
- 整体现状与重构评估：`reports/adp-auth-permission-isf-analysis-2026-05-22.md`
- ISF 角色 seed（外部 isf 仓）：`/Users/cx/Work/kweaver-ai/isf/Authorization/driveradapters/init_data/role.json`
- ISF Authorization 为自研 ABAC（非 Casbin）：`isf/Authorization/logics/policy_calc.go`、`obligation.go`、`resource_type_hierarchy.go`
