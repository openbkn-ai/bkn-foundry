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

### 平台健康与统计

```bash
# 可达性探测（Node CLI：带鉴权 GET .../catalogs?limit=1）
openbkn vega health

# Catalog 数量（最多列举 100 个 Catalog 后计数）
openbkn vega stats

# 健康探测 JSON + catalog_count（同样最多列举 100 个 Catalog）
openbkn vega inspect
```

CLI 不会请求 vega-backend Pod 的 `GET /health`，而是用已授权的 **catalogs 列表** 做探活。

### Catalog 管理

```bash
# 列出 Catalog（可选过滤）
openbkn vega catalog list
openbkn vega catalog list --status healthy --limit 20

# 获取单个 Catalog
openbkn vega catalog get <catalog_id>

# 批量健康检查，或检查全部
openbkn vega catalog health cat_pg001 cat_mysql002
openbkn vega catalog health --all

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

openbkn vega catalog delete <catalog_id> [<catalog_id> ...]   # 默认确认，加 -y 跳过
openbkn vega catalog delete cat_a,cat_b -y
```

### 资源管理

CLI **没有** `openbkn vega resource preview`。请用 **`resource query`** 并设置较小 `limit` 抽样查看数据。

```bash
# 列出资源（可选过滤）
openbkn vega resource list
openbkn vega resource list --catalog-id <catalog_id> --category table --limit 50

# 全量列举（GET .../resources/list）
openbkn vega resource list-all [--limit N] [--offset N]

openbkn vega resource get <resource_id>

# 结构化数据查询（POST .../resources/:id/data）
openbkn vega resource query <resource_id> \
  -d '{"limit":10,"offset":0,"need_total":true}'

# 创建 / 更新 / 删除资源
openbkn vega resource create \
  --catalog-id <catalog_id> \
  --name my_table \
  --category table \
  [--source-identifier <si>] [--database <db>] [-d '{"extra":"fields"}']

openbkn vega resource update <resource_id> [--name X] [--status X] [--tags t1,t2] [-d '{"k":"v"}']

openbkn vega resource delete <resource_id> [<resource_id> ...] [-y]
```

### 数据集（文档与构建）

针对 dataset 类资源，管理索引文档与异步构建任务：

```bash
openbkn vega dataset create-docs <resource_id> -d '[{"id":"doc1",...},...]'
openbkn vega dataset update-docs <resource_id> -d '[{"id":"doc1",...},...]'
openbkn vega dataset delete-docs <resource_id> <doc_id> [<doc_id> ...]
openbkn vega dataset delete-docs-query <resource_id> -d '{"filter":...}'

openbkn vega dataset build <resource_id> [--mode full|incremental|realtime]
openbkn vega dataset build-status <resource_id> <task_id>
```

### 结构化查询与 SQL 查询（vega-backend）

以下两条命令都走 **`vega-backend`**，**不依赖** `vega-calculate-coordinator`（Trino）。适合在仅安装 BKN Foundry、已配置 MySQL/PostgreSQL Catalog 的场景下查数。

**结构化查询** — `POST /api/vega-backend/v1/query/execute`

```bash
openbkn vega query execute -d '<json>'
```

请求体要点：`tables`（必填，`resource_id` + 可选 `alias`）、`joins`（同 Catalog 内多表）、`output_fields`、`filter_condition`、`sort`、`offset` / `limit`（`limit` 最大 10000）、`need_total`。首页分页时 `query_id` 可不传；翻页需带上次返回的 `query_id`。JOIN 的 `on` 条件里 **`left_field` / `right_field` 须与 `openbkn vega resource get` 返回的 `schema_definition[].name` 一致**。**所有表必须属于同一 Catalog**，否则返回 501。

`filter_condition` 常用 `operation`：`==`/`eq`、`!=`/`not_eq`、`>`/`gt`、`>=`/`gte`、`<`/`lt`、`<=`/`lte`、`in`/`not_in`、`like`/`not_like`（仅当该字段在 schema 中为 string 类型）、`range`、`null`/`not_null`；逻辑组合用 `and`/`or` 嵌套 `sub_conditions`。叶子条件通常含 `field`、`operation`、`value`、`value_from`（常量填 `"const"`）。

单表示例：

```bash
openbkn vega query execute -d '{"tables":[{"resource_id":"res_mysql_supplier"}],"limit":5,"need_total":true}'
```

两表 JOIN 示例（请替换为真实 `resource_id` 与字段名）：

```bash
openbkn vega query execute -d '{
  "tables": [
    {"resource_id":"res_a","alias":"a"},
    {"resource_id":"res_b","alias":"b"}
  ],
  "joins":[{"type":"inner","left_table_alias":"a","right_table_alias":"b","on":[{"left_field":"fk_id","right_field":"id"}]}],
  "output_fields":["a.name","b.amount"],
  "limit":10
}'
```

**直连 SQL** — `POST /api/vega-backend/v1/resources/query`

**简易模式**（不写 JSON body）：用两个参数分别传入引擎类型与 SQL（**请给 SQL 加引号**）。

```bash
openbkn vega sql --resource-type mysql --query "SELECT * FROM {{.res_mysql_supplier}} LIMIT 5"
```

**高级模式**：完整 JSON；一旦使用 `-d`，将忽略 `--resource-type` / `--query`。

```bash
openbkn vega sql -d '<json>'
openbkn vega sql --help
```

请求体必填：`query`（SQL 字符串，或 OpenSearch 的 DSL 对象）、`resource_type`（`mysql` | `mariadb` | `postgresql` | `opensearch`）。可选：`stream_size`（100–10000）、`query_timeout`（秒，1–3600）、`query_id`。

SQL 中可使用占位符 `{{.<资源ID>}}` 或 `{{<资源ID>}}`（资源 ID 为 Vega `resource_id`），后端替换为该资源的物理表标识。无占位符时也可写**原生 SQL**（仍需 `resource_type`），表名需符合目标库语法。

**三种查询方式对照**

| 方式 | 入口 | 依赖 | 典型用途 |
|------|------|------|----------|
| 结构化查询 | `openbkn vega query execute` | vega-backend | 同 Catalog 多表 JOIN、统一 filter DSL |
| 直连 SQL | `openbkn vega sql` | vega-backend | 复杂 SQL、聚合、占位符引用资源 |
| 单资源数据 API | `openbkn vega resource query <id> -d {...}` | vega-backend | 单表过滤、sort、`search_after` 分页 |
| Dataview + `--sql` | `openbkn dataview query ... --sql` | mdl-uniquery + **Trino**（Etrino） | 跨源/复杂 SQL 经计算集群（需单独安装 Etrino） |

TypeScript：`client.vega.executeQuery(jsonString)`、`client.vega.sqlQuery(jsonString)`。

### 连接器类型

```bash
openbkn vega connector-type list
openbkn vega connector-type get postgresql

openbkn vega connector-type register -d '{"name":"custom",...}'
openbkn vega connector-type update <type> -d '{...}'
openbkn vega connector-type delete <type> [-y]
openbkn vega connector-type enable <type> --enabled true
```

### 数据视图（Dataview）

数据视图由 **mdl-uniquery** 等模块提供，请使用 **`dataview`** 命令组（不走 `vega` 子命令）：

```bash
openbkn dataview list
openbkn dataview find --name "客户订单视图" --exact --wait
openbkn dataview get dv_001
# --sql 中 FROM 须为全限定名：请用 get 返回的 meta_table_name；下例 mysql_demo."sales"."customer_orders" 仅为格式示意
openbkn dataview query dv_001 \
  --sql "SELECT customer_name, order_count FROM mysql_demo.\"sales\".\"customer_orders\" WHERE region = '华东' LIMIT 20"
```

**自定义 SQL（`--sql`）与 Etrino**：不带 `--sql` 时，`dataview query` 使用视图内建定义，走直连数据源；`--sql` 会经 `vega-gateway-pro` 调用 **`vega-calculate-coordinator`**（Hetu/Presto 系引擎），该组件不在 BKN Foundry 默认清单中，需部署 **Etrino 相关 Chart**：`vega-hdfs`、`vega-calculate`（内含 coordinator）、`vega-metadata`。在 `deploy` 目录执行 `./deploy.sh etrino install` 即可单独安装 Etrino。**复杂 SQL 请使用 catalog.`"schema"."table"` 全限定名。** 步骤见 [安装与部署](../install.md) 中的「可选：Etrino」。

**`dataview get` 响应字段（自定义 `--sql` 时）**：`openbkn dataview get <view_id> --pretty` 返回的 JSON 中，与表引用直接相关的是下表（字段名与 REST / TypeScript SDK 一致）。

| 字段 | 说明 |
|------|------|
| **`meta_table_name`** | **必用**：全限定表名字符串（`catalog."schema"."table"`），在 `--sql` 的 `FROM` / `JOIN` 中原样使用；勿凭视图名手写 catalog。 |
| **`sql_str`** | 视图保存的 SQL，可与 `meta_table_name` 对照表引用。 |
| **`fields`** | 各列的 `name`、`type` 等元数据；**不含**全限定表名。 |

**全限定名 / `meta_table_name` 从哪里来**：走 `--sql` 时，引擎按 **Trino/Hetu 风格**解析表名，需要 **`catalog."schema"."table"`** 三段式，含义大致为：

- **catalog**：该数据源在 **Vega** 侧注册得到的 **catalog 标识**（与 `openbkn vega catalog list` 中每条 catalog 的 **id** 一致；常见会带连接器类型前缀，例如 `mysql_…`，具体以环境为准）。
- **schema**：源库中的 **命名空间**——对 MySQL 多为 **database 名**，对 PostgreSQL 多为 **schema 名**，依连接器与元数据而定。
- **table**：物理表名（或视图名）。

平台在创建数据视图时会解析元数据并写入 **`meta_table_name`**（以及内建 **`sql_str`**）。**不要凭视图逻辑名或表名单独拼 catalog**；应执行 **`openbkn dataview get <view_id>`**，将返回的 **`meta_table_name`** 原样用于 `FROM` / `JOIN`，或与 `sql_str` 中的表引用保持一致。多表 JOIN 须在同一数据源（同一 catalog）下。

TypeScript：`client.dataviews.list()`、`client.dataviews.find(...)`、`client.dataviews.query(id, { sql: '...' })`。

### 端到端流程

```bash
openbkn vega health
openbkn vega connector-type list
openbkn vega catalog health --all
openbkn vega catalog discover cat_pg001 --wait
openbkn vega catalog resources cat_pg001 --category table
openbkn vega resource query res_orders_001 -d '{"limit":5,"need_total":true}'
openbkn dataview query dv_001 \
  --sql "SELECT * FROM mysql_demo.\"sales\".\"customer_orders\" WHERE amount > 10000 ORDER BY amount DESC LIMIT 10"
```

---

## 📘 TypeScript SDK

`client.vega` 为**扁平**方法名（`listCatalogs`、`createCatalog` 等）。`executeQuery`、`sqlQuery`、`createResource`、`updateCatalog` 等需要 **JSON 字符串**（`JSON.stringify(...)`）。

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const client = createClient({ baseUrl: 'https://<访问地址>' });

const health = await client.vega.health();
console.log(health);

const catalogs = await client.vega.listCatalogs({ status: 'healthy', limit: 20 });
catalogs.forEach((c: any) => console.log(c.id, c.name));

const detail = await client.vega.getCatalog('cat_pg001');
const healthStatus = await client.vega.catalogHealthStatus('cat_pg001');
const test = await client.vega.testCatalogConnection('cat_pg001');
await client.vega.discoverCatalog('cat_pg001', { wait: true });
const catRes = await client.vega.listCatalogResources('cat_pg001', { category: 'table', limit: 50 });

await client.vega.createCatalog({
  name: 'my-mysql',
  connector_type: 'mysql',
  connector_config: { host: 'db.example.com', port: 3306, database: 'mydb', username: 'u', password: 'p' },
});
await client.vega.updateCatalog('cat_pg001', JSON.stringify({ name: 'renamed' }));
await client.vega.deleteCatalogs('cat_pg001');

const resources = await client.vega.listResources({ catalogId: 'cat_pg001', limit: 50 });
const allRes = await client.vega.listAllResources({ limit: 100 });
const res = await client.vega.getResource('res-001');
const data = await client.vega.queryResourceData('res-001', JSON.stringify({ limit: 5, need_total: true }));

await client.vega.createResource(JSON.stringify({
  catalog_id: 'cat-001', name: 't', category: 'table',
}));
await client.vega.updateResource('res-001', JSON.stringify({ status: 'active' }));
await client.vega.deleteResources('res-001');

await client.vega.createDatasetDocs('res-ds', JSON.stringify([{ id: 'd1' }]));
await client.vega.buildDataset('res-ds', 'full');
const build = await client.vega.getDatasetBuildStatus('res-ds', '<task-id>');

const structured = await client.vega.executeQuery(JSON.stringify({
  tables: [{ resource_id: 'res-001' }],
  limit: 5,
  need_total: true,
}));
const sqlResp = await client.vega.sqlQuery(JSON.stringify({
  resource_type: 'mysql',
  query: 'SELECT 1 AS one',
}));

const dvList = await client.dataviews.list({ limit: 50 });
const dvResult = await client.dataviews.query('dv_001', {
  // FROM 使用 get 返回的 meta_table_name；下为格式示意
  sql: "SELECT * FROM mysql_demo.\"sales\".\"customer_orders\" WHERE region = '华东' LIMIT 5",
});
```

---

## 🌐 curl

已 `openbkn auth login` 时，可将示例中的 **`Authorization: Bearer $(openbkn token)`** 用于受保护接口；将 **`https://<访问地址>`** 换为实际平台地址。

```bash
# 列举 Catalog 探活（与 Node CLI `openbkn vega health` 思路一致）
curl -sk "https://<访问地址>/api/vega-backend/v1/catalogs?limit=1" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "x-business-domain: bd_public"

# 可选：直连 vega-backend Pod 的 /health（不在 /v1 下）
# curl -sk "https://<访问地址>/health" -H "Authorization: Bearer $(openbkn token)"

curl -sk "https://<访问地址>/api/vega-backend/v1/catalogs?status=healthy&limit=20" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"
curl -sk "https://<访问地址>/api/vega-backend/v1/catalogs/cat_pg001" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"

curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/catalogs" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"name":"my","connector_type":"mysql","connector_config":{"host":"h","port":3306,"database":"d","username":"u","password":"p"}}'
curl -sk -X PUT "https://<访问地址>/api/vega-backend/v1/catalogs/cat_pg001" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"name":"new-name"}'
curl -sk -X DELETE "https://<访问地址>/api/vega-backend/v1/catalogs/cat_pg001" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"

curl -sk "https://<访问地址>/api/vega-backend/v1/catalogs/cat_pg001/health-status" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/catalogs/cat_pg001/test-connection" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/catalogs/cat_pg001/discover" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"
curl -sk "https://<访问地址>/api/vega-backend/v1/catalogs/cat_pg001/resources?category=table&limit=30" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"

curl -sk "https://<访问地址>/api/vega-backend/v1/resources?catalog_id=cat_pg001&limit=50" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/resources" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"catalog_id":"cat_pg001","name":"t","category":"table"}'
curl -sk -X PUT "https://<访问地址>/api/vega-backend/v1/resources/res_orders_001" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"status":"active"}'
curl -sk -X DELETE "https://<访问地址>/api/vega-backend/v1/resources/res_orders_001" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"

curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/resources/res_orders_001/data" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -H "x-http-method-override: GET" \
  -d '{"limit":10,"offset":0,"need_total":true}'

# Dataset 文档写入（使用 POST 覆盖）
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/resources/res-ds/data" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -H "x-http-method-override: POST" \
  -d '[{"id":"doc1","content":"..."}]'

# Dataset 构建任务
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/build-tasks" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"resource_id":"res-ds","mode":"full"}'
curl -sk "https://<访问地址>/api/vega-backend/v1/build-tasks/<task-id>" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"

curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/query/execute" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"tables":[{"resource_id":"res_orders_001"}],"limit":5,"need_total":true}'
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/resources/query" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"resource_type":"mysql","query":"SELECT 1 AS one"}'

curl -sk "https://<访问地址>/api/vega-backend/v1/connector-types" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"
curl -sk "https://<访问地址>/api/vega-backend/v1/connector-types/mysql" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/connector-types/mysql/enabled" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"enabled":true}'
```

Dataview 的 HTTP 路径由 **mdl-uniquery** / **mdl-data-model** 提供，不在 vega-backend 的 `router.go` 中；请使用 `openbkn dataview` 或 `client.dataviews`。

更多说明见 npm 包 `@openbkn/bkn-sdk` 以及 `openbkn vega --help`。
