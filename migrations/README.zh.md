# migrations —— 数据库初始化与升级脚本

[English](README.md) | 中文

本目录是 OpenBKN 各服务数据库脚本的唯一执行来源。构建
[data-migrator](../data-migrator/) 镜像时，`migrations/` 会被原样复制到镜像内的
`/app/repos`；运行时按服务名、数据库类型和版本号发现并执行脚本。

## 目录结构

```
migrations/
├── README.md
├── README.zh.md
└── <service-key>/
    └── <database-type>/
        └── <version>/
            ├── init.sql        # 该版本的完整 schema 快照，必需
            └── NN-*.sql        # 编号增量脚本，可选
```

- **service key**：必须与 `data-migrator/config.monorepo.yaml` 的 `services` 键一致。
- **database type**：目录名使用小写；当前已受管的脚本使用 `mariadb`。
- **version**：点分数字版本，如 `0.1.0`、`0.1.5`、`1.0.0`。
- **`init.sql`**：该版本的完整初始化快照。
- **`NN-*.sql`**：同一版本内按两位数字 `NN` 升序执行的增量脚本。

## 执行规则

### 首次安装

data-migrator 选择最大版本目录的 `init.sql` 执行，并将该版本记录为已安装版本。
因此，每个版本目录都必须提供可独立初始化目标数据库的完整 `init.sql`。

### 版本升级

已有安装记录时，data-migrator 跳过所有 `init.sql`，按版本升序和文件编号升序执行
所有高于已安装版本的增量脚本。每个脚本成功后记录执行进度，失败后可从最近成功的
脚本继续。

新增版本时必须同时：

1. 新建 `<version>/init.sql`，包含该版本的完整 schema 快照；
2. 在同一目录增加从上一版本升级所需的 `NN-*.sql`；
3. 保留既有版本及其脚本，避免破坏已部署环境的升级路径。

## 脚本规范

- 每个版本目录必须包含非空的 `init.sql`；缺失时 data-migrator lint 会失败。
- SQL 应使用可重复执行的写法，例如 `CREATE TABLE IF NOT EXISTS`、`INSERT ... ON DUPLICATE ...`。
- 所有脚本显式使用 `USE openbkn;`；当前受管服务统一写入 `openbkn` 数据库。
- `NN` 使用两位十进制序号，范围为 `01` 至 `99`，一个版本内不得重复。
- SQL 文件必须保留适用的许可证头：OpenBKN 新增文件使用 OpenBKN License；保留的上游文件
  使用其原有的 Apache 2.0 许可证头。具体许可见仓库根目录的 `LICENSE` 与
  `LICENSE-OPENBKN.txt`。

## 受管服务

| service key | 目标库 | 模块路径 |
| --- | --- | --- |
| `bkn-backend` | `openbkn` | `adp/bkn/bkn-backend` |
| `bkn-backend-trace-outbox` | `openbkn` | `adp/bkn/bkn-backend` |
| `ontology-query-trace-outbox` | `openbkn` | `adp/bkn/ontology-query` |
| `vega-backend` | `openbkn` | `vega/vega-backend` |
| `agent-operator-integration` | `openbkn` | `adp/execution-factory/operator-integration` |
| `mf-model-manager` | `openbkn` | `infra/mf-model-manager` |
| `bkn-agent` | `openbkn` | `infra/bkn-agent` |
| `oss-gateway-backend` | `openbkn` | `infra/oss-gateway-backend` |
| `sandbox` | `openbkn` | `infra/sandbox` |

`sandbox_control_plane` 使用独立的 Python 迁移器（单个 `.py` 文件，不遵循
`<database-type>/<version>/init.sql` 布局），不由 data-migrator 管理。
