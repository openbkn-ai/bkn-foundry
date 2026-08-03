# API 契约巡检工具

`api_contract_diff.py` —— 拿运行中的服务真实返回，逐字段比对文档声明的 200 响应 schema。

## 解决什么问题

`make api-docs-lint` 只能证明文档**自洽**（YAML 合法、`$ref` 可解析）。它证明不了文档
和实现**一致**。以下几类问题只有真打接口才会暴露：

- 类型写错：文档声明 `creator: string`，实际返回 `{id, type, name}` 对象
- 字段拼错：文档写 `updator`，实际返回 `updater`
- 结构写错：文档声明对象，实际返回数组
- 实际多返回、文档未声明的字段：调用方不知道有这些数据可用

静态读代码也有盲区——handler 返回结构体、跨层组装、`omitempty` 生效与否，都要跑起来才看得到。

## 用法

```bash
# 走开发 VM 的内部面（免 token）
make api-contract-diff

# 或直接调用
python3 docs/api/tools/api_contract_diff.py \
  --spec-dir docs/api --face in \
  --exec-mode kubectl --ssh parallels@10.211.55.4 \
  --account-id <admin-uuid> \
  --out report.md --json-out report.json
```

覆盖 Makefile 变量可切环境：

```bash
make api-contract-diff CONTRACT_SSH=root@14.103.77.23 CONTRACT_ACCOUNT_ID=<uuid>
```

拿到 token 后可切外部面（文档描述的就是外部面，覆盖更完整）：

```bash
python3 docs/api/tools/api_contract_diff.py --spec-dir docs/api \
  --face ex --token "$(openbkn auth token)" \
  --exec-mode kubectl --ssh parallels@10.211.55.4 --out report.md
```

退出码：`0` 无缺口，`1` 有缺口，`2` 执行错误。

远端执行带超时：ssh 用 `BatchMode` + `ConnectTimeout=10` 快速失败，外层按请求数
设上界（每请求 20s + 60s 建连余量）。ssh 或目标 pod 无响应时按执行错误退出，不会
无限期挂住。

## 只读保证

- 只发 `GET`。
- 带 `x-http-method-override: GET` 语义的只读 `POST` 需显式 `--include-query-post`。
- 文档中用 `x-contract-probe.readonly: true` 显式标注的只读 `POST` 需显式 `--include-probe-post`。
- 标了 `x-contract-probe` 的 `GET` 无条件走批次化路径（`GET` 本来就允许发，标注只用于排批次与取参）。
- 任何情况下都不发 `PUT` / `DELETE`，也不发未被上面两种方式显式标注为只读的 `POST`。

## 探测「查询即 POST」的服务

有的服务把查询也做成 `POST`，参数在请求体里、没有 override 头——context-loader 的
对外端点全是这样。这类接口靠前两条一个都覆盖不到，需要在 OpenAPI 的操作上写一段
`x-contract-probe`，声明「这个 POST 是只读的，可以这样调」：

```yaml
  /kn/list_knowledge_networks:
    post:
      operationId: listKnowledgeNetworks
      x-contract-probe:
        readonly: true                    # 显式承诺无副作用；不写就永远不会被请求
        order: 1                          # 批次；低的先跑，同批并发
        body: {limit: 3}                  # 请求体模板
        provides: {cl_kn_id: entries[0].id}   # 从响应里取值，供后续批次引用
  /kn/get_kn_detail:
    post:
      x-contract-probe:
        readonly: true
        order: 2
        body: {kn_id: '{cl_kn_id}'}       # 引用上一批产出的值
        provides: {cl_ot_id: object_types[0].id}
```

| 字段 | 说明 |
|---|---|
| `readonly` | 必须显式为 `true`。**这是唯一的开关**——工具不猜哪个 POST 安全，只认这行声明。`GET` 上写它不改变「能不能发」（本来就能），只是把该端点纳入批次化路径 |
| `order` | 执行批次，默认 1。低批先跑完并抽出 `provides`，高批才开始 |
| `body` / `query` | 请求体 / 额外 query 的模板。值里可写 `{name}` 引用已发现的参数 |
| `provides` | `{参数名: 取值路径}`，路径形如 `entries[0].id`。取不到就不产出，依赖它的后续接口会被标为「缺少探测参数」而不是发一个必然失败的请求 |

写标注时的三条纪律：

1. **有副作用的端点不要写这段**（`execute_action` 之类）。没有标注 = 永不请求。
2. **参数名建议加模块前缀**（如 `cl_kn_id`），避免和 `DISCOVERY` 表里自动发现的
   通用参数名（`kn_id` / `ot_id` / `id`）串用。
3. **标注与端点同源**，改接口时在同一个文件里改，评审时一起看。
4. **`GET` 只在需要分批时才标**。普通只读 `GET` 默认已在探测范围内，加标注没有
   收益；只有当它要 `provides` 产出 id 给后续端点、或需要固定 query 参数才有必要
   ——因为普通 GET 路径是一次性并发，拿不到同批其他请求的产出。

`fill_path` 在发现不到路径参数时，会退回该参数自身声明的 `example` / `default` /
`enum`（如 `template_type` 的枚举只有 `python`），这类取值不依赖环境数据，不必因为
「列表里没有」整条跳过。**代价是 example 里若写的是虚构 id，请求会照发**，多半拿回
一个空列表——这种情况归到报告的「响应样本为空」一节，不会算成「已验证」。

必填 `query` 参数两条路径共用一套规则（`with_required_query`）：probe 里显式写了的
以 probe 为准，没写的按 schema 造一个合规取值，造不出来就整条跳过。

## 工作方式

1. 解析 `docs/api/**.yaml`，展开 `$ref`（含跨文件）/ `oneOf` / `anyOf` / `allOf`，
   把 200 响应 schema 摊平成「字段路径 → (类型, 是否必填)」。
2. 调用列表接口**自动发现路径参数**（`kn_id`、`ot_id`、`log_id` …），再填进详情接口。
   发现值带作用域，避免 `id` 这种通用参数名在 catalogs / resources / build-tasks
   之间串用。
3. 真实发请求，把响应 JSON 摊平成同构的字段路径集合。
4. 比对，按严重度输出。

## 报告怎么读

每个接口一节，条目按严重度排列：

| 条目 | 含义 | 处理 |
|---|---|---|
| **类型不符** | 文档声明的类型与实际返回不同 | 必修。按文档写的客户端会解析失败 |
| **文档标为必填但实际未返回** | `required` 链上的字段实际没有 | 查是文档写错还是实现漏返回 |
| **实际返回但文档未声明** | 接口返回了文档没写的字段 | 补文档；调用方否则不知道有这些数据 |
| **可选字段本次未观测到** | 非必填字段这次没返回 | **不一定是缺陷**，可能是 `omitempty` 或该视图不返回 |

另有一节「响应样本为空（0 缺口 ≠ 已验证）」：列表返回空数组时，数组元素的字段无从
观测，既不算缺口也不算验过。**统计「完成比对 N 条、缺口 0」时要把这几条减掉**——
补上环境数据重跑才有结论。

`--json-out` 产出机器可读版本，便于 CI 断言和跨版本对比。

## 已知局限

- **只覆盖只读接口**。写接口的响应结构不在覆盖范围内。
- **只验证观测到的字段**。空列表、空数组的元素结构无法观测，报告里标为不可观测而非缺陷。
- **依赖环境有数据**。环境里没有的资源（如没有构建任务）对应的详情接口会因缺路径参数而跳过，
  报告末尾按原因列出。
- **内部面与外部面有差异**。`auth-resources`、`connector-types`、bkn `/resources` 只有外部面，
  用 `--face in` 会 404，这几个必须带 token 跑。
- **依赖真实数据才能探测的接口会被跳过**。探测参数取不到（如该知识网络没有行动类，
  就拿不到 `at_id`）时，报告会以「缺少探测参数 xxx」列出，不算缺口也不算已验证。
- **嵌套深度上限 7 层**（脚本 `MAX_DEPTH`），递归 schema（如 `condition.sub_conditions`）
  到引用环即停。

## 为什么没有定时任务

本工具必须连真实环境，而公共 runner 访问不到内网。要挂 CI 需要先具备 self-hosted
runner 或跳板机，再把环境地址与凭据配成仓库 secrets。这套前提目前不具备，因此暂以
手动执行为主，仓库里也没有对应的 workflow。
