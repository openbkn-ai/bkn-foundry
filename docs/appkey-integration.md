# AppKey（用户自助签发的 API Key）对接文档

面向**前端**（AppKey 管理页）与 **SDK / 外部客户端**（用 AppKey 访问 Context Loader）。

实现见 issue #75，分支 `feat/app-key-auth`。已在 VM（`10.211.55.4`）端到端验证通过。

---

## 0. 一句话原理

AppKey 是用户**自助签发的长期凭据**，以 `bak_` 开头。它**以签发者本人的身份**鉴权：校验后解析出 owner 的 `accessor_id` + `account_type`，下游授权与本人用 OAuth token 时**完全一致**，不是另一套权限。

- **签发/管理**：走 bkn-safe，需要用户**当前的 OAuth 会话**（和调 `/me`、`/admin` 一样）。
- **使用**：把 AppKey 当 `Authorization: Bearer` 用，**仅** Context Loader 的 MCP + REST 接口接受；平台其它服务仍要 OAuth token。
- **撤销即时生效**（每次调用查库）。明文 key **只在签发时返回一次**，之后不可再取。

---

## 1. 前端：AppKey 管理页

### 1.1 自助接口（用户管理自己的 key）

base：`/api/safe/v1/me/api-keys`　鉴权：用户当前 OAuth token（`Authorization: Bearer <oauth-token>`）

#### 签发　`POST /api/safe/v1/me/api-keys`

请求：
```json
{ "name": "我的 Cursor", "expires_at": "2027-01-01T00:00:00Z", "never_expire": false }
```
| 字段 | 必填 | 说明 |
|---|---|---|
| `name` | 是 | 展示名，便于用户区分用途 |
| `expires_at` | 否 | RFC3339；**省略 = 默认 1 年**；必须是将来时间，否则 400 |
| `never_expire` | 否 | `true` = 永不过期（优先级高于 `expires_at`）。**建议做成需显式勾选 + 文案警示** |

响应 `201`：
```json
{
  "id": "7d6042...",
  "key_id": "b3ffa7f4...",
  "name": "我的 Cursor",
  "key": "bak_b3ffa7f4..._fb74b234...",
  "enabled": true,
  "expires_at": "2027-06-26T11:49:59+02:00",
  "last_used_at": null,
  "created_at": "2026-06-26T11:49:59+02:00"
}
```
> ⚠️ **`key` 字段是完整明文，只此一次**。前端必须：弹窗展示 + 一键复制 + 明确提示"关闭后无法再次查看"。**不要**把它存进任何可再次读取的地方。列表接口永远不会再返回它。

#### 列出　`GET /api/safe/v1/me/api-keys`
```json
{ "keys": [
  { "id": "...", "key_id": "b3ffa7f4...", "name": "我的 Cursor",
    "enabled": true, "expires_at": "2027-...", "last_used_at": "2026-...", "created_at": "2026-..." }
] }
```
- 无 secret。`last_used_at` 为 `null` 表示从未使用 —— 可用于"僵尸 key"提示。
- `expires_at` 为 `null` 表示永不过期。

#### 撤销　`DELETE /api/safe/v1/me/api-keys/:id`
- 用路径里的 `id`（不是 `key_id`）。成功 `204`，不存在/非本人 `404`。
- 建议二次确认弹窗（撤销不可逆，且立即失效）。

### 1.2 管理员接口（治理 / 应急）

base：`/api/safe/v1/admin/api-keys`　鉴权：管理员 OAuth token（`RequireAdmin`）

- `GET /api/safe/v1/admin/api-keys?owner_id=<可选>` → 列出全部（或某用户）key，比自助多 `owner_user_id` 字段。
- `DELETE /api/safe/v1/admin/api-keys/:id` → 撤销任意 key。

> 管理员页用途：审计谁签了哪些 key、`last_used_at` 看活跃度、出事一键撤销。

### 1.3 前端 UX 清单

- [ ] 签发后明文一次性展示 + 复制 + "无法再次查看"提示
- [ ] 有效期：默认 1 年；可选自定义日期；"永不过期"独立勾选 + 风险文案
- [ ] 列表展示 `name / 创建时间 / 过期时间 / 最近使用 / 状态`
- [ ] 撤销二次确认
- [ ] 引导文案：告诉用户"把这个 key 填到 MCP 客户端 / SDK 的 Authorization 里，替代易过期的登录 token"
- [ ] （管理员页）全量列表 + 按 owner 过滤 + 撤销

---

## 2. SDK / 外部客户端：用 AppKey 访问 Context Loader

### 2.1 怎么用

把 AppKey 放进 **`Authorization: Bearer`**，调 Context Loader：

- MCP：`POST/GET https://<host>/api/agent-retrieval/v1/mcp` （Streamable HTTP）
- REST：`POST https://<host>/api/agent-retrieval/v1/kn/*`

网关按前缀自动分流：`bak_` → bkn-safe 验卡；其余 → 原 OAuth 内省。**两种凭据同一个 header、同一个端点，都能用**。

### 2.2 MCP 客户端配置（Cursor / Claude Desktop 等）

```json
{
  "mcpServers": {
    "bkn-agent-retrieval": {
      "type": "http",
      "url": "https://<host>/api/agent-retrieval/v1/mcp",
      "headers": {
        "Authorization": "Bearer bak_<keyid>_<secret>"
      }
    }
  }
}
```

> 相比旧配置：**只需要 `Authorization` 一个头**。删掉 `x-account-id` / `x-account-type` —— 公共链上身份完全由凭据解析，这两个头不被读取（留着无用，且指向私有入口时有冒充隐患）。token 值从易过期的 `ory_at_...` 换成长期的 `bak_...` 即可，其余不变（drop-in）。

### 2.3 REST 调用示例

```bash
curl -H "Authorization: Bearer bak_<keyid>_<secret>" \
     -H "Content-Type: application/json" -d '{}' \
     https://<host>/api/agent-retrieval/v1/kn/list_knowledge_networks
```

### 2.4 SDK 实现要点

- 凭据来源：让用户在 SDK 配置里填 AppKey（从管理页复制）。SDK 不负责签发，只消费。
- 注入方式：所有对 Context Loader 的请求统一加 `Authorization: Bearer <appkey>`。`getToken` 也兼容 `X-Authorization` 头与 `?token=` query，但**首选** `Authorization`。
- 错误处理：
  - `401` = key 无效 / 过期 / 已撤销 / owner 被禁用 → 提示用户去管理页**重新签发**（不要自动重试）。
  - 其它 4xx/5xx 按正常 API 错误处理。
- **范围限制**：AppKey **只**对 Context Loader MCP/REST 有效。SDK 若还要调 bkn-backend / vega / 其它服务，那些仍需 OAuth token —— 不要拿 AppKey 去调，会 401。
- AppKey **不能**用来调 bkn-safe 的 `/me`、`/admin`、`/me/api-keys` —— 即不能用 AppKey 再签 AppKey（无提权链）。签发必须有真实 OAuth 会话。

---

## 3. 行为契约速查（已在 VM 验证）

| 场景 | 结果 |
|---|---|
| 用 AppKey 调 Context Loader MCP/REST | 身份 = 签发者，授权同其本人 OAuth |
| 用 OAuth token 调（旧方式） | 不受影响，照常工作 |
| 无 token / 假 `bak_` / 过期 / 已撤销 key | `401` |
| 撤销后立即复用 | 立刻 `401`（查库即时生效） |
| 默认有效期 | 1 年（可改 / 可设永不过期） |
| 权限范围 | v1 全继承 owner 权限，无 scope 细分 |
| AppKey 调 bkn-safe `/me`、`/admin`、再签 key | `401`（仅数据面可用，无提权） |

---

## 4. 待补充（后续可做）

- 前端 AppKey 管理页（本仓为后端，UI 在 Studio 前端仓）。
- SDK 侧凭据配置 + 401 重签引导。
- v2 可选：scope 细粒度限权（按工具/资源）、key 轮换（rotate）接口、签发账户类型白名单。
