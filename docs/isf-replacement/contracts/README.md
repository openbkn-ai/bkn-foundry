# ISF 替换 —— 冻结契约 artifact（Phase 0）

> 日期：2026-06-03　分支：`feat/isf-replacement`
> 新服务：**bkn-safe**（代码内 `safe`）
> Spec：[`../isf-contract-freeze-2026-05-25.md`](../isf-contract-freeze-2026-05-25.md)

本目录把 ISF 对外契约的**权威源**固化进 repo（ISF 外部源会消失，必须 in-repo）。
bkn-safe + hydra 必须满足这些 artifact，作 contract test 与上线影子比对的基准。

## 溯源
- `authorization/*.json`、`role.json`：从 ISF `Authorization/driveradapters/`（jsonschema + init_data）逐字拷贝。
- ISF 源：`/Users/cx/Work/kweaver-ai/isf` @ commit `00c4a5d`。

## 内容

### `role.json` — 角色 seed（UUID 保号，9 角色）
6 system + 3 business。business 三角色 UUID 被 DA 等硬编码引用，**bkn-safe seed 必须沿用同 UUID**：
- 数据管理员 `00990824-4bf7-11f0-...`
- AI管理员 `3fb94948-5169-11f0-...`
- 应用管理员 `1572fb82-526f-11f0-bde6-e674ec8dde71`

### `authorization/` — authz 端点请求 JSON Schema（ISF 自带校验文件，权威）
| 文件 | 端点 | 关键约束 |
|---|---|---|
| `check.json` | operation-check（内网） | required: accessor/resource/operation/method；accessor.type enum=**user,app**；method enum=**GET**；含 `include` enum=`operation_obligations`、resource.ancestors/created_by 可选 |
| `check_public.json` | operation-check（public） | **无 accessor**（public 从 token 取主体）；required: resource/operation/method |
| `resource_operation.json` | resource-operation（内网） | required: accessor/resources/method（**operation 非 required**，**schema 无 allow_operation 字段**）；method enum=GET |
| `resource_operation_public.json` | resource-operation（public） | 无 accessor；required: resources/method |
| `resource_type_operation_public.json` | resource-type-operation（public） | required: resource_types[]/method |
| `resource_filter.json` | resource-filter（**仅内网**） | required: accessor/resources/operation/method；含 `allow_operation`(bool,default false)、`include` |
| `resource_list.json` | resource-list（**仅内网**） | required: accessor/resource/operation/method；resource 仅 required type |
| `policy.json` | POST /policy（建策略，**数组**） | item required: accessor/resource/operation；accessor.type enum=**user,department,group,role,app**（宽）；resource required id/name/type；operation required **allow+deny**；顶层 condition/expires_at/ancestors 可选 |
| `policy_delete.json` | POST /policy-delete | required: method/resources；method enum=**DELETE** |
| `modify_policy.json` | PUT /policy/:ids（改策略，数组） | item required operation(allow+deny) |
| `resource_ancestors_modify.json` / `resource_name_modify.json` | 资源元数据改 | — |

### `introspect/` — hydra `/admin/oauth2/introspect` 响应 golden（最硬契约）
lib `kweaver-go-lib/hydra@v1.0.5` `Introspect()` 用**无 nil 检查的类型断言**解析响应，缺字段直接 **panic**。bkn-safe 作 consent provider 注入的 session/ext 必须让 introspect 返回满足下列约束：

| fixture | 主体 | 约束（来自 lib 源码逐字核实） |
|---|---|---|
| `user.json` | 实名用户 | `active`(bool) 必有；`sub`≠`client_id`；`ext.{visitor_type,login_ip,udid,account_type,client_type}` **5 个全 string，缺一 panic** |
| `app.json` | 应用（client_credentials） | `sub`==`client_id` → 走 app 分支，`ext.*` 可缺 |
| `anonymous.json` | 匿名 | `ext.visitor_type`="anonymous"，其余 ext 字段不读；ClientTyp 默认 web |
| `inactive.json` | 失效 token | `active`=false 直接返回 |

枚举取值（lib 定义）：visitor_type∈realname/user/anonymous/app；account_type∈other/id_card；client_type∈windows/ios/android/harmony/mac_os/web/mobile_web/linux/office_plugin/console_web/deploy_web/unknown/app。

> DA 用 `kweaver-go-lib/rest.Hydra`（异源早期抽象），与 adp 的 `hydra` 包**两套 introspect 客户端**，最终都打同一 hydra → contract test 需分别覆盖。

## 与 spec 的漂移更正（2026-06-03 对源核实）
1. **method enum 锁 GET**：所有 policy_calc 端点 method enum=`GET`（policy-delete=`DELETE`），非 spec 描述的「任意被代理 HTTP 方法」。
2. **resource-operation 的 operation 非 required**，且 schema **无 `allow_operation`**（实测请求带 allow_operation，但校验 schema 不含 → 透传/被忽略）。
3. **operation-check accessor.type enum 仅 user/app**；只有 `policy.json`（写策略）才是 user/department/group/role/app 宽 enum。
4. **public 端点无 accessor**（从 token 取主体），与内网端点请求结构不同 → bkn-safe 内外网两组路由请求体不同构。

## DoD 状态（对 spec §8）
- [x] §1 introspect 约束对 lib 源码逐字核实 + golden fixture（user/app/anonymous/inactive）
- [x] §2 authz 端点权威 JSON Schema 全量冻结进 repo（含 public/private 分组、policy 双形态）
- [x] §5 角色 UUID seed 冻结（role.json，9 角色对齐 spec §5）
- [ ] §1 可执行 contract test（喂 golden 过真实 lib，断言不 panic）— 待 bkn-safe Go module 落地
- [ ] §2 policy/policy-delete 写端点 golden 响应 — 待隔离环境抓或从 ISF 源补
- [ ] §3 user-management 13 端点字段级 schema — 待抓真实流量/ISF UserManagement 源
- [ ] §4 Casbin model 用 golden 驱动验等价
