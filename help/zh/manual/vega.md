# 🗄️ VEGA 引擎

## 📖 概述

**VEGA** 提供跨异构数据源的**数据虚拟化**：**数据连接（Catalog）**、**资源发现**、**连接器类型**与**数据视图**（含原子视图与组合视图）。智能体与应用通过统一的类 SQL 访问面查询，而无需为每个数据源单独适配。

典型 Ingress 前缀：

| 前缀 | 作用 |
| --- | --- |
| `/api/vega-backend/v1` | VEGA 后台 — 连接、元数据、查询执行 |

**相关模块：** [BKN 引擎](bkn.md)、[Context Loader](context-loader.md)。

文末 **curl** 一节仅供需要 **自行拼 HTTP / 脚本里调 API** 时参考；只用 CLI 或语言 SDK 的读者可以不看。

---

## 💻 CLI

所有 `openbkn vega` 子命令支持的公共参数：`-bd` / `--biz-domain <s>`（默认取自 `openbkn config`）、`--pretty`（JSON 美化，默认开启）。完整列表见 `openbkn vega --help`。

### 平台可达性

CLI 没有单独的探活子命令，用一次带鉴权的 Catalog 列举即可判断服务是否可达与授权是否有效：

```bash
# 可达性探测：能返回列表即服务可达且 token 有效
openbkn vega catalog list --limit 1

# 各 Catalog 的连接健康状态
openbkn vega catalog health <catalog_id> [<catalog_id> ...]
```

vega-backend Pod 自身的 `GET /health` 不在 `/api/vega-backend/v1` 下，通常也不经 Ingress 暴露，排障时在集群内访问。

### Catalog 管理

```bash
# 列出 Catalog（可选过滤）
openbkn vega catalog list
openbkn vega catalog list --health-check-status healthy --limit 20

# 获取单个 Catalog
openbkn vega catalog get <catalog_id>

# 批量健康检查（多个 id 空格分隔）
openbkn vega catalog health cat_pg001 cat_mysql002

# 测试已注册 Catalog 的连接
openbkn vega catalog test-connection <catalog_id>

# 元数据发现；可选等待完成
openbkn vega catalog discover <catalog_id>
openbkn vega catalog discover <catalog_id> --wait

# Catalog 下的资源
openbkn vega catalog resources <catalog_id>
openbkn vega catalog resources <catalog_id> --category table --limit 30

# 创建 / 更新 / 删除 Catalog
openbkn vega catalog create \
  --name my-mysql \
  --connector-type mysql \
  --connector-config '{"host":"db.example.com","port":3306,"database":"mydb","username":"u","password":"p"}'

openbkn vega catalog update <catalog_id> --name new-name --connector-config '{"host":"..."}'

openbkn vega catalog delete <catalog_id>
```

### 资源管理

资源由 Catalog 发现产生，CLI 不提供手工创建与修改；抽样看数据用 `query` 并设小 `limit`。

`openbkn vega resource` 提供只读的三条（`list` / `get` / `query`）；顶层的 `openbkn resource`（别名 `res`）多两条：按名查找与删除。两者访问同一批资源。

```bash
# 列出资源（可选过滤）
openbkn vega resource list
openbkn vega resource list --catalog-id <catalog_id> --category table --limit 50

# 查看单个资源（含 schema_definition 字段清单与 index_config）
openbkn vega resource get <resource_id>

# 抽样查看数据行
openbkn vega resource query <resource_id> --limit 10 --need-total

# 按名称查找（模糊；--exact 精确匹配）
openbkn resource find --name "customer_orders" --catalog-id <catalog_id>

# 删除资源
openbkn resource delete <resource_id>
```

### 数据集（文档与构建）

针对 dataset 类资源，管理索引文档与异步构建任务：

**构建本地索引**（全文与/或向量），适用于 table 与 dataset 类资源。索引配置归属于
**资源**（`index_config` 给出构建键与默认分析器/模型，字段级 `features` 给出每个字段建哪种
索引），构建任务在创建时对其做快照。`dataset build` 一条命令完成两步：先 PUT 资源写配置，
再创建并启动构建任务。

```bash
openbkn vega dataset build <resource_id> --mode batch|streaming \
  [--execute-type full|incremental] \
  [--build-key-fields <列>[,<列>...]] \
  [--fulltext-fields <列>[,...]] [--fulltext-analyzer <分析器>] \
  [--embedding-fields <列>[,...]] [--embedding-model <模型名>] \
  [--wait] [--timeout <秒>]

openbkn vega dataset build-status <resource_id> <task_id>
openbkn vega dataset build-list [--resource-id <id>] [--catalog-id <id>] [--active]
openbkn vega dataset build-start <task_id> [--reset]
openbkn vega dataset build-stop <task_id>
openbkn vega dataset build-delete <task_id> [<task_id> ...]
```

- `--embedding-model` 传的是模型**名称**，传模型 ID 会被拒绝。
- 资源上没有 `index_config.build_key_fields` 时创建构建任务返回 400，因此首次为某个资源
  建索引必须带 `--build-key-fields`。
- 建索引会改变该资源的读取路径：表资源一旦有本地索引，Vega 就从索引读，只有在没有索引时
  才回源库实时查；源库的更新要到下次构建才可见。

文档级管理（dataset 资源）没有对应的 CLI 子命令，直接调 API：

```bash
openbkn call /api/vega-backend/v1/resources/<resource_id>/data -X POST -d '[{...}]'
openbkn call /api/vega-backend/v1/resources/<resource_id>/data -X PUT  -d '[{...}]'
openbkn call /api/vega-backend/v1/resources/<resource_id>/data/<doc_id> -X PUT -d '{...}'
openbkn call /api/vega-backend/v1/resources/<resource_id>/data/<doc_ids> -X DELETE
```

### 结构化查询与 SQL 查询（vega-backend）

以下两条命令都走 **`vega-backend`**，**不依赖** `vega-calculate-coordinator`（Trino）。适合在仅安装 BKN Foundry、已配置 MySQL/PostgreSQL Catalog 的场景下查数。

**结构化查询** — `POST /api/vega-backend/v1/resources/query`

CLI 没有对应的 typed 子命令，直接调接口：

```bash
openbkn call /api/vega-backend/v1/resources/query -X POST -d '<json>'
```

请求体要点：`tables`（必填，`resource_id` + 可选 `alias`）、`joins`（同 Catalog 内多表）、`output_fields`、`filter_condition`、`sort`、`offset` / `limit`（`limit` 最大 10000）、`need_total`。首页分页时 `query_id` 可不传；翻页需带上次返回的 `query_id`。JOIN 的 `on` 条件里 **`left_field` / `right_field` 须与 `openbkn vega resource get` 返回的 `schema_definition[].name` 一致**。**所有表必须属于同一 Catalog**，否则返回 501。

`filter_condition` 常用 `operation`：`==`/`eq`、`!=`/`not_eq`、`>`/`gt`、`>=`/`gte`、`<`/`lt`、`<=`/`lte`、`in`/`not_in`、`like`/`not_like`（仅当该字段在 schema 中为 string 类型）、`range`、`null`/`not_null`；逻辑组合用 `and`/`or` 嵌套 `sub_conditions`。叶子条件通常含 `field`、`operation`、`value`、`value_from`（常量填 `"const"`）。

单表示例：

```bash
openbkn call /api/vega-backend/v1/resources/query -X POST \
  -d '{"tables":[{"resource_id":"res_mysql_supplier"}],"limit":5,"need_total":true}'
```

两表 JOIN 示例（请替换为真实 `resource_id` 与字段名）：

```bash
openbkn call /api/vega-backend/v1/resources/query -X POST -d '{
  "tables": [
    {"resource_id":"res_a","alias":"a"},
    {"resource_id":"res_b","alias":"b"}
  ],
  "joins":[{"type":"inner","left_table_alias":"a","right_table_alias":"b","on":[{"left_field":"fk_id","right_field":"id"}]}],
  "output_fields":["a.name","b.amount"],
  "limit":10
}'
```

**直连 SQL** — `openbkn vega sql`

**简易模式**（不写 JSON body）：SQL 用 `--query` 传入（**请加引号**），方言用 `--input-dialect`。

```bash
openbkn vega sql --input-dialect mysql \
  --query "SELECT * FROM {{res_mysql_supplier}} LIMIT 5"
```

其余可选参数：`--limit` / `--offset` / `--need-total`、游标翻页的 `--paging-mode cursor` 与 `--cursor` / `--keep-alive-sec`、`--query-timeout-sec`。

**高级模式**：`-d` 传完整 JSON；一旦使用 `-d`，将忽略上述单项参数。

```bash
openbkn vega sql -d '<json>'
openbkn vega help sql
```

SQL 中可使用占位符 `{{.<资源ID>}}` 或 `{{<资源ID>}}`（资源 ID 为 Vega `resource_id`），后端替换为该资源的物理表标识。无占位符时也可写**原生 SQL**（仍需 `resource_type`），表名需符合目标库语法。

**三种查询方式对照**

| 方式 | 入口 | 典型用途 |
|------|------|----------|
| 结构化查询 | `openbkn call /api/vega-backend/v1/resources/query -X POST` | 同 Catalog 多表 JOIN、统一 filter DSL |
| 直连 SQL | `openbkn vega sql` | 复杂 SQL、聚合、占位符引用资源 |
| 单资源抽样 | `openbkn vega resource query <id> --limit N` | 单表快速看数 |

三者都只依赖 vega-backend。

TypeScript：直连 SQL 用 typed 方法 `bkn.vega.sql({...})`；结构化查询无 typed 方法，用 `bkn.call('/api/vega-backend/v1/resources/query', { method: 'POST', body })`。

### 连接器类型

CLI 只提供只读的两条；注册与启停连接器类型属于平台运维动作，走 `/api/vega-backend/v1/connector-types` 接口。

```bash
openbkn vega connector-type list
openbkn vega connector-type get postgresql
```

### 数据视图（Dataview）

**该能力已下线。** 数据视图由 mdl-uniquery / mdl-data-model 提供，这两个模块已不再发布，`openbkn dataview` 命令组在 CLI 0.1.2 中也只剩空壳、无任何子命令。自定义 SQL 依赖的 `vega-calculate-coordinator`（Trino/Hetu 系）同样不再随部署脚本安装。

现在的等价做法：

| 原用途 | 现行做法 |
|--------|----------|
| 浏览/查询视图数据 | 直接查资源：`openbkn vega resource query <resource_id> --limit N` |
| 视图内自定义 SQL | `openbkn vega sql --input-dialect <方言> --query "..."`，用 `{{<resource_id>}}` 占位符引用资源 |
| 跨表 JOIN | `openbkn call /api/vega-backend/v1/resources/query -X POST`，同一 Catalog 内多表 JOIN |

对象类若仍绑定在已废弃的 `data_view` 数据源上，查询会失败，需要改绑到 Vega 资源。

### 端到端流程

```bash
# 1. 探活与连接器
openbkn vega catalog list --limit 1
openbkn vega connector-type list

# 2. Catalog 健康与发现
openbkn vega catalog health cat_pg001
openbkn vega catalog discover cat_pg001 --wait
openbkn vega catalog resources cat_pg001 --category table

# 3. 看数
openbkn vega resource query res_orders_001 --limit 5 --need-total
openbkn vega sql --input-dialect mysql \
  --query "SELECT * FROM {{res_orders_001}} WHERE amount > 10000 ORDER BY amount DESC LIMIT 10"

# 4. 建检索索引（全文 + 向量）
openbkn vega dataset build res_orders_001 --mode batch --execute-type full \
  --build-key-fields id --fulltext-fields customer_name \
  --embedding-fields customer_name --embedding-model text-embedding-v4 --wait
```

---

## 📘 TypeScript SDK

`bkn.vega` 提供 Catalog、连接器类型、直连 SQL、构建任务的 typed 方法；资源浏览/查询在 `bkn.resource` 下。没有 typed 方法的端点（结构化查询 `/resources/query`、数据集文档写入）通过通用的 `bkn.call(...)` passthrough 访问。

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<访问地址>', token: process.env.BKN_TOKEN });

// Catalog（typed）
const catalogs = await bkn.vega.catalogs({ healthCheckStatus: 'healthy', limit: 20 });
catalogs.forEach((c) => console.log(c.id, c.name));

const detail = await bkn.vega.getCatalog('cat-001');
const healthStatus = await bkn.vega.catalogHealth(['cat-001', 'cat-002']);
await bkn.vega.discoverCatalog('cat-001', true); // 等待发现完成
const catRes = await bkn.vega.catalogResources('cat-001', 'table');

await bkn.vega.createCatalog({
  name: 'my-mysql',
  connector_type: 'mysql',
  connector_config: { host: 'db.example.com', port: 3306, database: 'mydb', username: 'u', password: 'p' },
});

// Catalog 的更新 / 删除无 typed 方法 —— 用 passthrough
await bkn.call('/api/vega-backend/v1/catalogs/cat-001', { method: 'PUT', body: { name: 'renamed' } });
await bkn.call('/api/vega-backend/v1/catalogs/cat-001', { method: 'DELETE' });

// 资源（typed：浏览 + 查询）
const resources = await bkn.resource.list({ catalogId: 'cat-001', category: 'table', limit: 50 });
const res = await bkn.resource.get('res-001');
const rows = await bkn.resource.query('res-001', { limit: 5 });

// 连接器类型（typed）
const connectors = await bkn.vega.connectorTypes();

// 直连 SQL（typed；实际打到 POST /resources/query）
const sqlOut = await bkn.vega.sql({
  query: 'SELECT * FROM {{res-001}} LIMIT 5',
  input_dialect: 'mysql',
});

// 结构化查询 —— 无 typed 方法，用 passthrough
const structured = await bkn.call('/api/vega-backend/v1/resources/query', {
  method: 'POST',
  body: { tables: [{ resource_id: 'res-001' }], limit: 5, need_total: true },
});

// 数据集构建任务（typed）
const build = await bkn.vega.build({ resource_id: 'res-ds', mode: 'batch' }, { wait: true });
const status = await bkn.vega.buildStatus(String(build.id));

```

---

## 🌐 curl

已 `openbkn auth login` 时，可将示例中的 **`Authorization: Bearer $(openbkn auth token)`** 用于受保护接口；将 **`https://<访问地址>`** 换为实际平台地址。

```bash
# 列举 Catalog 探活（能返回即服务可达、token 有效）
curl -sk "https://<访问地址>/api/vega-backend/v1/catalogs?limit=1" \
  -H "Authorization: Bearer $(openbkn auth token)" \
  -H "x-business-domain: bd_public"

# 可选：直连 vega-backend Pod 的 /health（不在 /v1 下）
# curl -sk "https://<访问地址>/health" -H "Authorization: Bearer $(openbkn auth token)"

curl -sk "https://<访问地址>/api/vega-backend/v1/catalogs?health_check_status=healthy&limit=20" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"
curl -sk "https://<访问地址>/api/vega-backend/v1/catalogs/cat_pg001" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"

curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/catalogs" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"name":"my","connector_type":"mysql","connector_config":{"host":"h","port":3306,"database":"d","username":"u","password":"p"}}'
curl -sk -X PUT "https://<访问地址>/api/vega-backend/v1/catalogs/cat_pg001" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"name":"new-name"}'
curl -sk -X DELETE "https://<访问地址>/api/vega-backend/v1/catalogs/cat_pg001" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"

curl -sk "https://<访问地址>/api/vega-backend/v1/catalogs/cat_pg001/health-status" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/catalogs/cat_pg001/test-connection" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/catalogs/cat_pg001/discover" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"
curl -sk "https://<访问地址>/api/vega-backend/v1/catalogs/cat_pg001/resources?category=table&limit=30" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"

curl -sk "https://<访问地址>/api/vega-backend/v1/resources?catalog_id=cat_pg001&limit=50" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/resources" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"catalog_id":"cat_pg001","name":"t","category":"table"}'
curl -sk -X PUT "https://<访问地址>/api/vega-backend/v1/resources/res_orders_001" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"status":"active"}'
curl -sk -X DELETE "https://<访问地址>/api/vega-backend/v1/resources/res_orders_001" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"

curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/resources/res_orders_001/data" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -H "x-http-method-override: GET" \
  -d '{"limit":10,"offset":0,"need_total":true}'

# Dataset 文档写入（使用 POST 覆盖）
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/resources/res-ds/data" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -H "x-http-method-override: POST" \
  -d '[{"id":"doc1","content":"..."}]'

# Dataset 构建任务
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/build-tasks" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"resource_id":"res-ds","mode":"full"}'
curl -sk "https://<访问地址>/api/vega-backend/v1/build-tasks/<task-id>" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"

curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/resources/query" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"tables":[{"resource_id":"res_orders_001"}],"limit":5,"need_total":true}'
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/resources/query" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"resource_type":"mysql","query":"SELECT 1 AS one"}'

curl -sk "https://<访问地址>/api/vega-backend/v1/connector-types" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"
curl -sk "https://<访问地址>/api/vega-backend/v1/connector-types/mysql" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/connector-types/mysql/enabled" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"enabled":true}'
```

Dataview 的 HTTP 路径原由 **mdl-uniquery** / **mdl-data-model** 提供，这两个模块已不再发布，相关接口在当前部署中不可用；改用 Vega 资源与 `openbkn vega sql`。

更多说明见 npm 包 `@openbkn/bkn-sdk` 以及 `openbkn vega --help`。
