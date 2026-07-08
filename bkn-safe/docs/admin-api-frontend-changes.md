# bkn-safe 管理 API 变更 —— 给前端的完整回应

针对前端在对接 `/api/safe/v1/admin/*` 时提出的缺口清单的逐条回应。所有端点均在 admin 组下，鉴权同现有：`Authorization: Bearer <超管 token>`（`RequireAdmin` = token introspect + casbin 超管校验）。

> 已部署到验证 VM（10.211.55.4，ns `openbkn`，镜像 `bkn-safe:0.1.1-alpha-deptaudit`）。代码见 PR #59。

## 结论速览

| 前端提出的缺口 | 结论 | 说明 |
|---|---|---|
| 人员↔部门归属写不了 | ✅ **已加** | 部门成员写端点 + 用户建/改内联 `department_ids` |
| 审计日志 | ✅ **已加** | 自动记录所有变更类 admin 请求 + 查询端点 |
| 部门扩展字段（负责人/编码/邮箱/备注） | ✅ **已加** | `manager_id` / `code` / `email` / `remark`；编码非空时全局唯一 |
| 冻结/解冻（独立于 enabled） | ❌ **按定调不做** | 继续用 `enabled` 表达停用/启用 |
| 搜索只 account/name 子串 | 未变 | 邮箱/部门/状态筛选仍客户端做 |
| 角色无 displayName | 未变 | 标识+显示名合并到 `name` |
| 权限是对象元组非扁平点 | 未变 | 角色权限 UI 按对象授权做（见末节） |

---

## 1. 人员归属写

### 1a. 部门成员写端点（部门视角，与已有 `GET .../members` 对称）

幂等。

```
POST   /api/safe/v1/admin/departments/:id/members
DELETE /api/safe/v1/admin/departments/:id/members
Body:  { "user_ids": ["u-1", "u-2"] }
```

| 场景 | 状态码 | 返回 |
|---|---|---|
| 成功 | `204` | 无 body |
| 部门不存在 | `404` | `{"error":"department not found"}` |
| 某 user_id 不存在（仅 POST） | `400` | `{"error":"unknown user id: [ghost]"}`，**整批拒绝，一个都不写** |

- POST 重复添加同一人 → 仍 `204`，不产生重复行。
- DELETE 移除不在部门里的人 → 仍 `204`（幂等）。

### 1b. 用户建/改内联 `department_ids`（用户视角，编辑表单一步到位）

**新建**（`POST /api/safe/v1/admin/users`）：可选 `department_ids`，建用户后归入这些部门。同时**新增收 `telephone`**（之前只有 update 收，现已对齐）。

```jsonc
POST /api/safe/v1/admin/users
{
  "account": "alice",          // 必填
  "name": "Alice",
  "email": "alice@x.com",
  "telephone": "13800000000",  // 新增
  "password": "...",           // 可选，缺省发平台初始密码 openbkn 并强制改密
  "account_type": "other",
  "department_ids": ["d-1","d-2"]  // 可选，初始归属
}
// -> 201 { "id": "..." }
// 任一部门 id 不存在 -> 400，且不会创建用户（先校验后建，无脏数据）
```

**修改**（`PUT /api/safe/v1/admin/users/:id`）：`department_ids` 为**替换**语义。

- 传数组 → 用该集合**整组替换**用户的部门归属。
- 传 `[]`（空数组）→ **清空**全部归属。
- **不传该 key** → 归属**不动**（只改其它资料字段）。

```jsonc
PUT /api/safe/v1/admin/users/:id
{ "name": "Alicia", "department_ids": ["d-2"] }   // 改名 + 归属只留 d-2
{ "department_ids": [] }                          // 仅清空归属（其它不动）
{ "telephone": "..." }                            // 仅改资料，归属不动
// -> 204
// 部门 id 不存在 -> 400（写入前校验，原归属不变）
// 用户不存在 -> 404
```

> 1a 与 1b 二选一即可，按 UI 形态选：组织树拖人用 1a，用户编辑表单选部门用 1b。底层同一张多对多表，互通。

### 读取（已有，回显用）

- `GET /api/safe/v1/admin/users/:id` 返回 `departments: [deptId...]` —— 编辑表单回显归属用它。
- `GET /api/safe/v1/admin/departments/:id/members` 返回部门下成员（直接成员，不含子部门）。
- `GET /api/safe/v1/me` 的 `departments` 字段同样反映当前登录人归属。

---

## 2. 审计日志

每个**变更类**（POST/PUT/DELETE）且**通过鉴权**的 admin 请求自动落一条。**GET 不记**（读不进审计，无回环）；鉴权失败（401/403）不记。

```
GET /api/safe/v1/admin/audit-logs
  ?actor_id=&resource=&action=&target_id=&from=&to=&offset=&limit=
```

| 参数 | 说明 |
|---|---|
| `actor_id` | 操作人 user id |
| `resource` | 顶层名词：`users` \| `departments` \| `roles` \| `role-bindings` |
| `action` | 点分路由：`users`、`users.password`、`departments`、`departments.members`、`roles`、`roles.permissions`、`role-bindings` |
| `target_id` | 被操作对象 id（路由 `:id`，无则空串） |
| `from` / `to` | **RFC3339** 时间戳（如 `2026-06-17T00:00:00Z`），格式错 → `400` |
| `offset` / `limit` | 分页，`limit` 默认 50、上限 500 |

返回（**按时间倒序**）：

```jsonc
{
  "total": 123,
  "logs": [
    {
      "id": "…",
      "actor_id": "<操作人 user id>",
      "method": "POST",
      "resource": "departments",
      "action": "departments.members",
      "target_id": "d-1",                    // 路由 :id（这里是部门 id）
      "detail": "{\"user_ids\":[\"u-3\"]}",  // 脱敏后的请求体快照
      "status": 204,
      "client_ip": "10.0.0.1",
      "created_at": "2026-06-17T08:30:00Z"
    }
  ]
}
```

前端实现要点：

- **谁做的**：`actor_id` 是 user id，不是名字。用 `POST /api/safe/v1/directory/names {user_ids:[...]}` 批量解析显示名。
- **对哪个对象**：`target_id` 是裸 id（部门/用户/角色 uuid），**必须解析成名字**再显示，否则就是一串 hex。按 `resource` 分流解析：
  - `departments` → `POST /directory/names {department_ids:[target_id]}`
  - `users` → `POST /directory/names {user_ids:[target_id]}`
  - `roles` → 用 `GET /admin/roles` 的 id→name 映射
  - `target_id` 为空（如新建类，无 `:id`）→ 对象列从 `detail` 取（见下）
- **改了什么内容**：`detail` 是**脱敏 + 截断**的请求体 JSON 快照（字符串，需 `JSON.parse`）。`password`/`new_password`/`old_password` 已掩码成 `***`。用它补全 `target_id` 看不到的信息：
  - 部门加人/移人 → `detail.user_ids`（再走 `/directory/names` 解析成人名，显示「把 张三、李四 加入 研发部」）
  - 新建部门/用户 → `detail.name`（target_id 为空时对象列显示它，如「新建部门：研发部」）
  - 改用户/部门 → `detail` 里有哪些 key 就是改了哪些字段
  - 角色授权/撤权 → `detail.resource` + `detail.operations`
  - `detail` 为 `""`（空体或非 JSON）→ 不显示内容行
- **做了什么动作**：`action` 单独不区分增改删，靠 `method` 区分。建议前端做映射表渲染人话：

| method | action | 显示 |
|---|---|---|
| POST | `users` | 新建用户 |
| PUT | `users` | 修改用户 |
| DELETE | `users` | 删除用户 |
| PUT | `users.password` | 重置密码 |
| POST | `departments` | 新建部门 |
| PUT | `departments` | 修改部门 |
| DELETE | `departments` | 删除部门 |
| POST | `departments.members` | 部门加人 |
| DELETE | `departments.members` | 部门移人 |
| POST | `roles` | 新建角色 |
| PUT | `roles` | 修改角色 |
| DELETE | `roles` | 删除角色 |
| POST | `roles.permissions` | 角色授权 |
| DELETE | `roles.permissions` | 角色撤权 |
| POST | `role-bindings` | 绑定角色 |
| DELETE | `role-bindings` | 解绑角色 |

- **失败筛选**：`status` 为真实码，4xx/5xx 都有条目，可做「失败操作」过滤。

---

## 3. 未变项 —— 前端按真实模型实现（后端不会改）

1. **用户搜索**：`GET /admin/users?search=` 只匹配 `account`/`name` 子串（另有 `?account=` 精确查）。邮箱/部门/状态筛选客户端做（注意分页）。
2. **角色搜索**：`GET /admin/roles` 只有 `?source=` 过滤，无文本搜索，客户端过滤。
3. **角色 displayName**：角色只有 `name` + `description`，无独立显示名。设计的「标识 + 显示名」合并到 `name`。
4. **角色权限模型**：是**对象元组**，不是扁平权限点。授权/撤权：
   ```
   POST   /api/safe/v1/admin/roles/:id/permissions
   DELETE /api/safe/v1/admin/roles/:id/permissions
   Body:  { "resource": { "type": "catalog", "id": "*" }, "operations": ["read","write"] }
   ```
   `id:"*"` = 对整类资源授权；具体 id = 对单实例授权（即「给某个数据连接授权」）。角色权限 UI 必须按对象授权做，不能用「勾选扁平权限点」。内置角色（system/business）权限只读，改它 → `403`。
5. **冻结/解冻**：无独立状态，用 `PUT /admin/users/:id {"enabled":false/true}` 表达停用/启用。
6. **部门扩展字段**：`manager_id`（须为已有用户 id）、`code`（非空时全局唯一）、`email`、`remark`；列表与详情均返回 `manager_name`。
