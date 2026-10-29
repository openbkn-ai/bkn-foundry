# SQL 编译与函数规则

本目录负责 Vega SQL 输入解析；公共结构保存在 [interfaces/sqlbuilder](../interfaces/sqlbuilder)，目标 SQL 由各连接器生成。
SQL 查询经过 `compiler.ParseSQL → SelectBuilder → 资源准备 → 连接器 ToSql/执行`。
结构化查询直接构造同一套 Builder 节点，不重新解析 SQL 字符串。

## 公共函数与连接器的边界

- 输入采用 MySQL 风格语法，函数集合由 Vega 显式定义，不限于 MySQL 已有的函数。
- MySQL 已有函数优先沿用其含义；MySQL 没有的函数可以作为 Vega 扩展，但必须先明确签名、参数顺序、返回含义、NULL 行为、编码和精度边界。
- 同一个公共函数名在 Builder 中只有一种含义。连接器可以换函数名、调换源端参数顺序或生成等价表达式，不能仅因源端存在同名函数就直接透传。
- 公共别名优先在编译器中归一化。`OCTET_LENGTH(value)` 与 `LENGTH(value)` 同义，均表示字节长度；Parser 统一构造 `Function{Name: "LENGTH"}`，保留原表达式的默认输出列名。`CHAR_LENGTH` 表示字符长度。
- Parser 检查公共函数名单、参数数量、`DISTINCT` 和 `*` 的调用形式；目标连接器检查目标能力并生成 SQL。字段类型及其隐式转换仍由目标数据库检查。
- 新函数可以逐连接器接入。尚无映射或无法保证已声明语义的参数范围，应返回可分类的能力错误，不回退到 SQLGlot，也不任意透传未知函数或 UDF。

## 内部辅助函数

结构化查询可以直接构造源端辅助节点，这不自动开放同名的公共 SQL 函数。
辅助函数若与公共名称冲突或采用不同的参数语义，使用连接器前缀，并只在对应生成器内映射成实际函数。
例如 HANA 的 `HANA_BINARY_LENGTH` 统计原生二进制长度，`HANA_LOCATE` 使用 HANA 的字符串在前、子串在后顺序；它们都不是公共输入函数。
与公共函数语义相同的节点继续复用公共名称，不需要增加内部标记或另一套 IR。

## 新增函数的步骤

1. 定义公共签名和语义，判断是否为已有函数的别名。扩展函数也必须有明确的只读语义。
2. 更新 `compiler/build.go` 的公共名单和调用形式校验；别名在构造节点前归一化，无须为普通函数名修改 ANTLR 语法。
3. 在目标连接器实现映射，核对参数顺序、NULL、空串、多字节字符、精度和大对象边界；目标原生行为不同则生成等价表达式或明确拒绝该范围。
4. 补充 Parser 的接受/拒绝测试，以及各连接器的 SQL 和绑定值回归测试。实际结果验证放在 EE 对应连接器的实库套件内；不为测试框架再增加测试。
5. 同步 API 能力说明与实库 README，标明已有支持范围和仍需实库验证的边界。不把本地 SQL 生成测试当成实库验证。

Doris 的 SQL 查询同样使用本规则及独立方言生成器。原生函数覆盖公共函数名单，`OCTET_LENGTH` 使用 `LENGTH`；`CAST AS SIGNED` 使用 `BIGINT`，默认字符串使用 `STRING`，显式长度使用 `VARCHAR(n)`。整数字面量在有符号 128 位 LARGEINT 范围内绑定；精确 DECIMAL 暂按默认 38 位能力校验，额外精度能力须另行定义。

## 当前映射与边界

| 函数组 | PostgreSQL | HANA |
| --- | --- | --- |
| `LENGTH` / `OCTET_LENGTH` | 原生 `OCTET_LENGTH` | 字符串编码为 UTF-8 后统计 BLOB 字节长度；结构化二进制元数据使用独立的原生长度辅助函数 |
| `CHAR_LENGTH` / `SUBSTRING` | 字符计数；补齐 MySQL 的零/负起点和负长度行为 | 使用 HANA 原生 CESU-8 字符计数；补充平面字符计为两个单位，与 MySQL 的计数有差异 |
| `CONCAT` / `CONCAT_WS` | 分别保持 NULL 传播 / 跳过 NULL 参数，NULL 分隔符返回 NULL | 同样保持 NULL 规则；保留原生 LOB 表达式，不隐式收窄为 `NVARCHAR(5000)` |
| `COUNT(DISTINCT a,b)` | 排除含 NULL 的组合后计数 | 排除含 NULL 的组合后计数 |
| `CHAR` | 按 MySQL 整数字节拼接后解码 UTF-8；PG text 的 NUL 和非法 UTF-8 由源端拒绝 | 接受 0–127 的 ASCII 整数字面量及 NULL，NULL 参数跳过 |
| `NOW` / `CURRENT_TIMESTAMP` | 本地事务时间；支持字面量精度 0–6 | 原生当前时间；接受无精度参数调用 |
| `SECOND` | 截断小数秒，返回整数秒 | 截断小数秒，返回整数秒 |
| `DATE_FORMAT` / `STR_TO_DATE` | 转换已列明的字面量格式标记 | 转换已列明的字面量格式标记；格式范围见 API |

以上是当前能力边界，不代表完整 MySQL 运行时兼容。字符排序规则、隐式类型转换、非法日期和数值溢出仍受目标数据库约束。
HANA 的 CESU-8 计数是现有边界，不作为后续新增函数默许语义漂移的依据。

公共名单和调用校验以 [compiler/build.go](compiler/build.go) 为准，用户可用能力见 [Raw Query API](../../../../docs/api/vega/raw-query.yaml)。

语义核对参考：[MySQL 字符串函数](https://dev.mysql.com/doc/refman/8.4/en/string-functions.html)、[MySQL 日期时间函数](https://dev.mysql.com/doc/refman/8.4/en/date-and-time-functions.html)、[PostgreSQL 字符串函数](https://www.postgresql.org/docs/current/functions-string.html)、[HANA SQL Reference](https://help.sap.com/doc/9b40bf74f8644b898fb07dabdd2a36ad/2.0.08/en-US/SAP_HANA_SQL_Reference_Guide_en.pdf)。

## Oracle 19c

Oracle SQL 查询使用公共 MySQL 输入语法，经 Parser/Builder 后由 EE Oracle 连接器生成 SQL。
使用 `:pN` 命名参数及驱动原生 NUMBER 绑定，独立于 `NLS_NUMERIC_CHARACTERS`；精确数值最多 38 位有效数字；当前驱动将极小 NUMBER 折叠为零，故绑定只接受十进制指数 -128..125。校验和驱动编码使用同一规范化数值表示，保证不同科学计数法写法不会改变值。
相同分组表达式复用命名绑定，HAVING 投影别名在本 SELECT 内展开；SQL 标识符保留原始大小写。
分页使用原生 OFFSET/FETCH，offset-only 无需补 LIMIT。GROUP BY 数字交给源端，19c 不支持位置分组。

- 常用聚合、数值、大小写、去空白、替换和空值函数使用显式映射。复合 `COUNT(DISTINCT a,b)` 返回能力错误。
- `CHAR` 接受 0–127 ASCII 整数字面量及 NULL；`CHAR_LENGTH` 使用 LENGTH，LENGTH/OCTET_LENGTH 使用 LENGTHB。
  字节长度以源端字符集为准；多字节 CLOB 的 LENGTHB 等原生限制由数据库返回错误。
- CONCAT/CONCAT_WS 补齐 NULL 传播/跳过规则；Oracle 将空字符串折叠为 NULL，是本目标的明确边界，不能从已存字段恢复空串。
  保留 LOB，不将结果隐式收窄为 VARCHAR2；源端长度限制仍适用。
- NOW/CURRENT_TIMESTAMP 使用会话时区的 LOCALTIMESTAMP，精度 0–6；SECOND 截断小数秒。
  DATE_FORMAT/STR_TO_DATE 使用明确格式模型和英文日期语言，支持 `%Y/%m/%d/%H/%h/%i/%s/%S/%f/%p/%%` 及常用分隔符。
  STR_TO_DATE 须包含完整 `%Y/%m/%d`，12 小时制须配合 `%p`；DATE_FORMAT 可以只输出年份等片段。
- 日期函数和日期 CAST 对 ISO 日期/日期时间字符串字面量先校验日历，再使用明确格式转换，支持空格或 T 分隔、最多六位小数秒。
  动态文本字段或文本计算结果须先使用 STR_TO_DATE；原生日期/时间字段仍由源端检查类型。
- REPLACE 显式保持 NULL 传播，已知空替换串仍执行删除；NULLIF 的首参数为 NULL 字面量时直接返回 NULL。
- CAST 支持 NUMBER、BINARY_FLOAT/DOUBLE、DATE、TIMESTAMP 及最多 4000 长度的 VARCHAR2；其他目标明确拒绝。
  Oracle 的转换、非法日期、排序规则及空串规则仍按源端运行时语义执行。

Raw SQL 的 RAW/LONG RAW/BLOB 保留原始字节（JSON 输出 base64），NUMBER 保留精度，SQL NULL 保持 null；
结构化查询的 binary_mode 不影响这条路径。Oracle 结构化查询本阶段仍使用原有构造方式。
源码生成和 mock 测试不代表已完成 Oracle 实库验收。

Oracle SQL 生成按 1 MiB 预算限制（含参数占位符预留），嵌套条件改写超过预算时在连接源库前返回能力错误，避免短输入展开为无界 SQL。
