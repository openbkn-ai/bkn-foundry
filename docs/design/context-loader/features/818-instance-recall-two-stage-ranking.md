# 语义实例召回两级排序：knn/match 拆查询 RRF 融合 + 可选 reranker 精排

- Issue：[#818](https://github.com/openbkn-ai/bkn-foundry/issues/818)
- 分支：`feature/818-instance-recall-two-stage-ranking`
- 模块：context-loader（agent-retrieval）
- 状态：in-review（PR1 已实现，PR2 待开）

## 1. 背景与问题

`search_schema` 的语义实例召回对每个对象类只发**一条 OR 查询**，把 `knn` 与 `match`
子句拼在一起（`semantic_instance_retrieval.go` 的 `buildSemanticSearchConditionStruct`），
拿回的 `_score` 是 OpenSearch 把两路子句分**直接相加**的结果，中途没有任何归一化——
vega 侧只是把 `hit._score` 原样透传（`opensearch_query.go:107`）。

一个混合分带来三个互相叠加的缺陷：

| # | 缺陷 | 代码位置 | 后果 |
|---|---|---|---|
| 1 | knn 分（0~1）与 BM25 分（无上界，实测 1~30）相加 | `buildSemanticSearchConditionStruct` | BM25 恒定主导，向量通道对排序几乎无贡献 |
| 2 | 绝对阈值打在未标定分数上 | `filterLowRelevanceNodes`（`MinDirectRelevance=0.3`） | BM25 行全部放过，纯向量行卡在阈值附近随机掉 |
| 3 | 跨对象类直接比较 BM25 | `semanticInstanceRetrieval` 的 `maxScore * GlobalFinalScoreRatio` | 不同索引 idf / 文档长度不同，BM25 跨索引不可比；一个类的高分能抹掉其他类的全部实例 |

缺陷 1 最重的一层不在排序而在**召回**：OR 结果按 `_score` 截到
`InitialCandidateCount=50`，BM25 命中行会把向量命中行挤出候选集，
后续无论怎么排都救不回来。

### 1.1 为什么不能就地归一化

想在现有单查询上归一化，前提是知道混合分里向量贡献了多少——拿不到。
OpenSearch 的 named queries（`_name` + `matched_queries`）只回答"哪个子句命中了"，
**不给子句分**；要取子句分只能走 `explain`，开销远大于多发一次查询。
所以"不拆查询也能调和两路分"这条路不存在，方案必须从拆查询开始。

## 2. 方案总览

```text
每个对象类:
  knn 查询   → InitialCandidateCount 条 (响应序即 rank_knn)
  match 查询 → InitialCandidateCount 条 (响应序即 rank_match)
        ↓
  【第一级】RRF 融合：按实例唯一标识去重合并，score = Σ 1/(k + rank_i)
        ↓
  跨对象类池化 → 全局有序候选 → 截 RerankTopN
        ↓
  【第二级·可选，默认关】reranker(cross-encoder) 重排 → 取 top-K
```

两级的分工是**不同的洞、不同的堵法**：第一级保证"该来的都来了"（两路各自保底名额，
量纲无关），第二级决定"谁真的在前面"（语义精度）。缺一不可——
只留 reranker 则 O(N) 次前向无法当召回，且候选集已被 BM25 污染；
只留 RRF 则判不出「张三的欠款记录」与「张三的还款单」的差别。

## 3. 第一级：拆查询 + RRF

### 3.1 拆法

`retrieveInstancesForObjectType` 由发一条 OR 改为发两条，可并发：

- **knn 通道**：只含 `KnOperationTypeKnn` 子条件（沿用 `MaxKnnSubConditionsPerType` 预算与
  `limit_key=k`）。对象类无向量字段时该通道整体跳过。
- **match 通道**：只含 `KnOperationTypeMatch` 子条件。

两条都保持 `OR` 外壳（单子条件的 OR 合法），**不动 ontology-query 契约**。
`Limit` 各自取 `InitialCandidateCount`，即候选池上限从 50 变成最多 100，
这是"两路都要有话语权"的直接代价，可接受。

顺带收一个爆炸半径：`condition_operations` 是建网时客户端声明、原样落库的不可信数据
（见 `ontology_query.go` 的 `classifyQueryError` 注释），标了 `knn` 的字段未必真有向量映射，
下游会回 400 `left field is not a vector field`。现在这个 400 会打掉整条 OR，
该对象类**一条实例都召不回**；拆开后 knn 通道的 400 只损失向量路，match 路照常返回。

### 3.2 融合

```text
score(d) = Σ_channels 1/(k + rank_c(d)) × (k+1) / 通道数
```

`k = 60`（`RRFK` 可配），`rank_c` 取该通道响应中的下标位置（首行记为 rank 1）。

选 RRF 不选"归一化 + 加权和"的理由：min-max 在候选少、分数集中时噪声大
（top1 恒为 1.0，第二名可能实际只差千分之一），且 knn/match 权重要按 KN 手调；
RRF 只有一个常数 `k`，跨 KN 不用调。

后面两个因子把裸 RRF 分归一到 `[0,1]`（各路都排第一即 1.0），各解一个问题：

- **除以通道数**——消除跨对象类偏置。没有向量字段的对象类只发一路，不除的话它们的
  融合分系统性只有双路对象类的一半，全局比例过滤会据此误杀。同一对象类内部，
  两路都命中的实例仍高于只命中一路的，那是真信号，保留。
- **乘 (k+1)**——把量纲拉回 0~1，与 3.3 的本地兜底打分同量级。两条路径的结果会汇进
  同一个池子做全局过滤；裸 RRF 分在 0.0x 量级，只要有一个对象类回落到本地打分
  （分档到 0.85），阈值就会把全部 RRF 结果抹掉。

### 3.3 名次的前提与兜底

RRF 吃的是名次，而名次来自"响应按相关性降序"这个前提。两种情况前提不成立：

- **回落源库直查**（vega 表源，非索引路径）：响应无 `_score`，顺序是库返回的自然序，
  名次无意义。此时**跳过 RRF**，沿用现有 `fallbackNodeScore` 本地打分——那套 0/0.3/0.5/0.85
  的分档本来就是为这条路设计的，`MinDirectRelevance` 也只在这条路上有意义。
- **只有一个通道有结果**：退化成该通道的原序，RRF 不改变单通道内的相对次序（单调），
  行为等价于现状但少了另一路的干扰。

判定依据：该通道返回的行是否带 `_score`。同一对象类两路一致（要么都走索引、要么都回落）。

### 3.4 去重键

合并前需要稳定的实例标识。`convertToKnSearchNode` 已提取 `unique_identities`
（`map[string]any`）。去重键 = 对该 map 的键排序后按 `k=v` 拼接（值走 `fmt.Sprint`），
再前缀对象类 ID。`unique_identities` 缺失时退回 `instance_name`；两者皆空的行不参与去重，
按独立候选处理（宁可重复，不可误合并两个不同实例）。

### 3.5 阈值语义修正

| 配置 | 现状 | 改后 |
|---|---|---|
| `MinDirectRelevance` | 打在混合 `_score` 上（对索引行是噪声） | **仅**作用于无 `_score` 的本地兜底打分路径 |
| `PerTypeInstanceLimit` | 按混合分截断 | 按 RRF 分截断（语义不变，尺子换了） |
| `GlobalFinalScoreRatio` | 池化后打在跨索引不可比的 BM25 上 | **移到通道内、融合之前**打在该通道自己的 `_score` 上；池化后的全局过滤保持原位但已基本无害 |

不新增阈值，只改"打在谁身上"。老配置字段名与默认值全部保留。

比例过滤下移到通道内是实现期定下来的：**原始分只在通道内可比**——同一对象类、
同一索引、同一查询、同一种算子，此时"与最高分差 4 倍"确实说明相关性差距。
融合之后就做不了了：RRF 只表达名次，第一名恒为 1.0，哪怕它其实毫不相关，
"整体都不相关"这个信息在名次里根本没有编码。这也意味着**绝对相关性判断在 PR1
是缺失的**，得靠 PR2 的 cross-encoder 补——那是 rerank 真正不可替代的地方。

## 4. 第二级：reranker 精排（默认关）

### 4.1 触发与开关

`InstanceRerankMode`：`off`（默认）/ `shadow` / `on`。

- `off`：不调用，返回 RRF 序。
- `shadow`：照常返回 RRF 序，但**额外**调一次 reranker，记录两个序列的 order-delta
  （Spearman 相关系数 + top-5 变动数）到日志，用于 A/B 取证。
- `on`：用 rerank 分覆盖排序。

默认 `off` 的理由：多一次模型调用、延迟涨 100~400ms，且 reranker 未注册在客户环境是常态
（见 #114、#788）。翻默认前必须有测试服真实 KN 的 A/B 数据。

### 4.2 文档文本与长度预算

送 rerank 的文本 = `instance_name` + 各 `searchableField` 的值拼接。两条硬约束：

1. **必须在属性过滤之前构造**。`filterNodeProperties` 会砍属性数（`MaxPropertiesPerInstance=20`）
   并把值截到 `MaxPropertyValueLength=500`；截完再送 rerank 就是拿残文本判相关性。
2. **自己控长度**。mf-model-api 的适配层会把 query 截到 1590 字符、单文档截到 4000 字符
   且**不报错**（`external_small_model_utils.py:187-193`）。单实例塞 20 个属性 × 500 字符即超限，
   尾部字段等于没参与打分。故按字段逐个截断（默认单字段 200 字符）并限制单文档总长。

### 4.3 调用与回填

- 端点 `POST /v1/small-model/reranker`，客户端复用 `mfModelAPIClient.Rerank`。
- **按 `index` 回填**，不能按返回顺序：厂商行为不一致，有的按分数降序返、有的按原序返，
  mf-model-api 内部那句 `sorted(result["results"], key=lambda x: x["index"])` 就是在补这个。
  响应的 `document` 字段通常是 `null`，原文必须自己留。
- **不在上层再切批**。下游 client 已按 64 一批、10 线程并发发出（`external_small_model_utils.py:194-203`），
  上层再按 `SemanticFieldRerankBatchSize=128` 切一刀只会两层批大小打架。该字段目前无任何消费点，
  本次**不启用**，是否删除另议。
- 候选量由 `RerankTopN`（默认 50）控制，取 RRF 池的前 N 条。

### 4.4 降级矩阵

| 情况 | 行为 |
|---|---|
| `mode=off` | 返回 RRF 序 |
| reranker 未注册（`NameNotExist`）/ 调用失败 / 超时 | warn 日志 + 返回 RRF 序，**结果非空** |
| rerank 返回条数与请求不符 | 按 `index` 能对上的覆盖，对不上的保留 RRF 名次 |
| 候选为空 | 不发起调用 |

对齐 #788 的要求：降级路径任何时候都不能把结果打空或塞入无关项。

## 5. 配置项

新增（`KnSearchSemanticInstanceRetrievalConfig`，均带默认值，老请求不传即取默认）：

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `enable_rrf_fusion` | bool | `true` | 关掉则回到单条 OR 查询的旧路径 |
| `rrf_k` | int | `60` | RRF 常数 |
| `instance_rerank_mode` | string | `off` | `off` / `shadow` / `on` |
| `instance_rerank_model` | string | `""` | 空则由下游解析部署级默认（`rerank_model` → `"reranker"`） |
| `rerank_top_n` | int | `50` | 进入精排的候选数 |
| `rerank_field_char_limit` | int | `200` | 单字段进 rerank 文本的截断长度 |

## 6. 兼容性

- 走三段式：PR1 的 RRF 默认开但保留 `enable_rrf_fusion=false` 的旧路径逃生门；
  PR2 的 rerank 默认 `off`，经 `shadow` 取证后再议翻默认。
- 请求契约只增可选字段，不改必填、不改响应结构（`KnSearchNode` 增加 `recall_score`
  保留原始 `_score` 用于观测，属新增字段）。
- 新装与升级两条路径都验：升级路径重点验老请求（不带任何新字段）行为可预期。

## 7. 测试

单测（`semantic_instance_retrieval_test.go`，mock `ontologyQuery`）：

1. **向量命中不被挤出**：match 路返回 50 条高 BM25 分、knn 路返回 5 条，断言 5 条向量命中全部进入最终候选。
2. **RRF 名次正确**：构造已知两路名次，断言融合分与排序与手算一致。
3. **knn 通道 400 隔离**：knn 查询返回 400，断言 match 路结果照常返回、不整体失败。
4. **无 `_score` 走兜底**：响应不带 `_score`，断言跳过 RRF、`MinDirectRelevance` 生效。
5. **去重**：同一实例在两路都出现，断言只出现一次且分数为两路之和。
6. **rerank 乱序回填**：mock 返回打乱 `index` 顺序，断言按 `index` 对齐而非按返回顺序。
7. **rerank 降级**：mock 返回 `NameNotExist`，断言结果非空且为 RRF 序。

集成验证：测试服 14.103.77.23 用真实 KN 跑 `search_schema`，对比开关前后的 top-K，
记录延迟差与 order-delta。

## 8. 分期与验收

**PR1 — RRF（本文档第 3 节）**
- 拆双查询 + RRF 融合 + 去重 + 阈值语义修正 + 单测 1~5
- 不依赖任何模型，可独立上线
- 验收：缺陷 1/2/3 各有对应单测；老请求行为不变

**PR2 — reranker 精排（第 4 节）**
- 精排级 + `shadow` 模式 + 降级矩阵 + 单测 6~7
- 默认 `off`；A/B 数据另开跟踪评论
- 验收：模型不可用时结果非空且等同 RRF 序

回滚：PR1 置 `enable_rrf_fusion=false` 回旧路径；PR2 置 `instance_rerank_mode=off` 即停用。

## 9. 已排除的方案

- **分数归一化 + 加权和**：见 3.2，比 RRF 脆且需按 KN 调参。
- **让 ontology-query 回传 per-clause 分数**：见 1.1，OpenSearch 不提供，`explain` 太贵。
- **只加 reranker 不拆查询**：召回阶段已丢的行重排救不回，且把整条链路押在"客户装了 reranker"上。
