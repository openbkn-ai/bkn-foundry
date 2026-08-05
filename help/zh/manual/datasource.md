# 📂 数据接入（Vega Catalog）

## 📖 概述

**数据接入**负责把外部数据库注册进平台、发现其表结构，并维护连接的生命周期。它是构建知识网络（BKN）的前置步骤——先把数据库注册成 **Catalog**，发现出的每张表成为一个 **Resource**，再由对象类绑定 Resource 转化为知识网络。

> **接口变更**：早期的独立数据源服务（`/api/builder/v1` 与 `openbkn ds` 系列命令）已下线，其职责并入 **vega-backend**。对照关系见文末[已下线命令对照](#已下线命令对照)。

典型 Ingress 前缀：

| 前缀 | 作用 |
| --- | --- |
| `/api/vega-backend/v1` | Catalog 注册、表发现、资源与索引管理 |

**相关模块：** [BKN 引擎](bkn.md)（从 Catalog 创建知识网络）、[VEGA 引擎](vega.md)（资源查询与索引构建）。

## 🗃️ 支持的数据库类型

mysql、postgresql、sqlserver、oracle、clickhouse、hive、opensearch、elasticsearch 等。执行 `openbkn vega connector-type list` 查看当前平台已安装的连接器类型。

### CLI

#### 注册 Catalog

```bash
# 注册 MySQL
openbkn vega catalog create --name "erp" --connector-type mysql \
  --connector-config '{"host":"db.example.com","port":3306,"username":"root","password":"pass123","databases":["erp"]}'
# → 返回 catalog id，例如 d9okoc9v287s739h2120

# 注册 PostgreSQL
openbkn vega catalog create --name "分析库" --connector-type postgresql \
  --connector-config '{"host":"pg.example.com","port":5432,"username":"reader","password":"pass456","databases":["analytics"]}'
```

`connector_config` 的字段随连接器类型而异，以 `openbkn vega connector-type get <type>` 返回的 schema 为准。**主机地址须由 vega-backend 所在网络可达**——通常是内网地址，而不是你本机能连通的那个。

#### 启用与发现

Catalog 创建后默认处于禁用状态，必须先启用再发现：

```bash
openbkn vega catalog enable <catalog_id>
openbkn vega catalog discover <catalog_id> --wait
```

发现是异步任务，`--wait` 会等到其结束。

#### 列出与查看

```bash
# 列出全部 Catalog
openbkn vega catalog list

# 查看单个 Catalog
openbkn vega catalog get <catalog_id>

# 连接健康状态
openbkn vega catalog health <catalog_id>

# 测试连接
openbkn vega catalog test-connection <catalog_id>
```

#### 查看表（资源）

发现出的每张表是一个 Resource，知识网络的对象类绑定的正是它的 id：

```bash
# 列出该 Catalog 下的表资源
openbkn vega resource list --catalog-id <catalog_id> --category table

# 查看单个资源（含 schema_definition，即字段清单）
openbkn vega resource get <resource_id>

# 抽样查看数据行
openbkn vega resource query <resource_id> --limit 10
```

#### 导入 CSV

平台侧的 CSV 导入命令已下线。现在的做法是先用标准 `mysql` 客户端把 CSV 装进目标数据库，再注册 Catalog 并发现——`examples/02-csv-to-kn` 就是这个流程的完整可运行版本。

```bash
# 以 examples/02-csv-to-kn 为例：CSV → MySQL → Catalog → 知识网络
cd examples/02-csv-to-kn
cp env.sample .env && vim .env
./run.sh
```

#### 删除 Catalog

```bash
openbkn vega catalog delete <catalog_id>
```

删除 Catalog 会级联清理其下资源与对应的索引；绑定了这些资源的对象类会随之失去数据源，需要重新绑定。

#### 端到端流程

```bash
# 1. 注册并启用 Catalog
CAT=$(openbkn --json vega catalog create --name "erp" --connector-type mysql \
  --connector-config '{"host":"db.example.com","port":3306,"username":"root","password":"pass123","databases":["erp"]}' \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')
openbkn vega catalog enable "$CAT"

# 2. 发现表
openbkn vega catalog discover "$CAT" --wait
openbkn vega resource list --catalog-id "$CAT" --category table

# 3. 从 Catalog 创建知识网络（可选 --build 顺带建检索索引）
openbkn bkn create-from-catalog "$CAT" \
  --name "erp-供应链" \
  --tables "orders,products,customers" \
  --build --embedding-model text-embedding-v4

# 4. 验证
openbkn bkn object-type list <kn_id>
openbkn bkn search <kn_id> "超期订单"
```

---

### TypeScript SDK

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<访问地址>', token: process.env.BKN_TOKEN });

// 列出 Catalog
const catalogs = await bkn.vega.catalogs({ limit: 20 });
console.log('Catalog:', catalogs);

// 注册新 Catalog
const created = await bkn.vega.createCatalog({
  name: 'erp',
  connector_type: 'mysql',
  connector_config: {
    host: 'db.example.com',
    port: 3306,
    username: 'root',
    password: 'pass123',
    databases: ['erp'],
  },
});
console.log('Catalog ID:', created.id);

// 启用并发现
await bkn.vega.enableCatalog(created.id);
await bkn.vega.discoverCatalog(created.id, true);

// 列出表资源
const resources = await bkn.resource.list({ catalogId: created.id, category: 'table' });
console.log('资源:', resources);

// 删除
await bkn.vega.deleteCatalog(created.id);
```

---

### curl

```bash
# 列出 Catalog
curl -sk "https://<访问地址>/api/vega-backend/v1/catalogs?limit=20" \
  -H "Authorization: Bearer $(openbkn auth token)"

# 注册新 Catalog
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/catalogs" \
  -H "Authorization: Bearer $(openbkn auth token)" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "erp",
    "connector_type": "mysql",
    "connector_config": {
      "host": "db.example.com",
      "port": 3306,
      "username": "root",
      "password": "pass123",
      "databases": ["erp"]
    }
  }'

# 启用与发现
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/catalogs/<catalog_id>/enable" \
  -H "Authorization: Bearer $(openbkn auth token)"
curl -sk -X POST "https://<访问地址>/api/vega-backend/v1/catalogs/<catalog_id>/discover?wait=true" \
  -H "Authorization: Bearer $(openbkn auth token)"

# 列出表资源
curl -sk "https://<访问地址>/api/vega-backend/v1/resources?catalog_id=<catalog_id>&category=table" \
  -H "Authorization: Bearer $(openbkn auth token)"

# 删除 Catalog
curl -sk -X DELETE "https://<访问地址>/api/vega-backend/v1/catalogs/<catalog_id>" \
  -H "Authorization: Bearer $(openbkn auth token)"
```

---

## 已下线命令对照

`/api/builder/v1` 与 `openbkn ds` 系列已随数据源服务一并下线。旧命令与现行做法的对照：

| 已下线 | 现行做法 |
| --- | --- |
| `openbkn ds connect <type> <host> <port> <db>` | `openbkn vega catalog create --connector-type <type> --connector-config '<json>'`，再 `enable` |
| `openbkn ds list` / `openbkn ds get <id>` | `openbkn vega catalog list` / `openbkn vega catalog get <id>` |
| `openbkn ds tables <id>` | `openbkn vega catalog discover <id> --wait` 后 `openbkn vega resource list --catalog-id <id> --category table` |
| `openbkn ds import-csv <id> --files ...` | 用 `mysql` 客户端把 CSV 装进数据库后再发现，参见 `examples/02-csv-to-kn` |
| `openbkn ds delete <id>` | `openbkn vega catalog delete <id>` |
| `openbkn bkn create-from-ds <ds_id>` | `openbkn bkn create-from-catalog <catalog_id>` |
