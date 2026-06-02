# Data Migrator

云原生数据库迁移引擎。微服务在代码仓库中维护 `migrations/` 目录，CI 流水线通过本工具完成脚本收集、语法校验、执行校验，生产部署时由 Helm Hook 自动触发迁移。

## 支持的数据库

| 类型 | 支持列表 |
|------|----------|
| 开源数据库 | MariaDB, MySQL, TiDB |
| 信创数据库 | DM8 (达梦), KDB9 (人大金仓), GoldenDB |
| 云/分布式数据库 | OceanBase, TDSQL, TXSQL |

## CI 工作流

```
fetch  →  lint  →  verify  →  (merge)  →  migrate
 拉脚本    静态校验    DB校验              生产部署
（无DB）  （无DB）   （测试DB）          （生产DB）
```

- **`fetch`** — 从 Git 仓库拉取各微服务 `migrations/` 到本地 `repos/`
- **`lint`** — 校验目录结构合规性 + SQL 语法正确性，无 DB 依赖，适合早期快速失败
- **`verify`** — 在测试 DB 上执行 SQL，对比多 DB 类型 schema 一致性
- **`migrate`** — 生产部署，由 Helm `pre-install/pre-upgrade` Hook 自动触发

---

## 迁移脚本目录规范

各微服务在代码仓库中维护 `migrations/` 目录：

```
<service-repo>/migrations/
├── mariadb/
│   ├── 1.0.0/
│   │   ├── init.sql              # 该版本的完整数据库快照（建表+建索引+初始数据）
│   │   ├── 01-add-column.sql     # 增量脚本，按编号顺序执行
│   │   ├── 02-create-index.sql
│   │   └── 03-data-clean.py      # Python 数据清洗脚本
│   └── 1.1.0/
│       ├── init.sql
│       └── 01-alter-table.sql
├── dm8/
│   └── ...
└── kdb9/
    └── ...
```

**规范要点：**
- 数据库类型目录名必须小写（`mariadb`、`dm8`、`kdb9` 等）
- 版本目录名为 semver 格式（`1.0.0`、`1.1.0`）
- 升级脚本编号 `01` ~ `99`，按编号顺序执行
- 支持 `.sql` 和 `.py` 两种格式（`.json` 支持但不建议继续使用）

**`init.sql` 定位：** 是该版本的完整数据库快照，而非增量脚本。每个版本目录必须包含 `init.sql`（lint 强制校验）。首次安装时，引擎取**最大版本**的 `init.sql` 执行；升级时跳过所有 `init.sql`，仅执行编号增量脚本。

**Python 脚本：** 以子进程方式执行，通过环境变量注入依赖服务连接信息：

| 前缀 | 环境变量 |
|------|---------|
| RDS | `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWD`, `DB_TYPE`, `DB_SOURCE_TYPE` |
| MongoDB | `MONGODB_HOST`, `MONGODB_PORT`, `MONGODB_USER`, `MONGODB_PASSWORD`, `MONGODB_AUTH_SOURCE` |
| OpenSearch | `OPENSEARCH_HOST`, `OPENSEARCH_PORT`, `OPENSEARCH_USER`, `OPENSEARCH_PASSWORD`, `OPENSEARCH_PROTOCOL` |
| Redis | `REDIS_CONNECT_TYPE`, `REDIS_HOST`, `REDIS_PORT`, `REDIS_USERNAME`, `REDIS_PASSWORD` |

---

## 使用方式

### 通用参数

所有子命令均支持以下参数：

| 参数 | 必填 | 说明 |
|------|------|------|
| `--config` | 是 | YAML 配置文件路径，参见 `config.yaml.example` |
| `--service` | 否 | 指定本次操作的服务名称，空格分隔；默认处理配置中全部服务 |
| `--log-level` | 否 | `DEBUG` / `INFO` / `WARNING` / `ERROR`，默认 `INFO` |

### fetch — 拉取迁移脚本

```bash
MY_PAT=<github_pat> python data-migrator.py fetch \
  --config config.yaml \
  --service bkn-backend vega-backend
```

`MY_PAT` 仅在 `repos/<service>` 目录不存在时才需要（用于克隆私有仓库）。目录已存在则跳过克隆。

### lint — 静态校验（无需 DB）

```bash
python data-migrator.py lint \
  --config config.yaml \
  --service bkn-backend vega-backend
```

校验内容：
- 目录结构：db 类型目录名、版本号格式、文件命名规范
- `init.sql`：`USE` 语句存在性、建表语法、表名/索引名命名规范、主键存在性
- 升级脚本：仅允许合法的 DDL / DML 语句类型

### verify — 执行校验（需要测试 DB）

```bash
python data-migrator.py verify \
  --config config.yaml \
  --verify-rds-config verify_rds_config.yaml \
  --service bkn-backend vega-backend
```

`--verify-rds-config` 指定各数据库类型的测试实例连接信息，默认路径 `server/verify/rds/verify_rds_config.yaml`。参见 `verify_rds_config.yaml.example`。

### migrate — 执行迁移（生产部署）

通常由 Helm Hook 自动触发，无需手动执行。本地调试时：

```bash
python data-migrator.py migrate \
  --config /app/config.yaml \
  --secret-config /etc/data-migrator/secret-config.yaml \
  --service service-a service-b service-c
```

`--secret-config` 指定依赖服务连接配置文件路径，默认 `/etc/data-migrator/secret-config.yaml`。K8s 部署时由 Secret 挂载到该路径，无需显式传参。参见 `secret-config.yaml.example`。

### 配置文件说明

| 文件 | 提交 git | 用途 |
|------|---------|------|
| `config.yaml` | ✅ | 服务列表、db_types、check_rules（参见 `config.yaml.example`）|
| `verify_rds_config.yaml` | ❌ | verify 用，多 DB 类型对比连接配置（参见 `verify_rds_config.yaml.example`）|
| `secret-config.yaml` | ❌ | migrate 用，依赖服务连接配置（参见 `secret-config.yaml.example`）|

### 本地开发环境变量

| 环境变量 | 用于子命令 | 说明 |
|----------|-----------|------|
| `MY_PAT` | `fetch` | GitHub PAT，拉取私有仓库时使用 |

---

## 本地开发 & 测试

### 运行单元测试

```bash
# 安装依赖
pip install pytest

# 运行全部单元测试（无需 DB 连接，约 0.1s）
python3 -m pytest

# 详细输出
python3 -m pytest -v
```

测试覆盖范围：token 解析、版本号工具、MariaDB / DM8 / KDB9 静态校验（lint）、迁移脚本选择器、MariaDB parser 层。详见 [tests/README.md](tests/README.md)。

### 端到端验证

```bash
# 静态校验（无需 DB）
python3 server/data-migrator.py lint --config config.yaml

# 执行校验（需要测试 DB）
python3 server/data-migrator.py verify --config config.yaml --verify-rds-config verify_rds_config.yaml
```

---

## 镜像构建

### 基础镜像（平台团队维护）

基础镜像包含引擎代码和所有运行时依赖，由平台团队构建并推送到镜像仓库：

```bash
docker build -f docker/Dockerfile -t acr.aishu.cn/dip/data-migrator-base:<version> .
docker push acr.aishu.cn/dip/data-migrator-base:<version>
```

### 部门镜像（各部门 CI 构建）

各部门基于基础镜像做二次构建，将自己的 `config.yaml` 和 `repos/` 打入镜像。

参考 `docker/Dockerfile.example`，复制到部门仓库后按需修改 `BASE_IMAGE`：

```bash
# 1. 拉取迁移脚本
MY_PAT=<github_pat> python3 server/data-migrator.py fetch --config config.yaml

# 2. 静态校验
python3 server/data-migrator.py lint --config config.yaml

# 3. 构建部门镜像（仅 COPY config.yaml + repos/，秒级完成）
docker build \
  --build-arg BASE_IMAGE=acr.aishu.cn/dip/data-migrator-base:<version> \
  -t <registry>/<dept>/data-migrator:<tag> .

# 4. 推送
docker push <registry>/<dept>/data-migrator:<tag>
```

### 文件说明

| 文件 | 用途 |
|------|------|
| `docker/Dockerfile` | 基础镜像构建文件，平台团队维护 |
| `docker/Dockerfile.example` | 部门镜像构建模板，复制到部门仓库使用 |

---

## 技术限制

- **DDL 不可回滚** — 多数数据库 DDL 触发隐式提交，失败后需人工修复业务库，引擎通过熔断锁定保护现场
- **SQL 幂等预检局限** — 基于 sqlparse 解析，极其复杂的非标准 SQL 可能识别失败，此类语句将直接执行
- **凭证可见性** — Helm values 中的密码经 kubelet 展开后明文出现在 Pod Spec 中，建议通过 RBAC 限制读取权限

---

## 平台参考

> 以下内容面向平台/运维同学，微服务开发者通常无需关心。

### 迁移工作流

#### 脚本筛选规则

**首次安装（task 表无记录）：**
1. 取最大版本目录下的 `init.sql`（每个版本均存在，lint 保证）
2. 执行 `<max_version>/init.sql`（完整快照）
3. 成功后写入 task 记录（`f_installed_version = max_version`，`f_script_file_name = <max_version>/init.sql`）
4. 失败则不写 task，下次 rerun 重新从 init.sql 开始

**版本升级（task 表已有记录）：**
1. 读取 `f_installed_version` 作为当前版本
2. 收集所有 `> installed_version` 的版本目录，跳过 `init.sql`，按编号排序增量脚本
3. 每个脚本执行成功后更新 `f_script_file_name`（断点续跑锚点）
4. 一个版本内所有脚本完成后更新 `f_installed_version`

#### 断点续跑

rerun 时通过 `f_script_file_name` 定位断点：
- 格式为 `<version>/<filename>`，如 `1.1.0/02-add-index.sql`
- 同版本内：跳过序号 `<= last_script_seq` 的脚本
- 跨版本：`f_installed_version` 之前的版本整体跳过，`f_installed_version` 对应版本按脚本锚点续跑

#### 核心执行流程

```
[节点 0] Helm Hook 拉起 Pod，通过 CLI args 接收全部配置
    │
    ▼
[节点 1] 确保管控表存在
    │  internal 模式：自动创建 deploy 库和管控表
    │  external 模式：仅校验库和表是否存在，不存在则报错退出
    ▼
[节点 2] 遍历服务（严格按 app_config.services 列表，目录不存在则警告跳过）
    │
    ▼
[节点 3] 路由判断
    │  task 无记录 → 首次安装路径（execute init.sql）
    │  task 有记录 → 升级路径（execute 增量脚本）
    ▼
[节点 4] 逐脚本执行
    │  .sql → sqlparse 解析 + 幂等预检 + 执行
    │  .py  → 子进程执行，环境变量注入 DB 连接信息
    │  失败 → 写 history（status=failed），raise 后 exit 1
    │  成功 → 写 history（status=success），更新 task 断点
    ▼
[节点 5] 全部版本完成 → exit 0
```

#### SQL 幂等预检

对每个 `.sql` 文件：
1. `sqlparse.split()` 拆分为独立语句
2. 过滤注释和空语句
3. 对每条语句：解析 DDL 类型 → 查询元数据判断是否已生效 → 已生效跳过，未生效执行
4. 执行失败 → 记录异常，`exit 1`

### Helm 集成与统一镜像构建

Hook Job 声明在 Umbrella Chart 的顶层，而非各子 Chart 内部。Helm 执行 `install/upgrade` 时先拉起此 Job 完成所有微服务的迁移，成功后再部署各子 Chart 的业务 Pod。

CI/CD 将各微服务 migrations 目录与引擎代码打包为统一镜像：

```
/app/
├── engine/                     # 迁移引擎代码
│   ├── data-migrator.py
│   └── ...
└── migrations/                 # 全量微服务迁移脚本
    ├── service-a/
    │   ├── mariadb/
    │   └── dm8/
    └── service-b/
```

### 管控库表结构

`deploy` 管控库采用"任务主表 + 历史流水表"双表设计：

```sql
-- 任务主表：每个服务唯一一条记录，仅记录成功态，兼做断点续跑锚点
CREATE TABLE IF NOT EXISTS t_schema_migration_task (
  f_id                BIGINT AUTO_INCREMENT PRIMARY KEY,
  f_service_name      VARCHAR(255) NOT NULL,
  f_installed_version VARCHAR(64)  NOT NULL DEFAULT '',  -- 已完成的最新版本
  f_target_version    VARCHAR(64)  NOT NULL DEFAULT '',  -- 本次迁移的目标版本
  f_script_file_name  VARCHAR(512) NOT NULL DEFAULT '',  -- 最后成功执行的脚本（version/filename）
  f_create_time       DATETIME NOT NULL,
  f_update_time       DATETIME NOT NULL,
  UNIQUE KEY uk_service_name (f_service_name)
);

-- 历史流水表：每次脚本执行追加一条，success 和 failed 均记录
CREATE TABLE IF NOT EXISTS t_schema_migration_history (
  f_id               BIGINT AUTO_INCREMENT PRIMARY KEY,
  f_service_name     VARCHAR(255) NOT NULL,
  f_version          VARCHAR(64)  NOT NULL DEFAULT '',
  f_script_file_name VARCHAR(512) NOT NULL DEFAULT '',   -- 如 1.0.0/01-add-column.sql
  f_checksum         VARCHAR(128) NOT NULL DEFAULT '',
  f_status           VARCHAR(32)  NOT NULL DEFAULT 'success',  -- success / failed
  f_create_time      DATETIME NOT NULL
);
```

**设计说明：**
- 任务主表无 `f_status` 字段，写入即代表成功；安装失败时不写记录，下次 rerun 从头重试
- `f_script_file_name` 格式为 `<version>/<filename>`，作为版本内断点续跑的脚本锚点
- `f_installed_version` 在每个版本所有脚本执行完成后才更新，保证跨版本升级时的断点粒度

### source_type 模式说明

RDS 配置中的 `source_type` 字段控制 deploy 管控库的初始化行为：

| 值 | 行为 |
|----|------|
| `internal`（默认） | 引擎自动创建 deploy 库和管控表（`CREATE DATABASE IF NOT EXISTS` + `CREATE TABLE IF NOT EXISTS`） |
| `external` | 引擎仅校验 deploy 库和管控表是否已存在，不存在则报错退出；适用于 DBA 统一管控建库建表的场景 |

### 存量环境基线补录

对已有数据库但未纳入引擎管控的存量环境，通过独立的 baseline 工具脚本执行一次性基线初始化，将 `<= target_version` 的所有脚本标记为已成功执行，仅写入 `deploy` 管控库，不操作业务库。详见 baseline 工具的独立文档。

---

## 依赖

- Python >= 3.10
- 主要依赖：PyYAML, requests, tenacity, sqlparse, GitPython, dbutils
- 内部依赖：proton-rds-sdk-py

## License

See [LICENSE](LICENSE).
