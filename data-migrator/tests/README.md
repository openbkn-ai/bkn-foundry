# 单元测试说明

## 运行方式

在**项目根目录**执行：

```bash
python3 -m pytest
```

运行单个文件：

```bash
python3 -m pytest tests/test_version.py
```

运行单个用例：

```bash
python3 -m pytest tests/test_lint_mariadb.py::TestParseTableOptions::test_default_charset
```

加 `-v` 输出每条用例名称，加 `-q` 精简输出：

```bash
python3 -m pytest -v   # 详细
python3 -m pytest -q   # 精简
```

## 依赖

仅需 `pytest`，无需数据库连接，无需环境变量：

```bash
pip install pytest
```

## 测试文件说明

### `test_token.py` — SQL token 解析

覆盖 `server/utils/token.py` 的三个函数：

| 函数 | 覆盖要点 |
|------|---------|
| `next_token` | 普通词、反引号/单/双引号、`=` 分隔符剥除、`(` 停止、空串 |
| `next_tokens` | 批量消费、不足时提前返回、size=0 |
| `find_matching_paren` | 嵌套括号、引号内括号跳过、无闭合返回 -1 |

> `find_matching_paren` 修复了 `rfind(")")` 在 `COMMENT = '...(...)...'` 场景下指向错误位置的 bug。

---

### `test_version.py` — 版本号工具

覆盖 `server/utils/version.py`：

| 函数 / 类 | 覆盖要点 |
|-----------|---------|
| `compare_version` | 基础大小、不等长补零、非数字报错 |
| `sort_versions` | 数字序（1.10 > 1.9）而非字典序 |
| `get_max_version` / `get_min_version` | 空列表返回 None |
| `is_version_dir` | 合法格式、带前缀 / 含字母 / 空串 |
| `extract_number` | 正常提取、init.sql / 无编号文件报错 |
| `VersionUtil` | `<` `>=` `==`、`sorted()` 内置排序、hash |

---

### `test_lint/test_lint_mariadb.py` — MariaDB 静态校验

覆盖 `server/lint/rds/mariadb.py`：

| 类 / 方法 | 覆盖要点 |
|-----------|---------|
| `check_init` | 首句必须是 `USE`、合法语句类型白名单、`CREATE OR REPLACE VIEW` |
| `check_update` | `ALTER` / `DROP` / `RENAME` / DML 合法、非法类型报错 |
| `_parse_and_check_create_table` | `IF NOT EXISTS` 可选、反引号表名、无主键报错、外键控制 |
| `_parse_table_options` | `DEFAULT CHARSET`、`DEFAULT CHARACTER SET`、完整选项串、非法关键字 |
| `_parse_table_options` (回归) | `COMMENT = '...(...)...'` 含括号时不误判表结尾 |
| `check_column` | TEXT/JSON/BLOB 有非 NULL 默认值报错 |

---

### `test_lint/test_lint_dm8.py` — DM8 静态校验

覆盖 `server/lint/rds/dm8.py`，重点测试 DM8 特有规则：

| 场景 | 规则 |
|------|------|
| 首句 | 必须是 `SET SCHEMA` |
| 建表 | `IF NOT EXISTS` 可选，不允许表选项 |
| 主键 | `CLUSTER PRIMARY KEY` |
| `SET IDENTITY_INSERT` | init / update 中合法 |
| `check_column` | `CHAR` 类型禁用；`VARCHAR` 必须使用 `VARCHAR(n CHAR)` 格式；整数类型不允许指定长度 |
| TEXT 字段建索引 | 报错（通过直接构造 Table 对象验证） |

---

### `test_lint/test_lint_kdb9.py` — KDB9 静态校验

覆盖 `server/lint/rds/kdb9.py`，重点测试 KDB9 特有规则：

| 场景 | 规则 |
|------|------|
| 首句 | 必须是 `SET SEARCH_PATH TO`（与 DM8 的 `SET SCHEMA` 不同） |
| 建表 | `IF NOT EXISTS` 可选，不允许表选项 |
| `check_update` | `INSERT` / `UPDATE` / `DELETE` 均允许 |

---

### `test_rds_config.py` — RDS 配置与 secret_config 加载

覆盖 `server/config/models.py`（RDSConfig）和 `server/config/loader.py`（load_config）：

| 类 / 场景 | 覆盖要点 |
|-----------|---------|
| `TestRDSConfigSourceTypeValidation` | `internal` / `external` 合法；非法值、空串、大小写不匹配均报 ValueError |
| `TestLoaderSourceTypeDefault` | `depServices.rds` 格式加载；`source_type` 默认 `internal`；非法值报错 |
| `TestSecretLoading` | secret_config 文件存在时覆盖 `depServices`；文件不存在静默跳过；`secret_config_path=None` 时使用 config |

> verify 命令通过 `--verify-rds-config` 参数指定多 DB 对比配置路径；migrate 命令通过 `--secret-config` 参数指定依赖服务连接配置路径，不再依赖 `VERIFY_RDS_CONFIG` / `SECRET_CONFIG_PATH` 环境变量。

---

### `test_fetch_executor.py` — Fetch 执行器

覆盖 `server/fetch/executor.py`，使用 `pytest` 的 `tmp_path` / `monkeypatch` fixture 构造临时目录，无网络/git 依赖：

| 类 / 方法 | 覆盖要点 |
|-----------|---------|
| `FetchExecutor._collect_repos` | 正常复制、db_type 缺失回退 DEFAULT_DB_TYPE、两者均缺失报错、多服务 × 多 db_type |
| `FetchExecutor._copy_version_dirs` | 只复制版本号目录、非版本目录跳过、空源目录不报错 |

---

### `test_script_selector.py` — 迁移脚本选择器

覆盖 `server/migrate/script_selector.py`，使用 `pytest` 的 `tmp_path` fixture 构造临时目录，无真实 DB 依赖：

| 方法 | 覆盖要点 |
|------|---------|
| `get_all_versions` | 版本目录识别、非版本目录过滤、db_type 隔离 |
| `find_init_sql` | 逆序查找、降级到旧版本、无 init.sql 返回 None |
| `_collect_scripts_from_dir` | 按编号排序、跳过 init.sql、过滤无编号文件 |
| `select_upgrade_scripts` | 首次安装（`installed_version=None`）、升级跳过已安装版本、已是最新无脚本、跨版本顺序、仅 init.sql 的版本不产生脚本 |

---

### `test_parser_mariadb.py` — MariaDB 解析器

覆盖 `server/db/dialect/_parser/base.py`（RDSParser）和 `server/db/dialect/_parser/mariadb.py`（MariaDBParser）：

| 方法 | 覆盖要点 |
|------|---------|
| `_parse_column_len` | 数字、`n CHAR`、`n,m` 精度、无括号返回 None |
| `_parse_column_unsigned` | 有 / 无 `UNSIGNED` |
| `_parse_default_value` | 字符串、数字、NULL、函数调用 `NOW()`、带参函数 `CURRENT_TIMESTAMP(3)` |
| `get_real_name` | 反引号剥除、尾部分号、`.` / `"` / `'` 非法字符 |
| `get_real_column_name` | 反引号、`col(191)` 长度后缀截取 |
| `parse_sql_use_db` | 普通 / 反引号、缺少 db 名报错 |
| `parse_sql_column_define` | 完整列定义（类型、长度、NULL、DEFAULT、COMMENT、CHARACTER SET、COLLATE、UNSIGNED、AUTO_INCREMENT）|
| `get_column_type` | 全类型参数化覆盖（17 种）、UNKNOWN、大小写不敏感 |


### 
测试用的容器启动脚本

docker run -d -p 5237:5236 --name dm8-1 -e SYSDBA_PWD= -e UNICODE_FLAG=1 -e LENGTH_IN_CHAR=1 -e COMPATIBLE_MODE=4 -e CASE_SENSITIVE=0 -e PAGE_SIZE=16 -e LD_LIBRARY_PATH=/opt/dmdbms/bin dm8:dm8_20250206_rev257733_x86_rh6_64

docker run -d -p 5238:5236 --name dm8-2 -e SYSDBA_PWD= -e UNICODE_FLAG=1 -e LENGTH_IN_CHAR=1 -e COMPATIBLE_MODE=4 -e CASE_SENSITIVE=0 -e PAGE_SIZE=16 -e LD_LIBRARY_PATH=/opt/dmdbms/bin dm8:dm8_20250206_rev257733_x86_rh6_64

docker run -d -p 54322:54321 --privileged --name kingbase-1 --shm-size=4g -e TZ=Asia/Shanghai -e DB_MODE=mysql -e DB_USER=system -e DB_PASSWORD= kingbase_v008r006c009b0014_single:v1 /usr/sbin/init

docker run -d -p 54323:54321 --privileged --name kingbase-2 --shm-size=4g -e TZ=Asia/Shanghai -e DB_MODE=mysql -e DB_USER=system -e DB_PASSWORD= kingbase_v008r006c009b0014_single:v1 /usr/sbin/init

docker run -d -p 3330:3306 --name mariadb-1 -e MARIADB_ROOT_USER=root -e MARIADB_ROOT_PASSWORD= mariadb:11.4.7 --lower-case-table-names=1 --skip-name-resolve

docker run -d -p 3331:3306 --name mariadb-2 -e MARIADB_ROOT_USER=root -e MARIADB_ROOT_PASSWORD= mariadb:11.4.7 --lower-case-table-names=1 --skip-name-resolve

docker run -d -p 3332:3306 --name mysql-1 -e MYSQL_ROOT_USER=root -e MYSQL_ROOT_PASSWORD= mysql:8.0.45 --lower-case-table-names=1 --skip-name-resolve

docker run -d -p 3333:3306 --name mysql-2 -e MYSQL_ROOT_USER=root -e MYSQL_ROOT_PASSWORD= mysql:8.0.45 --lower-case-table-names=1 --skip-name-resolve
