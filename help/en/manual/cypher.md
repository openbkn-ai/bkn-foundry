# 🔎 Cypher Queries

## 📖 Overview

BKN answers read-only **openCypher** queries over the instance data of a knowledge network. A query is written against the **model**: labels are object types, relationship types are relation types, and properties are logical property names. The server compiles the query into one SQL statement and runs it on the data source through VEGA, so the caller never needs physical table or column names.

What is supported is a **read-only subset** of openCypher 9 (M19). The subset only narrows the language and adds no syntax of its own: a query that runs means what it would mean in any openCypher engine. A construct outside the subset is refused by name, with the line and column where it appears and, usually, a suggested rewrite. Nothing is approximated.

The examples on this page use two sample knowledge networks:

| Knowledge network | Object types (labels) | Relation types |
| --- | --- | --- |
| World Cup `worldcup_vega_catalog_bkn` | `goals`, `teams`, `matches`, `players` | `rel_goals_team`, `rel_goals_match`, `rel_goals_player`, each from `goals` to the other end |
| Supply chain `supply_ontology_hand` | `产品` (product), `产品BOM` (BOM line), `物料` (material), `库存` (inventory), ... | `产品关联产品bom`, `产品bom关联物料`, `物料关联库存`, ... |

**Related modules:** [BKN Engine](bkn.md), [Context Loader](context-loader.md), [VEGA Engine](vega.md).

---

## 🚀 Running a query

All three entry points use the same compiler and return the same result.

### REST

```bash
curl -X POST "https://<access-address>/api/bkn-backend/v1/knowledge-networks/<kn_id>/cypher-queries" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"query": "MATCH (t:teams) WHERE t.team_name = $name RETURN t.team_id AS id", "parameters": {"name": "Brazil"}}'
```

### MCP / agents

Call the Context Loader tool `run_cypher` with `kn_id` and `query`, and optionally `parameters` and `object_properties`.

### CLI

```bash
openbkn context tool-call <kn_id> run_cypher --args '{"query":"MATCH (g:goals) RETURN count(*) AS n"}'
```

Request fields:

| Field | Description |
| --- | --- |
| `query` | The Cypher text, at most 8192 characters |
| `parameters` | Values for `$name`. A parameter is always a value, never a label, relationship type or property name; null is not accepted |
| `object_properties` | Extra properties a returned node carries, per node variable, as in `{"t": ["team_name"]}`. Each variable must be a node the query returns; at most 128 properties in all, authorized like any other property the query names |

The branch is chosen with the `branch` query parameter, `main` by default.

In the response, `columns` lists the columns in RETURN order and `entries` holds the rows, keyed by column name. A query that returns nodes or relationships also gets `value_types` and `execution` (see "Returning nodes and relationships" below).

---

## 🧭 Naming things

- A **label** is an object type's id or name, and a **relationship type** is a relation type's id or name, both as `search_schema`, `get_object_types` and `get_relation_types` return them.
- A **property** is a logical property name. Display names and physical column names are not accepted. Logic properties (computed by a function or a metric) cannot be used in Cypher; read them with `get_logic_properties_values`.
- Quote a name that holds spaces or special characters with backticks: `` (p:`产品BOM`) ``.

---

## 🧩 MATCH: the shape to find

```cypher
MATCH (g:goals)-[:rel_goals_team]->(t:teams)
WHERE t.team_name = 'Brazil'
RETURN count(*) AS goals
```

- A path may take several hops. A relationship is written `-[:R]->`, `<-[:R]-`, or undirected `-[:R]-`, and names exactly one relation type. A node names exactly one object type where its variable first appears.
- A node may carry inline properties, `(t:teams {team_name: 'Brazil'})`, which mean the same equalities in WHERE.
- **A variable written twice is the same node.** Several comma-separated paths, or several MATCH clauses, describe one shape: two paths that share a variable meet at that node, and paths that share none are a cartesian product.
- **To compare two sets** (the materials two products have in common, say), no WITH is needed. Let the two paths share a node:

```cypher
MATCH (p1:产品 {material_number: 'P61-000351'})-[:产品关联产品bom]->(:产品BOM)-[:产品bom关联物料]->(m:物料)
      <-[:产品bom关联物料]-(:产品BOM)<-[:产品关联产品bom]-(p2:产品 {material_number: 'U00-000151'})
RETURN DISTINCT m.material_code AS code
```

- **Relationship uniqueness**: as openCypher requires, two hops over one relation type in one MATCH never traverse the same relationship, and the compiler adds that condition. It needs both object types to have a primary key; where one of the hops is undirected the condition cannot be stated and the query is refused. Hops in different MATCH clauses are not compared.
- A query holds at most 8 relationships and 9 nodes.

### OPTIONAL MATCH

```cypher
MATCH (t:teams)
OPTIONAL MATCH (t)<-[:rel_goals_team]-(g:goals)
RETURN t.team_name AS team, count(g.goal_id) AS goals
ORDER BY goals, team LIMIT 5
```

When nothing matches, the row stays and the optional side's columns are null, so `count(<a property of the optional side>)` gives 0. Limits:

- OPTIONAL MATCH cannot be the first reading clause of the query; a MATCH must come before it.
- One OPTIONAL MATCH writes one relationship and introduces one new node, attached to a node an earlier clause found.
- What it introduces may not be required again by a later MATCH or by the query's WHERE, which would drop the rows it kept.

---

## 🔍 WHERE: conditions

| Form | Example |
| --- | --- |
| Comparison `=` `<>` `<` `>` `<=` `>=` | `g.minute_regulation >= 90` |
| Two properties | `g.team_id <> g.player_team_id` (own goals) |
| List | `t.team_name IN ['Brazil', 'Germany']`, or `IN $names` |
| Null | `g.match_period IS NULL`, `IS NOT NULL` |
| String | `STARTS WITH`, `ENDS WITH`, `CONTAINS` |
| Combining | `AND`, `OR`, `NOT`, with parentheses |
| Date | `o.placed_at >= date('2026-08-13')` (on a date or time property) |
| Arithmetic | `o.amount * o.rate > $floor` |

Notes:

- **Not-equal is `<>`.** `!=` is not openCypher; it is a syntax error that says so.
- **`IN $list`** takes the whole list as one parameter: a non-empty array of scalars of one kind, at most 500 elements.
- **String matching** is a literal substring match: `%` and `_` are not wildcards. It applies to string properties only, case sensitivity follows the data source's collation, and a null property never matches. `LIKE` is not openCypher; the error suggests these three instead.
- **Dates**: `date(...)`, `datetime(...)` and `localdatetime(...)` apply to date and time properties, over a string literal or a parameter. A property the model declares as a string is compared with a string, as in `m.match_date >= '2014-06-12'`.
- Two properties the model types as a string and a number cannot be compared.
- `XOR` is not supported.

---

## 📤 RETURN: what comes back

```cypher
MATCH (g:goals)-[:rel_goals_team]->(t:teams)
RETURN t.team_name AS team, count(*) AS goals, sum(g.minute_regulation) AS minutes
ORDER BY goals DESC LIMIT 10
```

- **Properties**, optionally renamed with `AS`. Without an alias the column is named after what was written, as in `t.team_name`.
- **`DISTINCT`** removes duplicate rows.
- **Aggregates**: `count`, `sum`, `avg`, `min` and `max`, each also in its `DISTINCT` form, plus `count(*)`. `count(n)` counts the rows where node n was found; `count(DISTINCT n)` counts distinct objects by primary key, which needs the object type to have exactly one.
- **There is no GROUP BY.** Returning an aggregate groups by every other returned column. Writing GROUP BY is a syntax error.
- **Arithmetic** `+ - * / %` with parentheses, as in `count(*) * 2`, `sum(o.price * o.qty)` or `count(o.x) * 1.0 / count(*)`.
  - Dividing two integers is refused: Cypher drops the remainder, and SQL databases do not agree on it. Multiply one side by `1.0` for a fractional result.
  - `%` takes integers only; `^` is not supported.
  - A column that reads a property outside an aggregate beside one (as in `o.x - avg(o.x)`) needs that property returned as a column of its own.
- **A condition as a value**: `g.penalty = '1' AS is_penalty` gives true, false, or null when it cannot be decided, and groups like any other column. To sort by a condition, alias it and sort by the alias.

### CASE and coalesce

```cypher
MATCH (g:goals)
RETURN CASE g.own_goal WHEN '1' THEN 'own' ELSE 'normal' END AS kind,
       count(*) AS n,
       sum(CASE WHEN g.penalty = '1' THEN 1 ELSE 0 END) AS penalties
```

- Both forms are accepted: `CASE WHEN <condition> THEN … ELSE … END` and `CASE x WHEN v THEN … END`. With no ELSE, a row no branch matches gives null, so `count(CASE WHEN … THEN 1 END)` is a conditional count.
- `coalesce(a, b, …)` gives the first argument that is not null and takes at least two, as in `coalesce(g.penalty, -1)`, which fills in the missing side of an OPTIONAL MATCH.
- Both stand wherever arithmetic may: in RETURN, WITH, ORDER BY, either side of a comparison and the argument of an aggregate.
- The branches (or arguments) must all be numbers or all be something else; a null literal fits either. A branch may not give a boolean: return the condition itself for that.
- A CASE that gives strings cannot take part in arithmetic or in `sum` and `avg`.
- A branch's condition cannot hold an aggregate directly (`CASE WHEN count(*) > 10`). Name the aggregate in a WITH and use the name:

```cypher
MATCH (g:goals)-[:rel_goals_team]->(t:teams)
WITH t, count(*) AS n
RETURN t.team_name AS team, CASE WHEN n > 200 THEN 'top' ELSE 'rest' END AS tier
ORDER BY n DESC LIMIT 4
```

- The simple form cannot use `WHEN null`, since nothing equals null; write `CASE WHEN x IS NULL THEN …`.
- Types are those the model declares.

### ORDER BY / SKIP / LIMIT

- ORDER BY takes a property, a returned column name, an aggregate or a computed value; `DESC` reverses it.
- A result collapsed by aggregation or `DISTINCT` can only be sorted by a value it returns.
- Without `LIMIT` at most **1000** rows come back. `LIMIT` may not exceed **10000**: a larger one is refused, not quietly reduced. Page with `SKIP n LIMIT m`.

---

## 🔗 WITH: working in stages

WITH has two uses, told apart by what follows it.

### 1. Filtering after aggregating

A WITH after every MATCH works like SQL's GROUP BY with HAVING:

```cypher
MATCH (g:goals)-[:rel_goals_team]->(t:teams)
WITH t, count(*) AS n WHERE n > 200
RETURN t.team_name AS team, n ORDER BY n DESC
```

- Values in a WITH are named with `AS`. A node may be passed on as it is (`WITH t`) and groups by its primary key, so two objects that share a name stay two groups.
- In the WITH's WHERE, a condition on grouping keys filters rows before grouping, and one on aggregates is tested per group.
- **WITHs may be chained.** Only the first aggregates; later ones compute from the names before them, each with its own WHERE:

```cypher
MATCH (p:产品 {material_number: '382-000005'})-[:产品关联产品bom]->(b:产品BOM)
      -[:产品bom关联物料]->(m:物料)-[:物料关联库存]->(i:库存)
WHERE b.alt_priority = 0 AND i.stock_status = '可用'
WITH m.material_code AS code, b.standard_usage AS usage, sum(i.available_inventory_qty) AS stock
WITH code, usage * 50 AS required, stock - usage * 50 AS shortage
WHERE shortage < 0
RETURN code, required, shortage ORDER BY shortage
```

- **Collecting and counting**: `collect([DISTINCT] x.p) AS s` in the first WITH is read only as `size(s)`, the number of values collected (nulls are skipped).
- **Totals over groups**: after an aggregating WITH, RETURN may hold only aggregates (`count(*)`, or `count`, `sum`, `avg`, `min` or `max` of a name the WITH passed on), which then aggregate the groups and return one row, so DISTINCT and ORDER BY are not allowed there:

```cypher
WITH ['382-000005', 'P61-000351', 'U00-000151'] AS products
MATCH (p:产品)-[:产品关联产品bom]->(b:产品BOM)-[:产品bom关联物料]->(m:物料)
WHERE p.material_number IN products AND b.alt_priority = 0
WITH m.material_code AS code, collect(DISTINCT p.material_number) AS used_by
WHERE size(used_by) = 3
RETURN count(*) AS common_materials
```

### 2. Filtering by an earlier stage's values (semi-join)

The first stage produces values, and the second keeps the rows whose value is among them:

```cypher
MATCH (g:goals) WHERE g.own_goal = '1'
WITH DISTINCT g.team_id AS tid
MATCH (t:teams) WHERE t.team_id = tid
RETURN count(*) AS teams_with_own_goal
```

A list works too: `WITH collect(b.material_code) AS codes MATCH (m:物料) WHERE m.material_code IN codes`.

- Each row of the second stage is kept **once** if its value is among the first stage's, however often the first stage produced it.
- This WITH holds one item. Without DISTINCT, RETURN must be DISTINCT and may not aggregate.
- `v` is used only with `=` and `s` only with `IN`, each as a condition of its own at the top of WHERE, not under OR or NOT. `v` may also stand in an inline property map: `{q: v}`.
- No WITH may follow the second stage.

### Naming constants up front

Either use may open with a WITH that names literals, lists or parameters, which the conditions of the following MATCH use by name (see the totals example above).

### What WITH still refuses

- MATCH after a second WITH;
- `WITH DISTINCT` (outside the semi-join) and `WITH *`;
- ORDER BY, SKIP or LIMIT on a WITH;
- IN or string matching in a WITH's WHERE;
- aggregating in a WITH after the first.

---

## 🕸️ Returning nodes and relationships

RETURN may return nodes, relationships, `collect(...)` and maps `{k: v}`:

```cypher
MATCH (g:goals)-[:rel_goals_team]->(t:teams)
RETURN t AS team, count(*) AS n ORDER BY n DESC LIMIT 3
```

With `"object_properties": {"t": ["team_name"]}` in the request, each node carries the team name:

```json
{"kind": "object", "network_id": "worldcup_vega_catalog_bkn", "branch": "main",
 "object_type_id": "teams",
 "key": {"key_id": {"type": "integer", "value": "9"}},
 "properties": {"team_name": "Brazil"}}
```

- **A node** is its object type and full primary key. Key values are exact text with their declared type; integers are strings too.
- **A relationship** is its relation type, the identities of its two ends, and the `basis` that joined them (key names and values). Relationships have no properties of their own: `r.x` is refused.
- Nodes and relationships group by identity, so `RETURN t, count(*)` counts per object.
- The response also carries `value_types` (each column's type) and `execution` (`match_rows`, `match_complete`, `conclusions_limited`).

### Derived relationships with evidence

The columns `source`, `relationType` and `target`, with `collect` for evidence, express a relationship that a path implies but the model does not register:

```cypher
MATCH (p:产品)-[:产品关联产品bom]->(b:产品BOM)-[:产品bom关联物料]->(m:物料)
WHERE p.material_number = 'U00-000151'
RETURN p AS source, 'USES' AS relationType, m AS target, collect({bom: b}) AS evidence
LIMIT 3
```

- With `collect`, each row is a conclusion: the values beside `collect`. LIMIT counts conclusions, and every matched row of each conclusion is collected.
- Evidence reaching 10000 rows fails the whole query (`ReadBudgetExceeded`) rather than returning part of it; add conditions or lower LIMIT.
- Graph values cannot be sorted or made DISTINCT; `collect` cannot be mixed with other aggregates; beside `collect` only nodes, relationships, properties and constants may be returned, and sorting is by the alias of a returned property.

---

## ⛔ Not supported

| Construct | Instead |
| --- | --- |
| `!=` | Write `<>` |
| `LIKE` | Use `CONTAINS`, `STARTS WITH` or `ENDS WITH` |
| `GROUP BY` | Leave it out: aggregation groups by the other returned columns |
| `UNION` | Run two queries |
| Variable-length paths `[:R*1..3]`, path variables `p = ...` | Write each length as a fixed path |
| `UNWIND`, list comprehensions `[x IN … WHERE …]`, indexing `xs[0]` | To intersect two sets, share a node in one MATCH |
| Function calls other than the date functions, `coalesce()`, `size()` (on a list a WITH collected) and the aggregates | Not supported yet |
| `XOR`, exponentiation `^` | Rewrite with AND / OR / NOT; multiply out |
| Relationship properties `r.x` | Relationships have none; read the end nodes' properties |
| MATCH after a second WITH | Share a node between the paths in one MATCH |
| `CREATE`, `MERGE`, `SET`, `DELETE`, `REMOVE`, `CALL` | The interface is read-only and runs no procedures |

---

## 🔐 Permissions

- Each object type and relation type the query reads needs `query_data`; a network-wide grant covers them.
- Every property the query names (in RETURN, WHERE, ORDER BY, an aggregate or an inline map) needs the caller's access level `full`:
  - a property at level `none` does not exist for the caller, so naming it reads as an unknown property, exactly like a misspelt one;
  - a property at level `masked` (column masking) or `schema` (structure only) is refused with 403 `PropertyForbidden`, naming it.
- Row filters are compiled into the SQL, including the first stage of a semi-join.
- An object type the caller may not query is absent from the model the query compiles against, so naming it reads as an unknown label. The interface cannot be used to find out what a model contains.
- The generated SQL is never returned: it names physical tables and columns.

---

## ❗ Error codes

| HTTP | error_code | Meaning |
| --- | --- | --- |
| 400 | `BknBackend.Cypher.SyntaxError` | Could not be parsed |
| 400 | `BknBackend.Cypher.Unsupported` | Valid openCypher outside the supported subset |
| 400 | `BknBackend.Cypher.InvalidQuery` | Does not match the model: an unknown label or property, a type mismatch, ... |
| 400 | `BknBackend.Cypher.LimitExceeded` | `LIMIT` above 10000 |
| 400 | `BknBackend.Cypher.InvalidParameter` | Empty or oversized query, or an invalid parameter |
| 400 | `BknBackend.Cypher.ReadBudgetExceeded` | The evidence a `collect` reads reaches 10000 rows |
| 403 | `BknBackend.Cypher.PropertyForbidden` | Names a masked or structure-only property |
| 403 | `BknBackend.Cypher.Forbidden` | The data source refused a resource the query reads, and the network's proxy account could not be used |
| 500 | `BknBackend.Cypher.QueryFailed` | The compiled statement failed to run |

`error_details` carries the compiler's message, with the line and column of the construct and usually a suggested rewrite.

---

## 📚 References

- API definition: [`docs/api/bkn/cypher-queries.yaml`](../../../docs/api/bkn/cypher-queries.yaml)
- MCP tool definition: [`docs/api/context-loader/cypher.yaml`](../../../docs/api/context-loader/cypher.yaml)
- Grammar (openCypher M19): [`adp/bkn/bkn-backend/server/logics/cypher/parser/`](../../../adp/bkn/bkn-backend/server/logics/cypher/parser/README.md)
- openCypher specification: <https://opencypher.org/resources/>
