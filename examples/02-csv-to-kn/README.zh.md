# 02 · 从 CSV 文件到知识网络

> 散落的表格，连接起来。不需要写 SQL。

## 场景背景

HR 总监的员工、部门、项目数据散落在三张表格里。想搞清楚"谁向谁汇报"
或"哪些项目人手不足"，需要手动 VLOOKUP 串联多个文件，费时又容易出错。

这个示例把这些 CSV 文件导入知识网络。关系自动发现，你可以探索 schema、
查询实例，并遍历组织架构，从而理解你的人员和项目。

## 示例流程

```
本地 CSV 文件
     │
     ▼
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│    MySQL     │────▶│ Vega Catalog │────▶│   知识网络    │
│（mysql 导入） │     │  （表发现）   │     └──────┬───────┘
└──────────────┘     └──────┬───────┘            │
                            ▼                    ▼
                    ┌──────────────┐     ┌──────────────┐
                    │  检索索引构建 │     │  Schema 探索  │
                    │（全文 + 向量）│     │   与实例查询  │
                    └──────────────┘     └──────────────┘
```

1. **导入** — 用标准 `mysql` 客户端把 CSV 装进 MySQL
2. **注册** Vega Catalog 并发现表，得到资源（resource）
3. **建网** — 对象类绑定发现出的资源
4. **建检索索引** — 为每个资源建全文与向量索引
5. **探索**对象类型和属性
6. **查询**对象实例

> 对象类绑定 Vega **资源** ID。全文与向量检索需要**一套索引**：由 Vega BuildTask 把数据同步进
> OpenSearch 并对指定字段做向量化（Step 4）。索引配置归属于 Vega **资源**（`index_config`
> 与字段级 `features`），构建任务创建时对其做快照；`openbkn vega dataset build` 一条命令
> 写配置并发起构建。
>
> **建索引会改变 Step 6 的读取路径。** 表资源一旦有了本地索引，Vega 就改从索引读，只有在没有
> 索引时才回源库实时查。因此在默认的 `DO_INDEX=1` 下，Step 6 返回的是构建快照，之后在 MySQL 里
> 执行的 `UPDATE` 要等到资源重建索引才可见。需要保持实时读就用 `DO_INDEX=0`，代价是没有全文与
> 向量检索。
>
> Step 4 其余参数：`EMBEDDING_MODEL_NAME=`（置空）只建全文索引；
> `INDEX_TIMEOUT`（默认 300 秒）为单个资源的等待上限。

### 示例数据

| 文件 | 内容 |
|------|------|
| `departments.csv` | 5 个部门，含预算和人数 |
| `employees.csv` | 16 名员工，含职级、薪资、汇报关系 |
| `projects.csv` | 8 个项目，含状态、预算、负责人 |

## 前置条件

```bash
# 1. 安装 openbkn CLI
npm install -g @openbkn/bkn-sdk

# 2. 登录 BKN Foundry 平台
openbkn auth login https://<platform-url>

# 3. 准备一个平台可访问的 MySQL 数据库
#    （脚本自动创建表，无需手动建表）
```

## 快速开始

```bash
cp env.sample .env
# 填写 DB_HOST、DB_NAME、DB_USER、DB_PASS（见 env.sample 中的注释）
vim .env
./run.sh
```

> **安全提示：** `.env` 已被 gitignore 排除。请勿将含有真实凭据的 `.env` 提交到版本控制。

### 使用自己的 CSV 文件

将 `data/` 目录中的文件替换为你自己的 CSV 即可：
- 第一行为列名（header）
- 文件名成为表名和对象类型名
- 所有列自动导入，数值列自动识别类型

## 关键命令

| 命令 | 作用 |
|------|------|
| `openbkn vega catalog create --connector-type mysql ...` | 把数据库注册为 Vega catalog |
| `openbkn vega catalog discover <catalog-id> --wait` | 发现表，生成资源 |
| `openbkn vega resource list --catalog-id <catalog-id> --category table` | 列出资源 ID |
| `openbkn bkn object-type create <kn-id> --resource-id <resource-id> ...` | 对象类绑定资源 |
| `openbkn vega dataset build <resource-id> --mode batch --build-key-fields id --fulltext-fields name --embedding-fields name --embedding-model <模型名> --wait` | 写索引配置并构建检索索引 |
| `openbkn vega dataset build-list --resource-id <resource-id>` | 查看索引构建状态 |
| `openbkn bkn object-type list <kn-id>` | 列出对象类型 |
| `openbkn bkn export <kn-id>` | 导出知识网络定义 |

## 与示例 01 的区别

| | 01-db-to-qa | 02-csv-to-kn |
|---|---|---|
| 数据来源 | 已有 MySQL 数据库 | 本地 CSV 文件 |
| 数据导入 | `seed.sql` 进 MySQL，再注册 Vega catalog | CSV 装进 MySQL，再注册 Vega catalog |
| Schema 准备 | 编写 SQL seed 文件 | 直接带 CSV |
| 网络特性展示 | 语义搜索 + 问答 | **多表 Schema** + 实例查询 |
| 数据领域 | 供应链（BOM、采购订单） | **HR（员工、部门、项目）** |

## 清理

脚本默认**保留**创建的知识网络与 Vega catalog，退出时打印资源 ID。
需要自动删除时用 `CLEANUP=1 ./run.sh`，或手动清理：

```bash
openbkn bkn delete <kn-id> -y
openbkn call /api/vega-backend/v1/catalogs/<catalog-id> -X DELETE
```
