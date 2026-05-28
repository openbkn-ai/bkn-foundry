# check 模块

数据库迁移脚本的校验模块，用于在 CI 环境中验证 SQL 脚本的正确性和跨数据库一致性。

## 模块结构

```
server/check/
├── executor.py          # 校验主入口，编排 RepoChecker 和 SchemaChecker
├── repo_checker.py      # 目录结构校验 + SQL 语法静态分析
├── schema_checker.py    # SQL 执行校验（连接真实数据库）
├── check_config.py      # 校验配置（CheckConfig）
└── rds/
    ├── base.py          # RDS 校验抽象基类（CheckRDS）
    ├── mariadb.py       # MariaDB 实现（CheckMariaDB）
    ├── dm8.py           # 达梦 DM8 实现（CheckDM8）
    └── kdb9.py          # 人大金仓 KDB9 实现（CheckKDB9）
```

## 校验流程

```
CheckExecutor.run()
  ├── RepoChecker.run()        # 阶段一：静态检查（无需数据库连接）
  │   ├── 目录结构校验           # 版本目录、文件命名（NN-xxx.sql/py/json）
  │   ├── init.sql 语法校验      # check_init() — 校验允许的语句类型
  │   └── 升级脚本语法校验       # check_update() — 校验允许的语句类型
  │
  └── SchemaChecker.run()      # 阶段二：执行校验（连接测试数据库）
      ├── reset_schema()        # 重置所有数据库 schema
      ├── 执行 init.sql          # run_sql() — 幂等执行
      ├── 执行升级脚本           # .sql → run_sql(), .json → 调用操作方法, .py → 子进程执行
      └── 跨库 schema 对比       # _compare_schema() — 表数量和列数量一致性
```

## RDS 连接配置（verify_rds_config.yaml）

阶段二执行校验需要连接真实数据库，连接信息通过独立的 `verify_rds_config.yaml` 提供，与主配置文件分离。

### 文件加载规则

| 方式 | 说明 |
|------|------|
| 默认路径 | `server/check/rds/verify_rds_config.yaml` |
| 环境变量覆盖 | `VERIFY_RDS_CONFIG=/path/to/your_config.yaml` |

### 文件结构

顶层 key 为数据库类型（`mariadb` / `dm8` / `kdb9`），每个类型下有 `primary`（主库）和 `secondary`（副库）两个连接段。连接段的字段直接透传给 `rdsdriver.connect()`。

```yaml
mariadb:
  primary:
    host: 127.0.0.1
    port: 3306
    user: root
    password: your_password
  secondary:
    host: 127.0.0.1
    port: 3306
    user: root
    password: your_password

dm8:
  primary:
    host: 127.0.0.1
    port: 5236
    user: SYSDBA
    password: your_password
  secondary:
    host: 127.0.0.1
    port: 5236
    user: SYSDBA
    password: your_password

kdb9:
  primary:
    host: 127.0.0.1
    port: 54321
    user: system
    password: your_password
  secondary:
    host: 127.0.0.1
    port: 54321
    user: system
    password: your_password
```

### 说明

- 只需配置 `config.yaml` 中 `db_types` 列表里包含的数据库类型，其余类型可省略
- `DB_TYPE` 字段由代码自动注入，**无需**在配置文件中填写
- `secondary` 用于跨库 schema 对比阶段，如无需对比可与 `primary` 填写相同连接信息
- 该文件含有数据库密码，**不应**提交到代码仓库，建议加入 `.gitignore`

---

## 校验配置（CheckConfig）

| 配置项 | 说明 |
|--------|------|
| `DBTypes` | 需要校验的数据库类型列表 |
| `DATABASES` | 数据库名称列表 |
| `CheckType` | 校验范围：`CheckLatest`(最新版本) / `CheckRecently`(最近两个) / `CheckAll`(全部) |
| `AllowNonePrimaryKey` | 是否允许表无主键 |
| `AllowForeignKey` | 是否允许外键约束 |
| `AllowPythonException` | 是否允许 Python 异常 |
| `AllowTableCompareDismatch` | 是否允许跨库表对比不匹配 |

## 阶段一：静态校验

### 目录结构校验

- 版本目录下不允许子目录
- 升级文件必须以 `NN-` 开头（两位数字序号），后缀为 `.sql`、`.py` 或 `.json`
- 同一版本内文件序号不能重复
- `init.sql` 为初始化脚本

### SQL 语句类型校验

#### check_init — 初始化脚本允许的语句

| 语句类型 | MariaDB | DM8 | KDB9 |
|----------|---------|-----|------|
| USE / SET SCHEMA / SET SEARCH_PATH TO | ✅ | ✅ | ✅ |
| CREATE TABLE IF NOT EXISTS | ✅ | ✅ | ✅ |
| CREATE VIEW | ✅ | ✅ | ✅ |
| CREATE OR REPLACE VIEW | ✅ | ✅ | ✅ |
| CREATE \[UNIQUE\] INDEX IF NOT EXISTS | ❌ | ✅ | ✅ |
| INSERT | ✅ | ✅ | ✅ |
| SET IDENTITY_INSERT | ❌ | ✅ | ❌ |

#### check_update — 升级脚本允许的语句

| 语句类型 | MariaDB | DM8 | KDB9 |
|----------|---------|-----|------|
| USE / SET SCHEMA / SET SEARCH_PATH TO | ✅ | ✅ | ✅ |
| CREATE TABLE IF NOT EXISTS | ✅ | ✅ | ✅ |
| CREATE VIEW | ✅ | ✅ | ✅ |
| CREATE OR REPLACE VIEW | ✅ | ✅ | ✅ |
| CREATE \[UNIQUE\] INDEX IF NOT EXISTS | ❌ | ✅ | ✅ |
| INSERT | ✅ | ✅ | ✅ |
| UPDATE | ✅ | ✅ | ✅ |
| DELETE | ❌ | ✅ | ❌ |
| ALTER TABLE | ✅ | ✅ | ✅ |
| DROP TABLE | ✅ | ✅ | ✅ |
| DROP VIEW | ✅ | ✅ | ✅ |
| DROP INDEX | ✅ | ✅ | ✅ |
| RENAME TABLE | ✅ | ❌ | ❌ |
| SET IDENTITY_INSERT | ❌ | ✅ | ❌ |

### 建表语句校验

在 `check_init` 和 `check_update` 中，CREATE TABLE 语句会被深度解析和校验：

**通用校验：**
- 建表语句必须以 `CREATE TABLE IF NOT EXISTS` 开头
- 主键索引必须存在（可通过 `AllowNonePrimaryKey` 放宽）
- 主键和索引中的列必须在表中定义
- 外键约束需配置允许（`AllowForeignKey`）

**MariaDB 特有校验：**
- `TINYINT(1)` 不允许，需使用 `BOOLEAN`
- TEXT/BLOB/JSON 类型不支持非 NULL 默认值
- 表选项支持：ENGINE、AUTO_INCREMENT、COLLATE、COMMENT、DEFAULT CHARSET

**DM8 特有校验：**
- `CHAR` 类型不允许，需使用 `VARCHAR`
- `VARCHAR` 必须使用 `VARCHAR(n CHAR)` 格式
- 整数类型（INT/BIGINT 等）长度必须为空
- TEXT 类型列不允许建索引
- 不支持表选项

**KDB9 特有校验：**
- TEXT/BLOB/JSON 类型不支持非 NULL 默认值
- 不支持表选项

## 阶段二：执行校验

### run_sql — 幂等执行引擎

`run_sql` 是核心执行方法，接收 SQL 语句列表并幂等执行。通过模板方法模式，基类处理通用逻辑，子类 override 差异部分。

#### 主循环分发（base.py）

```
run_sql(sql_list)
  ├── USE / SET SCHEMA / SET SEARCH_PATH TO   → 切换当前数据库
  ├── CREATE  → _run_sql_create()              → 检查对象是否已存在
  ├── DROP    → _run_sql_drop()                → 检查对象是否存在
  ├── ALTER   → _run_sql_alter()               → 子类 override
  ├── RENAME  → _run_sql_rename()              → 子类 override
  └── 其他    → 直接执行（INSERT, UPDATE, DELETE）
```

#### 幂等检查覆盖矩阵

##### CREATE 语句

| 操作 | 处理位置 | MariaDB | DM8 | KDB9 |
|------|---------|---------|-----|------|
| CREATE TABLE | base `_run_sql_create` | 查 QUERY_TABLE_SQL | 查 QUERY_TABLE_SQL | 查 QUERY_TABLE_SQL |
| CREATE VIEW | base `_run_sql_create` | 查 QUERY_VIEW_SQL | 查 QUERY_VIEW_SQL | 查 QUERY_VIEW_SQL |
| CREATE OR REPLACE VIEW | base `_run_sql_create` | 天然幂等，直接执行 | 天然幂等，直接执行 | 天然幂等，直接执行 |
| CREATE \[UNIQUE\] INDEX | base `_run_sql_create_index` | 查 QUERY_INDEX_SQL | 查 QUERY_INDEX_SQL | 直接执行 (QUERY_INDEX_SQL=None) |

##### DROP 语句

| 操作 | 处理位置 | MariaDB | DM8 | KDB9 |
|------|---------|---------|-----|------|
| DROP TABLE | base `_run_sql_drop` | 查 QUERY_TABLE_SQL | 查 QUERY_TABLE_SQL | 查 QUERY_TABLE_SQL |
| DROP VIEW | base `_run_sql_drop` | 查 QUERY_VIEW_SQL | 查 QUERY_VIEW_SQL | 查 QUERY_VIEW_SQL |
| DROP INDEX | 子类 `_run_sql_drop_index` | 解析 `<idx> ON <tbl>` + 查存在 | 直接执行 (SQL 自带 IF EXISTS) | 直接执行 (SQL 自带 IF EXISTS) |

##### ALTER TABLE 语句

| 操作 | MariaDB | DM8 | KDB9 |
|------|---------|-----|------|
| ADD COLUMN | 查 QUERY_COLUMN_SQL | 查 QUERY_COLUMN_SQL | 查 QUERY_COLUMN_SQL |
| DROP COLUMN | 查 QUERY_COLUMN_SQL | 查 QUERY_COLUMN_SQL | 查 QUERY_COLUMN_SQL |
| MODIFY COLUMN | 查 QUERY_COLUMN_SQL | 查 QUERY_COLUMN_SQL (无 COLUMN 关键字) | 查 QUERY_COLUMN_SQL |
| RENAME COLUMN | 查 QUERY_COLUMN_SQL | 查 QUERY_COLUMN_SQL | 查 QUERY_COLUMN_SQL |
| ADD CONSTRAINT | 查 QUERY_CONSTRAINT_SQL | 查 QUERY_CONSTRAINT_SQL | 查 QUERY_CONSTRAINT_SQL |
| DROP CONSTRAINT | 查 QUERY_CONSTRAINT_SQL | 查 QUERY_CONSTRAINT_SQL (无 IF EXISTS) | 查 QUERY_CONSTRAINT_SQL |
| RENAME CONSTRAINT | 不支持 | 查 QUERY_CONSTRAINT_SQL | 查 QUERY_CONSTRAINT_SQL |
| RENAME INDEX | 查 QUERY_INDEX_SQL | N/A (用 ALTER INDEX) | 不支持 |
| RENAME TO (表重命名) | N/A | 查 QUERY_TABLE_SQL | 查 QUERY_TABLE_SQL |

##### 其他语句

| 操作 | MariaDB | DM8 | KDB9 |
|------|---------|-----|------|
| RENAME TABLE | 查 QUERY_TABLE_SQL | N/A (用 ALTER TABLE RENAME TO) | N/A (用 ALTER TABLE RENAME TO) |
| ALTER INDEX RENAME TO | N/A | 直接执行 (无 table_name 无法查存在性) | N/A |
| INSERT / UPDATE / DELETE | 直接执行 | 直接执行 | 直接执行 |

### JSON 升级文件

JSON 文件通过结构化描述调用 CheckRDS 的操作方法，每个操作自带幂等检查。

**支持的操作：**

| object_type | operation_type | 调用方法 |
|-------------|---------------|---------|
| COLUMN | ADD | `add_column()` |
| COLUMN | MODIFY | `modify_column()` |
| COLUMN | RENAME | `rename_column()` |
| COLUMN | DROP | `drop_column()` |
| INDEX / UNIQUE INDEX | ADD | `add_index()` |
| INDEX / UNIQUE INDEX | RENAME | `rename_index()` |
| INDEX / UNIQUE INDEX | DROP | `drop_index()` |
| CONSTRAINT | ADD | `add_constraint()` |
| CONSTRAINT | RENAME | `rename_constraint()` |
| CONSTRAINT | DROP | `drop_constraint()` |
| TABLE | RENAME | `rename_table()` |
| TABLE | DROP | `drop_table()` |
| DB | DROP | `drop_db()` |

### 跨库 Schema 对比

SchemaChecker 在所有脚本执行完毕后，对比不同数据库类型之间的 schema 一致性：

- 各库的表数量必须一致（可通过 `AllowTableCompareDismatch` 放宽）
- 同名表的列数量必须一致

## 数据库差异对照

### 切库语法

| 数据库 | 语法 |
|--------|------|
| MariaDB | `USE {db_name}` |
| DM8 | `SET SCHEMA {db_name}` |
| KDB9 | `SET SEARCH_PATH TO {db_name}` |

### 名称引用方式

| 数据库 | 表名引用 | 示例 |
|--------|---------|------|
| MariaDB | 反引号 | `` `table_name` `` |
| DM8 | 双引号 | `"table_name"` |
| KDB9 | 反引号 | `` `table_name` `` |

### DROP INDEX 语法

| 数据库 | 语法 |
|--------|------|
| MariaDB | `DROP INDEX [IF EXISTS] <idx> ON <tbl>` |
| DM8 | `DROP INDEX [IF EXISTS] <schema>.<idx>` |
| KDB9 | `DROP INDEX [IF EXISTS] <schema>.<idx> CASCADE` |

### ALTER TABLE MODIFY 语法

| 数据库 | 语法 |
|--------|------|
| MariaDB | `ALTER TABLE db.tbl MODIFY COLUMN [IF EXISTS] col_name ...` |
| DM8 | `ALTER TABLE db."tbl" MODIFY col_name ...`（无 COLUMN 关键字） |
| KDB9 | `ALTER TABLE db.tbl MODIFY COLUMN col_name ...` |

### 表重命名语法

| 数据库 | 语法 |
|--------|------|
| MariaDB | `RENAME TABLE [IF EXISTS] db.tbl TO new_name` |
| DM8 | `ALTER TABLE db."tbl" RENAME TO new_name` |
| KDB9 | `ALTER TABLE [IF EXISTS] db.tbl RENAME TO new_name` |

### 数据类型映射

| 类型分类 | MariaDB | DM8 | KDB9 |
|----------|---------|-----|------|
| IntegerType | INTEGER, INT, SMALLINT, TINYINT, MEDIUMINT, BIGINT, BOOLEAN | INTEGER, INT, SMALLINT, TINYINT, BYTE, MEDIUMINT, BIGINT | INTEGER, INT, SMALLINT, TINYINT, MEDIUMINT, BIGINT |
| FixedPointType | DECIMAL, NUMERIC | DECIMAL, NUMERIC | DECIMAL, NUMERIC |
| FloatingPointType | FLOAT, DOUBLE | FLOAT, DOUBLE, REAL | FLOAT, DOUBLE, REAL |
| BooleanType | — (归入 IntegerType) | — | BOOLEAN |
| BitValueType | BIT | BIT | BIT |
| StringType | CHAR, VARCHAR, BINARY, VARBINARY, *BLOB, *TEXT | CHAR, VARCHAR, BINARY, VARBINARY, BLOB, CLOB, TEXT, LONG, LONGVARCHAR | CHAR, VARCHAR, BINARY, VARBINARY, *BLOB, *TEXT |
| DateAndTimeType | DATE, DATETIME, TIMESTAMP, TIME | DATE, DATETIME, TIMESTAMP, TIME | DATE, DATETIME, TIMESTAMP, TIME |

### 功能支持矩阵

| 功能 | MariaDB | DM8 | KDB9 |
|------|---------|-----|------|
| QUERY_INDEX_SQL | ✅ (SHOW INDEX) | ✅ (ALL_INDEXES) | ❌ (None) |
| RENAME_INDEX_SQL | ✅ (ALTER TABLE) | ✅ (ALTER INDEX) | ❌ (None) |
| RENAME_CONSTRAINT_SQL | ❌ (None) | ✅ | ✅ |
| SET IDENTITY_INSERT | ❌ | ✅ | ❌ |
| CASCADE on DROP | ❌ | ✅ | ✅ |
| 表选项 (ENGINE 等) | ✅ | ❌ | ❌ |

## 模板方法架构

```
CheckRDS (base.py, ABC)
  │
  │  核心流程（不可 override）
  │  ├── run_sql()              主循环 + 连接管理
  │  ├── _run_sql_create()      CREATE TABLE/VIEW 通用幂等
  │  ├── _run_sql_drop()        DROP TABLE/VIEW 通用幂等
  │  └── _run_sql_create_index() CREATE INDEX 通用幂等
  │
  │  模板方法（子类可 override）
  │  ├── _run_sql_drop_index()   默认直接执行
  │  ├── _run_sql_alter()        默认直接执行
  │  └── _run_sql_rename()       默认直接执行
  │
  │  抽象方法（子类必须实现）
  │  ├── check_init()
  │  ├── check_update()
  │  ├── get_column_type()
  │  ├── parse_sql_column_define()
  │  ├── check_column()
  │  ├── parse_sql_use_db()
  │  └── get_real_name()
  │
  ├── CheckMariaDB (mariadb.py)
  │     override: _run_sql_drop_index, _run_sql_alter, _run_sql_rename
  │
  ├── CheckDM8 (dm8.py)
  │     override: _run_sql_alter
  │
  └── CheckKDB9 (kdb9.py)
        override: _run_sql_alter
```
