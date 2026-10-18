# BKN 语言规范

版本：3.0.0（草案）

本文描述本目录 Go 实现（`bkn/`）实际接受和产出的 BKN 格式。它是 BKN 格式的唯一权威说明：
文档与代码不一致时，先确认哪一方是预期行为，再改另一方，不允许两者长期分叉。
bkn-sdk 中的 `SPECIFICATION.llm.md`、`bkn-validate.ts` 与 create-bkn 模板都跟随本文同步。

每一节都按同一结构书写：语法、字段、解析行为、校验规则。「解析行为」描述解析器遇到缺省、
写错或多余内容时会怎样，这是写 BKN 文件的人最容易踩坑的地方。

当前实现与本文期望不一致之处集中列在[附录 A](#附录-a已知问题)，修复前以「现状」为准。

---

## 1. 概述

BKN（Business Knowledge Network）是基于 Markdown 的声明式建模语言，用来描述业务知识网络中的
对象类、关系类、行动类、风险类、概念分组与对象指标。BKN 只描述模型，不包含执行逻辑：
数据读取、行动执行、指标计算由消费模型的平台实现。

### 1.1 术语

| 术语 | 含义 |
|------|------|
| 知识网络（knowledge_network） | 一组定义的顶层容器，对应目录根部的 `network.bkn` |
| 对象类（object_type） | 业务对象的类型，如订单、物料 |
| 关系类（relation_type） | 连接两个对象类的关系 |
| 行动类（action_type） | 绑定到对象类、调用工具或 MCP 工具执行的操作 |
| 风险类（risk_type） | 对执行风险的结构化建模；BKN 文件只承载其名称与说明 |
| 概念分组（concept_group） | 把相关对象类组织在一起 |
| 对象指标（object metric V1） | 归属于对象类、带版本的指标定义，以 JSON 归档 |
| 资源（resource） | Vega 中的数据资源；对象类和间接关系通过资源 ID 读取数据 |
| 能力依赖（capabilities） | 网络依赖的 Skill、工具箱工具与 MCP 工具的声明 |
| 定义 | 上述任一类型的单个实例，即一个 `.bkn` 文件或一个指标 JSON 文件 |

### 1.2 规范版本

BKN 包在 `network.bkn` 的 frontmatter 中用 `bkn_spec` 声明自己按哪个规范版本书写：

```yaml
bkn_spec: "3.0"
```

| 声明 | 读取规则 |
|------|----------|
| 不写，或写 `2.0` / `2.0.x` | 2.x 规则。这是 `bkn_spec` 出现之前所有包的读法，**永久保持不变**，包括附录 A 中标注「2.x 现状」的行为 |
| `3.0`、`3` 或 `3.0.x` | 3.0 规则：本文标注「3.0」的条款生效 |
| 其他任何值 | 拒绝导入，错误信息为 `unsupported bkn_spec`；不会按猜测读取 |

- 版本只在 `network.bkn` 中声明，对整个包生效；定义文件里写 `bkn_spec` 没有作用。
- 写成不带引号的 `3.0` 也可以：YAML 会把它读成数字 3，解析器按 `3.0` 处理。
- 导出器目前**不写** `bkn_spec`，也就是导出的包都按 2.x 读取。3.0 导入后入库的数据已经是修正后的含义，
  按当前列布局导出、再按 2.x 读回时含义不变。
- 不认识 `bkn_spec` 的旧版解析器会忽略这个字段，按 2.x 读取：不报错，只是得不到 3.0 的修正。

3.0 相对 2.x 的区别：

| 条款 | 2.x | 3.0 |
|------|-----|-----|
| `### Tool Configuration` 小节（§8.6） | 解析为 Action Source，同时并入描述 | 只解析为 Action Source |
| 旧参数表 `Parameter \| Source \| Binding`（§8.7） | `Binding` 按 `ValueFrom` 读取，值的来源错位 | 转换为当前含义 |
| 参数的 `ValueFrom` 不是 `property` / `input` / `const`（§8.7，含逻辑属性参数） | 原样导入，执行时该参数被丢弃 | 拒绝导入，并指出是哪个参数 |

3.0 的拒绝原因会出现在上传接口的错误详情中。

---

## 2. 目录结构与加载

### 2.1 目录

一个知识网络就是一个目录：

```text
{network_dir}/
├── network.bkn          # 必需，根文件
├── SKILL.md             # 可选，Agent Skill 入口
├── CHECKSUM             # 可选，一致性校验
├── object_types/        # *.bkn
├── relation_types/      # *.bkn
├── action_types/        # *.bkn
├── risk_types/          # *.bkn
├── concept_groups/      # *.bkn
├── object_metrics/      # <id>@<version>.json
└── capabilities/        # 规划中，见 §13.2；当前解析器忽略
```

也可以打成压缩包上传，包内结构与目录相同。支持三种格式，按文件开头的字节识别，与文件名无关：

| 格式 | 识别方式 | 说明 |
|------|----------|------|
| tar | 不是下面两种时都按 tar 读取 | 导出使用的格式 |
| tar.gz | 以 gzip 标识 `1f 8b` 开头 | 解压后按 tar 读取；解压后的数据流不超过 256 MB |
| zip | 以 `PK` 标识开头 | 压缩包本身不超过 128 MB；macOS「压缩」生成的 `__MACOSX/` 目录被跳过 |

上传时以 `application/octet-stream` 之外的类型提交，文件名须以 `.tar`、`.tgz`、`.tar.gz` 或 `.zip` 结尾（不区分大小写）。

三种格式按同一套规则读取：

- **根目录**：`network.bkn` 所在的目录。包里有多个 `network.bkn` 时，以最后读到的为准。
- **读取的文件**：`.bkn`、`.md`、`CHECKSUM`，以及 `object_metrics/` 下的 `.json`；其他文件忽略。
  macOS 生成的 `._*` 扩展属性文件被跳过。
- **拒绝的条目**：绝对路径、含 `..` 或反斜杠的路径、符号链接等非普通文件，以及旧版 `metrics/` 目录。
- **大小限制**：最多 10000 个条目；读取的单个文件不超过 16 MB，合计不超过 64 MB。
  zip 条目实际内容超过其头部声明的大小时拒绝。

导出始终是未压缩的 tar。

### 2.2 加载行为

| 情形 | 行为 |
|------|------|
| 根目录没有 `network.bkn` | 报错 |
| `SKILL.md` 存在 | 全文读入，参与 CHECKSUM；不参与模型解析 |
| 存在 `CHECKSUM` | 先校验；不一致时打印警告并继续加载，但含对象指标的网络此时拒绝加载 |
| 存在 `metrics/` 目录 | 报错：旧版 `metrics/*.bkn` 已不支持，须先迁移为对象指标 V1 |
| 子目录中的非 `.bkn` 文件 | 忽略（`object_metrics/` 除外，见 §11） |
| 子目录中的嵌套目录 | 忽略（`object_metrics/` 中出现时报错） |
| 根目录中除 `network.bkn` 外的 `.bkn` 文件 | 忽略 |
| 上述 6 个子目录之外的目录 | 忽略；包括规划中的 `capabilities/`（§13.2），其中的文件也不参与 CHECKSUM |

**定义的类型由所在子目录决定，不由 frontmatter 的 `type` 决定。** 例如放在 `object_types/`
下的文件一律按对象类解析，即使 frontmatter 写的是 `type: relation_type`。frontmatter 的
`type` 只在计算 CHECKSUM 时使用（见 §12）。文件名不参与解析，导出时文件名为 `{id}.bkn`。

---

## 3. 文件结构

### 3.1 组成

每个 `.bkn` 文件由 YAML frontmatter 和 Markdown 正文组成，UTF-8 编码，可带 BOM：

```markdown
---
type: object_type
id: material
name: 物料
tags: [主数据]
---

## ObjectType: 物料

物料是……

### Data Properties
...
```

frontmatter 以文件开头的 `---` 开始，以下一个独占一行的 `---` 结束。文件不以 `---` 开头时，
视为没有 frontmatter。frontmatter 不是合法 YAML 时，整个文件解析失败。

### 3.2 标题层级

| 层级 | 用途 |
|------|------|
| `#` | 网络标题，仅用于 `network.bkn` |
| `##` | 定义标题，如 `## ObjectType: {name}`；网络文件中为 `## Network Overview` 等 |
| `###` | 定义内的小节，如 `### Data Properties` |
| `####` | 小节内的子项，目前只用于逻辑属性 |

解析器按标题层级识别结构，**不读取 `##` 标题中冒号后的名称**：名称以 frontmatter 的 `name`
为准。`##` 标题的文字也不校验，写成 `## Object: x` 同样能解析。

### 3.3 描述

定义的描述是正文中第一个 `##` 标题之后、第一个 `###` 小节之前的文字；网络文件则是 `#` 标题之后、
第一个 `##` 之前的文字。

**未知小节并入描述。** 标题不在该类型已知小节列表中的 `###` 小节，会以
`### {标题}\n\n{内容}` 的形式按原顺序追加到描述末尾。它们不会丢失，但只作为文字保存，
不会被结构化解析。各类型的已知小节见各自章节。小节标题区分大小写，必须与本文完全一致。

**摘要** 取描述的第一句：截止到第一个 `。`、`. `（句点加空格）或换行，取三者中最早出现的位置。
摘要用于网络概览表与概念分组表的说明列。

### 3.4 表格

表格使用标准 Markdown 语法，第一行为表头，第二行为分隔行。

- **列名**区分大小写，只接受本文列出的英文写法；不接受中文列名。列的顺序无关紧要。
- **分隔行**可以省略：第二行不像分隔行时，从第二行起都按数据行处理。
- **缺少的单元格**按空字符串处理；多出表头的单元格被忽略。
- **单元格内的竖线**写作 `\|`。
- **单元格内的换行**写作 `<br>`；字面意义的 `<br>` 写作 `&lt;br&gt;`。
- **被换行截断的行**：上一行不以 `|` 结尾、且当前行不以 `#` 开头时，当前行接到上一行末尾。
  这是为了兼容早期未转义换行的导出文件，新文件应使用 `<br>`。
- 每个小节只读取其中的第一张表格。

### 3.5 YAML 代码块

条件等嵌套结构写在 ```` ```yaml ```` 代码块中。每个小节只读取第一个 yaml 代码块；
代码块不是合法 YAML 时，该小节被视为空，不报错。

---

## 4. 通用 frontmatter 与命名规则

### 4.1 通用字段

所有定义文件都有以下字段：

| 字段 | 必需 | 说明 |
|------|:----:|------|
| `type` | 是 | 文件类型，见各章节；实际类型由所在子目录决定（§2.2） |
| `id` | 是 | 定义 ID，网络内同类型唯一 |
| `name` | 是 | 显示名称 |
| `tags` | 否 | 标签列表，写作 `[a, b]` 或 YAML 列表；写成单个字符串时视为一个标签 |

frontmatter 中的其他字段除各章节明确列出的之外，一律被忽略，不报错（见附录 A-5）。

### 4.2 校验规则

| 对象 | 规则 |
|------|------|
| `id` | 匹配 `^[a-z0-9][a-z0-9_-]{0,39}$`：小写字母或数字开头，可含小写字母、数字、`_`、`-`，最长 40 个字符 |
| `name` | 非空，最长 40 个字符（按 Unicode 字符计） |
| `tags` | 最多 5 个；每个去掉首尾空格后非空、最长 40 个字符，且不含下列任一字符：`` / : ? \ " < > \| ： ？ ‘ ’ “ ” ！ 《 》 , # [ ] { } % & * $ ^ ! = . ' `` |
| 重复 | 网络内同类型的 `id` 不得重复；对象类、行动类、风险类、概念分组的 `name` 也不得重复。关系类的 `name` 可以重复（如多个「属于」），与后端一致 |
| 属性名 | 数据属性与逻辑属性的 `Name` 匹配 `^[a-zA-Z0-9][a-zA-Z0-9_-]{0,39}$` |

---

## 5. 网络文件（network.bkn）

### 5.1 frontmatter

```yaml
---
type: knowledge_network
id: supply_chain
name: 供应链
tags: [制造]
bkn_spec: "3.0"       # 可选，规范版本，见 §1.2
version: "1.0"        # 可选
branch: main          # 可选
capabilities:         # 可选，见 §13
  skills: []
  functions: []
  mcp_tools: []
---
```

| 字段 | 必需 | 说明 |
|------|:----:|------|
| `type` | 是 | `knowledge_network`；缺失时解析失败 |
| `id` | 是 | 网络 ID；缺失时解析失败 |
| `name` | 是 | 网络名称 |
| `tags` | 否 | 同 §4 |
| `bkn_spec` | 否 | 本包遵循的规范版本，见 §1.2；与 `version` 无关 |
| `version` | 否 | 网络自身的版本号，原样保存 |
| `branch` | 否 | 分支名，原样保存 |
| `capabilities` | 否 | 能力依赖声明，见 §13 |

### 5.2 正文

```markdown
# 供应链

供应链网络覆盖物料、BOM 与采购……

## Network Overview
...
```

`#` 标题之后、第一个 `##` 之前是网络描述。`## Network Overview`、`## Directory Structure`
等 `##` 小节由导出器生成，导入时忽略，手工修改它们不会产生任何效果。

---

## 6. 对象类（object_types/*.bkn）

### 6.1 语法

```markdown
---
type: object_type
id: material
name: 物料
tags: [主数据]
---

## ObjectType: 物料

物料主数据。

### Data Source

| Type | ID | Name |
|------|----|------|
| resource | res_material | 物料表 |

### Data Properties

| Name | Display Name | Type | Description | Mapped Field |
|------|--------------|------|-------------|--------------|
| material_id | 物料编码 | string | 主键 | material_id |
| material_name | 物料名称 | string | | material_name |
| updated_at | 更新时间 | datetime | | updated_at |

### Logic Properties

### Keys

Primary Keys: material_id
Display Key: material_name
Incremental Key: updated_at
```

已知小节：`Data Source`、`Data Properties`、`Logic Properties`、`Keys`。

| 小节 | 必需 | 说明 |
|------|:----:|------|
| Data Properties | 是 | 缺少该小节时校验报错 |
| Keys | 是 | 缺少该小节时校验报错 |
| Data Source | 否 | 不写表示对象类不绑定资源 |
| Logic Properties | 否 | 可写空小节 |

### 6.2 Data Source

| 列 | 说明 |
|----|------|
| `Type` | 只能是 `resource` |
| `ID` | Vega 资源 ID |
| `Name` | 资源名称，仅供阅读 |

只读第一行。索引的配置与构建在 Vega 中管理，不在 BKN 中声明。

### 6.3 Data Properties

| 列 | 必需 | 说明 |
|----|:----:|------|
| `Name` | 是 | 属性名，规则见 §4.2；为空时整个文件解析失败 |
| `Display Name` | 是 | 显示名，非空，最长 40 个字符 |
| `Type` | 否 | 数据类型，见 §6.4 |
| `Description` | 否 | 说明；属性级业务规则写在这里 |
| `Mapped Field` | 否 | 映射到资源中的字段名 |
| `Mask Rule` | 否 | 脱敏规则，JSON 对象，见 §6.5 |

数据属性最多 1000 个。

### 6.4 数据类型

`Type` 不区分大小写。取值必须在下表中，否则校验报错。

| 类型 | 可作主键 | 可作显示键 | 可作增量键 |
|------|:--------:|:----------:|:----------:|
| `string` | ✓ | ✓ | |
| `text` | ✓ | ✓ | |
| `keyword` | | | |
| `integer` | ✓ | ✓ | ✓ |
| `unsigned integer` | ✓ | ✓ | |
| `float` | | ✓ | |
| `decimal` | | ✓ | |
| `boolean` | | ✓ | |
| `date` | | ✓ | |
| `time` | | ✓ | |
| `datetime` | | ✓ | ✓ |
| `timestamp` | | ✓ | ✓ |
| `json` | | | |
| `binary` | | | |
| `vector` | | | |
| `point` | | | |
| `shape` | | | |
| `ip` | | | |

### 6.5 Mask Rule

`Mask Rule` 列写一个 JSON 对象，`kind` 决定其余字段：

| kind | 字段 |
|------|------|
| `fixed` | `replacement` |
| `partial` | `keep_start`、`keep_end`、`replacement` |
| `email` | `local_keep_start`、`preserve_domain`、`replacement` |
| `round` | `step` |
| `date_granularity` | `granularity` |

不属于该 `kind` 的字段会被拒绝；规则是否适用于属性类型由 `common/maskrule` 校验。
JSON 不合法时整个文件解析失败。JSON 中的竖线写作 `|`。

只要有一个属性带脱敏规则，导出时整张表都带 `Mask Rule` 列；否则不输出该列。

### 6.6 Keys

```markdown
### Keys

Primary Keys: order_id, line_no
Display Key: order_name
Incremental Key: updated_at
```

| 行 | 必需 | 规则 |
|----|:----:|------|
| `Primary Keys:` | 是 | 逗号分隔，至少一个；每个都必须是已定义的数据属性，类型可作主键 |
| `Display Key:` | 是 | 一个；必须是已定义的数据属性，类型可作显示键 |
| `Incremental Key:` | 否 | 可留空；非空时必须是已定义的数据属性，类型可作增量键 |

### 6.7 Logic Properties

每个逻辑属性一个 `####` 子项，子项下用粗体标签分隔四张表：

```markdown
### Logic Properties

#### lead_time_days

**Meta**

| Display Name | Type | Description |
|--------------|------|-------------|
| 交期天数 | tool | 按供应商计算交期 |

**Source**

| Source Type | Source ID | Source Name | BoxID | ToolID | ResultPath |
|-------------|-----------|-------------|-------|--------|------------|
| tool | | | box_supply | tool_lead_time | $.days |

**Parameters**

| Name | Type | Source | Operation | ValueFrom | Value | Description |
|------|------|--------|-----------|-----------|-------|-------------|
| supplier_id | string | Query | | property | supplier_id | 供应商 |
| as_of | string | Query | | input | | 查询日期 |

**Analysis Dimensions**

| Name | Display Name | Type | Description |
|------|--------------|------|-------------|
```

`####` 后的文字就是属性名。粗体标签必须独占一行，写作 `**Meta**` 等。逻辑属性最多 1000 个。

**Meta**：`Display Name` 必需，最长 40 个字符；`Type` 为 `metric` 或 `tool`。

**Source**：

| 列 | 说明 |
|----|------|
| `Source Type` | `metric` 或 `tool`；与 Meta 的 `Type` 都写时必须一致 |
| `Source ID` | `metric` 时必需，为指标 ID |
| `Source Name` | 仅供阅读 |
| `BoxID` | `tool` 时必需，工具箱 ID；也接受 `Box ID`、`Toolbox ID` |
| `ToolID` | `tool` 时必需，工具 ID；也接受 `Tool ID` |
| `ResultPath` | 仅 `tool` 可写，从工具结果中取值的 JSONPath；也接受 `Result Path` |

**Parameters** 与行动类的参数表同义，见 §8.7；3.0 下同样要求 `ValueFrom` 为 `property`、`input` 或 `const`。
逻辑属性的参数表不接受旧列布局。

**Analysis Dimensions**：分析维度，列为 `Name`、`Display Name`、`Type`、`Description`。

没有任何 `####` 子项时，解析器把小节内的第一张表当作扁平写法读取（列 `Name`、`Display Name`、
`Type`、`Description`），这种写法只有元数据，没有来源与参数。

---

## 7. 关系类（relation_types/*.bkn）

### 7.1 语法

```markdown
---
type: relation_type
id: material_in_bom
name: 物料属于BOM
tags: []
---

## RelationType: 物料属于BOM

### Endpoint

| Source | Target | Type |
|--------|--------|------|
| material | bom | direct |

### Mapping Rules

| Source Property | Target Property |
|-----------------|-----------------|
| material_id | material_id |
```

已知小节：`Endpoint`、`Mapping Rules`、`Backing Resource`、`Source Mapping`、`Target Mapping`、
`Source Condition`、`Target Condition`。

### 7.2 Endpoint

| 列 | 必需 | 说明 |
|----|:----:|------|
| `Source` | 是 | 起点对象类 ID，必须在网络中已定义 |
| `Target` | 是 | 终点对象类 ID，必须在网络中已定义 |
| `Type` | 是 | 映射方式：`direct`、`indirect` 或 `filtered_cross_join`，区分大小写 |

只读第一行。`Type` 决定读取哪些小节，其余小节被忽略。

### 7.3 direct：属性直接匹配

```markdown
### Mapping Rules

| Source Property | Target Property |
|-----------------|-----------------|
| supplier_id | supplier_id |
```

至少一行；两列都不能为空；同一对映射不得重复。两列都为空的行被跳过。

### 7.4 indirect：经由中间资源

```markdown
### Backing Resource

| Type | ID |
|------|----|
| resource | res_bom_line |

### Source Mapping

| Source Property | Resource Property |
|-----------------|-------------------|
| bom_id | parent_bom_id |

### Target Mapping

| Resource Property | Target Property |
|-------------------|-----------------|
| child_material_id | material_id |
```

| 小节 | 规则 |
|------|------|
| Backing Resource | 必需；`Type` 只能是 `resource`，`ID` 非空 |
| Source Mapping | 至少一行；两列非空；不得重复 |
| Target Mapping | 至少一行；两列非空；不得重复 |

### 7.5 filtered_cross_join：按条件交叉连接

起点和终点各自按条件筛选，筛选结果两两相连，不做键映射：

````markdown
### Source Condition

```yaml
field: category
operation: ==
value: 原材料
```

### Target Condition

```yaml
field: status
operation: ==
value: 启用
```
````

两个小节都必需。条件结构见 §8.5，但没有 `object_type_id` 字段；也可以包在 `condition:` 或
`trigger_condition:` 键下。

---

## 8. 行动类（action_types/*.bkn）

### 8.1 语法

````markdown
---
type: action_type
id: restart_pod
name: 重启Pod
tags: [运维]
action_intent: modify
---

## ActionType: 重启Pod

重启处于异常状态的 Pod。

### Bound Object

| Bound Object |
|--------------|
| pod |

### Trigger Condition

```yaml
condition:
  object_type_id: pod
  field: status
  operation: ==
  value: Failed
```

### Action Source

| Type | BoxID | ToolID | McpID | ToolName |
|------|-------|--------|-------|----------|
| tool | box_k8s | tool_restart | | |

### Parameter Binding

| Name | Type | Source | Operation | ValueFrom | Value | Description |
|------|------|--------|-----------|-----------|-------|-------------|
| pod_name | string | Body | | property | name | Pod 名称 |
| reason | string | Body | | input | | 重启原因 |

### Schedule

| Type | Expression |
|------|------------|
````

已知小节：`Bound Object`、`Affect Object`、`Impact Contracts`、`Trigger Condition`、
`Action Source`、`Parameter Binding`、`Schedule`。

### 8.2 frontmatter

| 字段 | 说明 |
|------|------|
| `action_intent` | 行动意图：`add`、`modify` 或 `delete`（不区分大小写）。推荐使用 |
| `action_type` | 已废弃，与 `action_intent` 同义，仍兼容 |

两者只写一个时，另一个取相同的值；都没写时，取 `Bound Object` 表 `Action Type` 列的值。
导出时两个字段都写出。

### 8.3 Bound Object

| 列 | 说明 |
|----|------|
| `Bound Object` | 绑定的对象类 ID；非空时必须在网络中已定义 |
| `Action Type` | 可选，旧写法，作用同 `action_intent` |

只读第一行。可以不绑定对象类，此时不能写 `Trigger Condition`，参数也不能取自对象属性。

### 8.4 影响声明

**Impact Contracts**（推荐）写成 yaml 代码块：

```yaml
impact_contracts:
  - object_type_id: inventory
    expected_operation: modify
    description: 扣减库存
    affected_fields: [quantity]
```

`object_type_id` 必须在网络中已定义；`expected_operation` 为 `add`、`modify` 或 `delete`。
也可以省略 `impact_contracts:` 键，直接写列表。

**Affect Object**（旧写法）是一张表，列为 `Affect Object` 和 `Affect Description`，只读第一行。

### 8.5 Trigger Condition

````markdown
### Trigger Condition

```yaml
condition:
  operation: and
  sub_conds:
    - object_type_id: pod
      field: status
      operation: ==
      value: Failed
    - object_type_id: pod
      field: restart_count
      operation: <
      value: 3
```
````

| 字段 | 说明 |
|------|------|
| `object_type_id` | 条件所针对的对象类 |
| `field` | 属性名；叶子条件必需 |
| `operation` | 操作符，必需，不区分大小写 |
| `sub_conds` | `and` / `or` 的子条件，最多 100 个 |
| `value_from` | 值的来源，可选 |
| `value` | 比较值 |

条件可以包在 `condition:` 或 `trigger_condition:` 键下，也可以直接写。

可用操作符：

| 类别 | 操作符 |
|------|--------|
| 逻辑 | `and`、`or` |
| 比较 | `==`、`!=`、`>`、`>=`、`<`、`<=`、`eq`、`not_eq`、`gt`、`gte`、`lt`、`lte` |
| 集合 | `in`、`not_in`、`contain`、`not_contain` |
| 区间 | `range`、`out_range`、`between`、`before`、`current` |
| 文本 | `like`、`not_like`、`prefix`、`not_prefix`、`regex` |
| 空值与存在 | `empty`、`not_empty`、`null`、`not_null`、`exist`、`not_exist` |
| 布尔 | `true`、`false` |

### 8.6 Action Source

| 列 | 说明 |
|----|------|
| `Type` | `tool` 或 `mcp` |
| `BoxID` | `tool` 时填写，工具箱 ID；也接受 `Box ID`、`Toolbox ID` |
| `ToolID` | `tool` 时填写，工具 ID；也接受 `Tool ID` |
| `McpID` | `mcp` 时填写，MCP 服务 ID；也接受 `MCP ID`、`Mcp ID` |
| `ToolName` | `mcp` 时填写，MCP 工具名；也接受 `Tool Name` |

只读第一行。`tool` 不能填 MCP 的两列，`mcp` 不能填工具箱的两列。

标题也可以写作 `### Tool Configuration`，同样解析为 Action Source，列名也可以用 `Toolbox ID`、`Tool ID`、`MCP ID`、`Tool Name`。
2.x 下这一节还会被当作未知小节并入描述（附录 A-1）；3.0 下不会。新文件请使用 `### Action Source`。

### 8.7 Parameter Binding

行动类的参数表与逻辑属性的 **Parameters** 表结构相同：

| 列 | 说明 |
|----|------|
| `Name` | 参数名，必需；也接受旧列名 `Parameter` |
| `Type` | 参数类型，如 `string`、`number`、`boolean`、`object`、`array` |
| `Source` | 参数在工具请求中的位置，如 `Query`、`Body`、`Header`、`Path`；原样传给执行方，不校验 |
| `Operation` | 指标参数的比较操作符；行动参数通常为空 |
| `ValueFrom` | 值的来源：`property`（对象属性）、`input`（运行时输入）、`const`（常量）；也接受列名 `Value From`、`Binding` |
| `Value` | `ValueFrom` 为 `property` 时写属性名，为 `const` 时写常量值，为 `input` 时留空 |
| `Description` | 说明 |

注意：值的来源写在 `ValueFrom`，不是 `Source`。

**旧列布局** `Parameter | Type | Source | Binding | Description` 把值的来源写在 `Source`、
属性名或常量写在 `Binding`（`input` 时写 `-`）。

- 2.x：`Binding` 按 `ValueFrom` 读取，值的来源错位，参数在执行时被丢弃（附录 A-2）。
- 3.0：表中有 `Binding` 列且没有 `ValueFrom` 列、并且该行 `Source` 是 `property` / `input` / `const`
  之一时，`Source` 转为 `ValueFrom`，`Binding` 转为 `Value`，`Source` 置空。

**3.0 的校验**：每个参数的 `ValueFrom` 必须是 `property`、`input` 或 `const`，否则拒绝导入。
值的来源误写在 `Source` 列时，错误信息会直接指出。2.x 不做这项检查（附录 A-4）。

导出一律使用当前列布局。

### 8.8 Schedule

| 列 | 说明 |
|----|------|
| `Type` | `FIX_RATE` 或 `CRON` |
| `Expression` | 间隔或 cron 表达式 |

只读第一行，可以留空表。

---

## 9. 风险类（risk_types/*.bkn）

```markdown
---
type: risk_type
id: inventory_adjust_risk
name: 库存调整风险
tags: []
---

## RiskType: 库存调整风险

库存调整需要双人复核……
```

BKN 文件只承载风险类的通用字段与描述，没有已知小节：正文中的所有 `###` 小节都并入描述。
风险类的管控函数与参数在平台上配置，不随 BKN 文件导入导出。

---

## 10. 概念分组（concept_groups/*.bkn）

```markdown
---
type: concept_group
id: procurement
name: 采购
tags: []
---

## ConceptGroup: 采购

### Object Types

| ID | Name | Description |
|----|------|-------------|
| supplier | 供应商 | |
| purchase_order | 采购订单 | |
```

已知小节：`Object Types`。只读取 `ID` 列，每个 ID 都必须是网络中已定义的对象类；
`Name` 和 `Description` 列由导出器根据对象类自动填写，导入时忽略。

---

## 11. 对象指标 V1（object_metrics/*.json）

对象指标以 JSON 文件归档，每个版本一个文件，文件名必须是 `{id}@{version}.json`：

```json
{
  "definition": {
    "id": "monthly_purchase_amount",
    "version": 2,
    "name": "月采购额",
    "description": "……"
  }
}
```

| 规则 | 说明 |
|------|------|
| `definition.id` | 只含字母、数字、`-`、`_`、`.`，不能是 `.` 或 `..` |
| `definition.version` | 正整数 |
| 文件名 | 必须与 `id`、`version` 一致，否则报错 |
| 文件类型 | `object_metrics/` 下出现非 `.json` 文件或子目录时报错 |
| CHECKSUM | 含对象指标的网络，CHECKSUM 不一致时拒绝加载 |

文件中是完整的指标记录，包括定义、生命周期状态与持久化元数据，导出时包含所有版本，
以保证固定版本的依赖在导入后仍可解析。指标定义的具体结构以 bkn-backend 对象指标接口为准，
BKN 解析器只读取 `definition` 中的身份字段。

---

## 12. CHECKSUM

`CHECKSUM` 文件记录每个定义的哈希，用于发现文件被改动或缺失：

```text
# BKN Directory Checksum
# format: 2
# generated: 2026-10-15T16:52:54+08:00
network  sha256:1a2b3c4d5e6f7a8b
object_type:material  sha256:...
relation_type:material_in_bom  sha256:...
object_metric_v1:monthly_purchase_amount@2  sha256:...
SKILL.md  sha256:...
```

| 规则 | 说明 |
|------|------|
| 键 | 根据 frontmatter 的 `type` 和 `id` 生成：网络为 `network`，其他为 `{type}:{id}` |
| 哈希 | SHA-256 的前 8 字节，16 位十六进制 |
| 规范化 | 统一换行符，去掉行尾空白与空行后再计算 |
| 格式 1 | 没有 `# format:` 头的旧文件；只对正文计算哈希 |
| 格式 2 | 当前格式；把 frontmatter（键排序后）也纳入哈希，改名、改标签、改能力依赖都会改变哈希 |

---

## 13. 能力依赖（capabilities）

`network.bkn` frontmatter 中的 `capabilities` 声明网络依赖哪些 Skill、工具箱工具和 MCP 工具。
它只是依赖声明，不携带能力本身：Skill 包、工具箱、MCP 服务通过各自的导入导出渠道迁移，
类似 package.json 只列依赖、不打包依赖。

```yaml
capabilities:
  skills:
    - id: skill_8f2c
      name: 交期评估
  functions:
    - box_id: box_supply
      tool_id: tool_bom_expand
      box_name: 供应链计算
      tool_name: BOM 多层展开
  mcp_tools:
    - mcp_id: mcp_erp
      mcp_name: ERP 查询
      tool_name: query_po
```

| 类别 | 字段 | 说明 |
|------|------|------|
| `skills` | `id`、`name` | Skill |
| `functions` | `box_id`、`tool_id`、`box_name`、`tool_name` | 工具箱中的工具，包括函数工具和 OpenAPI 工具 |
| `mcp_tools` | `mcp_id`、`mcp_name`、`tool_name` | MCP 工具；工具本身按名称寻址 |

**为什么同时写 ID 和名称：** ID 由各环境自行生成，跨环境不保留；名称是跨环境导入时唯一能匹配的依据。

**导入行为：**

- 先按 ID 匹配，匹配不到再按名称匹配。
- 匹配不到的条目不会让导入失败，而是在导入结果中列为跳过项并说明原因。
- 上传时 `binding_policy=detach` 会忽略整段声明。
- 这一段格式错误时被整体忽略，网络仍可导入。

### 13.1 跨环境迁移（现状）

BKN 包只携带对工具的**引用**，不携带工具的实现：

- Action Source 与逻辑属性的 tool 来源只写环境内的 ID（§6.7、§8.6），执行时按 ID 找工具。
- `capabilities` 写 ID 和名称，但只用于挂载能力绑定，不会改写行动与逻辑属性里的 ID。

因此把包导入到另一个环境之前，需要先把用到的能力搬过去：

| 能力 | 搬迁方式 | 导入后 ID |
|------|----------|-----------|
| 工具箱（OpenAPI 工具、函数工具） | 执行工厂 `GET /impex/export/toolbox/{box_id}` 导出，`POST /impex/import/toolbox` 导入；导出内容包含 OpenAPI 元数据、函数代码与服务地址 | 保留 `box_id`、`tool_id` |
| MCP 服务 | 执行工厂 `GET /impex/export/mcp/{mcp_id}` 导出，`POST /impex/import/mcp` 导入；包含服务配置、工具列表及依赖的工具箱 | 保留 `mcp_id` |
| Skill | 下载 zip 包后在目标环境注册 | **不保留**，注册时生成新 ID，只能靠 `capabilities` 中的名称匹配 |

按「先导入工具箱与 MCP，再导入 BKN 包」的顺序，行动与逻辑属性的 ID 引用可以直接对上。注意：

- 顺序反过来时导入不会失败，但 `capabilities` 中的条目会被列为跳过项，行动指向的工具也不存在。
- 目标环境已有同 ID 的工具时，impex 的 `create` 模式会报错，`upsert` 模式会覆盖。
- 工具箱的服务地址、MCP 的连接地址按原样导入，可能需要按目标环境修改。

### 13.2 内嵌能力实现（规划中，尚未实现）

为了让一个 BKN 包就能完成迁移，包内可以选择性地携带能力的实现：

```text
{network}/
├── network.bkn
├── capabilities/                 # 可选
│   ├── toolbox/<box_id>.json     # 执行工厂 impex 导出的原样内容
│   ├── mcp/<mcp_id>.json         # 执行工厂 impex 导出的原样内容
│   └── skill/<name>.zip          # Skill 包
└── ...
```

已确定的原则：

- **复用现有格式**：工具箱与 MCP 直接使用执行工厂 impex 的导出内容，Skill 使用其 zip 包，不另定义格式。
- **只带引用的包仍然有效**：没有 `capabilities/` 目录时，导入行为与 §13 相同。
- **向后兼容**：当前解析器忽略该目录，带内嵌实现的包在旧版本上仍可按只带引用的方式导入。
- **导出默认不变**：携带实现需要导出时显式开启，不开启时导出内容与现在相同。

待设计：

- 导入顺序（预期为：内嵌能力 → 模型 → 能力绑定）与失败时的处理。
- 目标环境已有同 ID 或同名能力时的冲突策略：跳过、覆盖或报错。
- 服务地址、连接地址等环境相关配置的替换方式，以及凭证是否导出。
- CHECKSUM 覆盖 `capabilities/` 下的文件。
- Skill 导入后 ID 变化时，如何回写 `capabilities` 与引用。

---

## 14. 导入与导出

### 14.1 导入

导入以定义为单位按 `(网络, 类型, id)` 新增或覆盖。BKN 文件不能表达删除；
删除定义需调用相应的删除接口。

上传接口的参数：

| 参数 | 取值 | 缺省 |
|------|------|------|
| `branch` | 分支名 | `main` |
| `import_mode` | `normal`、`ignore`、`overwrite` | `overwrite` |
| `strict_mode` | 布尔 | `false` |
| `binding_policy` | `preserve`、`detach` | `preserve` |

各导入模式下已存在定义如何处理、归档中缺失的定义是否会被删除，以 bkn-backend 接口文档为准。

### 14.2 导出

导出器按固定格式输出，保证同一模型导出的文件逐字节相同：

- frontmatter 字段按固定顺序输出；`tags` 总是输出，即使为空。
- frontmatter 的值原样写出，不加引号（见附录 A-3）。
- 网络文件附带 `## Network Overview`（各类定义一览表）和 `## Directory Structure`（目录树）。
- 各类定义按 ID 排序。
- 对象类总是输出 `Data Source`、`Data Properties`、`Logic Properties`、`Keys` 四个小节，
  行动类总是输出 `Bound Object`、`Affect Object`、`Trigger Condition`、`Action Source`、
  `Parameter Binding`、`Schedule`，没有内容的输出空表。
- 逻辑属性的 Source 表在来源为 `tool` 时输出 6 列，否则输出 3 列。

---

## 15. 业务规则放在哪里

BKN 只保存本文定义的结构。业务规则按作用范围放在：

| 范围 | 位置 |
|------|------|
| 网络 | `network.bkn` 的 `#` 标题之后的描述 |
| 定义 | 定义的 `##` 标题之后、第一个 `###` 之前的描述 |
| 属性 | Data Properties 表的 `Description` 列 |

不要依赖自定义的 frontmatter 字段：它们会被忽略。自定义的 `###` 小节会并入描述，
内容保留但不被结构化。

---

## 附录 A：已知问题

以下是实现与预期不一致、或格式本身有缺口的地方，修复后从本附录移除并更新正文。

| # | 问题 | 现状 | 影响 |
|---|------|------|------|
| A-1 | `### Tool Configuration` 不在行动类已知小节中 | 2.x 现状：能被解析为 Action Source，但整节同时并入描述。3.0 已修正 | 按 SDK 模板写的行动，描述里多出一段工具表 |
| A-2 | 旧列名 `Binding` 映射到 `ValueFrom` | 2.x 现状：旧布局把 `property` 等写在 `Source`、属性名写在 `Binding`，读取后值的来源错位。3.0 已修正 | 按 SDK 模板写的行动参数在执行时被静默丢弃；已入库的数据不会自动修复 |
| A-3 | 导出时 frontmatter 由字符串拼接生成 | 值不加引号；导出端尚未按版本区分 | 名称含 `: ` 时导出文件无法再导入；`001`、`1.0` 等读回后变成 `1` |
| A-4 | 当前列布局下把值的来源写进 `Source` | 2.x 现状：`Source` 写 `property`、属性名写在 `ValueFrom` 的表（bkn-sdk 的 `SPECIFICATION.llm.md` 对逻辑属性就是这样教的）按字面读取。3.0 拒绝导入 | 2.x 下该参数在执行时被静默丢弃；SDK 文档需修正 |
| A-5 | 未知的 frontmatter 字段与 `###` 小节都被静默处理 | 字段被丢弃，小节并入描述，都不提示 | 拼错的小节名、`enabled` / `risk_level` / `requires_approval` 等字段写了无效且没人知道 |
| A-6 | 往返丢失字段 | 对象类与关系类的 `icon`、`color`，参数的 `required`、`default`、`if_system_generate` 都没有 BKN 写法 | 导出再导入后这些设置丢失 |
| A-7 | 工具引用只有 ID | Action Source 与逻辑属性的 tool 来源只写环境内 ID，没有名称 | 跨环境导入后 capabilities 能按名称匹配，行动和逻辑属性仍指向不存在的 ID；现状的规避方式见 §13.1 |
| A-8 | 引用未与能力依赖对照 | 校验不检查行动与逻辑属性引用的工具是否在 `capabilities` 中声明 | 依赖清单可能不完整 |
| A-9 | 子目录与 frontmatter `type` 可以不一致 | 解析看子目录，CHECKSUM 看 `type` | 同一文件在两处被当作不同类型 |
| A-10 | 能力依赖缺少版本与接口信息 | 只有 ID 和名称 | 按名称匹配到的工具与原工具参数不兼容时无法发现 |
| A-11 | BKN 包不携带能力实现 | 函数代码、OpenAPI 定义、MCP 配置、Skill 包都要另外通过执行工厂搬迁（§13.1） | 跨环境迁移需要多步手工操作，顺序错误时静默失败；规划见 §13.2 |

## 附录 B：相对 2.0.1 的变化

2.0.1 是 `kweaver-ai/bkn-specification` 仓库中的最后版本（2026-05）。本仓库在 2026-07
只迁入了 Go 实现，规范文档没有随之迁移。之后的实现变化如下：

| 变化 | 2.0.1 | 3.0.0 |
|------|-------|-------|
| 数据来源类型 | `data_view` | `resource`（Vega 资源） |
| 间接关系 | `data_view` 类型 + `### Mapping View` + `View Property` 列 | `indirect` 类型 + `### Backing Resource` + `Resource Property` 列 |
| 关系映射方式 | `direct`、`data_view` | `direct`、`indirect`、`filtered_cross_join` |
| 逻辑属性 | 列表式元数据，类型 `metric` / `operator` | 四张表（Meta / Source / Parameters / Analysis Dimensions），类型 `metric` / `tool` |
| 参数表 | `Parameter`、`Source`、`Binding` | `Name`、`Type`、`Source`、`Operation`、`ValueFrom`、`Value`、`Description` |
| 行动执行配置 | `### Tool Configuration` | `### Action Source`（旧标题仍可读，见 A-1） |
| 行动类小节 | 含 `Pre-conditions`、`Scope of Impact`、`Execution Description` | 不再结构化解析，写了会并入描述 |
| 行动类 frontmatter | 含 `enabled`、`risk_level`、`requires_approval` | 不再解析，写了会被忽略 |
| Bound Object 的 Action Type | 含 `query` | 只有 `add`、`modify`、`delete` |
| 风险类小节 | `Control Scope`、`Control Policy` 等 | 不再结构化解析，全部并入描述 |
| 指标 | `metrics/*.bkn` | `object_metrics/{id}@{version}.json`；旧目录报错 |
| 数据属性 | 5 列 | 增加可选的 `Mask Rule` 列；类型增加 `keyword`、`unsigned integer`、`timestamp`、`vector`、`point`、`shape`、`ip` |
| 网络 frontmatter | 含 `business_domain` | 去掉 `business_domain`，增加 `version`、`branch`、`capabilities`、`bkn_spec` |
| 规范版本声明 | 无 | `bkn_spec`；不声明的包按 2.x 规则读取（§1.2） |
| CHECKSUM | 只对正文计算 | 格式 2 纳入 frontmatter |
| 列名 | 声称接受中文列名 | 只接受英文列名 |
| 实例数据 | `data/*.csv` | 不再属于 BKN 包；数据由资源提供 |
