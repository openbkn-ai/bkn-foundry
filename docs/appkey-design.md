# AppKey 设计与运维文档

AppKey（用户自助签发的 API Key）的架构、数据模型、安全模型与部署说明。配套消费侧对接见 [appkey-integration.md](./appkey-integration.md)；需求/拆解见 Linear OPE-14、GitHub openbkn-ai/bkn-foundry#75。

---

## 1. 背景与目标

Context Loader 的 MCP（`/api/agent-retrieval/v1/mcp`）与 REST（`/kn/*`）此前只接受 Hydra 签发的 OAuth access token（`ory_at_...`，~1h 过期）。外部客户端（Cursor / Claude Desktop / 脚本 / SDK）每次都要重新复制易过期 token，体验差。

AppKey 引入**用户自助签发的长期凭据**，以签发者本人身份鉴权，权限同源；可自助签发 / 列出 / 撤销，管理员可全局管控。这是行业标准的 PAT（Personal Access Token）模式。

非目标（v1 不做）：scope 细粒度限权、key 轮换接口、其它微服务接入、按账户类型限制签发。

---

## 2. 架构

两个服务，单一咽喉：

```
客户端 ──Authorization: Bearer <凭据>──▶ agent-retrieval 网关
                                          │ middlewareIntrospectVerify
                                          │   token 前缀判别：
                                          ├─ bak_…  ─▶ bkn-safe /api-keys/introspect ─▶ owner 身份
                                          └─ 其余    ─▶ hydra /oauth2/introspect       ─▶ 用户身份
                                          ▼
                                   AccountAuthContext{AccountID, AccountType}
                                          ▼
                                   下游 casbin 授权（0 改动）
```

- **bkn-safe**：凭据权威。签发 / 存储 / 校验 / 撤销 AppKey。
- **agent-retrieval**：消费方。仅在 `middlewareIntrospectVerify` 一处按 `bak_` 前缀分流；两条路产出**同一个** `*TokenInfo` → `AccountAuthContext`，下游一致。

关键性质：AppKey 校验后解析出的是 **owner 的 accessor_id + account_type**，因此不是第二套权限系统 —— 授权完全复用用户本人的 casbin 策略。

代码落点：
- 网关分支：`adp/context-loader/agent-retrieval/server/driveradapters/middleware.go`（`middlewareIntrospectVerify`）
- 验卡 adapter：`.../drivenadapters/appkey.go`
- bkn-safe 凭据层：`bkn-safe/server/internal/auth/apikey.go`（store）、`.../internal/httpapi/apikey.go`（HTTP）、`.../internal/model/model.go`（`APIKey`）

---

## 3. 数据模型

`APIKey`（GORM，AutoMigrate 建表 `api_keys`）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `ID` | string(64) PK | 行 id（删除 / 管理用） |
| `KeyID` | string(64) uniqueIndex | 公开查找半段，嵌在明文里 |
| `OwnerUserID` | string(64) index | 该 key 充当的 `User.ID` |
| `Name` | string(128) | 用户可见标签 |
| `SecretHash` | string(128) | secret 半段的 sha256 hex |
| `ExpiresAt` | *time.Time index | nil = 永不过期 |
| `LastUsedAt` | *time.Time | 每次成功校验更新 |
| `Enabled` | bool | 防御性软禁用（校验也查） |
| `CreatedAt` | time.Time | |

注：撤销 = 删行（`DeleteOwned` / `Delete`）。`Enabled` 保留作未来"禁用不删"用途，校验路径也防御性检查。

---

## 4. Key 格式与哈希

明文形状：`bak_<keyid>_<secret>`

- `bak_` —— AppKey 前缀（`bkn app key`），网关分流判据 + 密钥扫描器/日志脱敏可识别。常量 `auth.KeyPrefix`（bkn-safe）与 `interfaces.AppKeyPrefix`（agent-retrieval）**两处必须一致**。
- `keyid` —— 128-bit 随机 hex，公开，按它查行。
- `secret` —— 256-bit 随机 hex，**只存 sha256**，明文仅签发时返回一次（show-once）。

校验比对：按 `keyid` 查行 → `subtle.ConstantTimeCompare(sha256(secret), SecretHash)`。高熵随机串用 sha256 足够（非 bcrypt），保证每次请求校验廉价。

---

## 5. 信任面与端点

| 端点 | 鉴权 | 暴露面 | 用途 |
|---|---|---|---|
| `POST/GET/DELETE /api/safe/v1/me/api-keys` | `RequireUser`（OAuth token） | 网关 | 用户自助签发/列出/撤销自己的 key |
| `GET/DELETE /api/safe/v1/admin/api-keys` | `RequireAdmin` | 网关 | 管理员全局列出/撤销任意 key |
| `POST /api/safe/v1/api-keys/introspect` | **免 token** | **仅 ClusterIP** | 网关验卡，解析 owner 身份 |

验卡端点响应对齐 OAuth2 introspect：失败 `200 {active:false}`（不泄漏原因），成功 `200 {active:true, sub, account_type, key_id}`。

> ⚠️ 验卡端点与 `/authz`、`/directory` 同信任面 —— **务必只在集群内网（ClusterIP）暴露**，不可经 ingress 对外。

---

## 6. 安全模型 / 威胁分析

**核心约束**：AppKey 是 OAuth 身份的派生，不是平行身份提供者。

- 签发必须有真实 OAuth 会话（`RequireUser`）→ key 权限永不超过本人。
- bkn-safe 的 `RequireUser` 走 hydra 内省、**不认识 `bak_`** → 不能用 AppKey 调 `/me`、`/admin`、`/me/api-keys`。即**无"密钥造密钥"提权链**，AppKey 严格锁在 Context Loader 数据面。
- 撤销即时生效（每次校验查库）。
- owner 被禁用 / 删除 → 其 key 校验即失效（校验查 `User.Enabled`）。

**爆炸半径**：AppKey 是一个与本人等权、长寿命的 bearer 凭据，会被塞进 MCP 客户端配置 / `.env` / shell history，泄漏面比交互 token 大。缓解：

- 只存哈希、明文一次性。
- `last_used_at` 落库 → 发现异常 / 清理僵尸 key。
- 管理员全局撤销作为应急杠杆。
- 默认 1 年而非永久；永不过期需显式勾选。

**为何 v1 不做 scope**：先按 PAT 经典模式（全继承）落地；若 MCP key 跑在半可信环境，后续走 GitHub fine-grained PAT 路线加按工具/资源限权（v2）。

**顺带清理**：公共链上客户端自带的 `x-account-id` / `x-account-type` 头在公共（introspect）链**不被读取**，但若被指到私有 header-trust 链存在冒充隐患 —— 文档/示例已移除这两个头。

---

## 7. 决策

| 项 | 决策 |
|---|---|
| 默认有效期 | 1 年，可改 / 可设永不过期（显式勾选） |
| scope 限权 | v1 不做，全继承 owner 权限 |
| 管理员管控 | 可全局列出 / 撤销任意 key |
| 创建鉴权 | 仅认证（OAuth 会话），不加额外授权门（无提权，符合 PAT 惯例） |
| account_type 白名单 | 不做（明确否决） |

---

## 8. 运维 / 部署

### bkn-safe
- **无需 data-migrator**：表 `api_keys` 由启动时 GORM `AutoMigrate` 创建（`database.Migrate` → `model.AllModels()` 含 `APIKey`）。用 `kubectl set image` 升级也能自建表。
- 验卡端点 `POST /api/safe/v1/api-keys/introspect` 随服务自动挂载（`deps.DB != nil` 时）。

### agent-retrieval
- 必须配置 bkn-safe 地址，否则 `bak_` 校验打到空 URL：
  ```yaml
  bkn_safe:
    private_protocol: "http"
    private_host: "bkn-safe"
    private_port: 3000
  ```
  落点：`server/infra/config/config.go`（`BknSafe PrivateBaseConfig`）+ `agent-retrieval.yaml` + helm `configmap.yaml` / `values.yaml`。
- `AUTH_ENABLED=false` 时 `NewAppKeyVerifier()` 返回 nil → `bak_` token 回落 hydra（noop），不崩。生效需 `AUTH_ENABLED=true`。
- 改了配置需重启 pod（configmap 挂载文件）。

### 验证（VM 已跑通）
登录拿 OAuth token → `POST /me/api-keys` 拿 `bak_` → 当 `Authorization: Bearer` 调 `/mcp` 或 `/kn/*` → 撤销复验 401。完整矩阵见 OPE-14 评论 / [appkey-integration.md](./appkey-integration.md) §3。

---

## 9. 测试

- bkn-safe store：`internal/auth/apikey_test.go`（签发/校验/过期/禁用/坏 secret/owner 禁用/越权删/列表隔离）。
- bkn-safe httpapi：`internal/httpapi/apikey_test.go`（签发一次性明文/列表无 secret/撤销/`resolveExpiry` 默认1年·RFC3339·never_expire·过去时间400/越权删404/introspect active 形状/admin 管控/非管理员403）。
- agent-retrieval adapter：`drivenadapters/appkey_test.go`（active→TokenInfo、account_type→VisitorType、inactive/HTTP/decode 错误）。
- agent-retrieval 中间件：`driveradapters/appkey_middleware_test.go`（`bak_`→appkey、其余→hydra、nil verifier 回落）。
