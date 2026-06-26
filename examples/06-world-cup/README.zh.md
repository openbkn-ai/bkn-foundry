# 06 · 世界杯数据 → Vega Catalog → BKN → Vega-SQL 工具

> 将公开的 [Fjelstul World Cup Database](https://github.com/jfjelstul/worldcup) 落成 MySQL **`wc_*` 表**，由单脚本 **`./run.sh`** 串起 **Vega 扫描 → BKN 推送 → 索引构建 → Vega-SQL 工具注册**，最终得到一个已发布、可直接对 27 张表执行 SQL 的 **`vega_sql_execute`** 工具。

[English](./README.md)

## 主路径

```
                       ┌─ 1) 下载 CSV     从 jfjelstul/worldcup 下载 27 个 CSV（已有则跳过）
                       │
                       ├─ 2) 导入 MySQL   openbkn ds connect + ds import-csv → wc_* 表
                       │                 （预建 wc_matches / wc_team_appearances 为 VARCHAR(255)
                       │                   规避 MySQL Error 1118 行宽超限）
                       │
                       ├─ 3) Vega 扫描    vega catalog create + discover --wait
                       │
   ./run.sh  ─────────►├─ 4) 渲染 BKN     map vega Resources → render worldcup-bkn.tar
                       │
                       ├─ 5) Push BKN +   bkn validate + push（幂等），
                       │   建索引          再为 7 张实体表的 vega resource 建 OpenSearch
                       │                  dataset（带向量 embedding，便于 LLM 做模糊名匹配）
                       │
                       └─ 6) 上传工具箱   openbkn toolbox create + tool upload <OpenAPI>
                                          （注册并发布 `vega_sql_execute`，
                                           直接对 wc_* 表跑原生 SQL）
```

主路径到此结束：一个 Vega catalog **BKN**（`worldcup_vega_catalog_bkn`），背后挂着一个已发布、可查询的 **`vega_sql_execute`** 工具，覆盖 27 张 `wc_*` MySQL 表。

仓库内 checked-in 资产：

- **`worldcup-bkn.tar`** — 离线 BKN 模板（27 个对象类、29 条 `rel_*` 关系）打包成 tar；每个 OT 末行带 **`resource | {{*_RES_ID}}`** 占位；`network.bkn` 的 `id` 为 **`worldcup_vega_catalog_bkn`**。`run.sh` 渲染前会解包到 `.tmp/worldcup-bkn/`。
- **`vega_sql_execute.openapi.json`** — SQL-execute 工具的 OpenAPI 3.0 描述。step 6 通过 `openbkn tool upload`（OpenAPI 解析器路径）注册，避开 0.7.0 `openbkn toolbox import` 把 `api_spec` 写为 null 的 bug。
- **`bkn-network-structure.html`** — BKN 网络结构的单文件可视化：4 个概念组、全部 27 个对象类（虚线 = minimal 模式下无 FK 关系）、`matches` / `tournaments` 双枢纽，以及完整的 29 条关系类表。浏览器直接打开，无需构建。

## 数据来源与许可

CSV 来自 Joshua C. Fjelstul 的 **The Fjelstul World Cup Database**（[仓库](https://github.com/jfjelstul/worldcup)）。

- **© 2023 Joshua C. Fjelstul, Ph.D.**
- 许可：**CC-BY-SA 4.0** — [许可全文](https://creativecommons.org/licenses/by-sa/4.0/legalcode)

再分发衍生数据或教程时需保留署名并保持同许可。

**锁定版本：** `.env` 中设置 `WORLDCUP_REF`（默认 `master`，会随上游变更）。

## 首次启用清单（First-time setup）

`run.sh` 只自动化上述 6 步。换一台机器 + 一个新集群，下面这 5 件平台级事必须先手动做完一次：

1. **安装 BKN Foundry 平台**（k8s + bkn-backend + ontology-query + vega-backend + mf-model-* + opensearch + minio + mariadb）。用仓库根目录的 `deploy/onboard.sh`，详见 [deploy/README.zh.md](../../deploy/README.zh.md)。**建议 `0.8.0+`**（修了 `_score` resource-path bug 与 toolbox import 写入 bug）。
2. **CLI 登录**：`openbkn auth login https://<你的平台地址>`（凭据写入 `~/.bkn/`）。
3. **注册 embedding 模型**（向量索引需要；不注册会自动降级为关键字索引）：
   ```bash
   openbkn model small add --name text-embedding-v4-cn \
     --type embedding --batch-size 10 --max-tokens 512 --embedding-dim 1024 \
     --model-config-file <emb.json>
   ```
   `.env` 里的 `EMBEDDING_MODEL_NAME` 运行时被解析成 `model_id`（默认 `text-embedding-v4-cn`）。
4. **把 BKN 默认 embedding 写进 ConfigMap**（KN 级语义检索路径用到；本示例不强依赖）：
   ```bash
   sudo bash deploy/onboard.sh --enable-bkn-search \
     --bkn-embedding-name=text-embedding-v4-cn
   ```
5. **MySQL 准备**：建 `worldcup` 库 + 一个能从 openbkn 平台 pod 网络可达的账号。填 `.env` 的 `DB_HOST / DB_PORT / DB_NAME / DB_USER / DB_PASS`。

以上做完，`./run.sh` 就是一键 + 幂等。

## 单次执行的前置（per-shell）

```bash
npm install -g @openbkn/bkn-sdk
openbkn auth login https://<你的平台地址>
# CLI：用 Node SDK 的 `openbkn`，避免 `which openbkn` 落到无效的 /usr/local/bin/openbkn
# MySQL：平台 DS 与 Vega 连接器均需能访问
# curl + jq + python3 + （可选）本地 `mysql` 客户端用于宽表预建
```

## 快速开始

```bash
cd examples/06-world-cup
cp env.sample .env
vim .env   # 至少填 DB_*

# 一键跑六步，全部幂等
./run.sh
```

`./run.sh --help` 列出全部 flags。常用：

| 命令 | 作用 |
|------|------|
| `./run.sh` | 完整跑 1→6 |
| `./run.sh --dry-run` | 只打印计划，不调 API |
| `./run.sh --from 3` | 从 Vega 扫描起重跑（CSV 已在 MySQL） |
| `./run.sh --only 5` | 只跑 step 5（push BKN + 建索引） |
| `./run.sh --only 6` | 只跑 toolbox 创建 + tool upload + publish |

## 注册的 `vega_sql_execute` 工具

step 6 会发布一个 OpenAPI 描述的工具 `vega_sql_execute`，对 `worldcup_vega_catalog_bkn` BKN 背后的 Vega resource 跑原生 MySQL SQL：

| 工具 | 来自工具箱 | 什么时候用 |
|---|---|---|
| **`vega_sql_execute`** | step 6 用 `vega_sql_execute.openapi.json` 注册 | 原生 MySQL SQL —— `SELECT` / `WHERE` / `ORDER BY` / `GROUP BY` / 多表 `JOIN` / `COUNT(*)`。表名用 `{{<resource_id>}}` 占位，`resource_id` 通过 `openbkn vega resource list` 拿。 |

平台内置的 `search_schema` / `query_object_instance` / `query_instance_subgraph` 工具仍然挂在同一个 KN 上，可用于 schema 探索，以及对 step 5 建的 OpenSearch dataset 做等值 / 范围查询。

## 实际能查什么

跑完 `./run.sh` 后，调用已发布的 `vega_sql_execute` 工具。先拿到某张表的 `resource_id`，再在 SQL 里用 `{{<resource_id>}}` 引用：

```bash
# 列出表 resource，拿 resource_id
openbkn vega resource list --datasource-id <catalog_id> --type table

# 通过已发布的工具跑 SQL（TOOLBOX_BOX_ID / VEGA_TOOL_ID 由 step 6 打印出来）
openbkn tool invoke <VEGA_TOOL_ID> --toolbox <TOOLBOX_BOX_ID> \
  --input query='<带 {{<resource_id>}} 占位的 SQL>' \
  --input resource_type=mysql
```

### Q1 · 梅西在世界杯获过哪些奖
**SQL**：`SELECT tournament_name, award_name FROM {{<award_winners_resource_id>}} WHERE family_name='Messi' AND given_name='Lionel'` → 返回：

- 2014 巴西世界杯 — **金球奖**
- 2022 卡塔尔世界杯 — **金球奖** + **银靴奖**

### Q2 · 孙雯每届女足世界杯进球数 + 历史排名
**SQL**：`SELECT tournament_name, COUNT(*) FROM {{<goals_resource_id>}} WHERE family_name='Sun' AND given_name='Wen' GROUP BY tournament_name`（再来一条 SQL 算榜单）→ 返回：

| 届次 | 进球 |
|---|---|
| 1991 女足 | 1 |
| 1995 女足 | 2 |
| **1999 女足** | **7**（同届还包揽金球 + 金靴） |
| 2003 女足 | 1 |
| **总计** | **11**（女足世界杯历史并列第 5） |

### Q3 · 近三届男足世界杯冠军
**SQL**：`SELECT year, host_country, winner FROM {{<tournaments_resource_id>}} WHERE tournament_name LIKE '%Men%' ORDER BY CAST(year AS UNSIGNED) DESC LIMIT 3` → 返回：

- 2022 卡塔尔 → 阿根廷
- 2018 俄罗斯 → 法国
- 2014 巴西 → 德国

每个数字都从 step 2 导入的 27 张 `wc_*` 表直接拿 —— **精确，无近似**。

## 27 个数据集（分组）

1. **基础实体** — `tournaments`、`confederations`、`teams`、`players`、`managers`、`referees`、`stadiums`、`matches`、`awards`
2. **赛事级映射** — `qualified_teams`、`squads`、`manager_appointments`、`referee_appointments`
3. **场次出场** — `team_appearances`、`player_appearances`、`manager_appearances`、`referee_appearances`
4. **场内事件** — `goals`、`penalty_kicks`、`bookings`、`substitutions`
5. **积分榜与奖项结果** — `host_countries`、`tournament_stages`、`groups`、`group_standings`、`tournament_standings`、`award_winners`

## 故障排查

| 现象 | 处理 |
|------|------|
| step 1 下载失败 | 检查网络，确认 `WORLDCUP_REF` 指向含 `data-csv/` 的版本 |
| `openbkn auth` 401 | `openbkn auth login` 再来一次；`openbkn config show` 核对业务域 |
| `import-csv` 触发 MySQL **Error 1118** | step 2 已用本地 `mysql` CLI 预建 `wc_matches` / `wc_team_appearances`（VARCHAR(255)），未装 mysql client 时需手动建表或放宽列类型 |
| Vega `discover` 失败 | 设 `VEGA_CATALOG_ID` 后 `./run.sh --from 4` |
| Resource 少于 27 张表 | connector `databases` 或 discover 不全；调整 `VEGA_MYSQL_DATABASES` 后重跑 step 3 |
| Step 5 某些大表被 skip | 0.7.0 平台预期行为 — `vega-backend` 对 `>2× batch_size` 的表（8 张事件表）有 batch-sync 游标 bug，会卡死循环。这些表仍可通过 `vega_sql_execute` 查询。0.8.0+ 已修。 |
| Step 5 提示 `embedding model … not registered` | 要么注册一个（见首次启用清单），要么设 `DO_INDEX=0` / `EMBEDDING_MODEL_NAME=`（空）让脚本走纯关键字索引 |
| step 6 `tool upload` / `toolbox publish` 失败 | 确认 CLI 已登录、`vega_sql_execute.openapi.json` 存在；同名 toolbox 残留时设 `FORCE_TOOLBOX_REIMPORT=1` 删除并重导 |

## 与示例 02 的差异

| | 02-csv-to-kn | 06-world-cup |
|---|--------------|--------------|
| 数据 | 仓库内置 3 个小 CSV | upstream 27 份 CSV（CC-BY-SA，运行时下载） |
| 建网方式 | `create-from-csv` | **MySQL + Vega Resource** + **`worldcup-bkn.tar` push** |
| 工具 | 无 | OpenAPI 注册 `vega_sql_execute`，对 catalog 跑原生 SQL |
| 入口 | 多脚本 | 单脚本 `./run.sh`（六步可拆，全部幂等） |

## 清理

`./run.sh` 不会自动删除数据源、MySQL 表、Vega catalog、KN 或 Toolbox；不用时在 Studio / CLI 自行清理。
