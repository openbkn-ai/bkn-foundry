# 从 CSV 一键建知识网络

> 写新 Recipe 请复制 [`_TEMPLATE.md`](./_TEMPLATE.md)；本文是首个示例，演示模版各段如何填。

> - **难度**：⭐ 入门
> - **耗时**：约 10 分钟
> - **涉及模块**：`bkn`、`datasource`
> - **CLI 版本**：`openbkn >= 0.6`

## 1. Goal（目标）

**完成后你将拥有：** 一个名为 `supply-kn` 的知识网络（KN），每张 CSV 自动成为一个对象类（OT），可用 `bkn object-type query` 查询、`bkn search` 语义检索 —— 全程一条命令，无需手写 schema。

## 2. Prerequisites（前置条件）

- 已通过 `openbkn auth login <平台地址>` 登录。
- 账号上下文正确：使用 `openbkn config show` 确认当前平台配置。
- 准备一个 BKN Foundry 可访问的 **数据源**（CSV 会先入到该数据源做中间存储）。
- 本地 CSV 文件（首行表头，UTF-8）。下文以两份为例：`物料.csv`、`库存.csv`，均含 `material_code`、`material_name` 两列。

## 3. Steps（操作步骤）

### 3.1 选/建 Catalog

先看现有 Catalog，从中挑一个：

```bash
openbkn vega catalog list
```

如果没有合适的，注册一个新的（示例为 MySQL）：

```bash
openbkn vega catalog create --name "erp" --connector-type mysql \
  --connector-config '{"host":"db.example.com","port":3306,"username":"root","password":"pass123","databases":["erp"]}'
# → 返回 catalog id

openbkn vega catalog enable <catalog_id>
openbkn vega catalog discover <catalog_id> --wait
```

> 选好后记下 **`<catalog_id>`**。下面把它当成已知量。

### 3.2 一键从 CSV 建 KN

```bash
openbkn bkn create-from-csv <catalog_id> \
  --files "物料.csv,库存.csv" \
  --name "supply-kn" \
  --table-prefix sc_
# → 返回 kn_id
```

> **该命令当前不可用**：它的 CSV 入库依赖已下线的 dataflow 通道。请走下面的分步路径
> （mysql 客户端装库 → 重新发现 → `create-from-catalog`）。

参数速查：

| 参数 | 是否必填 | 说明 |
| --- | --- | --- |
| `<catalog_id>` | 是 | CSV 落地库对应的 Catalog ID |
| `--files` | 是 | 逗号分隔或 glob，例如 `"*.csv"` |
| `--name` | 是 | 知识网络名 |
| `--table-prefix` | 否 | 表名前缀，避免和已有表冲突 |
| `--build` / `--no-build` | 否 | 默认 `--build`；`--no-build` 跳过构建 |
| `--timeout` | 否 | 构建等待超时秒数（默认 300） |

<details>
<summary>等价的两步路径（想自定义主键 / 显示键时用）</summary>

```bash
# 1. 用 mysql 客户端把 CSV 装进 Catalog 对应的库（平台侧 import-csv 已下线）
mysql -h db.example.com -u root -p erp < load_csv.sql

# 2. 重新发现表，再从 Catalog 建网
openbkn vega catalog discover <catalog_id> --wait
openbkn bkn create-from-catalog <catalog_id> --name "supply-kn" --build
```

分步路径下可在 `bkn object-type create` 时用 `--primary-key` / `--display-key` 显式指定字段。

</details>

### 3.3 验证 KN 可用

```bash
# 列对象类，确认每张 CSV 都生成了一个 OT
openbkn bkn object-type list <kn_id>

# 抽样查询（限制 limit，避免大宽表 JSON 截断）
openbkn bkn object-type query <kn_id> <ot_id> '{"limit":5}'

# 语义检索
openbkn bkn search <kn_id> "物料"
```

## 4. Expected output（期望输出）

> **判定成功的依据**：`object-type query` 返回的 `total > 0`，且 `datas[0]` 包含你导入的 CSV 列；`bkn search` 返回非空 `concepts`。

`object-type query` 应返回类似：

```jsonc
{
  "total": 1234,
  "datas": [
    {
      "_instance_identity": "...",
      "material_code": "M-001",
      "material_name": "螺丝",
      // ... 其它列
    }
  ]
}
```

`bkn search` 的 `concepts` 列表非空说明检索通道正常。

## 5. Troubleshooting（常见问题）

> 「现象」列写**用户能直接看到的具体输出 / 报错**，便于复制搜索。

| 现象 | 可能原因 | 处理 |
| --- | --- | --- |
| `401 Unauthorized` 或返回体含 `oauth info is not active` | token 过期 | `openbkn auth login <平台地址>` |
| `openbkn bkn object-type list <kn_id>` 输出 `[]` | CSV 路径错 / glob 没匹配到 | 确认 `--files` 路径，必要时改用绝对路径 |
| `object-type query` 响应中 `total = 0` | 源表为空、映射错，或索引未建好 | 先 `openbkn vega resource query <resource_id> --limit 5` 看源端有没有数据；建过索引的用 `openbkn vega dataset build-list --resource-id <resource_id>` 看 `index_health` |
| 装 CSV 时报 `table already exists` | 同名表已存在 | 在 SQL 里先 `DROP TABLE IF EXISTS`（平台侧的 `ds import-csv` 已下线，导入由 mysql 客户端完成） |
| 自动选出的主键不是业务唯一键 | 启发式无法识别 | 走分步路径，`openbkn bkn object-type create` 显式 `--primary-key` / `--display-key` |
| `bkn search` 返回 `HTTP 500` | 视图不支持全文检索 | 把查询 `condition` 从 `match` 改为 `like` |

## 6. See also（延伸阅读）

- 参考：[BKN 引擎](../manual/bkn.md) · [数据源管理](../manual/datasource.md) · [快速开始](../quick-start.md)
