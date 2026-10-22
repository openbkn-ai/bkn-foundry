# 🔎 Cypher 查询

## 📖 概述

BKN 支持用只读的 **openCypher** 查询知识网络里的实例数据。查询按**模型**来写：标签写对象类，关系写关系类，属性写逻辑属性名。服务端把查询编译成一条 SQL，经 VEGA 在数据源上执行，调用方不需要知道物理表名和列名。

支持的是 openCypher 9（M19）的一个**只读子集**。子集只做收窄，不加非标准语法：凡是能执行的查询，写法和含义都与标准一致。超出子集的写法会被明确拒绝，报错会指出是哪个语法、在第几行第几列，并给出改写建议，不会近似执行。

本页的示例使用两个示例知识网络：

| 知识网络 | 对象类（标签） | 关系类 |
| --- | --- | --- |
| 世界杯 `worldcup_vega_catalog_bkn` | `goals` 进球、`teams` 球队、`matches` 比赛、`players` 球员 | `rel_goals_team`、`rel_goals_match`、`rel_goals_player`，都从 `goals` 指向另一端 |
| 供应链 `supply_ontology_hand` | `产品`、`产品BOM`、`物料`、`库存` 等 | `产品关联产品bom`、`产品bom关联物料`、`物料关联库存` 等 |

**相关模块：** [BKN 引擎](bkn.md)、[Context Loader](context-loader.md)、[VEGA 引擎](vega.md)。

---

## 🚀 怎么调用

三种入口执行的是同一个编译器，结果相同。

### REST

```bash
curl -X POST "https://<访问地址>/api/bkn-backend/v1/knowledge-networks/<kn_id>/cypher-queries" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"query": "MATCH (t:teams) WHERE t.team_name = $name RETURN t.team_id AS id", "parameters": {"name": "Brazil"}}'
```

### MCP / 智能体

调用 Context Loader 的 `run_cypher` 工具，参数是 `kn_id`、`query`，可选 `parameters`、`object_properties` 和 `explain`。

### CLI

```bash
openbkn context tool-call <kn_id> run_cypher --args '{"query":"MATCH (g:goals) RETURN count(*) AS n"}'
```

请求体字段：

| 字段 | 说明 |
| --- | --- |
| `query` | Cypher 文本，最长 8192 字符 |
| `parameters` | `$name` 的取值。参数只能是值，不能是标签、关系或属性名；不接受 null |
| `object_properties` | 返回节点时，每个节点变量额外带回哪些属性，例如 `{"t": ["team_name"]}`。变量必须是查询里返回的节点，属性总数最多 128 个，授权规则与查询中其他属性相同 |
| `explain` | 为 `true` 时只返回执行计划、不读数据，见下文「先用 explain 校验」 |

分支用查询参数 `branch` 指定，默认 `main`。

响应里，`columns` 按 RETURN 的顺序列出列名，`entries` 是每一行（按列名取值）。返回节点或关系时，还会带上 `value_types` 和 `execution`（见下文「返回节点与关系」）。

### 先用 explain 校验

请求里带 `"explain": true` 时，查询照常解析、授权和编译，但不执行，也不读数据，响应里 `entries` 为空，改为返回 `plan`：

```json
{"query": "MATCH (g:goals)-[:rel_goals_team]->(t:teams) WITH t, count(*) AS n WHERE n > 200 RETURN t.team_name AS team, n", "explain": true}
```

- 会被拒的写法，返回的错误码和改写提示与执行时完全相同；能通过 explain 的查询，执行时也不会因为写法被拒。
- `plan` 里有：`shape`（查询形状：match、aggregate、totals、semi_join、list_comparison、carried）、`objects` 和 `relations`（每个变量对应的对象类、走的关系类）、`conditions`（每个条件的运算符、读的属性，以及作用在行、分组还是前一段上）、`grouping`、`columns`（每一列的名字、怎么算出来、值类型）、`ordering`、`limit`（生效的行数上限）、`reads`（会读的属性）、`row_filtered`（行范围被访问策略收窄的对象类）和 `warnings`。
- `warnings` 会提醒：模式里有既没有关系、共享节点，也没有 `a.code = b.code` 这类相等条件连起来的部分（结果是两两组合）、没写 LIMIT 时最多返回 1000 行、行范围被收窄、`collect` 的证据达到 10000 行会被拒。每条提醒在 `warning_codes` 里有对应的编号（`disconnected_parts`、`default_limit`、`row_filtered`、`collect_cap`），顺序相同，方便按编号显示成自己的语言。
- explain 不读数据，也看不出对象类绑定的是什么数据源：绑定数据集或索引的对象类上，「数据集和索引上的对象类」一节里不支持的写法 explain 照样通过，执行时才被拒。
- 不返回 SQL，条件里写的值也不重复列出。权限与执行时相同：读不到的对象类和属性，explain 里同样看不到。
- 适合先检查带 WITH、多段 MATCH 或多跳的复杂查询，确认分组、过滤和连接都符合预期，再去执行。

---

## 🧭 名字怎么写

- **标签**写对象类的 id 或名称，**关系类型**写关系类的 id 或名称，两者都从 `search_schema`、`get_object_types`、`get_relation_types` 的返回里取。
- **属性**写逻辑属性名。显示名、物理列名都不认。逻辑属性（由函数或指标计算的属性）不能在 Cypher 里用，要用 `get_logic_properties_values` 单独读取。
- 名字里有空格或特殊字符时，用反引号括起来：`` (p:`产品BOM`) ``。

---

## 🧩 MATCH：描述要找的形状

```cypher
MATCH (g:goals)-[:rel_goals_team]->(t:teams)
WHERE t.team_name = 'Brazil'
RETURN count(*) AS goals
```

- 一条路径可以有多跳；关系可以写成 `-[:R]->`、`<-[:R]-`，或无向的 `-[:R]-`。每条关系必须写明且只写一个关系类，每个节点第一次出现时必须写明且只写一个对象类。
- 节点可以带内联属性 `(t:teams {team_name: 'Brazil'})`，等价于 WHERE 里的相等条件。
- **同名变量指同一个节点**。可以用逗号写多条路径，也可以写多个 MATCH，它们描述的是同一个形状；两条路径共享一个变量就是在这个节点处相交，不共享变量则两两组合。只要有两部分既没连起来、又都没有任何条件限制（结果是两张表所有行的两两组合），查询就会被拒（`Unsupported`）；用关系类或 `a.code = b.code` 这类相等条件连起来，或者给其中一边加条件，比如 `(c:Customer {code: 'A'})`。
- **比较两组对象**（例如两个产品共用哪些物料）不需要 WITH，让两条路径共享节点即可：

```cypher
MATCH (p1:产品 {material_number: 'P61-000351'})-[:产品关联产品bom]->(:产品BOM)-[:产品bom关联物料]->(m:物料)
      <-[:产品bom关联物料]-(:产品BOM)<-[:产品关联产品bom]-(p2:产品 {material_number: 'U00-000151'})
RETURN DISTINCT m.material_code AS code
```

- **关系唯一性**：按 openCypher 的规定，同一个 MATCH 里，同一关系类的两跳不会走同一条关系，编译器会自动加上这个条件。这要求两端对象类有主键；有一跳是无向的时无法表达这个条件，查询会被拒绝。写在不同 MATCH 里的两跳不受这个限制。
- 一条查询最多 8 条关系、9 个节点。

### OPTIONAL MATCH

```cypher
MATCH (t:teams)
OPTIONAL MATCH (t)<-[:rel_goals_team]-(g:goals)
RETURN t.team_name AS team, count(g.goal_id) AS goals
ORDER BY goals, team LIMIT 5
```

匹配不到时这一行保留，可选侧的列为 null，所以 `count(可选侧的属性)` 会得到 0。限制：

- OPTIONAL MATCH 不能是查询的第一个读取子句，前面必须有 MATCH；
- 一个 OPTIONAL MATCH 只写一条关系，只引入一个新节点，挂在前面已经找到的节点上；
- 它引入的节点，不能再被后面的 MATCH 或查询的 WHERE 要求，否则保留下来的行又会被删掉。

---

## 🔍 WHERE：条件

| 写法 | 示例 |
| --- | --- |
| 比较 `=` `<>` `<` `>` `<=` `>=` | `g.minute_regulation >= 90` |
| 两个属性比较 | `g.team_id <> g.player_team_id`（乌龙球） |
| 列表 | `t.team_name IN ['Brazil', 'Germany']`，或 `IN $names` |
| 空值 | `g.match_period IS NULL`、`IS NOT NULL` |
| 字符串 | `STARTS WITH`、`ENDS WITH`、`CONTAINS` |
| 组合 | `AND`、`OR`、`NOT`，可以加括号 |
| 日期 | `o.placed_at >= date('2026-08-13')`（属性为日期或时间类型时） |
| 算术 | `o.amount * o.rate > $floor` |

说明：

- **不等于写 `<>`**。`!=` 不是 openCypher 语法，会报语法错误并提示改写。
- **`IN $list`**：整个列表作为一个参数传入，必须是非空、同一类型的标量数组，最多 500 个元素。
- **字符串匹配**是字面子串匹配，`%` 和 `_` 不是通配符；只能用在字符串属性上；大小写是否敏感取决于数据源的排序规则；属性为 null 时不匹配。`LIKE` 不是 openCypher 语法，会提示改用这三种写法。
- **日期**：`date(...)`、`datetime(...)`、`localdatetime(...)` 只能用在日期、时间类型的属性上，参数是字符串字面量或参数。属性在模型里声明为字符串时，直接与字符串比较即可，例如 `m.match_date >= '2014-06-12'`。
- 模型里一个是字符串、一个是数字的两个属性不能互相比较。
- 不支持 `XOR`。

---

## 📤 RETURN：返回什么

```cypher
MATCH (g:goals)-[:rel_goals_team]->(t:teams)
RETURN t.team_name AS team, count(*) AS goals, sum(g.minute_regulation) AS minutes
ORDER BY goals DESC LIMIT 10
```

- **属性**，可以用 `AS` 起别名；不起别名时，列名就是写下的原文，如 `t.team_name`。
- **`DISTINCT`** 去重。
- **聚合**：`count`、`sum`、`avg`、`min`、`max`，以及各自的 `DISTINCT` 形式和 `count(*)`。`count(n)` 统计找到节点 n 的行数；`count(DISTINCT n)` 按主键统计不同对象的个数，主键有多个属性时按它们的组合区分，对象类没有主键时不能用。
- **没有 GROUP BY**：只要返回了聚合，就按 RETURN 里其余各列分组。写 GROUP BY 会报语法错误。
- **算术** `+ - * / %`，可以加括号，如 `count(*) * 2`、`sum(o.price * o.qty)`、`count(o.x) * 1.0 / count(*)`。
  - 两个整数相除会被拒绝：Cypher 会丢掉余数，而各个 SQL 库的做法不一致。要小数结果，把一边乘 `1.0`。
  - `%` 只能用于整数；不支持乘方 `^`。
  - 一个列既有聚合、又在聚合之外直接读属性时（如 `o.x - avg(o.x)`），这个属性必须同时作为单独一列返回。
- **条件作为返回值**：`g.penalty = '1' AS is_penalty`，值为 true、false，或在无法判断时为 null；可以作为分组列。要按条件排序，先给它起别名，再按别名排序。

### CASE 与 coalesce

```cypher
MATCH (g:goals)
RETURN CASE g.own_goal WHEN '1' THEN 'own' ELSE 'normal' END AS kind,
       count(*) AS n,
       sum(CASE WHEN g.penalty = '1' THEN 1 ELSE 0 END) AS penalties
```

- 两种形式都支持：`CASE WHEN <条件> THEN … ELSE … END`，以及 `CASE x WHEN v THEN … END`。没有 ELSE 且没有命中时结果为 null，所以 `count(CASE WHEN … THEN 1 END)` 就是条件计数。
- `coalesce(a, b, …)` 返回第一个不为 null 的值，至少两个参数。例如 `coalesce(g.penalty, -1)`，可以配合 OPTIONAL MATCH 给缺失的一侧兜底。
- 能写在算术能写的所有地方：RETURN、WITH、ORDER BY、比较的两侧，以及聚合的参数里。
- 各分支（或各参数）必须都是数字，或者都不是数字；null 字面量两种都可以搭配。分支结果不能是布尔值（包括布尔类型的属性），要返回条件就直接返回条件本身。
- 用在比较里时，另一侧要和结果同类：字符串结果和字符串字面量、参数或字符串属性比较，如 `coalesce(c.nickname, c.name) = $name`；数字结果和数字比较。字符串和数字互相比较会被拒绝，因为 MySQL 会把字符串转成数字再比，结果和 Cypher 不同。
- 结果是字符串的 CASE 不能参与算术，也不能用 `sum`、`avg` 聚合。
- 分支条件里不能直接写聚合（`CASE WHEN count(*) > 10`）。先用 WITH 给聚合命名，再在 CASE 里使用这个名字：

```cypher
MATCH (g:goals)-[:rel_goals_team]->(t:teams)
WITH t, count(*) AS n
RETURN t.team_name AS team, CASE WHEN n > 200 THEN 'top' ELSE 'rest' END AS tier
ORDER BY n DESC LIMIT 4
```

- 简单形式不能写 `WHEN null`，因为没有任何值等于 null，要写成 `CASE WHEN x IS NULL THEN …`。
- 类型按模型里声明的类型判断。

### ORDER BY / SKIP / LIMIT

- ORDER BY 可以用属性、返回的列名、聚合或算出来的值；加 `DESC` 倒序。
- 结果被聚合或 `DISTINCT` 折叠后，只能按返回的值排序。
- 不写 `LIMIT` 时最多返回 **1000** 行，返回满 1000 行时结果带 `limited: true`，说明可能还有更多；`LIMIT` 不能超过 **10000**，超过会被拒绝，不会悄悄截断。分页用 `SKIP n LIMIT m`。

---

## 🔗 WITH：分段处理

WITH 有四种用法，都只用 MATCH 前后的写法来区分。

### 1. 先聚合再过滤

WITH 写在所有 MATCH 之后，作用相当于 SQL 的 GROUP BY 加 HAVING：

```cypher
MATCH (g:goals)-[:rel_goals_team]->(t:teams)
WITH t, count(*) AS n WHERE n > 200
RETURN t.team_name AS team, n ORDER BY n DESC
```

- WITH 里的值要用 `AS` 命名。节点可以直接传下去（`WITH t`），按对象的主键分组，所以同名的两个对象不会被合并。传下去的节点可以在 RETURN 里直接返回，例如 `WITH p, t, count(g) AS n WHERE n > 1 RETURN p AS source, 'SCORED_FOR' AS relationType, t AS target, n`（见「虚拟边与证据」）。
- WITH 的 WHERE 里，只涉及分组键的条件在分组之前过滤，涉及聚合的条件对每个分组判断。
- **可以连写多个 WITH 接力计算**。只有第一个 WITH 能聚合，后面的 WITH 用前面的名字算新值，并各自加条件：

```cypher
MATCH (p:产品 {material_number: '382-000005'})-[:产品关联产品bom]->(b:产品BOM)
      -[:产品bom关联物料]->(m:物料)-[:物料关联库存]->(i:库存)
WHERE b.alt_priority = 0 AND i.stock_status = '可用'
WITH m.material_code AS code, b.standard_usage AS usage, sum(i.available_inventory_qty) AS stock
WITH code, usage * 50 AS required, stock - usage * 50 AS shortage
WHERE shortage < 0
RETURN code, required, shortage ORDER BY shortage
```

- **收集再计数**：第一个 WITH 里的 `collect([DISTINCT] x.p) AS s` 只能通过 `size(s)` 读取，即收集到的个数（跳过 null）。
- **对分组再统计**：聚合的 WITH 之后，RETURN 可以只写聚合（`count(*)`，或对 WITH 里某个名字求 `count`、`sum`、`avg`、`min`、`max`），对各个分组再统计一次，结果只有一行，因此不能写 DISTINCT 和 ORDER BY：

```cypher
WITH ['382-000005', 'P61-000351', 'U00-000151'] AS products
MATCH (p:产品)-[:产品关联产品bom]->(b:产品BOM)-[:产品bom关联物料]->(m:物料)
WHERE p.material_number IN products AND b.alt_priority = 0
WITH m.material_code AS code, collect(DISTINCT p.material_number) AS used_by
WHERE size(used_by) = 3
RETURN count(*) AS common_materials
```

### 2. 按上一段的值过滤（半连接）

第一段查出一批值，第二段只保留值在这批里面的行：

```cypher
MATCH (g:goals) WHERE g.own_goal = '1'
WITH DISTINCT g.team_id AS tid
MATCH (t:teams) WHERE t.team_id = tid
RETURN count(*) AS teams_with_own_goal
```

也可以用列表：`WITH collect(b.material_code) AS codes MATCH (m:物料) WHERE m.material_code IN codes`。

- 第二段的每一行只要值在第一段里就保留**一次**，不会因为第一段有重复值而重复。
- 这个 WITH 只能有一项。不写 DISTINCT 时，RETURN 必须写 DISTINCT 且不能聚合。
- `v` 只能用在 `=` 里，`s` 只能用在 `IN` 里，而且要作为 WHERE 顶层的一个独立条件，不能放在 OR 或 NOT 下面。`v` 也可以写在内联属性里：`{q: v}`。
- 第二段之后不能再接 WITH。

### 3. 比较两个收集的列表

两段各自收集一个列表，RETURN 返回两个列表的大小，以及它们共有的元素个数：

```cypher
MATCH (p1:产品 {material_number: 'P61-000351'})-[:产品关联产品bom]->(b1:产品BOM)-[:产品bom关联物料]->(m1:物料)
WHERE b1.alt_priority = 0
WITH collect(DISTINCT m1.material_code) AS codes1
MATCH (p2:产品 {material_number: 'U00-000151'})-[:产品关联产品bom]->(b2:产品BOM)-[:产品bom关联物料]->(m2:物料)
WHERE b2.alt_priority = 0
WITH codes1, collect(DISTINCT m2.material_code) AS codes2
RETURN size([x IN codes1 WHERE x IN codes2]) AS common, size(codes1) AS n1, size(codes2) AS n2
```

- 两个 WITH 各收集一个属性：`collect([DISTINCT] x.p) AS 名字`；第二个 WITH 还要把第一个列表原样传下去。两个 WITH 都不能带 WHERE，第二段 MATCH 也不能使用第一个列表。
- RETURN 只能写三种大小：`size(a)`、`size(b)`，以及共有元素个数 `size([x IN a WHERE x IN b])`（反过来写 `[x IN b WHERE x IN a]` 也可以）。结果是一行。
- 没写 DISTINCT 的列表保留重复值，共有元素个数按被过滤那个列表的重复次数计算，和 Cypher 一致；所以两个方向同时求，要求两个列表都写 DISTINCT。
- 第二段 MATCH 什么都没匹配到时，结果没有行；第一段没匹配到时，返回一行，第一个列表的大小和共有元素个数都是 0。
- 不能直接返回列表本身。要列出共有的值，用上面「比较两组对象」的共享节点写法。

### 4. 把节点传给下一段 MATCH

WITH 把节点和为它算出的值传下去，下一段 MATCH 从这些节点接着走，可以在第二层再聚合：

```cypher
MATCH (p:players)<-[:rel_goals_player]-(g:goals)
WITH p, count(g) AS total WHERE total >= 10
MATCH (p)<-[:rel_goals_player]-(g2:goals)-[:rel_goals_team]->(t:teams)
RETURN p.family_name AS name, t.team_name AS team, count(g2) AS n, total
ORDER BY total DESC, name, team
```

- 下一段 MATCH 里，传下来的节点可以不写标签（`(p)`），按主键和上一段对应起来。WITH 传下来的每一行都会和下一段匹配到的行连接，下一段没匹配到的节点不出现在结果里。
- WITH 里命名的值（如 `total`）可以在 RETURN 和 ORDER BY 里使用和计算，例如 `count(g2) * 1.0 / total AS share`。查询含聚合时，在表达式或 ORDER BY 里用到它，都要把它也作为单独一列返回（`RETURN …, total, count(g2) * 1.0 / total AS share`），这样它才是分组键。
- 这些值不能写在下一段 MATCH 的 WHERE 里；对它们的条件写在 WITH 自己的 WHERE 里。
- 传下来的每个节点都必须在下一段 MATCH 里用到；节点不能改名（`WITH p AS q`），也不能和 `collect` 列表一起传。
- 下一段不能是 OPTIONAL MATCH，之后也不能再接 WITH；传节点的 WITH 不能写 DISTINCT、ORDER BY、SKIP 或 LIMIT。
- WITH 不聚合时（`WITH p`），每个节点按第一段匹配到的行数重复传下去，下一段的计数会跟着成倍增加；要每个节点只传一次，就在 WITH 里聚合，例如 `WITH p, count(*) AS n`。
- RETURN 可以返回下一段的节点和传下来的节点，画成带数值的虚拟边：`RETURN p AS source, 'SCORED_FOR' AS relationType, t AS target, count(g2) AS n, total`。

### 开头定义常量

这几种用法都可以用一个只含字面量、列表或参数的 WITH 开头，后面 MATCH 的条件按名字使用这些值（见上面「对分组再统计」的示例）。

### WITH 仍不支持的写法

- 第二个 WITH 之后再写 MATCH；
- 第二段 MATCH 之后再接 WITH（比较两个列表除外）；
- `WITH DISTINCT`（半连接除外）、`WITH *`；
- WITH 上的 ORDER BY、SKIP、LIMIT；
- WITH 的 WHERE 里写 IN 或字符串匹配；
- 第一个之后的 WITH 再聚合。

---

## 🕸️ 返回节点与关系

RETURN 可以直接返回节点、关系、`collect(...)` 和 map `{k: v}`：

```cypher
MATCH (g:goals)-[:rel_goals_team]->(t:teams)
RETURN t AS team, count(*) AS n ORDER BY n DESC LIMIT 3
```

请求里带上 `"object_properties": {"t": ["team_name"]}`，返回的节点就会带上球队名称：

```json
{"kind": "object", "network_id": "worldcup_vega_catalog_bkn", "branch": "main",
 "object_type_id": "teams",
 "key": {"key_id": {"type": "integer", "value": "9"}},
 "properties": {"team_name": "Brazil"}}
```

- **节点**：对象类加上完整主键。主键值一律是精确文本，带声明的类型，整数也是字符串。
- **关系**：关系类、两端节点的身份，以及连接依据 `basis`（连接用的键名和键值）。关系没有自己的属性，写 `r.x` 会被拒绝。
- 节点和关系按身份分组，所以 `RETURN t, count(*)` 是按对象计数。
- 响应额外带上 `value_types`（每列的类型）和 `execution`（`match_rows`、`match_complete`、`conclusions_limited`）。

### 虚拟边与证据

用 `source`、`relationType`、`target` 三列加上 `collect` 证据，可以表达一条由路径推导出来、但模型里没有登记的关系：

```cypher
MATCH (p:产品)-[:产品关联产品bom]->(b:产品BOM)-[:产品bom关联物料]->(m:物料)
WHERE p.material_number = 'U00-000151'
RETURN p AS source, 'USES' AS relationType, m AS target, collect({bom: b}) AS evidence
LIMIT 3
```

- 带 `collect` 时，每一行是一个「结论」，即 collect 旁边那些列的取值；LIMIT 数的是结论的个数，每个结论的证据会全部收进来。
- 证据达到 10000 行时整条查询失败（`ReadBudgetExceeded`），不会只返回一部分。这时加条件或调低 LIMIT。
- 图值不能排序，也不能 DISTINCT；`collect` 不能和其他聚合混用；`collect` 旁边只能返回节点、关系、属性和常量，排序只能按返回的属性的别名。

---

## 🗂️ 数据集和索引上的对象类

对象类绑定的资源不在 SQL 数据源里，比如数据集（dataset）或 OpenSearch 索引，或者一条查询读到的对象类分布在多个数据源时，查询不能编译成一条 SQL，改为通过资源数据接口逐表读取，再关联起来。调用方式不变，系统会自动选择。

这时只支持基础写法：

| 写法 | 数据集和索引上 |
| --- | --- |
| MATCH、多跳关系、OPTIONAL MATCH | 支持 |
| WHERE 条件（比较、IN、字符串匹配、IS NULL、算术） | 支持 |
| RETURN 属性、聚合（count、sum、avg、min、max）、ORDER BY / SKIP / LIMIT | 支持 |
| WITH 先聚合再过滤、按上一段的值过滤（半连接） | 支持 |
| CASE、coalesce | 不支持 |
| 对分组再统计、比较两个列表、把节点传给下一个 MATCH | 不支持 |
| 返回节点、关系和虚拟边 | 支持，和 SQL 数据源上一样画图 |
| `collect` | 不支持 |

- 用到不支持的写法时返回 400，说明哪些写法不能用。explain 只做编译，看不出对象类存放在哪里，所以这类拒绝要到执行时才会出现。
- 数据要逐表读出再关联，比 SQL 慢；要读的数据超过上限或超时会返回 400 `ReadBudgetExceeded`，请加条件缩小范围。
- 这里只有关系类能按键关联。两个节点只靠 `a.code = b.code` 这类相等条件连起来时，两边会先全部读出、两两组合，再按条件过滤，很容易超过上限；能用关系类就用关系类。explain 不会对这种情况给出提醒。

---

## ⛔ 不支持的写法

| 写法 | 改写建议 |
| --- | --- |
| `!=` | 写 `<>` |
| `LIKE` | 用 `CONTAINS`、`STARTS WITH`、`ENDS WITH` |
| `GROUP BY` | 不写，按 RETURN 里其余各列自动分组 |
| `UNION` | 拆成两条查询 |
| 变长路径 `[:R*1..3]`、路径变量 `p = ...` | 按跳数分别写成固定长度的路径 |
| `UNWIND`、下标 `xs[0]`；列表推导式 `[x IN … WHERE …]` 只能用在比较两个列表的 `size(...)` 里 | 数两个列表的共有元素用 `size([x IN a WHERE x IN b])`；列出共有的值，在一个 MATCH 里共享节点 |
| 日期函数、`coalesce()`、`size()`（只用于 WITH 收集的列表）和聚合以外的函数调用 | 暂不支持 |
| `XOR`、乘方 `^` | 用 AND / OR / NOT 改写；乘方展开为乘法 |
| 关系属性 `r.x` | 关系没有自己的属性；读两端节点的属性 |
| 第二个 WITH 之后再 MATCH | 让两条路径在一个 MATCH 里共享节点 |
| `CREATE`、`MERGE`、`SET`、`DELETE`、`REMOVE`、`CALL` | 只读接口，不支持写操作和过程调用 |

---

## 🔐 权限

- 每个被查询的对象类和关系类都需要 `query_data` 权限；知识网络级别的授权可以覆盖。
- 查询中写到的每个属性（RETURN、WHERE、ORDER BY、聚合、内联属性）都要求调用者对它有 `full` 级别的访问权限：
  - 级别为 `none` 的属性对调用者来说不存在，写了会报「未知属性」，和写错属性名一样；
  - 级别为 `masked`（列脱敏）或 `schema`（仅结构）的属性会返回 403 `PropertyForbidden`，并指出是哪个属性。
- 行级过滤规则会编译进 SQL，WITH 的第一段（半连接）同样受约束。
- 调用者没有权限的对象类不会出现在编译所用的模型里，写了会报「未知标签」。因此不能用这个接口探测模型里有什么。
- 生成的 SQL 不会返回给调用者，因为它包含物理表名和列名。

---

## ❗ 错误码

| HTTP | error_code | 含义 |
| --- | --- | --- |
| 400 | `BknBackend.Cypher.SyntaxError` | 无法解析 |
| 400 | `BknBackend.Cypher.Unsupported` | 合法的 openCypher，但不在支持的子集内；或模式里有两部分既没连起来、又都没有条件 |
| 400 | `BknBackend.Cypher.InvalidQuery` | 与模型不匹配：未知标签或属性、类型不符等 |
| 400 | `BknBackend.Cypher.LimitExceeded` | `LIMIT` 超过 10000 |
| 400 | `BknBackend.Cypher.InvalidParameter` | 查询为空或过长、参数不合法 |
| 400 | `BknBackend.Cypher.ReadBudgetExceeded` | `collect` 读取的证据达到 10000 行；或数据集、索引上的查询要读的数据超过上限或超时 |
| 403 | `BknBackend.Cypher.PropertyForbidden` | 写到了脱敏或仅结构的属性 |
| 403 | `BknBackend.Cypher.Forbidden` | 数据源拒绝了查询读取的资源，知识网络的代理账号也不可用 |
| 500 | `BknBackend.Cypher.QueryFailed` | 编译出的语句执行失败 |

`error_details` 是编译器给出的说明，带有出错构造所在的行号和列号，通常还附有改写建议。

---

## 📚 参考

- 接口定义：[`docs/api/bkn/cypher-queries.yaml`](../../../docs/api/bkn/cypher-queries.yaml)
- MCP 工具定义：[`docs/api/context-loader/cypher.yaml`](../../../docs/api/context-loader/cypher.yaml)
- 语法文件（openCypher M19）：[`adp/bkn/bkn-backend/server/logics/cypher/parser/`](../../../adp/bkn/bkn-backend/server/logics/cypher/parser/README.md)
- openCypher 规范：<https://opencypher.org/resources/>
