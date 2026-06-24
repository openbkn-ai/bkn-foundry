# 前端改造说明：小模型可配置化

本次后端改造把"小模型(embedding/rerank)"的选择做成 **系统默认 + 任务/对象级可选** 双层，
系统默认改为 **运行时接口可配**（不再读 configmap）。本文列出前端需要对接的 **新增/变更接口与字段**。

涉及四块，前端工作量从大到小：**模型工厂默认配置 UI** > **BKN 建知识网络选模型** > **检索重排选模型** > **Skill 索引(无需改)**。

> 通用：所有"选模型"的下拉数据来自模型工厂的小模型列表，详见 §1.3。

---

## 1. 模型工厂：系统默认小模型（mf-model-manager）

新增"设为默认/查看默认"能力。embedding 与 reranker **各有一个**系统默认（按 `model_type` 区分）。

### 1.1 设置系统默认小模型（管理员）
- `POST /api/.../mf-model-manager/v1/small-model/set-default`
- Body：`{ "model_id": "<小模型id>" }`
- 行为：把该模型设为它所属 `model_type`(embedding 或 reranker) 的系统默认；**同类型互斥**（自动取消旧默认）。
- 鉴权：需要对该模型的 `modify` 权限（管理员操作）；无权限返回 403。
- 失败：model_id 不存在 → 400。
- 注意：默认变更后各后端服务有 **最长 ~60s 缓存延迟** 才完全生效（无需重启服务/改配置）。

### 1.2 查询系统默认小模型
- `GET /api/.../mf-model-manager/v1/small-model/get_default?model_type=embedding`（或 `reranker`）
- 返回：默认模型对象 `{ model_id, model_name, model_type, embedding_dim, ... , default:true }`；
  **未配置默认时返回空对象 `{}`**（前端据此显示"未设置默认"）。

### 1.3 小模型列表 / 详情（已存在，新增 `default` 字段）
- `GET /api/.../mf-model-manager/v1/small-model/list?model_type=embedding&page=1&size=20`
- `GET /small-model/get?model_id=` 、`GET /small-model/get_by_name?model_name=`
- **变更**：`list` 的每项、以及 `get`/`get_by_name` 的返回，新增布尔字段 **`default`**（是否为该类型系统默认）。
- 前端用途：模型选择下拉（model picker）的数据源；列表里标注哪个是"默认"。

> 建议前端在"模型工厂-小模型"页加：每个 embedding/reranker 模型一个"设为默认"操作 + 默认标记。

---

## 2. BKN：建知识网络时可选 embedding 模型

`CreateKN`（创建知识网络）请求体 **新增可选字段**：

- 入参字段：`embedding_model`（字符串，**模型名或 model_id 均可**；留空 = 用系统默认）
- 行为：后端建 KN 时锁定该模型并持久化到 KN；之后该 KN 的概念向量化与语义检索**全程用这个锁定模型**，前端在查询/检索时**无需也不应再传模型**。
- **校验（重要，前端需提示）**：
  - 模型必须是 embedding 类型，否则 400；
  - 维度必须 **等于全局概念库维度**（BKN 所有知识网络共用一个全局概念库，维度统一）。选了不同维度的模型会 **400 拒绝**，错误详情形如
    `embedding model dimension(X) must equal global concept dataset dimension(Y)`。
  - 因此前端的可选项**应只列出与全局维度一致的 embedding 模型**（可先 `get_default?model_type=embedding` 拿到全局默认的 `embedding_dim`，再用 `list` 过滤同维度模型），或在报错时友好提示。
- KN 详情/列表返回 **新增字段**：`embedding_model_id`、`embedding_dim`（展示该 KN 锁定的模型）。

> 注意：KN 的 embedding 模型 **建时锁定、之后不可改**（更新 KN 不会改这两字段）。要换模型需新建知识网络。

---

## 3. Context-loader：语义检索可按请求选 rerank 模型

`POST /api/.../agent-retrieval/.../kn/semantic-search`（`SemanticSearchRequest`）**新增两个可选字段**：

- `rerank_llm_model`（字符串）：仅当 `rerank_action=llm` 时生效，覆盖 LLM 重排模型；留空 = 用后端默认。
- `rerank_vector_model`（字符串）：仅当 `rerank_action=vector` 时生效，覆盖向量重排(reranker)模型；留空 = 用后端默认 `reranker`。
- 二者均可选(`omitempty`)，不传则行为与改造前完全一致（无回归）。
- `rerank_action` 字段不变（`default`/`llm`/`vector`）。

> 仅当产品要让用户在检索时切换重排模型才需要用到；否则前端可不传。

---

## 4. Skill 索引：无需前端改动

Skill 索引是系统级基础设施，其 embedding **直接采用系统默认模型**（见 §1，由模型工厂"设为默认"控制）。
- 建 Skill 索引任务的接口 **不新增模型字段**，前端不变。
- 改了系统默认 embedding 模型后，**新建的** Skill 索引才用新默认；已建索引仍用建时模型（如需切换需重建索引）。

---

## 字段速查

| 接口 | 新增/变更 | 字段 |
|---|---|---|
| `POST /small-model/set-default` | 新增 | body `model_id` |
| `GET /small-model/get_default` | 新增 | query `model_type`；返回模型或 `{}` |
| `GET /small-model/list` `get` `get_by_name` | 变更 | 返回新增 `default`(bool) |
| BKN `CreateKN` | 新增入参 | `embedding_model`(名或id，可空) |
| BKN KN 详情/列表 | 变更 | 返回新增 `embedding_model_id`、`embedding_dim` |
| Context-loader `kn/semantic-search` | 新增入参 | `rerank_llm_model`、`rerank_vector_model`(可空) |
| Skill 索引构建 | 无 | — |
