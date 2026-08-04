# 01 · 从数据库到语义搜索

> 把一个原始 MySQL 数据库变成可检索的知识网络——用自然语言，不用写 SQL。

## 场景背景

供应链分析师有多年的采购和库存数据存在 MySQL 里。每次需要回答业务问题——
"哪些供应商最可靠？""哪些物料有断货风险？"——都要找 DBA 写 SQL，一来一回耗费半天。

这个示例把数据库接入知识网络：自动发现表结构、查询实例，并对其做语义检索，答案来自你真实的数据。

## 示例流程

```
MySQL 数据库
     │
     ▼
┌──────────────┐     ┌──────────────┐     ┌─────────────────┐
│ Vega Catalog │────▶│   知识网络    │────▶│  上下文加载器    │
│  （资源发现） │     │   (KN)       │     │   语义搜索       │
└──────────────┘     └──────────────┘     └─────────────────┘
        │                   │
        ▼                   ▼
┌──────────────┐     ┌──────────────┐
│ 检索索引构建  │     │  Schema 探索  │
│（全文 + 向量）│     │   与实例查询  │
└──────────────┘     └──────────────┘
```

0. **导入数据** — 将示例数据（虚构的智能家居供应链）导入 MySQL
1. **注册 Vega Catalog** — 以 MySQL 连接器接入并自动发现表，得到资源（resource）
2. **创建知识网络** — 对象类绑定 Vega 资源
3. **构建检索索引** — 为每个资源建全文与向量索引
4. **探索 Schema** — 查看对象类型和属性
5. **查询数据** — 通过知识网络查询实例
6. **语义搜索** — 对知识网络做自然语言检索

> 对象类绑定的是 Vega **资源** ID，不走已废弃的 `data-connection` 数据源路径。
> 全文与向量检索需要**一套索引**：由 Vega BuildTask 把资源数据同步进 OpenSearch，
> 并对指定字段做向量化（Step 3）。索引配置归属于 Vega **资源**（`index_config` 给出
> 构建键与默认分析器/模型，字段级 `features` 给出每个字段建哪种索引），构建任务在创建时
> 对其做快照；`openbkn vega dataset build` 一条命令完成两步。知识网络层面已无
> `bkn build`，该接口已下线。
>
> **建索引会改变对象类的读取路径。** 表资源一旦有了本地索引，Vega 就改从索引读，只有在
> 没有索引时才回源库实时查。因此在默认的 `DO_INDEX=1` 下，Step 5 返回的是构建快照，
> 之后在 MySQL 里执行的 `UPDATE` 要等到资源重建索引才可见
> （`openbkn vega dataset build <resource-id> --mode batch --execute-type full`）。
> 需要保持实时读就用 `DO_INDEX=0`，代价是没有全文与向量检索。
>
> Step 3 其余参数：`EMBEDDING_MODEL_NAME=`（置空）只建全文索引；
> `INDEX_TIMEOUT`（默认 300 秒）为单个资源的等待上限。
>
> 另需注意：建好的索引目前还接不到知识网络的语义层——对象类属性不会暴露 `match` / `knn`
> 操作，`bkn search` 仍停留在 Schema 概念匹配，详见 PR 中关于 `f_index_available` 的说明。

## 前置条件

```bash
# 1. 安装 openbkn CLI
npm install -g @openbkn/bkn-sdk

# 2. 安装 MySQL 客户端（Step 0 在本机执行 mysql 导入 seed.sql）
#    macOS:  brew install mysql-client
#    Ubuntu: sudo apt install -y mysql-client

# 3. 登录 BKN Foundry 平台
openbkn auth login https://<platform-url>

# 4. 准备一个平台可访问的 MySQL 数据库
#    DB 用户需要 CREATE TABLE / INSERT / SELECT 权限
```

## 快速开始

```bash
cp env.sample .env
# 填写 DB_HOST、DB_NAME、DB_USER、DB_PASS（见 env.sample 中的注释）
vim .env
./run.sh
```

> **安全提示：** `.env` 已被 gitignore 排除。请勿将含有真实凭据的 `.env` 提交到版本控制。

## 配置说明

**`DB_HOST` 与 `DB_HOST_SEED`**
Step 0 的 `mysql` 在本机运行，Step 1 的 Catalog 连接由平台（vega-backend）发起。
如果本机需要公网 IP 而平台需要内网 IP，分别设置：`DB_HOST`（内网）和 `DB_HOST_SEED`（公网）。
不设 `DB_HOST_SEED` 时默认使用 `DB_HOST`。

**`DEBUG=1`** 打印详细诊断信息（API 响应、配置等），不会泄露密码。

## 关键命令

```bash
# 1. 注册 Vega Catalog（MySQL 连接器）并发现表
openbkn vega catalog create --name "my-cat" --connector-type mysql \
  --connector-config '{"host":"'$DB_HOST'","port":'$DB_PORT',"username":"'$DB_USER'","password":"'$DB_PASS'","databases":["'$DB_NAME'"]}'
openbkn call "/api/vega-backend/v1/catalogs/<catalog-id>/enable" -X POST   # catalog 创建后默认禁用
openbkn vega catalog discover <catalog-id> --wait
openbkn vega resource list --catalog-id <catalog-id> --category table       # → 资源 ID

# 2. 建知识网络，对象类绑定 Vega 资源
openbkn bkn create --name "my-kn"
openbkn bkn object-type create <kn-id> --name 物料 --resource-id <resource-id> \
  --primary-key material_code --display-key material_name

# 3. 为资源构建检索索引（先写 index_config，再建并启动构建任务）
openbkn vega dataset build <resource-id> --mode batch --execute-type full \
  --build-key-fields material_code \
  --fulltext-fields material_name,bom_material_code \
  --embedding-fields material_name --embedding-model text-embedding-v4 --wait
#    --embedding-model 传的是模型**名称**，传模型 ID 会被拒绝
#    事后查看进度：openbkn vega dataset build-list --resource-id <resource-id>

# 4. 探索 + 查询 + 检索
openbkn bkn object-type list <kn-id>
openbkn bkn search <kn-id> "物料"
```

## 常见问题

**`ERROR 1044 Access denied`** — DB 用户对 `DB_NAME` 没有权限。请 DBA 执行：
`GRANT ALL ON your_db.* TO 'your_user'@'%';`

## 清理

脚本默认**保留**创建的知识网络与 Vega catalog，退出时打印资源 ID 供查看。
需要自动删除时用 `CLEANUP=1 ./run.sh`。手动清理：

```bash
openbkn bkn delete <kn-id> -y
openbkn call /api/vega-backend/v1/catalogs/<catalog-id> -X DELETE
```
