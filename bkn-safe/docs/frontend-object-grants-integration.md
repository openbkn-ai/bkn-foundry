# BKN Studio「授权管理」对接 bkn-safe 后端 — 前端改造说明

> 背景：BKN Studio 的授权页（`js/data-authz.js` / `js/page-authorizations.js` /
> `js/page-authz-drawer.js`）目前是**纯前端 mock**，权限点词表用的是旧 ISF 的
> （`vega:catalog:*` / `model:invoke`…），调用的 `POST /authorization/v1/object-grants`
> 也是虚构接口。后端已在 bkn-safe 落地真实的**对象级授权**接口，下面是对接说明。

## 一、核心模型（和 mock 一致）

把**一个具体资源实例**（某个 catalog / 某个模型 / 某个算子…）的若干操作权限，授予**一个用户**。
建立在角色 RBAC 之上。**被授权方只支持「用户」**——部门已从后端移除（casbin 无 user→部门成员
规则，授给部门是死策略）。请前端把 grantee 的「部门」选项去掉，只保留用户。

## 二、真实接口（替换 mock 的 `/authorization/v1/object-grants`）

全部在 **`/api/safe/v1/admin/object-grants`**，需 **管理员 Bearer Token**（`Authorization: Bearer <token>`），
非管理员 403。

### 1. 列出授权（总览页 / 按对象 / 按成员）
```
GET /api/safe/v1/admin/object-grants?accessor_id=&resource_type=&resource_id=
```
三个 query 都是可选过滤器（都不传=全量）。
- 总览页：不带参
- 「按对象」聚合：前端拿全量自己按 resource 分组，或带 `resource_type`+`resource_id`
- 「按成员」聚合：带 `accessor_id`

响应：
```json
{ "entries": [
  { "accessor_id": "u-123", "resource": { "type": "catalog", "id": "d7ni..." }, "operations": ["view_detail","modify"] }
] }
```
> 只返回「用户」对「具体实例」的授权；角色授权、类型级 `*` 通配不在此列。

### 2. 新建 / 修改授权（set 语义，替换该用户在该对象上的整套权限点）
```
POST /api/safe/v1/admin/object-grants
{ "accessor_id": "u-123", "resource": { "type": "catalog", "id": "d7ni..." }, "operations": ["view_detail","modify"] }
```
- 成功 `204`。**整套替换**：传什么就是什么（不是增量），勾掉的会被移除。
- `operations` 不能为空（清空请用 DELETE）；`resource.id` 必须是具体值，不能传 `*`。
- 后端校验：`accessor_id` 必须是已知用户；`operations` 必须是该 type 在词表里登记的操作，否则 400。

### 3. 撤销某用户在某对象上的授权
```
DELETE /api/safe/v1/admin/object-grants
{ "accessor_id": "u-123", "resource": { "type": "catalog", "id": "d7ni..." } }
```
成功 `204`，只撤这一个用户，不影响该对象上其他人的授权。

## 三、权限点词表（**换掉 ISF 词表，用这套**）

bkn-safe 的操作词表（`type` → `operations`），按这套渲染权限点 chip / 矩阵列：

| type（resource.type） | 中文 | operations |
|---|---|---|
| `knowledge_network` | 知识网络 | `view_detail` `create` `modify` `delete` `data_query` `authorize` `task_manage` |
| `catalog` | 数据目录 | `view_detail` `create` `modify` `delete` `authorize` `task_manage` |
| `resource` | 数据资源 | `view_detail` `create` `modify` `delete` `authorize` `task_manage` |
| `connector_type` | 连接器类型 | `view_detail` `create` `modify` `delete` `authorize` `task_manage` |
| `stream_data_pipeline` | 流式数据管道 | `view_detail` `create` `modify` `delete` `authorize` `data_query` |
| `operator` | 算子 | `create` `modify` `delete` `view` `publish` `unpublish` `authorize` `public_access` `execute` |
| `tool_box` | 工具箱 | 同 operator |
| `mcp` | MCP | 同 operator |
| `skill` | Skill | 同 operator |
| `small_model` | 小模型 | `create` `display` `modify` `delete` `execute` |
| `large_model` | 大模型 | `create` `display` `modify` `delete` `execute` |
| `agent` / `agent_tpl` | 智能体/模板 | （见后端 catalog.json，授权页一般不涉及） |

> 注意：授权页面授给用户某对象时，通常只给「实例级」操作（如 `view_detail/modify/delete/execute/display`），
> `create` 是「类型级」语义（在具体实例上没意义），按需从对象授权 UI 里隐藏 `create`。

## 四、设计 `OBJ_TYPES` 需要调整

| mock 里的 type | 改成后端 type | 说明 |
|---|---|---|
| `kn` | `knowledge_network` | 知识网络 |
| `catalog` | `catalog` | 不变 |
| `resource` | `resource` | 不变 |
| `model` | `small_model` / `large_model` | **拆成两类**：小模型走 `small_model`，大模型（LLM）走 `large_model` |
| （缺） | `operator` | **新增「算子」对象类型**（后端已支持） |

## 五、配套接口（前端构建 UI 用）

- **被授权方（用户）选择器**：用现有管理 API 列用户
  `GET /api/safe/v1/admin/users`（搜索/分页，返回用户 id/name/account）。
- **accessor_id → 用户名** 回显：`GET /api/safe/v1/directory/...` 或上面的 users 列表本地映射。
- **要授权的「对象实例」从哪来**：bkn-safe **不存资源名**（它对资源 id 不透明）。
  对象列表（某个 catalog/模型/算子的 id+名字）请前端**从各自的领域服务**取：
  catalog/resource 走 vega，模型走 mf-model，算子走执行工厂。bkn-safe 只认 `type:id`。
- **「授给某用户整类 / 一切」**：对象级接口**不支持** `*` 通配。需要「整类」或「全部」时，走**角色**：
  `POST /api/safe/v1/admin/roles/:id/permissions`（`resource.id` 传 `*` = 整类）。
  即设计里的「或者一切」应落到角色管理页，不在对象授权抽屉里。

## 六、要点清单（给前端的 TODO）

1. 删掉部门 grantee，只留用户。
2. 接口换成 `/api/safe/v1/admin/object-grants`（GET/POST/DELETE），带管理员 Bearer Token。
3. 权限点词表换成上面第三节这套（丢弃 `vega:* / model:* / ontology-manager:*`）。
4. `OBJ_TYPES`：`kn→knowledge_network`、`model` 拆 `small_model`/`large_model`、新增 `operator`。
5. POST 是「整套替换」语义，前端 set 好完整 operations 再提交；空集合走 DELETE。
6. 对象实例列表从领域服务取，bkn-safe 不提供资源名。
7. 「一切/整类」授权走角色页（`roles/:id/permissions`，id=`*`），不在对象抽屉。
8. `src/api/admin.ts` 里打 `/api/authorization/v1` + ISFWeb thrift 的那套是旧 ISF，整体迁到 `/api/safe/v1/*`。

## 七、对象 id → 名称解析（各领域服务，bkn-safe 不提供）

bkn-safe 只存 `type:id`、对资源名不透明。授权页两处需要把 id 换成可读名称，**由各领域服务提供**：
- **新建授权 · 对象选择器** → 用「列实例」接口（按类型列 id+name，可搜索分页）
- **授权列表/分组 · 回显对象名** → 用「按 id 批量取名」接口

### 7.1 列实例（id+name，搜索+分页）— 全部已就绪

| type | 接口 |
|---|---|
| `catalog` | `GET /api/vega-backend/v1/catalogs?name=&offset=&limit=&sort=` |
| `resource` | `GET /api/vega-backend/v1/resources?name=&catalog_id=&offset=&limit=` |
| `small_model` | `GET /api/mf-model-manager/v1/small-model/list?model_name=&page=&size=` |
| `large_model` | `GET /api/mf-model-manager/v1/llm/list?name=&page=&size=` |
| `operator` | `GET /api/agent-operator-integration/v1/operator/info/list?name=&page=&page_size=` |
| `tool_box` | `GET /api/agent-operator-integration/v1/tool-box/list?name=&page=&page_size=` |
| `mcp` | `GET /api/agent-operator-integration/v1/mcp/list?name=&page=&page_size=` |
| `skill` | `GET /api/agent-operator-integration/v1/skills?name=&page=&page_size=` |
| `knowledge_network` | `GET /api/bkn-backend/v1/knowledge-networks?name_pattern=&offset=&limit=` |

> 各接口响应里的 id/name 字段名不统一（vega/kn=`id`/`name`、operator=`operator_id`/`name`、toolbox=`box_id`/`box_name`、skill=`skill_id`/`name`、mcp=`mcp_id`/`name`、模型=`model_id`/`model_name`）。前端按类型映射成统一的 `{id, name}`。

### 7.2 按 id 批量取名 — 统一契约

**新增的 6 个接口统一契约**（catalog/resource/mcp 是各自已有的旧接口，形态不同，见下表备注）：
```
POST <上面对应服务前缀>/<resource>/names
请求: {"ids": ["id1","id2", ...]}
响应: {"entries": [{"id":"id1","name":"name1"}, ...]}   // 缺失 id 略过, 空 ids 返回 {"entries":[]}
```

| type | 批量取名接口 | 形态 |
|---|---|---|
| `small_model` | `POST /api/mf-model-manager/v1/small-model/names` | ✅ 统一契约（新）|
| `large_model` | `POST /api/mf-model-manager/v1/llm/names` | ✅ 统一契约（新）|
| `operator` | `POST /api/agent-operator-integration/v1/operator/names` | ✅ 统一契约（新）|
| `tool_box` | `POST /api/agent-operator-integration/v1/tool-box/names` | ✅ 统一契约（新）|
| `skill` | `POST /api/agent-operator-integration/v1/skills/names` | ✅ 统一契约（新）|
| `knowledge_network` | `POST /api/bkn-backend/v1/knowledge-networks/names` | ✅ 统一契约（新）|
| `catalog` | `GET /api/vega-backend/v1/catalogs/id1,id2,id3` | ⚠️ 旧接口：逗号分隔 path、返回完整对象、**任一 id 不存在整批 404**（取名前先确保 id 都有效，或退化为单个）|
| `resource` | `GET /api/vega-backend/v1/resources/id1,id2,id3` | ⚠️ 同上 |
| `mcp` | `GET /api/agent-operator-integration/v1/mcp/market/batch/{mcp_ids}/{fields}` | ⚠️ 旧接口：逗号分隔 path + 选字段，如 `.../batch/id1,id2/mcp_id,name` |

> 批量取名为**低敏只读、不做对象级授权拦截**——授权列表里可能有「当前管理员无权但被授权对象引用」的对象，也要能回显名称。
> id 一律 string（雪花/slug，无精度问题；`llm/add` 返回 id 已修为 string）。
