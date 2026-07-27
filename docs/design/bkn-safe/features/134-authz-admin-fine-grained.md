---
issue: "openbkn-ai/bkn-studio#134"
branch: "feature/134-authz-admin-fine-grained"
module: "bkn-safe"
status: "draft"
author: "@sh00tg0a1"
created: "2026-07-27"
pr: ""
---

# Feature #134：授权管理接口的细粒度权限与前后端契约

## 背景与目标

授权管理页面（`/studio/system/authorizations`）对接的是 bkn-safe 的管理接口。此前的问题是接口权限粒度与契约描述两处不足：部分动作只靠 `safe_admin:console:manage` 这一粗门禁或管理员身份放行，而对象授权、角色成员管理、角色权限配置、策略只读审查这四类能力没有各自的权限点；同时对象授权的主体类型、主体标识到 Casbin subject 的映射、资源与操作的合法取值、幂等与撤权未命中的语义，均无正式约定，前后端各自按实现推测。

本次改动的目标有三项：管理接口按动作校验权限点；三员管理（系统管理员、安全管理员、审计管理员）的职责边界在接口层强制；授权面契约成文，供前端按权限点控制入口，并作为回归测试的判据。

`safe_admin:console:manage` 的定位在本次改动后明确为"允许进入安全管理 API 面"，不代表任何具体写权限。

## 设计

### 权限点矩阵

管理面接口与其权限点的对应关系如下。`admin` / `security` / `audit` 三列表示种子角色是否持有该点位。

| 方法与路径 | 权限点 | admin | security | audit |
| --- | --- | --- | --- | --- |
| `GET /admin/object-grants` | `admin-authz:view` | 否 | 是 | 是 |
| `GET /admin/policies` | `admin-authz:view` | 否 | 是 | 是 |
| `POST /admin/object-grants` | `admin-authz:grant` | 否 | 是 | 否 |
| `DELETE /admin/object-grants` | `admin-authz:revoke` | 否 | 是 | 否 |
| `GET /admin/roles`、`GET /admin/roles/:id` | `admin-role:view` | 否 | 是 | 是 |
| `GET /admin/roles/:id/members` | `admin-role:view` | 否 | 是 | 是 |
| `GET /admin/roles/:id/permissions` | `admin-role:view` | 否 | 是 | 是 |
| `GET /admin/role-bindings` | `admin-role:view` | 否 | 是 | 是 |
| `POST`、`DELETE /admin/role-bindings` | `admin-role:members` | 否 | 是 | 否 |
| `POST`、`DELETE /admin/roles/:id/permissions` | `admin-role:permissions`（兼容期同时接受 `admin-authz:grant` / `admin-authz:revoke`） | 否 | 是 | 否 |
| `POST /admin/roles`、`PUT`、`DELETE /admin/roles/:id` | `admin-role:create` / `edit` / `delete` | 否 | 是 | 否 |
| `GET /admin/audit-logs` | `admin-audit:view` | 否 | 否 | 是 |

`admin-role:permissions` 是本次新增的点位。角色权限配置是"塑造角色"，与"把某个具体对象授予某个具体用户"（`admin-authz:grant`）是两件事，拆开后部署方可以分别下发。

角色权限的写接口（`POST` / `DELETE /admin/roles/:id/permissions` 及角色 CRUD 的写操作）由 rbac_basic 插座提供，仅企业版构建挂载；社区版构建不注册 mounter，这些路径返回 404 而非 403。读接口在两种构建下都存在。

### 对象授权契约

**主体（accessor）**

- `accessor_id` 只接受用户行的主键，即 `users.id`（UUID）。应用账号（AppKey 的持有者）同样以用户行建模，通过 `account_type` 区分，因此也是合法主体。
- 部门与用户组不支持，请求会返回 400。原因是 Casbin 中不存在用户到部门的 `g` 规则，部门授权在鉴权时永不命中，写入即成为死策略。需要按组织维度放权时，应改用角色：将部门成员绑定到角色，再配置角色权限。
- 映射规则：`accessor_id` 原样作为 Casbin 策略的 `v0`，bkn-safe 不做任何编码或前缀处理。

**资源（resource）**

- `resource.type` 必须是资源目录（`resource_types` / `operations` 两张表）中已注册的类型，否则返回 400。
- `resource.id` 必须是具体实例标识，不接受空值与 `*`，否则返回 400。对象授权的语义是"整个对象实例"，类型级放权走角色权限而非对象授权。
- `operations` 中每个操作都必须是该资源类型已注册的操作，否则返回 400。此校验阻断拼写错误产生的、任何 `/check` 都无法满足的死策略。
- `resource.type` 为 `safe_admin` 时返回 403。该类型的 `console:manage` 正是 `CanAdmin` 判定的依据，若允许通过对象授权发放，任何被授权者都会成为平台管理员，绕过角色绑定及其提权防护。管理能力只能由角色绑定赋予。

**角色权限配置的资源规则**

- `resource.id` 允许为 `*`，含义是整个资源类型，这是该接口的既定用途。
- `resource.type` 为 `*` 或 `operations` 含 `*` 时返回 400。该形态等价于种子中超级管理员的通配授权，从自定义角色铸造出来即构成提权路径。
- `resource.type` 为 `safe_admin` 时返回 403，理由同上。
- 内置角色（种子角色）的权限由种子管理，接口返回 403。

### 幂等、未命中与错误语义

| 场景 | 响应 | 说明 |
| --- | --- | --- |
| `POST /admin/object-grants` 重复提交相同内容 | 204 | 该接口是覆盖写：主体在该实例上的操作集变为请求所给的集合。重复提交结果一致。 |
| `POST /admin/object-grants` 的 `operations` 为空数组 | 400 | 空集合会静默清空授权，与撤权混淆，因此拒绝；清空请用 `DELETE`。 |
| `DELETE /admin/object-grants` 命中已有授权 | 204 | 审计 `detail._outcome.removed` 记录实际删除的策略条数。 |
| `DELETE /admin/object-grants` 未命中任何授权 | 204 | 幂等设计，重试与重复点击安全。审计中 `_outcome.removed` 为 0，可据此区分"撤权生效"与"未命中"。 |
| `DELETE /admin/object-grants` 的 `resource.type` 未注册 | 400 | 未注册类型撤权本身无副作用，但操作者会把静默空操作读成撤权成功，对安全操作而言是最差结果，故拒绝。 |
| 未携带或携带无效令牌 | 401 | `RequireAdmin` / `RequireUser` 的认证失败。 |
| 令牌有效但不具备进入管理面的能力 | 403 | `CanAdmin` 判定失败。 |
| 具备管理面能力但缺少该接口权限点 | 403 | 响应体 `error` 字段指明缺失的点位，形如 `not authorized for admin-authz:grant`。 |

### 前端权限点映射

前端的入口控制以 `GET /api/safe/v1/me/permissions` 为唯一数据来源。该响应包含 `is_admin` 与 `permissions` 两部分，`permissions` 中同样返回 `admin-*` 系列的类型级行，因此按钮、菜单、路由守卫可直接按权限点判定。

判定规则：某账号对 `(type, id)` 是否可执行 `op`，取类型级行（`id` 为 `*`）与实例行的并集。持有资源通配授权的账号，`permissions` 会折叠成单行 `{type:"*", id:"*", ops:["*"]}`。

界面能力与权限点的对应：

| 界面能力 | 权限点 |
| --- | --- |
| 对象授权按钮 | `admin-authz:grant` |
| 对象撤权按钮 | `admin-authz:revoke` |
| 角色成员绑定与解绑入口 | `admin-role:members` |
| 角色权限配置入口 | `admin-role:permissions` |
| 策略查看与权限审查入口 | `admin-authz:view` |
| 审计日志入口 | `admin-audit:view` |

无权限时应隐藏或禁用对应操作，并给出权限不足提示；直接访问受控路由应渲染 403。前端隐藏不构成防护，后端在接口层已强制同一套边界。

策略审查所需的两个只读接口本次一并补齐：

- `GET /admin/policies?resource_type=&resource_id=`：返回该资源实例上每个主体的授权，含通过角色继承而来的授权。此前对等能力只存在于内部面 `GET /api/safe/v1/authz/policies`，该接口无鉴权、仅集群内可达且不经网关暴露，控制台用户没有可调用的端点。
- `GET /admin/roles/:id/permissions`：单独返回角色的权限集合，与 `GET /admin/roles/:id` 内嵌的同一份数据，供角色权限编辑器与审计审查使用，无需拉取成员负载。

## 兼容性

`admin-role:permissions` 的引入采用宽限期而非硬切换：

- 种子角色的权限每次启动由 `reconcileSeedRoles` 按 `grants.json` 重灌，因此 `security` 角色在升级后自动获得新点位，无需迁移脚本。
- 自定义角色的权限不被重灌。若某自定义角色此前只被授予 `admin-authz:grant` / `revoke`，硬切换会使其在升级后失去角色权限配置能力。因此两个写接口在兼容期同时接受新旧点位，由 `RequireAnyPermission` 实现。
- 经旧点位放行的请求会输出 `admin request authorized via a superseded permission point` 级别为 warn 的日志，携带 accessor、命中的点位与路由。宽限期的退出以日志静默为依据：确认无调用后，将两个接口改回 `RequirePermission` 单点校验。
- `adminwrite.Services` 接口未新增方法，any-of 能力以可选接口 `AnyPermissionRequirer` 提供。未实现该可选接口的 Services（例如企业版仓库中的测试替身）退化为只校验规范点位，不会因编译不通过而中断。

## 测试

后端回归测试覆盖以下场景：

- 仅持有 `safe_admin:console:manage` 的调用者：对象授权读写、角色绑定读写、角色权限读写、策略读取全部 403。
- 三员矩阵：`admin` 在授权类接口（对象授权读写、角色权限配置、策略读取）全部 403；`security` 对象授权与角色权限读写全部通过；`audit` 策略与角色权限只读通过、写操作全部 403。
- 点位拆分：仅 `admin-role:permissions` 可完成角色权限读写；仅 `admin-authz:grant` 在兼容期可写入但不能撤销；仅 `admin-authz:view` 两者皆不可。
- 撤权语义：未注册资源类型 400；命中撤权与未命中撤权均为 204，审计 `_outcome.removed` 分别为实际条数与 0。
- 种子矩阵：`security` 持有 `admin-role:permissions`，`admin` 与 `audit` 不持有。

## 待跟进

- 前端按权限点拆分入口（bkn-studio#134）。其中角色权限配置页需在资源目录里补上 `admin-role` 的 `permissions` 操作，否则该点位无法在界面上勾选配置。
- 对象授权页对接（bkn-studio#171）。
- 兼容期结束后收紧为单点校验。
