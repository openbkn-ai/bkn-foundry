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
| 能力依赖（capabilities） | 网络挂载的 Skill、工具箱工具与 MCP 工具的声明 |
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

- 版本只在 `network.bkn` 中声明，对整个包生效；定义文件里写 `bkn_spec` 在 2.x 下没有作用，3.0 下按未列出的字段拒绝（§1.3）。
- 写成不带引号的 `3.0` 也可以：YAML 会把它读成数字 3，解析器按 `3.0` 处理。
- 网络不含 3.0 声明时，导出器不写 `bkn_spec`，导出的包按 2.x 读取；含 3.0 声明时写 `bkn_spec: "3.0"` 并按 3.0 的写法输出（§14.2）。3.0 导入后入库的数据已经是修正后的含义，
  按当前列布局导出、再按 2.x 读回时含义不变。
- 不认识 `bkn_spec` 的旧版解析器会忽略这个字段，按 2.x 读取：不报错，只是得不到 3.0 的修正。

3.0 相对 2.x 的区别：

| 条款 | 2.x | 3.0 |
|------|-----|-----|
| `### Tool Configuration` 小节（§8.6） | 解析为 Action Source，同时并入描述 | 拒绝，错误信息给出 `### Action Source`（§1.3） |
| 旧参数表 `Parameter \| Source \| Binding`（§8.7） | `Binding` 按 `ValueFrom` 读取，值的来源错位 | 拒绝，错误信息给出按当前列布局改写后的写法（§1.3） |
| 参数的 `ValueFrom` 不是 `property` / `input` / `const`（§8.7，含逻辑属性参数） | 原样导入，执行时该参数被丢弃 | 拒绝导入，并指出是哪个参数 |
| 属性声明：`Semantic`、`Required` 列与 `### Property Constraints`（§6.8） | 新列忽略，小节并入描述 | 解析为声明，写错时拒绝 |
| 别名：frontmatter `aliases` 与 `Aliases` 列（§6.8、§7.6、§8.2） | 字段与列忽略 | 解析为别名，写错时拒绝 |
| 生命周期 `lifecycle`，关系主次 `importance` / `fallback_for` 与可信度 `verification`（§4.3、§7.6） | 字段忽略 | 解析为声明，写错时拒绝 |
| 关系声明：`reverse_name`、`Cardinality`、`### Relation Constraints`、关系属性（§7.6） | 新字段忽略，小节并入描述 | 解析为声明，写错时拒绝 |
| 值类型 `value_types/`（§16） | 目录忽略 | 解析为值类型定义 |
| 同义写法、未知内容、被忽略的多余内容（§1.3） | 静默接受、忽略或并入描述 | 每样东西只有一种写法，其余拒绝并指出位置 |

3.0 的拒绝原因会出现在上传接口的错误详情中。

### 1.3 3.0 只保留一种写法

设计见 [bkn-docs issue-168](https://github.com/openbkn-ai/bkn-docs/blob/main/docs/foundry/bkn/design/issue-168-property-and-relation-declarations.md)。

2.x 为了兼容接受多种同义写法，并静默忽略不认识或多余的内容，写错的地方不报错、不生效。3.0 每样东西只保留一种写法，其余一律拒绝，写错立即可见。各节中「也接受」「旧写法」「被忽略」的说明只适用于 2.x，下文列出的例外（生成内容）除外；`capabilities` 的 3.0 规则见 §13。

| 对象 | 3.0 唯一写法 | 3.0 拒绝（括号内为 2.x 的处理） |
|------|--------------|--------------------------------|
| frontmatter 字段 | 本文列出的字段 | 未列出的字段（静默丢弃） |
| frontmatter `type` | 与所在子目录一致 | 不一致（以目录为准，`type` 不起作用） |
| `tags`、`aliases` | YAML 列表 | 单个字符串（视为一个元素） |
| `##` 定义标题 | `## {前缀}: {name}`，前缀与类型对应（`ObjectType`、`RelationType`、`ActionType`、`RiskType`、`ConceptGroup`、`ValueType`），`name` 与 frontmatter 相同 | 前缀或名称不一致（不读标题文字） |
| `###` 小节 | 该类型的已知小节；描述中以 `#` 开头的行写作 `\#`（导出器自动转义，见 §14.2） | 未知小节（并入描述）。风险类例外：它没有已知小节，正文全部是描述 |
| 表格列 | 本文列出的列名，每列至多一次 | 未知列（忽略）；同名列重复 |
| 表格行 | 有分隔行；每行以 `\|` 开头和结尾；单元格数不超过表头 | 缺少分隔行（按数据行读）；被换行截断的行（拼接到上一行）；多出的单元格（忽略） |
| 只取一行的表 | `Data Source`、`Endpoint`、`Backing Resource`、`Bound Object`、`Action Source`、`Schedule` 只写一行数据 | 多行（只读第一行） |
| 小节内的表格与 YAML 块 | 每个小节至多一张表或一个 YAML 块；YAML 必须合法 | 第二张表或第二个块（忽略）；YAML 不合法（小节视为空） |
| 行动意图 | frontmatter `action_intent` | `action_type` 字段；`Bound Object` 表的 `Action Type` 列 |
| 行动的工具 | `### Action Source`，列 `BoxID`、`ToolID`、`McpID`、`ToolName` | `### Tool Configuration`；列 `Box ID`、`Toolbox ID`、`Tool ID`、`MCP ID`、`Mcp ID`、`Tool Name` |
| 逻辑属性 | `####` 子项写法；Source 列 `BoxID`、`ToolID`、`ResultPath` | 无 `####` 的扁平表；列 `Box ID`、`Toolbox ID`、`Tool ID`、`Result Path` |
| 参数表 | 列 `Name`、`ValueFrom` | 列 `Parameter`、`Value From`、`Binding`；旧列布局 `Parameter \| Source \| Binding`（3.0 原为自动转换，改为拒绝并提示改写） |
| 条件 YAML（`Trigger Condition`、`Source Condition`、`Target Condition`） | 直接写条件 | 包在 `condition:` 或 `trigger_condition:` 键下 |
| `Impact Contracts` | `impact_contracts:` 键下的列表 | 省略键、直接写列表 |
| `### Keys` 中的行 | `Primary Keys:`、`Display Key:`、`Incremental Key:` | 其他行（忽略） |
| 逻辑属性子项中的粗体标签 | `**Meta**`、`**Source**`、`**Parameters**`、`**Analysis Dimensions**` | 其他标签（忽略） |
| 关系类的映射小节 | 只写 `Endpoint` 的 `Type` 对应的小节 | 与 `Type` 不符的小节，如 `indirect` 关系写了 `Mapping Rules`（忽略） |

- **生成内容不在收紧范围。** `network.bkn` 的 `## Network Overview`、`## Directory Structure` 与概念分组表的 `Name`、`Description` 列由导出器生成，导入时仍然忽略、不报错，导出的包才能原样导入。手工修改它们没有效果。
- **条件操作符不在收紧范围。** `==` 与 `eq` 等同义操作符沿用后端的取值集合。
- **`capabilities` 的 3.0 规则见 §13**：格式写错时拒绝；匹配不到的条目仍列为跳过项；不要求行动与逻辑属性引用的工具写入。
- **错误信息必须可定位。** 每条拒绝都指出文件、小节、行或字段，并给出唯一写法，例如「`Tool ID` 不是 3.0 列名，请改为 `ToolID`」。
- **3.0 导出只写唯一写法。** 按 3.0 导出时（§14.2），导出器只输出上表「唯一写法」一列的形式；2.x 导出不变。
- **显式写默认值不算另一种写法。** `importance: primary`、`lifecycle` 的 `status: active`、非主键属性的 `Required` 写 `false`，与省略等价，3.0 接受；导出时省略：`importance` 与 `lifecycle` 不输出，`Required` 列输出时非必填与主键属性都留空。

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
├── value_types/         # *.bkn；3.0，见 §16；2.x 忽略
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

**3.0**：描述中以 `#` 开头的行写作 `\#`；3.0 导入时去掉行首的一个 `\`，导出时自动加上（§14.2）。

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

frontmatter 中的其他字段除各章节明确列出的之外，2.x 下一律被忽略，不报错（见附录 A-5）；3.0 下拒绝（§1.3）。

### 4.2 校验规则

| 对象 | 规则 |
|------|------|
| `id` | 匹配 `^[a-z0-9][a-z0-9_-]{0,39}$`：小写字母或数字开头，可含小写字母、数字、`_`、`-`，最长 40 个字符 |
| `name` | 非空，最长 40 个字符（按 Unicode 字符计） |
| `tags` | 最多 5 个；每个去掉首尾空格后非空、最长 40 个字符，且不含下列任一字符：`` / : ? \ " < > \| ： ？ ‘ ’ “ ” ！ 《 》 , # [ ] { } % & * $ ^ ! = . ' `` |
| 重复 | 网络内同类型的 `id` 不得重复；对象类、行动类、风险类、概念分组、值类型的 `name` 也不得重复。关系类的 `name` 可以重复（如多个「属于」），与后端一致 |
| 属性名 | 数据属性与逻辑属性的 `Name` 匹配 `^[a-zA-Z0-9][a-zA-Z0-9_-]{0,39}$` |

### 4.3 生命周期（3.0）

> **状态：3.0。** 规范库已实现解析、校验与导出。bkn-backend 存储对象类、关系类、行动类的 `lifecycle`，接口读写它，保存时按本节规则检查，跨定义的规则（`replaced_by`、端点弃用）在任何一方保存时都检查，删除仍被 `replaced_by` 引用的定义时拒绝；上传与导出 3.0 包时随包往返。`deprecated` 不进概念召回仍在实现中（bkn-foundry#2284）。设计见 [bkn-docs issue-168](https://github.com/openbkn-ai/bkn-docs/blob/main/docs/foundry/bkn/design/issue-168-property-and-relation-declarations.md)。

对象类、关系类、行动类的 frontmatter 可写 `lifecycle`，取值与含义参考 Palantir 的 status；接口与 JSON 导入导出中是同名、同结构的字段：

```yaml
lifecycle:                      # 不写即 active
  status: deprecated            # active | deprecated
  reason: 改用「工单的报工」     # deprecated 时必填
  replaced_by: order_has_report # 可选，同类型定义的 ID
```

字段名不用 `status`：对象类接口已有 `status`（索引信息），数据属性的语义取值也有 `status`（§6.8）。

| 状态 | 含义 | 使用方 |
|------|------|--------|
| `active` | 正式在用；不写即为此值 | 照常使用 |
| `deprecated` | 计划淘汰，不应再依赖 | 默认不进概念召回，按 ID 仍可查询；有 `replaced_by` 时提示改用替代定义 |

- 端点对象类为 `deprecated` 时，关系类只能是 `deprecated`；不满足时拒绝，不自动改写。
- 不设 `experimental`：关系的主次用 `importance`，可信度用 `verification`（§7.6）。
- `lifecycle` 不写即 `active`，导出时 `active` 不输出；`reason`、`replaced_by` 只在 `deprecated` 时出现，`reason` 必填；`replaced_by` 须是同类型的已定义 ID，不能是自身或 `deprecated` 的定义；仍被 `replaced_by` 指向的定义不能改为 `deprecated`。
- 名称只写业务含义，不写「【核心关系】」「【待确认关系】」一类的状态前缀：生命周期用 `lifecycle`，关系的主次与可信度用 `importance`、`verification`（§7.6）。

**拒绝条件**：`lifecycle` 中出现 `status`、`reason`、`replaced_by` 以外的键；`status` 取值非法；`deprecated` 缺 `reason`；非 `deprecated` 写了 `reason` 或 `replaced_by`；`replaced_by` 不存在、类型不同、指向自身或指向 `deprecated` 定义；端点对象类 `deprecated` 而关系类不是；主关系改为 `deprecated` 时仍被 `fallback_for` 指向；仍被 `replaced_by` 指向的定义改为 `deprecated`。这些约束在任何一方保存时都检查。

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

已知小节：`Data Source`、`Data Properties`、`Logic Properties`、`Keys`；3.0 另有 `Property Constraints`（§6.8）。

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

3.0 另有可选列 `Semantic`、`Required` 与 `Aliases`（§6.8）。

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

3.0 下脱敏规则仍写在本列。旧版后端忽略 `bkn_spec`，写在其他位置的脱敏规则会被它丢弃，因此不移动。

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

### 6.8 属性声明（3.0）

> **状态：3.0。** `Semantic`、`Required`、`Aliases`、对象类的 `aliases` 与 `### Property Constraints` 的全部键（含 `value_type`，§16）已在规范库实现。bkn-backend 存储属性声明（`Aliases`、`Semantic`、`Required`、`### Property Constraints` 的全部键）与对象类、行动类的 `aliases`，对象类、行动类接口读写它们，保存时按本节规则校验：`value_type` 须是本网络已有的值类型（§16），别名不得是同类其他定义的名称。上传与导出 3.0 包时它们随包往返。设计见 [bkn-docs issue-168](https://github.com/openbkn-ai/bkn-docs/blob/main/docs/foundry/bkn/design/issue-168-property-and-relation-declarations.md)。

属性声明描述数据应当满足的要求，只用于理解与展示，**不校验源数据**；声明本身写错时在导入和保存时拒绝。

**Data Properties 表新增两列**（可选，列顺序不限）：

| 列 | 取值 |
|----|------|
| `Semantic` | 下表中的一个取值，或空 |
| `Required` | `true` / `false`；空即 `false` |

`Required: true` 表示每个实例的该属性都应有值：不为 null，字符串类型也不为空字符串。主键属性视为必填，不必再写；写 `false` 时拒绝。

`Semantic` 说明属性在业务上是什么，分时间、空间、状态三组。每个取值在一个对象类中至多出现一次。

| 组 | Semantic | 含义 | 允许的 Type | 规则 | 示例（对象类.属性） |
|----|----------|------|-------------|------|---------------------|
| 时间 | `event_time` | 事件发生的时间 | `date`、`datetime`、`timestamp` | 含它的对象类即事件类 | 订单.下单时间；风险事件.发生时间 |
| 时间 | `end_time` | 事件结束的时间 | 同上 | 必须与 `event_time` 同时出现 | 住院记录.出院时间；工单.关闭时间 |
| 时间 | `valid_from` | 有效期开始 | 同上 | 可单独出现 | 合同.生效日期；价格.起始日期 |
| 时间 | `valid_to` | 有效期结束；值为空表示长期有效 | 同上 | 可单独出现 | 合同.到期日期；证件.有效期至 |
| 空间 | `location` | 位置 | `point`、`shape` | 不能与 `latitude` / `longitude` 同时出现 | 门店.坐标（`point`）；配送区域.范围（`shape`） |
| 空间 | `latitude` | 纬度 | `float`、`decimal` | 必须与 `longitude` 成对出现 | 车辆.纬度 |
| 空间 | `longitude` | 经度 | `float`、`decimal` | 必须与 `latitude` 成对出现 | 车辆.经度 |
| 状态 | `status` | 当前状态 | `string`、`keyword`、`integer` | | 工单.状态（待处理 / 处理中 / 已关闭） |
| 状态 | `severity` | 严重程度 | `string`、`keyword`、`integer` | | 告警.级别（P1–P4）；风险事件.风险等级 |

`event_time` / `end_time` 描述一件事发生的时刻与结束；`valid_from` / `valid_to` 描述一个对象在哪段时间内成立。
两组可以同时出现，例如合同的签订日期是 `event_time`，生效日期与到期日期是 `valid_from` 与 `valid_to`。
源表把经纬度存成一列时用 `location`，存成两列时用 `latitude` 与 `longitude`。

`Description` 只写结构化声明表达不了的内容（业务口径、计算方式、注意事项），不重复语义、单位、枚举、必填；两者矛盾时以结构化声明为准，平台不解析描述。

主键、显示名、增量更新时间已由 `### Keys`（§6.6）声明，单位由 `### Property Constraints` 的 `unit` 声明，度量由指标声明，都不另设 Semantic。

示例：

```markdown
### Data Properties

| Name | Display Name | Type | Semantic | Required | Description | Mapped Field |
|------|--------------|------|----------|----------|-------------|--------------|
| ticket_id | 工单编号 | string | | true | 主键 | ticket_id |
| reported_at | 报修时间 | datetime | event_time | true | | reported_at |
| closed_at | 关闭时间 | datetime | end_time | | 未关闭时为空 | closed_at |
| site | 现场位置 | point | location | | | site_geo |
| state | 状态 | keyword | status | true | | state |
| level | 级别 | keyword | severity | | P1–P4 | level |
```

**`### Property Constraints`**：一个 YAML 代码块，键为数据属性名：

````markdown
### Property Constraints

```yaml
status:
  value_type: lis_apply_status
amount:
  range: { min: 0 }
  unit: 元
code:
  length: { max: 8 }
  pattern: "^[A-Z]{2}\\d{6}$"
  unique: true
```
````

| 键 | 适用 Type | 含义 |
|----|-----------|------|
| `value_type` | 与值类型的 `base_type` 相同 | 引用值类型（§16） |
| `range` | 数值类、`date`、`datetime`、`timestamp` | 闭区间；`min` / `max` 可只写一个。数值类写数字；日期时间类写 ISO 8601 字符串：`date` 写 `"2020-01-01"`，`datetime` / `timestamp` 写 `"2020-01-01T00:00:00Z"`（可带时区偏移） |
| `length` | `string`、`keyword`、`text` | 字符数闭区间 |
| `pattern` | `string`、`keyword`、`text` | 正则，RE2 语法 |
| `unit` | 数值类 | 自由文本 |
| `unique` | 除 `json`、`binary`、`vector`、`point`、`shape` 外 | 主键之外的唯一性 |
| `crs` | Semantic 为 `location`、`latitude`、`longitude` 的属性 | 坐标系：`wgs84`（GPS）、`gcj02`（高德、腾讯）、`bd09`（百度）。同一点在不同坐标系下可相差数百米；不写为未声明，不默认 `wgs84`。`latitude` 与 `longitude` 写了时必须相同 |
| `timezone` | `datetime` | 不带时区的值按哪个时区解释，IANA 名称，如 `Asia/Shanghai`。不写为未声明。`timestamp` 与带时区偏移的值不需要 |

- **引用了值类型的属性，不再内联写取值约束。** 这类属性在 `### Property Constraints` 中只能再写与取值无关的 `unique`、`crs`、`timezone`；写 `range`、`length`、`pattern`、`unit` 时拒绝。值的规则只有一个来源，不存在叠加与冲突（例如值类型要求 ≥ 0、内联又要求 ≤ -1）。需要不同规则时另建值类型。

**导出列顺序**：3.0 导出的 Data Properties 表固定为 `Name | Display Name | Type | Semantic | Required | Aliases | Description | Mapped Field | Mask Rule`；`Semantic`、`Required`、`Aliases`、`Mask Rule` 只在至少一个属性用到时输出。`### Property Constraints` 中的属性按 Data Properties 表的顺序排列，键按上表顺序排列。

**别名**：同一个概念的其他叫法，只供概念搜索召回与模型理解，不参与查询，也不改变 ID 与名称的唯一性规则。

| 位置 | 写法 |
|------|------|
| 对象类 frontmatter | `aliases`，YAML 列表 |
| Data Properties 表 | 可选列 `Aliases`，多个别名用半角逗号 `,` 分隔 |

````markdown
---
type: object_type
id: patient
name: 患者
aliases: [病人, 就诊人]
---

## ObjectType: 患者

### Data Properties

| Name | Display Name | Type | Aliases | Description | Mapped Field |
|------|--------------|------|---------|-------------|--------------|
| patient_id | 患者编号 | string | 病历号, 就诊号 | 主键 | patient_id |
| admit_dept | 入院科室 | keyword | 收治科室 | | admit_dept |
````

- 每个别名去掉首尾空格后非空，最长 40 个字符，不含 `,` 与 `，`；每个对象类、属性至多 10 个。
- `aliases` 必须写成列表，写成单个字符串时拒绝。
- 同一组别名内不重复，不与自身名称相同（对象类比较 `name`，属性比较 `Name` 与 `Display Name`）。
- 对象类的别名不得与网络内其他对象类的 `name` 相同；不同对象类之间别名可以相同，召回时都返回。
- 属性的别名不得与同一对象类其他属性的 `Name` 或 `Display Name` 相同。

**拒绝条件**：Semantic 取值非法、与 Type 不匹配、同一取值出现多次、有 `end_time` 无 `event_time`、
`latitude` 与 `longitude` 不成对、`location` 与 `latitude` / `longitude` 同时出现；
`Required` 不是 `true` / `false` / 空；约束的键不是已定义的数据属性、出现未知键、与 Type 不匹配、
`pattern` 无法编译、`min` 大于 `max`；引用的值类型不存在或 `base_type` 与 Type 不同；引用了值类型的属性写了 `range`、`length`、`pattern` 或 `unit`；`crs`、`timezone` 取值非法或用在不适用的属性上，`latitude` 与 `longitude` 的 `crs` 不同；别名违反上述规则。

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
`Source Condition`、`Target Condition`；3.0 另有 `Relation Constraints`、`Data Properties`、`Property Constraints`（§7.6，后两者只用于 `indirect`）。

### 7.2 Endpoint

| 列 | 必需 | 说明 |
|----|:----:|------|
| `Source` | 是 | 起点对象类 ID，必须在网络中已定义 |
| `Target` | 是 | 终点对象类 ID，必须在网络中已定义 |
| `Type` | 是 | 映射方式：`direct`、`indirect` 或 `filtered_cross_join`，区分大小写 |

只读第一行。`Type` 决定读取哪些小节，其余小节被忽略。

按「两端的关联记录在哪里」选择映射方式：

| Type | 适用场景 | 示例 |
|------|----------|------|
| `direct` | 一端的资源里有另一端的键（外键） | 订单.customer_id → 客户；物料.supplier_id → 供应商 |
| `indirect` | 两端都没有对方的键，关联记录在第三个资源（关联表）里。多对多关系，以及关系本身带信息的场景（只有 `indirect` 能声明关系属性，§7.6） | 学生 ↔ 课程，经选课表；BOM → 子物料，经 BOM 行表；员工 → 部门，经任职表（带职务、起止日期）；股东 → 公司，经持股表（带持股比例） |
| `filtered_cross_join` | 没有任何键可以对应，按条件圈定两端后全部相连 | 类别为「原材料」的物料 ↔ 状态为「启用」的仓库 |

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

两个小节都必需。条件结构见 §8.5，但没有 `object_type_id` 字段；2.x 下也可以包在 `condition:` 或
`trigger_condition:` 键下，3.0 只接受直接写（§1.3）。

### 7.6 关系声明（3.0）

> **状态：3.0。** 规范库已实现解析、校验与导出。bkn-backend 存储关系声明（`reverse_name`、`aliases`、`Cardinality`、`### Relation Constraints`、`importance`、`fallback_for`、`verification`）与关系属性，关系类接口读写它们，保存时按本节规则检查，跨定义的规则（`fallback_for`）在任何一方保存时都检查，删除仍被 `fallback_for` 或 `replaced_by` 引用的关系类时拒绝；上传与导出 3.0 包时随包往返。设计见 [bkn-docs issue-168](https://github.com/openbkn-ai/bkn-docs/blob/main/docs/foundry/bkn/design/issue-168-property-and-relation-declarations.md)。

````markdown
---
type: relation_type
id: order_produces_fee
name: 产生
reverse_name: 来源于
---

## RelationType: 产生

### Endpoint

| Source | Target | Type | Cardinality |
|--------|--------|------|-------------|
| medical_order | fee_detail | direct | one_to_many |

### Relation Constraints

```yaml
required: [target]
```
````

| 位置 | 字段 | 说明 |
|------|------|------|
| frontmatter | `reverse_name` | 可选。从终点看向起点时的名称，规则同 `name`。一条关系、两个名字 |
| frontmatter | `aliases` | 可选。别名列表，规则同 §6.8；不得与 `name`、`reverse_name` 相同。关系类 `name` 允许重复，不做跨关系检查 |
| frontmatter | `importance` | 可选。`primary`：业务主干关系，回答问题时优先使用；`secondary`：辅助关系，补充细节或主干缺失时的替代路径，主要关系答不了或问题明确涉及时才使用。不写即 `primary`；不限制端点 |
| frontmatter | `fallback_for` | 可选，只用于 `secondary`。本关系是所指主关系的替代路径，按 `Source` 端实例判断：Source 实例沿主关系没有任何对端时（包括外键指向不存在的实例），它的对端由本关系给出，结果按本关系的 `verification` 标注；有主关系对端时，本关系中从它出发的边不用。有效关系 = 主关系的全部边 + 本关系中 Source 实例没有主关系对端的边，两个方向查的都是这组边。是否有主关系对端只看数据、不带查询条件；主关系查询失败、超时、分页未完成、结果截断或无权限时不替代，并注明结果不完整。主关系须声明 `Cardinality` 为 `many_to_one` 或 `one_to_one`（Source 实例至多一个主关系对端）。存在性检查用调用者身份，主关系对端被行级权限过滤时不替代。平台不在查询时自动执行替代，由使用方按 `search_schema` 给出的规则执行。端点与某条 `primary` 相同、回答同一问题的 `secondary` 关系，须写 `fallback_for` 或在数据层与主关系合并，否则使用方可能把两者结果相加 |
| frontmatter | `verification` | 可选。`confirmed`：按真实标识关联（外键或业务上为关联而设的 ID）；`unconfirmed`：按规则推测（同号匹配、按来源单号匹配、经中间对象推导），规则写在描述中；不写为未声明。用到 `unconfirmed` 关系的结果应标为「待确认」。与 `lifecycle`（§4.3）相互独立 |
| `### Endpoint` 表 | `Cardinality` 列 | 可选。`one_to_one`、`one_to_many`、`many_to_one`、`many_to_many`；空为未声明。`filtered_cross_join` 把两端全部相连，只能写 `many_to_many` 或不写 |
| `### Relation Constraints` | `required` | 可选。`[source]`、`[target]` 或两者：该端每个实例至少关联一个对端实例 |
| `### Relation Constraints` | `characteristics` | 可选。`symmetric`、`transitive` 的子集；只允许用于两端为同一对象类的关系 |

**基数（`Cardinality`）与必有（`required`）**。基数说的是：一端的一个实例最多连几个对端实例（方向为 `Source` → `Target`）。`required` 补的是「至少一个」。

| 写法 | 含义 | 例子 |
|------|------|------|
| `one_to_one` | 一个起点最多连一个终点，一个终点最多被一个起点连到 | 员工 → 工位 |
| `one_to_many` | 一个起点可连多个终点，一个终点只属于一个起点 | 医嘱 → 费用明细 |
| `many_to_one` | 多个起点可连同一个终点，一个起点只连一个终点 | 检验申请 → 患者 |
| `many_to_many` | 两端都可以是多个 | 医生 ↔ 科室，经任职表 |
| 不写 | 未声明 | |
| `required: [source]` | 每个起点至少连一个终点 | 每张检验申请都有患者 |
| `required: [target]` | 每个终点至少被一个起点连到 | 每笔费用明细都来自某条医嘱 |
| `required: [source, target]` | 两端都至少有一个 | 每个订单至少一行明细、每行明细都属于订单 |
| 不写 `required` | 两端都可以一个都没有 | 一级科室没有上级科室 |

两者合起来描述精确的数量：`many_to_one` + `required: [source]` 即「每张检验申请恰好对应一个患者」；`one_to_many` 不加 `required` 即「每条医嘱有零到多笔费用」。
`one_to_many` 与 `many_to_one` 是同一种关系的两个方向，取决于哪端写在 `Source`。
基数的主要用途是防止统计重复计数：沿 `one_to_many` 连接后，「一」端的每个实例会重复出现，此时对「一」端的数值求和会被放大。

**基数统计的是不同的对端实例，不是边的条数。** `indirect` 关系中，同一对实例可以有多条边（同一员工在同一部门有两段任职，任职表里是两行），此时按「不同部门」算仍是 `many_to_one`，按边连接却会得到两行。因此**查询方不能仅凭 `many_to_one` 就认为聚合不会放大**：沿 `indirect` 关系聚合前，先按端点实例去重，或明确按边聚合。`direct` 关系由外键支撑，同一对实例只有一条边；`filtered_cross_join` 每对实例只连一次。

**关系特性（`characteristics`）**，只用于两端为同一对象类的关系：

| 写法 | 含义 | 例子 | 反例 |
|------|------|------|------|
| `symmetric` | A 与 B 有这条关系，则 B 与 A 也有 | 配偶、同事、门店相邻 | 上级（A 是 B 的上级，B 不是 A 的上级） |
| `transitive` | A 与 B 有、B 与 C 有，则 A 与 C 也有 | 科室隶属（心内科 → 内科 → 大内科）、部件组成、地区包含 | 直接上级（大内科不是心内科的直接上级） |

- 数据只存直接的边。对称关系可能只存了一个方向，传递关系只存相邻两层，使用方查询时按特性展开（例如 Cypher 可变长度路径），平台不生成派生的边。
- **对称关系**的 `Cardinality` 只能是 `one_to_one`、`many_to_many` 或不写：A → B 成立即 B → A 成立，两端数量必然对等，写 `one_to_many` / `many_to_one` 自相矛盾。对称关系不写 `reverse_name`：两个方向叫法相同，写了反而暗示有方向。`required` 只能不写或写 `[source, target]`：两端是同一对象类、关系没有方向，只要求一端没有意义。
- **传递关系**要起对名字：「隶属于」可以标 `transitive`，「直接上级」不可以。
- **基数只约束直接的边。** 传递关系按特性展开后，一个实例可以对应多个祖先或后代：「科室隶属」的直接边是 `many_to_one`，展开后的「所有上级科室」是多对多。查询方沿展开路径连接时按 `many_to_many` 处理，避免把放大的结果当成多对一。

**关系属性**：只有 `indirect` 关系可以声明 `### Data Properties`，列与对象类相同，`Mapped Field` 指向中间资源的列。
`Semantic` 规则与对象类相同，按每个关系计；别名在同一关系类内比较；关系属性不支持 `unique`（边没有主键）。关系的有效期用 `valid_from` / `valid_to`：

```markdown
### Data Properties

| Name | Display Name | Type | Semantic | Description | Mapped Field |
|------|--------------|------|----------|-------------|--------------|
| title | 职务 | string | | 任职职务 | title |
| start_date | 起始日期 | date | valid_from | 任职开始 | start_date |
| end_date | 结束日期 | date | valid_to | 任职结束；在职时为空 | end_date |
```

关系属性的 `Mapped Field` 不得是 `Source Mapping`、`Target Mapping` 已经用作连接的列。

关系属性的约束写在关系类的 `### Property Constraints` 中，规则同 §6.8，键为关系属性名：

````markdown
### Property Constraints

```yaml
ratio:
  value_type: percent
```
````

中间资源中同一对端点有多行时，一行即一条边。`direct` 与 `filtered_cross_join` 关系声明 Data Properties 或 Property Constraints 时拒绝。

**拒绝条件**：上述字段取值非法；`characteristics` 用于两端不同的关系；`symmetric` 关系的 `Cardinality` 是 `one_to_many` / `many_to_one`、写了 `reverse_name`，或 `required` 只写一端；关系属性写了 `unique`；关系属性映射到连接列；非 `indirect` 关系声明 Data Properties；`verification` 取值非法；`importance` 取值非法；`fallback_for` 写在非 `secondary` 关系上，或所指关系不存在、不是 `primary`、不是 `active`、端点或方向不同、是自身；同一主关系有多个替代路径；替代路径被再替代；主关系或替代路径是 `filtered_cross_join` 或声明了 `characteristics`；主关系未声明 `Cardinality` 或不是 `many_to_one` / `one_to_one`；删除仍被 `fallback_for` 或 `replaced_by` 引用的定义。被引用方的修改（主关系改 `Cardinality`、`importance`、端点或生命周期）使替代路径不合法时同样拒绝，并列出引用方。关系属性的 `Aliases` 列规则同 §6.8。

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
| `action_type` | 已废弃，与 `action_intent` 同义；2.x 仍兼容，3.0 拒绝（§1.3） |
| `aliases` | 3.0。别名列表，规则同 §6.8；不得与网络内其他行动类的 `name` 相同 |

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

**Affect Object** 是一张表，列为 `Affect Object` 和 `Affect Description`，只读第一行，对应行动类的 `affect` 字段（受影响的对象类与说明，Studio 行动类表单可编辑）。它与 `Impact Contracts` 是两个不同的字段，不是同义写法，3.0 下保留。导出时 2.x 总是输出这张表（多为空表）；**3.0** 只在有内容时输出。

### 8.5 Trigger Condition

````markdown
### Trigger Condition

```yaml
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

条件直接写在 yaml 块中。2.x 下也可以包在 `condition:` 或 `trigger_condition:` 键下；3.0 只接受直接写，包装键按 §1.3 拒绝。

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
以上旧标题与旧列名只在 2.x 下可读，2.x 下这一节还会被当作未知小节并入描述（附录 A-1）；3.0 下按 §1.3 拒绝并给出 `### Action Source` 与对应列名。新文件请使用 `### Action Source`。

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
- 3.0：拒绝旧布局，不自动转换；错误信息给出按当前列布局的改写（`Source` 中的值的来源写到 `ValueFrom`，
  `Binding` 写到 `Value`，`Source` 置空）。旧列名 `Parameter`、`Value From`、`Binding` 也按 §1.3 拒绝。

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

`network.bkn` frontmatter 中的 `capabilities` 声明网络挂载了哪些 Skill、工具箱工具和 MCP 工具。
它只是声明，不携带能力本身：Skill 包、工具箱、MCP 服务通过各自的导入导出渠道迁移，
类似 package.json 只列依赖、不打包依赖。

行动类与逻辑属性引用的工具（§6.7、§8.6）不必写入 `capabilities`：引用由模型文件本身给出，
导出时也不会写进这一段；写进去会在导入时把引用变成显式挂载，和源网络不一致。
迁移时要搬的完整依赖是 `capabilities` 加上模型中的工具引用（§13.1）。

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
- 2.x：这一段格式错误时被整体忽略，网络仍可导入；写错的类别或字段被静默丢弃。
- **（3.0）** 格式写错时拒绝并指出位置（类别、条目序号与字段）：`capabilities` 不是映射；出现 `skills`、`functions`、`mcp_tools` 以外的类别，或上表以外的字段；值的类型不对（类别不是列表、条目不是映射、字段不是单个值）；条目缺少匹配所需的字段，在任何环境都无法匹配：`skills` 的 `id`、`name` 都没写，`functions` 的 `box_id` + `tool_id` 与 `box_name` + `tool_name` 两组都不齐全，`mcp_tools` 没写 `tool_name`，或 `mcp_id`、`mcp_name` 都没写。匹配不到的条目与 2.x 相同，列为跳过项，不让导入失败：能否匹配取决于目标环境，不是写法错误。

### 13.1 跨环境迁移（现状）

BKN 包只携带对工具的**引用**，不携带工具的实现：

- Action Source 与逻辑属性的 tool 来源只写环境内的 ID（§6.7、§8.6），执行时按 ID 找工具。
- `capabilities` 写 ID 和名称，但只用于挂载能力绑定，不会改写行动与逻辑属性里的 ID。

因此把包导入到另一个环境之前，需要先把用到的能力搬过去：既包括 `capabilities` 中的条目，
也包括 Action Source 与逻辑属性 tool 来源引用的工具箱和 MCP 服务，后者不一定出现在 `capabilities` 中。

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
- **（3.0）** 网络中存在任一 3.0 专有声明（§4.3、§6.8、§7.6、§8.2 的 `aliases`、§16）时，`network.bkn` 写
  `bkn_spec: "3.0"` 并输出新列、新小节与 `value_types/`，全部内容按 §1.3 的唯一写法输出（只写 `action_intent`，列顺序固定，`Affect Object` 只在有内容时输出）；否则导出与现在逐字节相同，不写 `bkn_spec`。
  bkn-backend 导出时可用 `bkn_spec` 参数指定：不传即按上面的规则自动选择；`2.0` 按 2.x 导出，不含 3.0 声明，结果与从未有过声明的网络逐字节相同；`3.0` 按 3.0 导出。
  按 3.0 导出前，bkn-backend 先做升级预检（即网络体检，`GET /bkns/{kn_id}/check`）：`Source` 为 `property` / `input` / `const`（不区分大小写）的参数视为疑似错位，按旧参数表转换预填建议值（`ValueFrom` 转小写，`input` 行与 `-` 的 `Value` 为空），由人工确认；`ValueFrom` 不是 `property` / `input` / `const`、`Source` 也不是值来源的参数一并列出，没有建议值；写出的包还会按 3.0 规则读回一遍，读不回时拒绝并给出原因。修正前不按 3.0 导出，但可以显式选择按 2.x 导出（不含 3.0 声明）。预检不阻止在平台上保存声明。同一检查也可随时对 2.x 网络运行。
- **（3.0）** **描述中的 `#` 行首转义。** 描述（网络、定义的描述文字）里以 `#` 开头的行会被当成标题，打乱小节结构。3.0 导出时，行首若是零个或多个 `\` 后接 `#`，在行首再加一个 `\`；3.0 导入时，行首若是一个或多个 `\` 后接 `#`，去掉一个 `\`。这一变换可逆：存储中的描述原样不动，往返逐字节不变，用户在 Studio 里写的标题也不必改写。2.x 导出不转义；旧后端读到 `\#` 时会原样保留反斜杠，只影响显示。
- **（3.0）** 按 3.0 导出时，frontmatter 使用保留字符串类型的 YAML 序列化（`id`、`name`、`tags`、`aliases`、`reverse_name` 等需要时加引号）；3.0 导入按原文读取这些字段。2.x 导出不变（附录 A-3）。

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

## 16. 值类型（value_types/*.bkn，3.0）

> **状态：3.0。** 规范库已实现解析、校验与导出。bkn-backend 存储值类型，提供值类型接口（`/knowledge-networks/{kn_id}/value-types`），按 3.0 导出时写出 `value_types/`；被数据属性引用的值类型不能删除，修改后的 `base_type` 须仍与引用方的 Type 相同。上传 3.0 包时 `value_types/` 中的值类型先于对象类入库，整网 JSON 导入导出以 `value_types` 携带。设计见 [bkn-docs issue-168](https://github.com/openbkn-ai/bkn-docs/blob/main/docs/foundry/bkn/design/issue-168-property-and-relation-declarations.md)。

值类型是「基础类型 + 一组约束」的命名定义，供多个属性复用：`Type` 说明数据怎么存，值类型说明存的是什么、哪些值合法。
作用范围是单个知识网络；平台不预置值类型。

````markdown
---
type: value_type
id: lis_apply_status
name: LIS 检验申请状态
tags: [检验]
---

## ValueType: LIS 检验申请状态

LIS 系统的检验申请状态码。X 是旧系统的撤销编码，存量数据中仍存在。

### Definition

```yaml
base_type: keyword
enum:
  - { value: N, display: 已开单 }
  - { value: S, display: 已采样 }
  - { value: P, display: 已出报告 }
  - { value: C, display: 已撤销 }
  - { value: X, display: 已撤销, deprecated: true }
```
````

已知小节只有 `Definition`。`id`、`name`、`tags` 规则同 §4。

| 键 | 说明 |
|----|------|
| `base_type` | 必需。`string`、`keyword`、`integer`、`unsigned integer`、`float`、`decimal`、`date`、`datetime`、`timestamp` 之一 |
| `enum` | 枚举项列表：`value`（必需，符合 `base_type`，不可重复）、`display`（必需，可以重复，用于新旧编码并存）、`deprecated`（可选）、`description`（可选） |
| `range` / `length` / `pattern` / `unit` | 与 §6.8 同名键含义相同 |

- 有 `enum` 时不能再写 `range`、`length`、`pattern`；`unit` 可以同时出现。
- 属性通过 §6.8 的 `value_type` 引用；仍被属性（含关系属性）引用的值类型不能删除，也不能修改 `base_type`。枚举项、约束与名称可以修改，保存时重新检查所有引用方。
- 值类型的 `name` 在网络内不得重复（§4.2）。

按用途大致分三种，下面每种给出 `### Definition` 的写法：

**枚举 / 字典**：取值是一组固定编码，每个编码有显示名。

```yaml
# 科室代码 dept_code
base_type: string
enum:
  - { value: "0101", display: 心内科 }
  - { value: "0102", display: 呼吸内科 }
  - { value: "0201", display: 普外科 }
```

```yaml
# 性别 gender（编码为整数）
base_type: integer
enum:
  - { value: 1, display: 男 }
  - { value: 2, display: 女 }
  - { value: 9, display: 未说明 }
```

**格式**：取值是自由文本，但必须符合固定格式。

```yaml
# 居民身份证号 cn_id_card
base_type: string
length: { min: 18, max: 18 }
pattern: "^\\d{17}[\\dX]$"
```

```yaml
# 物料编码 material_code，形如 RM-000123
base_type: keyword
pattern: "^[A-Z]{2}-\\d{6}$"
```

**区间**：取值是数值，必须落在一个范围内，通常带单位。

```yaml
# 百分比 percent
base_type: decimal
range: { min: 0, max: 100 }
unit: "%"
```

```yaml
# 满意度评分 rating_1_5
base_type: integer
range: { min: 1, max: 5 }
```

```yaml
# 人民币金额 amount_cny
base_type: decimal
range: { min: 0 }
unit: 元
```
- CHECKSUM 键为 `value_type:<id>`。

**拒绝条件**：`base_type` 非法；枚举与 `range` / `length` / `pattern` 同时出现；枚举项 `value` 重复或类型不符。

---

## 附录 A：已知问题

以下是实现与预期不一致、或格式本身有缺口的地方，修复后从本附录移除并更新正文。

| # | 问题 | 现状 | 影响 |
|---|------|------|------|
| A-1 | `### Tool Configuration` 不在行动类已知小节中 | 2.x 现状：能被解析为 Action Source，但整节同时并入描述。3.0 拒绝旧标题，错误信息给出 `### Action Source` | 按 SDK 模板写的行动，描述里多出一段工具表 |
| A-2 | 旧列名 `Binding` 映射到 `ValueFrom` | 2.x 现状：旧布局把 `property` 等写在 `Source`、属性名写在 `Binding`，读取后值的来源错位。3.0 拒绝旧布局，错误信息给出改写 | 按 SDK 模板写的行动参数在执行时被静默丢弃；已入库的数据不会自动修复，3.0 升级预检（网络体检）会列出疑似错位的参数并预填建议值（§14.2） |
| A-3 | 导出时 frontmatter 由字符串拼接生成 | 值不加引号；2.x 保持现状，3.0 导出保留字符串类型（§14.2） | 名称含 `: ` 时导出文件无法再导入；`001`、`1.0` 等读回后变成 `1` |
| A-4 | 当前列布局下把值的来源写进 `Source` | 2.x 现状：`Source` 写 `property`、属性名写在 `ValueFrom` 的表（bkn-sdk 的 `SPECIFICATION.llm.md` 对逻辑属性就是这样教的）按字面读取。3.0 拒绝导入 | 2.x 下该参数在执行时被静默丢弃；SDK 文档需修正 |
| A-5 | 未知的 frontmatter 字段与 `###` 小节都被静默处理 | 2.x 现状：字段被丢弃，小节并入描述，都不提示。3.0 拒绝（§1.3） | 拼错的小节名、`enabled` / `risk_level` / `requires_approval` 等字段写了无效且没人知道 |
| A-6 | 往返丢失字段 | 对象类与关系类的 `icon`、`color`，参数的 `required`、`default`、`if_system_generate` 都没有 BKN 写法 | 导出再导入后这些设置丢失 |
| A-7 | 工具引用只有 ID | Action Source 与逻辑属性的 tool 来源只写环境内 ID，没有名称 | 跨环境导入后 capabilities 能按名称匹配，行动和逻辑属性仍指向不存在的 ID；现状的规避方式见 §13.1 |
| A-9 | 子目录与 frontmatter `type` 可以不一致 | 2.x 现状：解析看子目录，CHECKSUM 看 `type`。3.0 拒绝不一致（§1.3） | 同一文件在两处被当作不同类型 |
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
| 行动执行配置 | `### Tool Configuration` | `### Action Source`（旧标题 2.x 下仍可读，3.0 拒绝，见 A-1） |
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
| 属性与关系声明、值类型、别名 | 无 | 3.0：§6.8、§7.6、§8.2、§16 |
| 写法收紧 | 同义写法与未知内容静默处理 | 3.0：每样东西只保留一种写法，其余拒绝（§1.3） |
| 生命周期、主次与可信度 | 无 | 3.0：`lifecycle`（§4.3）、关系 `importance` / `fallback_for` / `verification`（§7.6） |

---

## 附录 C：3.0 完整示例

> **状态：3.0。** 本例的全部声明已在规范库实现，规范库按本例加载与校验通过；bkn-backend 可上传、存储并导出本例。设计见 [bkn-docs issue-168](https://github.com/openbkn-ai/bkn-docs/blob/main/docs/foundry/bkn/design/issue-168-property-and-relation-declarations.md)。

一个门诊检验网络，用到 §6.8、§7.6、§16 的全部声明：时间 / 空间 / 状态语义、必填、约束、三种值类型、别名、反向名称、基数、关系约束与关系属性。

```text
clinic_lab/
├── network.bkn
├── value_types/
│   ├── lis_apply_status.bkn      # 枚举
│   ├── cn_id_card.bkn            # 格式
│   └── amount_cny.bkn            # 区间
├── object_types/
│   ├── patient.bkn
│   ├── doctor.bkn
│   ├── department.bkn
│   └── lab_order.bkn
└── relation_types/
    ├── order_for_patient.bkn     # direct，多对一，必填
    ├── doctor_in_department.bkn  # indirect，带关系属性
    └── department_under.bkn      # 自引用，传递
```

### C.1 network.bkn

```markdown
---
type: knowledge_network
id: clinic_lab
name: 门诊检验
tags: [医疗]
bkn_spec: "3.0"
---

# 门诊检验

门诊检验申请，以及相关的患者、医生与科室。
```

`bkn_spec: "3.0"` 必须写，否则下面所有新声明按 2.x 被忽略。

### C.2 value_types/lis_apply_status.bkn（枚举）

````markdown
---
type: value_type
id: lis_apply_status
name: LIS 检验申请状态
tags: [检验]
---

## ValueType: LIS 检验申请状态

LIS 系统的检验申请状态码。X 是旧系统的撤销编码，存量数据中仍存在。

### Definition

```yaml
base_type: keyword
enum:
  - { value: N, display: 已开单 }
  - { value: S, display: 已采样 }
  - { value: P, display: 已出报告 }
  - { value: C, display: 已撤销 }
  - { value: X, display: 已撤销, deprecated: true }
```
````

### C.3 value_types/cn_id_card.bkn（格式）

````markdown
---
type: value_type
id: cn_id_card
name: 居民身份证号
---

## ValueType: 居民身份证号

### Definition

```yaml
base_type: string
length: { min: 18, max: 18 }
pattern: "^\\d{17}[\\dX]$"
```
````

### C.4 value_types/amount_cny.bkn（区间）

````markdown
---
type: value_type
id: amount_cny
name: 人民币金额
---

## ValueType: 人民币金额

### Definition

```yaml
base_type: decimal
range: { min: 0 }
unit: 元
```
````

### C.5 object_types/patient.bkn

````markdown
---
type: object_type
id: patient
name: 患者
aliases: [病人, 就诊人]
tags: [主数据]
---

## ObjectType: 患者

### Data Source

| Type | ID | Name |
|------|----|------|
| resource | res_patient | 患者表 |

### Data Properties

| Name | Display Name | Type | Semantic | Required | Aliases | Description | Mapped Field | Mask Rule |
|------|--------------|------|----------|----------|---------|-------------|--------------|-----------|
| patient_id | 患者编号 | string | | true | 病历号, 就诊号 | 主键 | patient_id | |
| patient_name | 姓名 | string | | true | | | name | |
| id_card | 身份证号 | string | | | 证件号 | | id_card_no | {"kind":"partial","keep_start":6,"keep_end":4,"replacement":"*"} |
| age | 年龄 | integer | | | | | age | |
| updated_at | 更新时间 | datetime | | | | | updated_at | |

### Property Constraints

```yaml
id_card:
  value_type: cn_id_card
  unique: true
age:
  range: { min: 0, max: 150 }
  unit: 岁
```

### Keys

Primary Keys: patient_id
Display Key: patient_name
Incremental Key: updated_at
````

要点：`id_card` 引用值类型，另加与取值无关的 `unique`；脱敏规则仍写在 `Mask Rule` 列，旧后端也能读到。`age` 没有引用值类型，直接写内联约束。主键 `patient_id` 写了 `Required: true`，不写也一样。

### C.6 object_types/doctor.bkn

````markdown
---
type: object_type
id: doctor
name: 医生
aliases: [医师]
---

## ObjectType: 医生

### Data Source

| Type | ID | Name |
|------|----|------|
| resource | res_doctor | 医生表 |

### Data Properties

| Name | Display Name | Type | Semantic | Required | Description | Mapped Field |
|------|--------------|------|----------|----------|-------------|--------------|
| doctor_id | 工号 | string | | true | 主键 | doctor_id |
| doctor_name | 姓名 | string | | true | | name |
| license_expiry | 执业证有效期至 | date | valid_to | | 为空表示长期有效 | license_expire_date |

### Keys

Primary Keys: doctor_id
Display Key: doctor_name
````

要点：执业证只有到期日，`valid_to` 单独出现。

### C.7 object_types/department.bkn

````markdown
---
type: object_type
id: department
name: 科室
---

## ObjectType: 科室

### Data Source

| Type | ID | Name |
|------|----|------|
| resource | res_department | 科室表 |

### Data Properties

| Name | Display Name | Type | Semantic | Required | Description | Mapped Field |
|------|--------------|------|----------|----------|-------------|--------------|
| dept_id | 科室代码 | string | | true | 主键 | dept_code |
| dept_name | 科室名称 | string | | true | | dept_name |
| parent_dept_id | 上级科室代码 | string | | | 一级科室为空 | parent_code |
| site | 所在位置 | point | location | | 科室所在楼宇的坐标 | geo |

### Property Constraints

```yaml
site:
  crs: gcj02
```

### Keys

Primary Keys: dept_id
Display Key: dept_name
````

### C.8 object_types/lab_order.bkn

````markdown
---
type: object_type
id: lab_order
name: 检验申请
aliases: [化验单, 检验单]
tags: [检验]
---

## ObjectType: 检验申请

### Data Source

| Type | ID | Name |
|------|----|------|
| resource | res_lab_order | 检验申请表 |

### Data Properties

| Name | Display Name | Type | Semantic | Required | Aliases | Description | Mapped Field |
|------|--------------|------|----------|----------|---------|-------------|--------------|
| order_no | 申请单号 | string | | true | 条码号 | 主键 | order_no |
| patient_id | 患者编号 | string | | true | | | patient_id |
| ordered_at | 开单时间 | datetime | event_time | true | 申请时间 | | apply_time |
| reported_at | 报告时间 | datetime | end_time | | 出报告时间 | 未出报告时为空 | report_time |
| status | 申请状态 | keyword | status | true | | | apply_status |
| critical_level | 危急程度 | keyword | severity | | | 正常 / 异常 / 危急 | critical_flag |
| fee | 费用 | decimal | | | 检验费 | | fee_amount |
| updated_at | 更新时间 | datetime | | | | | updated_at |

### Property Constraints

```yaml
order_no:
  pattern: "^LO\\d{10}$"
  unique: true
ordered_at:
  timezone: Asia/Shanghai
reported_at:
  timezone: Asia/Shanghai
status:
  value_type: lis_apply_status
fee:
  value_type: amount_cny
```

### Keys

Primary Keys: order_no
Display Key: order_no
Incremental Key: updated_at
````

要点：有 `event_time`，因此是事件类；问「最近一周的检验申请」按 `ordered_at` 过滤，问「平均出报告时长」用 `reported_at - ordered_at`。
`search_schema` 返回 `status` 时会展开五个枚举项，`X` 带 `deprecated: true`，模型筛「已撤销」时写 `status IN ('C', 'X')`。

### C.9 relation_types/order_for_patient.bkn（direct）

````markdown
---
type: relation_type
id: order_for_patient
name: 检验对象
reverse_name: 检验申请
verification: confirmed
---

## RelationType: 检验对象

### Endpoint

| Source | Target | Type | Cardinality |
|--------|--------|------|-------------|
| lab_order | patient | direct | many_to_one |

### Mapping Rules

| Source Property | Target Property |
|-----------------|-----------------|
| patient_id | patient_id |

### Relation Constraints

```yaml
required: [source]
```
````

要点：`many_to_one` 加 `required: [source]` 表示每张检验申请恰好对应一个患者。检验申请表里直接存了患者编号，因此 `verification: confirmed`。从患者出发时用 `reverse_name`，即「患者的检验申请」。

### C.10 relation_types/doctor_in_department.bkn（indirect，带关系属性）

````markdown
---
type: relation_type
id: doctor_in_department
name: 任职于
reverse_name: 在职医生
aliases: [所属科室]
---

## RelationType: 任职于

医生可以同时在多个科室任职，任职记录在任职表中。

### Endpoint

| Source | Target | Type | Cardinality |
|--------|--------|------|-------------|
| doctor | department | indirect | many_to_many |

### Backing Resource

| Type | ID |
|------|----|
| resource | res_doctor_assignment |

### Source Mapping

| Source Property | Resource Property |
|-----------------|-------------------|
| doctor_id | doctor_id |

### Target Mapping

| Resource Property | Target Property |
|-------------------|-----------------|
| dept_code | dept_id |

### Data Properties

| Name | Display Name | Type | Semantic | Description | Mapped Field |
|------|--------------|------|----------|-------------|--------------|
| title | 职务 | string | | 如主任、副主任 | title |
| start_date | 任职开始 | date | valid_from | | start_date |
| end_date | 任职结束 | date | valid_to | 在职时为空 | end_date |
````

要点：两端都没有对方的键，所以用 `indirect`；任职表的一行是一条边，`title` 与任职起止是这条边的属性。问「心内科现在有哪些医生」时，按 `end_date` 为空或晚于今天过滤。

### C.11 relation_types/department_under.bkn（自引用）

````markdown
---
type: relation_type
id: department_under
name: 隶属于
reverse_name: 下辖
---

## RelationType: 隶属于

### Endpoint

| Source | Target | Type | Cardinality |
|--------|--------|------|-------------|
| department | department | direct | many_to_one |

### Mapping Rules

| Source Property | Target Property |
|-----------------|-----------------|
| parent_dept_id | dept_id |

### Relation Constraints

```yaml
characteristics: [transitive]
```
````

要点：两端都是科室，`reverse_name` 消除方向歧义：「心内科隶属于内科」「内科下辖心内科」。`transitive` 表示隶属可以逐级传递，因此「内科下辖的所有科室」包括下级的下级。一级科室没有上级，所以不写 `required`。
