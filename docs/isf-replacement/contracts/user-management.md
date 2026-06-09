# ISF user-management —— 调用侧契约冻结（Phase 0 §3）

> 日期：2026-06-03　新服务：**bkn-safe**（代码 `safe`）
> 口径：**调用侧**（Kowell 应用实际 send/unmarshal 的字段），非 ISF handler 暴露的全集。
> bkn-safe 必须满足这些形状，应用零改。base path = `/api/user-management`。

调用方（client，各服务自带，无共享 lib）：
- **bkn** — `adp/bkn/bkn-backend/server/drivenadapters/user_mgmt/user_mgmt_access.go`
- **vega** — `adp/vega/vega-backend/server/drivenadapters/user_mgmt/user_mgmt_access.go`
- **flow** — `adp/dataflow/flow-automation/drivenadapters/user_management.go`（+ `mock-server/main.go` 金参照）
- **agent-um** — DA `src/infra/cmp/umcmp/*`
- **agent-acc** — DA `src/drivenadapter/httpaccess/usermanagementacc/user_management.go`

## 两条贯穿全局的承重约束（DoD 必须满足）
1. **`/v1/names`、`/v2/names`、`/v1/group-members`、`/v1/search-org` 是 POST-by-body 的"GET"**：body 带 `"method":"GET"`，按 ID-type 数组分键。`/v1/group-members` 是 POST（虽语义 GET）。
2. **未知 ID 的处理 v1/v2 相反，且承重**：
   - `/v1/names`（及 `/v1/users` 多 ID 路径）：未知 ID 必须**报错**，envelope = `{code, detail:{ids:[...]}}`；调用方据 code 剔除这些 ID 重试。错误码：`400019001` user / `400019002` dept / `400019003` group / `400019004` contactor 未找到；DA `/v1/users` 路径认 `404019001`(`UmNotFound`)。
   - `/v2/names`（`strict:false`）：未知 ID **不报错**，返 200 直接省略（客户端缺省名填 `"-"`）。
   - bkn-safe 两套行为都要精确实现，别统一。

---

## 1. `GET /v1/users/{user_ids}/{fields}`
callers: flow（单 ID）、agent-acc（逗号拼多 ID+fields）、agent-um。
**请求**：path：`{user_ids}`=逗号拼 ID，`{fields}`=逗号拼字段名。无 body/query。
- flow 固定要：`name,parent_deps,csf_level,roles,email,telephone,enabled,custom_attr`（单 ID）
- agent-um 字段表：`name,parent_deps,enabled,roles,account,groups`
- agent-acc：传入的 fields

**响应 — 裸 JSON 数组**（无 envelope），每用户一对象，按 `id` 键回（返回 `id` 必须等于请求 ID）：
```json
[{ "id": "string",
   "name": "string",
   "parent_deps": [[{"id":"string","name":"string","type":"string"}]],
   "enabled": true,
   "roles": ["string"],
   "account": "string",
   "groups": [{"id":"string","name":"string","notes":"string"}],
   "csf_level": 1,
   "telephone": "string",
   "email": "string",
   "custom_attr": {"is_knowledge": "1"} }]
```
**类型承重**：`parent_deps` = **list of lists**（每条内 list = 根→直接父部门链；flow 取每链最后元素 id）；`csf_level` = **JSON number(float64)**，非字符串；`enabled` = bool；`custom_attr.is_knowledge` 按 `fmt("%v")=="1"` 判。
**not-found**：多 ID 路径未知 ID 报 `404019001 + detail.ids`，agent-um 剔除重试（≤3 轮），全剔则空 map 成功。

## 2. `GET /v1/users/{user_id}/accessor_ids`
caller: flow。path 末段字面 `accessor_ids`。**响应 — 裸字符串数组** `["id1","id2"]`。

## 3. `POST /v1/apps`
caller: flow（注册内部账户）。**请求 body**（全 required）：`{"name":string,"type":"internal","password":string}`（type 硬编码 internal）。
**响应**（200/201）裸对象：`{"id":string}`（flow 只读 id）。
**承重**：**HTTP 409** 时 flow 读 `detail.id`（已存在 app 的 id）当成功返回 → 冲突 envelope 必须 `{..,"detail":{"id":<existing>}}`。

## 4. `GET /v1/apps/{app_id}`
callers: flow（QueryInternalAccount 读 name / GetAppAccountInfo / IsApp）。**响应**裸对象 `{"id":string,"name":string}`。
**承重**：**HTTP 404** 有意义 → 返 `("",nil)` / `(false,nil)`，非 404 错误才上抛。

## 5. `POST /v1/names`
callers: flow、agent-um。**请求 body**：`method` 恒 `"GET"`，按需带任意子集：
```json
{"method":"GET","user_ids":[],"department_ids":[],"contactor_ids":[],"group_ids":[],"app_ids":[]}
```
**响应 — 裸对象**，5 条并行 name 数组（envelope 即对象本身，无 wrapper）：
```json
{ "user_names":[{"id":"string","name":"string"}],
  "group_names":[...], "department_names":[...], "contactor_names":[...], "app_names":[...] }
```
每元素 `{id,name}`。未请求的 type 数组可空 `[]`。
**承重**：未知 ID 报 `{code, detail:{ids:[...]}}`，code∈`400019001/2/3/4`，调用方剔除对应 ID 后重试（flow / agent-um ≤10 轮）。

## 6. `POST /v2/names`
callers: bkn、vega（GetAccountNames，代码一致）。**请求 body**（四键恒发）：
```json
{"method":"GET","user_ids":[],"app_ids":[],"strict":false}
```
**响应 — 裸对象**，调用方只读两数组（其余忽略）：`{"app_names":[{id,name}],"user_names":[{id,name}]}`。
**承重**：要求 **HTTP 200 精确**（≠200 即错）；`strict:false` 下未知 ID **不报错**、省略即可（客户端填 `"-"`）。与 §5 相反。

## 7. `GET /v1/emails`
caller: flow。**请求**：query 重复参数（非逗号拼）：`?user_id=a&user_id=b` 或 `?department_id=a&...`。
**响应 — 裸对象**：user 查询 → `{"user_emails":[{"email":string,...}]}`；dept 查询 → `{"department_emails":[{"email":string,...}]}`。flow 只读 `email`，空串过滤；元素可带额外字段。

## 8. `GET /v1/departments?level={int}`
caller: flow（GetDepartments）。**请求** query `level`（int，required，如 0）。
**响应 — 裸数组** `[{"id":string,"name":string,"type":string}]`。

## 9. `GET /v1/departments/{department_id}/...`（子路径）
- **9a `/name,parent_deps`**：响应**裸数组**取 `[0]`：`[{"department_id":string,"name":string,"parent_deps":[{"id","name","type"}]}]`。
- **9b `/member_ids`**：响应裸对象 `{"user_ids":[],"department_ids":[]}`。

## 10. `POST /v1/internal-groups`
caller: flow（建组）。body `{}`（空对象）。响应裸对象 `{"id":string}`。

## 11. `DELETE /v1/internal-groups/{ids}`
caller: flow。path `{ids}`=逗号拼。无 body，响应体不读（看 status）。

## 12. `/v1/internal-group-members/{group_id}`（GET + PUT）
- **12a PUT**：body **裸数组** `[{"id":string,"type":"user"}]`（type 恒 user）。响应体不读。
- **12b GET**：响应**裸数组** `[{"id":string,...}]`，flow 读每元素 `id`。

## 13. `POST /v1/group-members`
caller: flow（GetGroupUserList）。HTTP **POST**。body `{"method":"GET","group_ids":[]}`。
响应裸对象 `{"user_ids":[]}`。

## 14. `POST /v1/search-org`
caller: agent-um（SearchOrg）。body（数组规整为 `[]` 不为 null）：
```json
{"method":"GET","user_ids":[],"department_ids":[],"scope":[]}
```
`scope` = 界定组织子树的部门 ID 数组。**响应**裸对象 `{"user_ids":[],"department_ids":[]}`（输入 ID 中落在 scope 内的子集）。要求 2xx；此端点无逐 ID not-found 重试。

---

## 覆盖 / 缺口
- **`GET /v1/apps`（列表，无 ID）：无调用方** —— 只用到 `POST /v1/apps`（建）+ `GET /v1/apps/{id}`（单查）。裸列表不冻结。
- 解码器：bkn/vega 用 bytedance/sonic；flow 用 `interface{}`（容忍多余字段，但严格依赖上述**类型**，尤其 `csf_level` number、`enabled` bool）；agent-um/agent-acc 用 typed struct。

## DoD（对 spec §3）
- [x] 13 端点调用侧 req/resp 字段级 schema 冻结（含 envelope、错误码、POST-by-body、v1/v2 not-found 分歧、parent_deps 套娃、csf_level number 等承重点）
- [x] 标注读/写（读为主：1/2/4/5/6/7/8/9/12b/13/14；写：3/10/11/12a；create+single-fetch 覆盖 apps）
- [ ] 写端点（apps/internal-groups/group-members）的成功响应 golden（实测或 mock-server 取，bkn-safe MVP 时补）
