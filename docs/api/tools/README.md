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

## 只读保证

- 只发 `GET`。
- 带 `x-http-method-override: GET` 语义的只读 `POST` 需显式 `--include-query-post`。
- 任何情况下都不发 `PUT` / `DELETE`，也不发不带 override 的 `POST`。

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

`--json-out` 产出机器可读版本，便于 CI 断言和跨版本对比。

## 已知局限

- **只覆盖只读接口**。写接口的响应结构不在覆盖范围内。
- **只验证观测到的字段**。空列表、空数组的元素结构无法观测，报告里标为不可观测而非缺陷。
- **依赖环境有数据**。环境里没有的资源（如没有构建任务）对应的详情接口会因缺路径参数而跳过，
  报告末尾按原因列出。
- **内部面与外部面有差异**。`auth-resources`、`connector-types`、bkn `/resources` 只有外部面，
  用 `--face in` 会 404，这几个必须带 token 跑。
- **嵌套深度上限 4 层**，递归 schema（如 `condition.sub_conditions`）到环即停。

## 定时任务

`.github/workflows/api-contract-diff.yml`：手动触发 + 每周一定时。

公共 runner 访问不到内网环境，需要二选一：

- 配置 self-hosted runner，设仓库变量 `CONTRACT_RUNNER` 指向它；
- 或配置 `secrets.CONTRACT_SSH_KEY` 走跳板。

另需 `secrets.CONTRACT_ACCOUNT_ID`（内部面）或 `secrets.CONTRACT_TOKEN`（外部面）。
两者都没配时 job 会**显式跳过并告警**，不会给出「通过」的假象。
