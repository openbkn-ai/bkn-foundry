# migrations —— 数据库初始化脚本

本目录集中管理各服务的数据库初始化 SQL，替代原先散落在
`adp/*/*/migrations`、`infra/*/migrations`、`trace-ai/*/migrations` 下的分布式布局。
集中后便于统一管理与镜像构建。

> 本目录由 [data-migrator](../data-migrator/) 在构建镜像时直接消费：目录布局
> `migrations/<模块名>/<数据库类型>/<版本号>/` 即镜像内 `/app/repos` 的最终结构，
> 由 Dockerfile 原样 `COPY`，无需 `copy_repos.py` 之类的收集脚本转换。

## 目录结构

```
migrations/
├── README.md
└── <模块名>/
    └── <数据库类型>/
        └── <版本号>/
            ├── init.sql        # 全量 schema 快照，必需
            └── NN-*.sql        # 增量升级脚本，可选（当前 0.1.0 没有，以后其他版本才会有）
```

- **模块名**：与 service 名一致（如 `bkn-backend`、`vega-backend`）。
- **数据库类型**：当前仅支持 `mariadb`。
- **版本号**：形如 `0.1.0`（点分数字，各段均为整数）。
- **init.sql**：每个版本目录必需的全量初始化脚本。
- **NN-*.sql**：可选的增量升级脚本（`NN` 为两位序号）。当前基线版本 `0.1.0`
  不含增量脚本，仅在未来新增版本（如 `0.2.0`）时才会出现。

当前基线只保留 `mariadb`，各模块只保留单一版本 `0.1.0`。

## 规范

### 1. 单一基线版本

- 每个模块在每种数据库类型下**只保留一个版本目录 `0.1.0`**。
- `0.1.0/init.sql` 为**全量 schema 快照**（累积的完整建表/初始化语句），
  面向全新安装。data-migrator 全新安装时只执行最大版本的 `init.sql`。
- 本基线**不保留历史增量脚本**，不支持从旧版本平滑升级。

### 2. init.sql 是必需项

- 每个版本目录**必须包含非空的 `init.sql`**；lint 阶段缺失或空目录会报错。
- 文件使用 `CREATE TABLE IF NOT EXISTS ...`、`INSERT ... ON DUPLICATE ...`
  等幂等写法，保证重复执行安全。
- 通过 `USE openbkn;` 显式指定目标库；所有模块统一写入 `openbkn` 库。

### 3. 增量脚本（本基线暂不使用）

- data-migrator 支持在版本目录内放置 `NN-*.sql` / `NN-*.py`（`NN` 为两位序号）
  作为升级脚本，按序号执行，`init.sql` 不参与升级路径。
- 由于本次重铸为单一 `0.1.0` 基线，**新版本目录内不放置增量脚本**，仅保留 `init.sql`。
- 未来若需迭代，新增 `0.2.0/` 等版本目录，并在其中放置增量脚本。

### 4. 版本号命名

- 采用点分数字（如 `0.1.0`、`0.2.0`、`1.0.0`），各段均为整数。
- 目录名即版本号，按语义版本排序，取最大版本。

### 5. 文件头许可声明

沿用现有 SQL 文件的许可头：

```sql
-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.
```

## 模块清单

| 模块 (service key)          | 目标库   | 原路径 |
| --------------------------- | -------- | ------ |
| bkn-backend                 | openbkn  | adp/bkn/bkn-backend/migrations |
| vega-backend                | openbkn  | adp/vega/vega-backend/migrations |
| agent-operator-integration  | openbkn  | adp/execution-factory/operator-integration/migrations |
| mf-model-manager            | openbkn  | infra/mf-model-manager/migrations |
| oss-gateway-backend         | openbkn  | infra/oss-gateway-backend/migrations |
| sandbox                     | openbkn  | infra/sandbox/migrations |

> 各模块 `init.sql` 均通过 `USE openbkn;` 写入统一的 `openbkn` 库。

> `sandbox_control_plane` 使用独立的 Python 迁移器（单个 `.py`，非
> `<数据库类型>/<版本号>/init.sql` 布局），不纳入本目录。
