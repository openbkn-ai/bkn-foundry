# SQL 编译与函数规则

本目录负责 Vega SQL 输入解析；公共结构保存在 [interfaces/sqlbuilder](../interfaces/sqlbuilder)，目标 SQL 由各连接器生成。
SQL 查询经过 `compiler.ParseSQL → SelectBuilder → 资源准备 → 连接器 ToSql/执行`。
结构化查询直接构造同一套 Builder 节点，不重新解析 SQL 字符串。

SQL 来源支持带括号的 JOIN 组，例如 `a LEFT JOIN (b JOIN c ON ...) ON ...`。
Parser 使用 `JoinedTable` 保留括号与连接边界，资源准备递归绑定组内全部来源；不会展开成多个外连接而改变 NULL 扩展语义。

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
结构化查询的 binary_mode 不影响 SQL 查询路径。Oracle 结构化查询直接构造同一套 Builder，保留原生 NULL 排序、精确 NUMBER 绑定、日期/时区、CSV 匹配和日历分组语义；binary_mode 继续控制结构化二进制预览，LONG RAW 长度在扫描时计算。内部 ORACLE_ 辅助函数不开放给公共 SQL。COUNT 独立执行，不承诺与数据查询共享快照。
结构化 BINARY_FLOAT/DOUBLE 条件按原生浮点范围绑定，通过规范化科学计数法文本和显式格式/NLS 设置转换，不经过驱动的 NUMBER 编码；SUM/AVG/MIN/MAX 的 HAVING 沿用源字段类型，COUNT 仍绑定为 NUMBER。原生 FLOAT 属于 NUMBER，保留精确数值的精度和范围限制。
源码生成和 mock 测试不代表已完成 Oracle 实库验收。

Oracle SQL 生成按 1 MiB 预算限制（含参数占位符预留），嵌套条件改写超过预算时在连接源库前返回能力错误，避免短输入展开为无界 SQL。


## SQL Server

SQL Server SQL 输入同样采用 mysql/mariadb，经 Parser → Builder → EE SQL Server 生成器执行；不再接受独立的 T-SQL 输入方言。
使用 `@pN` 命名绑定、方括号标识符及 DECIMAL(38) 精度边界；分组表达式共享命名参数。资源准备阶段从 schema_definition 填充 Table.ColumnNames，保留源字段大小写；EE 生成器按不区分大小写比较源字段和投影别名。GROUP BY/HAVING 优先引用同名源字段，只在源字段名列表完整且无同名字段时展开当前 SELECT 的投影别名。
子查询按输出列判断，SQL 中的星号不展开；字段信息未知时保留源端解析。
单次执行最多 2098 个业务绑定，为驱动 sp_executesql 保留两个 RPC 参数槽；旧版 ROW_NUMBER 分页的两个绑定也计入此上限，因此分页前最多 2096 个绑定。
LIMIT 0（包括非零 OFFSET）生成 TOP (0)，LIMIT-only 使用 TOP，其他分页使用 OFFSET/FETCH；offset-only 不补最大 LIMIT。
子查询中的排序保留在同一 SELECT 内，以 TOP 或 OFFSET 0 满足源端限制。GROUP BY 位置语义交给 SQL Server 检查。

- SQL 查询通道要求数据库兼容级别至少 110；结构化查询直接构造同一套 Builder，并在低兼容级别保留 ROW_NUMBER 分页。
- 结构化查询保留 binary_mode、原生整数 AVG、CSV/正则能力检查和日历分桶语义；内部 SQLSERVER_ 辅助函数不开放给公共 SQL。COUNT 独立执行，不承诺与数据查询的快照一致。
- COUNT 使用 COUNT_BIG；复合 COUNT DISTINCT、正则、无符号/JSON CAST 及未知函数返回能力错误。
- 精确数值绑定为显式 BIGINT/DECIMAL；科学计数法使用 FLOAT(53)。数值运算的结果精度遵循 SQL Server 的类型推导与溢出规则。
- CONCAT 保持 NULL 传播，CONCAT_WS 跳过 NULL；字符串计算使用 NVARCHAR(MAX)，不隐式截断至 4000 字符。
- CHAR_LENGTH 保留尾随空格，使用补充字符 collation 按 Unicode 码点计数；LENGTH/OCTET_LENGTH 使用 UTF-8 collation 计算字节数，要求 SQL Server 2019+。
- SUBSTRING 补齐零/负起点、NULL 与非正长度；LIKE 的字面模式额外转义 SQL Server 的 `[`，动态模式明确拒绝。
- CHAR 接受 ASCII 整数字面量和 NULL；NOW/CURRENT_TIMESTAMP 精度为 0–6。日期取值使用 DATEPART，DATE_FORMAT 使用固定英文文化的 FORMAT。
  STR_TO_DATE 只接受已映射的完整 ISO 日期/日期时间格式，格式详见 EE SQL Server README。
- 除法避免整数截断；除法与取模都通过 NULLIF 保护零除数。来源类型不兼容时由源端拒绝。

Raw SQL 的 GUID 使用原生结果元数据解码，DECIMAL 保留文本精度，二进制保留字节，TIME 使用既有格式；ColumnInfo 内部的 NativeType 不参与 JSON 输出。
R01–R04 已定义 Parser/Builder、函数、分页/COUNT、持续 Rows 和 NULL/EOF 断言；尚未执行 SQL Server 实库验收。
