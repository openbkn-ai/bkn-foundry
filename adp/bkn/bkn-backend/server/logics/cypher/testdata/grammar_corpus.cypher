# Every construct of the openCypher M19 grammar, one query per line.
#
# This corpus exists to prove a property the unit tests cannot state on their
# own: that the compiler has an answer for the whole language, not only for the
# part it implements. The accompanying test asserts that every rule of the
# grammar is reached by at least one query here, and that each query is either
# compiled or refused by name -- never a panic, and never an error of a type
# nobody maps to a status.
#
# Queries are written against the probe network used for live runs
# (cypher_probe_fcj), so the same file can be replayed against a deployment.
# Blank lines and lines starting with # are ignored.


# projection
MATCH (o:probe_order) RETURN o.o_key AS k LIMIT 2
MATCH (o:probe_order) RETURN o.o_key LIMIT 1
MATCH (o:probe_order) RETURN o.o_key AS k, o.o_state AS s, o.o_amount AS a LIMIT 2
MATCH (o:probe_order) RETURN DISTINCT o.o_state AS s

# clauses
MATCH (o:probe_order) RETURN o.o_key AS k ORDER BY o.o_key LIMIT 3
MATCH (o:probe_order) RETURN o.o_key AS k ORDER BY o.o_key DESC LIMIT 3
MATCH (o:probe_order) RETURN o.o_key AS k ORDER BY o.o_state, o.o_key DESC LIMIT 3
MATCH (o:probe_order) RETURN o.o_key AS k ORDER BY o.o_key SKIP 5 LIMIT 3
MATCH (o:probe_order) RETURN o.o_key AS k LIMIT 1
MATCH (o:probe_order) RETURN DISTINCT o.o_state AS s ORDER BY o.o_state

# operators
MATCH (o:probe_order) WHERE o.o_state = 'refunding' RETURN o.o_key AS k LIMIT 2
MATCH (o:probe_order) WHERE o.o_state <> 'refunding' RETURN o.o_key AS k LIMIT 2
MATCH (o:probe_order) WHERE o.o_amount < 100 RETURN o.o_key AS k LIMIT 2
MATCH (o:probe_order) WHERE o.o_amount > 100 RETURN o.o_key AS k LIMIT 2
MATCH (o:probe_order) WHERE o.o_amount <= 100 RETURN o.o_key AS k LIMIT 2
MATCH (o:probe_order) WHERE o.o_amount >= 100 RETURN o.o_key AS k LIMIT 2
MATCH (o:probe_order) WHERE o.o_amount > 10 AND o.o_state = 'refunding' AND o.o_chan >= 1 RETURN o.o_key AS k LIMIT 2

# literals
MATCH (o:probe_order) WHERE o.o_state = 'refunding' RETURN o.o_key AS k LIMIT 1
MATCH (o:probe_order) WHERE o.o_state = "refunding" RETURN o.o_key AS k LIMIT 1
MATCH (o:probe_order) WHERE o.o_key = 10774 RETURN o.o_key AS k
MATCH (o:probe_order) WHERE o.o_amount > -1 RETURN o.o_key AS k LIMIT 1
MATCH (o:probe_order) WHERE o.o_amount > 1.5 RETURN o.o_key AS k LIMIT 1
MATCH (o:probe_order) WHERE o.o_key = 0x2A16 RETURN o.o_key AS k
MATCH (o:probe_order) WHERE o.o_key = 0o25126 RETURN o.o_key AS k
MATCH (o:probe_order) WHERE o.o_state = 'a\tb' RETURN o.o_key AS k LIMIT 1
MATCH (o:probe_order) WHERE o.o_state = '\u0041' RETURN o.o_key AS k LIMIT 1
MATCH (o:probe_order) WHERE o.o_state = '\U0001F600' RETURN o.o_key AS k LIMIT 1
MATCH (o:probe_order) WHERE o.o_state = 'O''Brien' RETURN o.o_key AS k LIMIT 1
MATCH (o:probe_order) WHERE o.o_state = 'a\'; DROP TABLE x --' RETURN o.o_key AS k LIMIT 1
MATCH (o:probe_order) WHERE o.o_state = 'back\\slash' RETURN o.o_key AS k LIMIT 1

# patterns
MATCH (i:probe_item)-[:probe_direct]->(o:probe_order) RETURN i.i_key AS i, o.o_key AS o LIMIT 2
MATCH (o:probe_order)<-[:probe_direct]-(i:probe_item) RETURN i.i_key AS i LIMIT 2
MATCH (i:probe_item)-[:probe_direct]->(:probe_order) RETURN i.i_key AS i LIMIT 2
MATCH ((o:probe_order)) RETURN o.o_key AS k LIMIT 1
MATCH (i:probe_item)-[:probe_direct2]->(o:probe_order) RETURN i.i_key AS i, o.o_key AS o LIMIT 2
MATCH (o:`探针订单`) RETURN o.o_key AS k LIMIT 1
MATCH (i:probe_item)-[:`探针明细属于订单`]->(o:probe_order) RETURN o.o_key AS k LIMIT 1
match (o:probe_order) return o.o_key as k limit 1
MATCH (o:probe_order) RETURN o.o_key AS k LIMIT 1

# reject-clause
OPTIONAL MATCH (o:probe_order) RETURN o.o_key
MATCH (o:probe_order) RETURN o.o_key UNION MATCH (p:probe_order) RETURN p.o_key
MATCH (o:probe_order) WITH o RETURN o.o_key
UNWIND [1, 2] AS x RETURN x
CALL db.labels() YIELD label RETURN label
MATCH (o:probe_order) MATCH (i:probe_item) RETURN o.o_key
MATCH (o:probe_order), (i:probe_item) RETURN o.o_key
MATCH p = (o:probe_order) RETURN o.o_key
CALL db.labels()
CALL db.labels() YIELD label AS l RETURN l
CALL db.labels

# reject-write
CREATE (o:probe_order) RETURN o.o_key
MATCH (o:probe_order) SET o.o_state = 'x' RETURN o.o_key
MATCH (o:probe_order) DETACH DELETE o RETURN o.o_key
MATCH (o:probe_order) REMOVE o.o_state RETURN o.o_key
MERGE (o:probe_order) RETURN o.o_key
MERGE (o:probe_order) ON CREATE SET o.o_state = 'x' RETURN o.o_key

# reject-pattern
MATCH (o) RETURN o.o_key
MATCH (o:probe_order:probe_item) RETURN o.o_key
MATCH (o:probe_order {o_key: 1}) RETURN o.o_key
MATCH (i:probe_item)-[:probe_direct]-(o:probe_order) RETURN i.i_key
MATCH (i:probe_item)<-[:probe_direct]->(o:probe_order) RETURN i.i_key
MATCH (i:probe_item)-->(o:probe_order) RETURN i.i_key
MATCH (i:probe_item)-[r:probe_direct]->(o:probe_order) RETURN i.i_key
MATCH (i:probe_item)-[:probe_direct*1..3]->(o:probe_order) RETURN i.i_key
MATCH (i:probe_item)-[:probe_direct|:probe_direct2]->(o:probe_order) RETURN i.i_key
MATCH (i:probe_item)-[:probe_direct {x: 1}]->(o:probe_order) RETURN i.i_key
MATCH (c:probe_channel)-[:probe_fcj]->(o:probe_order)-[:probe_direct]->(i:probe_item) RETURN o.o_key

# aggregates
MATCH (o:probe_order) RETURN count(*) AS n
MATCH (o:probe_order) RETURN count(o.o_state) AS n
MATCH (o:probe_order) RETURN count(DISTINCT o.o_state) AS n
MATCH (o:probe_order) RETURN o.o_state AS s, count(*) AS n
MATCH (o:probe_order) RETURN o.o_state AS s, count(*) AS n ORDER BY n DESC LIMIT 5
MATCH (o:probe_order) RETURN o.o_state AS s, count(*) AS n ORDER BY count(*) DESC LIMIT 5
MATCH (o:probe_order) RETURN sum(o.o_amount) AS total, avg(o.o_amount) AS mean, min(o.o_amount) AS lo, max(o.o_amount) AS hi
MATCH (i:probe_item)-[:probe_direct]->(o:probe_order) RETURN o.o_state AS s, count(*) AS n
MATCH (o:probe_order) WHERE o.o_amount > 100 RETURN o.o_state AS s, count(*) AS n

# reject-aggregate
MATCH (o:probe_order) RETURN o.o_state AS s, count(*) AS n ORDER BY o.o_amount
MATCH (o:probe_order) RETURN o.o_state AS s, count(*) AS n ORDER BY nope
MATCH (o:probe_order) RETURN sum(1) AS n
MATCH (o:probe_order) RETURN collect(o.o_state) AS n
MATCH (o:probe_order) RETURN max(o.o_amount, o.o_state) AS n

# reject-expr
MATCH (o:probe_order) RETURN *
MATCH (o:probe_order) RETURN o
MATCH (o:probe_order) RETURN lower(o.o_state)
MATCH (o:probe_order) RETURN o.o_amount + 1
MATCH (o:probe_order) RETURN (o.o_key)
MATCH (o:probe_order) RETURN o.o_state[0]
MATCH (o:probe_order) RETURN o.a.b
MATCH (o:probe_order) RETURN 1
MATCH (o:probe_order) RETURN CASE o.o_state WHEN 'a' THEN 1 ELSE 2 END
MATCH (o:probe_order) RETURN [x IN [1, 2] | x]
MATCH (o:probe_order) WHERE any(x IN [1] WHERE x = 1) RETURN o.o_key
MATCH (o:probe_order) RETURN [(o)-[:probe_direct]->(i:probe_item) | i.i_key]

# predicates
MATCH (o:probe_order) WHERE o.o_key = 10774 OR o.o_key = 10963 RETURN o.o_key AS k
MATCH (o:probe_order) WHERE NOT o.o_state = 'refunding' RETURN o.o_key AS k LIMIT 2
MATCH (o:probe_order) WHERE NOT NOT o.o_state = 'refunding' RETURN o.o_key AS k LIMIT 2
MATCH (o:probe_order) WHERE o.o_key IN [10774, 10963] RETURN o.o_key AS k
MATCH (o:probe_order) WHERE NOT o.o_key IN [10774] RETURN o.o_key AS k LIMIT 2
MATCH (o:probe_order) WHERE o.o_key IN [] RETURN o.o_key AS k
MATCH (o:probe_order) WHERE o.o_state IS NULL RETURN o.o_key AS k LIMIT 2
MATCH (o:probe_order) WHERE o.o_state IS NOT NULL RETURN o.o_key AS k LIMIT 2
MATCH (o:probe_order) WHERE (o.o_key = 10774 OR o.o_key = 10963) AND o.o_amount > 0 RETURN o.o_key AS k
MATCH (o:probe_order) WHERE NOT (o.o_key = 10774 OR o.o_key = 10963) RETURN o.o_key AS k LIMIT 2
MATCH (o:probe_order) WHERE o.o_state = $state RETURN o.o_key AS k LIMIT 2
MATCH (o:probe_order) WHERE o.o_key IN [$first, 10963] RETURN o.o_key AS k

# reject-where
MATCH (o:probe_order) WHERE o.o_key = 1 XOR o.o_key = 2 RETURN o.o_key
MATCH (o:probe_order) WHERE o.o_key IN o.o_chan RETURN o.o_key
MATCH (o:probe_order) WHERE o.o_key IN [o.o_chan] RETURN o.o_key
MATCH (o:probe_order) WHERE o.o_key IN [1, null] RETURN o.o_key
MATCH (o:probe_order) WHERE 1 IS NULL RETURN o.o_key
MATCH (o:probe_order) WHERE o.o_amount > -$floor RETURN o.o_key
MATCH (o:probe_order) WHERE o.o_state = $0 RETURN o.o_key
MATCH (o:probe_order) WHERE o.o_state STARTS WITH 'a' RETURN o.o_key
MATCH (o:probe_order) WHERE o.o_state CONTAINS 'a' RETURN o.o_key
MATCH (o:probe_order) WHERE o.o_key < o.o_chan < o.o_amount RETURN o.o_key
MATCH (o:probe_order) WHERE o.o_key RETURN o.o_key
MATCH (o:probe_order) WHERE o.o_key = [1] RETURN o.o_key
MATCH (o:probe_order) WHERE o.o_key = null RETURN o.o_key
MATCH (o:probe_order) WHERE (o)-[:probe_direct]->(:probe_item) RETURN o.o_key
MATCH (o:probe_order) WHERE EXISTS { (o)-[:probe_direct]->(:probe_item) } RETURN o.o_key
MATCH (o:probe_order) WHERE (o.o_key = 1) RETURN o.o_key
MATCH (o:probe_order) WHERE o.o_key = o.o_chan RETURN o.o_key

# reject-paging
MATCH (o:probe_order) RETURN o.o_key LIMIT 1 + 1
MATCH (o:probe_order) RETURN o.o_key LIMIT 'ten'
MATCH (o:probe_order) RETURN o.o_key SKIP -1
MATCH (o:probe_order) RETURN o.o_key LIMIT 20000
MATCH (o:probe_order) RETURN DISTINCT o.o_state AS s ORDER BY o.o_amount
MATCH (o:probe_order) RETURN o.o_key LIMIT true
MATCH (o:probe_order) RETURN o.o_key LIMIT false

# reject-literal
MATCH (o:probe_order) WHERE o.o_state = '\uFFFFFFFF' RETURN o.o_key
MATCH (o:probe_order) WHERE o.o_state = '\uD800' RETURN o.o_key

# reject-model
MATCH (o:no_such_label) RETURN o.x
MATCH (o:probe_order) RETURN o.no_such_property
MATCH (o:probe_order) RETURN x.o_key
MATCH (o:probe_order)-[:probe_direct]->(o:probe_item) RETURN o.o_key
MATCH (o:probe_order) RETURN o.o_key AS a, o.o_state AS a
MATCH (o:probe_order)-[:probe_direct]->(i:probe_item) RETURN o.o_key
MATCH (c:probe_channel)-[:probe_fcj]->(o:probe_order) RETURN c.c_key
MATCH (i:probe_item)-[:no_such_relation]->(o:probe_order) RETURN i.i_key
MATCH (o:probe_order) RETURN o.`订单状态`
MATCH (o:probe_order) RETURN o.order_status
MATCH (o:probe_order RETURN o.o_key
MATCH (o:ALL) RETURN o.o_key
