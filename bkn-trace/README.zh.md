# BKN Trace

[English](README.md)

[![许可证](https://img.shields.io/badge/license-OpenBKN-blue.svg)](../LICENSE-OPENBKN.txt)

BKN Trace 是 OpenBKN 的社区版 Trace Core。它记录受管 Agent、SDK 与 MCP 调用的一手执行
事实，并提供受保护的技术 Trace 分析 API 以及与平台日志的精确关联能力。

## 记录的事实

Operation 的一次 attempt 是权威执行单元。已完成或失败的 attempt 可以保留 Conversation、
Interaction、Operation 与 Request 标识，执行时间和状态，调用身份、协议和工具信息，以及调用
边界实际收到的输入、输出或可诊断错误。记录 Trace 不得改变业务工具原本的返回结果。

BKN Trace 只记录一手事实，不从最终回答反推执行信息。未记录的信息必须明确为“未记录”，不能由
读取端或界面猜测补全。

## 公开社区能力

- 接收并持久化受管调用事实及关联 Evidence 事件。
- 提供受访问控制保护的 Trace、Conversation、Interaction、Operation、Evidence 和技术日志读取
  API。
- 返回技术执行详情：记录的输入/输出或错误、Trace 与 Span 关系，以及用于日志下钻的精确关联
  标识。
- 在返回 Trace 或 Evidence 数据前执行 Access Profile 和记录范围校验。
- 为 OpenBKN 客户端保留稳定的社区 Trace 事实模型与公开 API 合同。

## 架构

```text
受管 MCP / SDK 调用
        |
        v
Trace 生产者采集一手执行事实
        |
        v
BKN Trace Core
  - 生命周期与 Operation 调用事实
  - Evidence 与技术关联
  - 访问控制读取 API
        |
        +--> 技术 Trace 分析
        +--> 精确日志下钻
```

Core 是技术事实服务：不从最终回答推导业务语义，也不建立第二套权威 Trace 存储。

## 组件

| 路径 | 职责 |
| --- | --- |
| `agent-observability/` | Go Trace Core 服务、OpenAPI 接口、存储适配器和部署 Chart。 |
| `otelcol-contribute-chart/` | 用于 OTLP 采集及 OpenSearch 导出的 OpenTelemetry Collector Contrib Chart。 |
| `scripts/` | 可重复执行的合同与部署安全检查。 |

本地开发和服务配置请见
[agent-observability/README.md](agent-observability/README.md)；Collector 的部署与验证请见
[otelcol-contribute-chart/README.md](otelcol-contribute-chart/README.md)。

## Trace 根工具筛选

在身份分页支持的自有、应用及管理员精确权限范围内，Trace 列表的 `tool`
匹配根工具，而非任意子调用。根工具取具有有效 Trace/request 标识的授权回执中，
按持久化签发时间、回执 ID 排序后的第一个非空工具名；移除两端 Unicode 空白后
精确匹配，区分大小写，不归一化名称内部文本。

有无工具筛选均使用同一根元数据，即使最早回执位于查询时间窗之外或未进入近期
有界投影。时间窗仍决定候选 Trace，不重新定义根。读取仅获取选中身份的元数据，
迁移037注册非唯一回执查询索引，不改写历史事实。仅 Tool 及 Service+Tool 的身份
分页继续使用这一快速路径；下述新增扫描场景不替换它。

037虽仅增加索引，但迁移账本会拒绝清单落后于数据库的旧镜像。执行后需使用保留
相同037清单的兼容镜像回滚，或另行评审数据库回滚方案；不能直接重启原037之前
的镜像。大生产表的建索引耗时、锁影响和写入成本需单独做上线评审。

## Trace 状态、错误及共享业务网络筛选

内置 Core 投影提供可选扫描能力，覆盖 Trace 列表的 `status`、`error_keyword`
及它们与 `service`／`tool` 的组合，也覆盖不带其他不支持内容条件的共享业务网络
列表。旧投影适配器保持兼容行为。扫描在单次请求内共用一个回执索引 PIT，每个
搜索页读取256份文档，每批处理64个 Trace 身份。先完整读取 Interaction 并核对
权限，再决定是否开放同 Interaction 的兄弟 Trace；根工具来自完整授权生命周期，
状态来自请求时间窗内的事件。只要位于查询窗口中，最新20条之外的失败回执也不会
使整条 Trace 被错误地当成 completed。

`error_keyword` 仅按原有忽略大小写的子串语义匹配最终展示的错误摘要。普通错误
保留前512个 Unicode 码点；包装执行 JSON 中的 stderr 保留末尾512个码点，截断时
显示省略号。被裁掉的文字、后续失败以及无关 input／output 都不能命中。窄 SQL
先限定已授权的 Trace／conversation／Interaction 元组，再读取首个失败的错误
载荷及首个服务模块；外部 owner 的同 Trace ID 碰撞不能影响错误摘要或根服务。
该扫描能力不增加迁移，也不建立新的物化读取模型。普通列表快速路径和详情读取
保持原实现。

资源上限为65,536个不同候选 Trace ID、一次展开／批次10,000条回执、单个回执 PIT
响应32 MiB，以及30秒查询期限。深页最多保留10,000行，包含判断下一游标所需的
额外一行。确定性的数量、大小或深页限制返回 HTTP 422 `SUMMARY_QUERY_BUDGET_EXCEEDED`；
查询期限耗尽返回 HTTP 503 `SUMMARY_QUERY_TIMEOUT`，不会把不完整
总数伪装成成功。请缩小时间范围或调整筛选条件减少扫描；使用返回的 cursor 代替
深页码可避免保留行数上限，但 cursor 仍需扫描精确总数，不能绕过其他资源限制。

对非全局权限调用者，新增扫描路径显示 `span_count_status=unavailable`。既有 span
统计端口没有授权范围参数，不能暴露其他 owner 同 Trace ID 碰撞产生的计数；具有
全局权限的管理员仍可获取该统计。既有普通列表／详情的统计路径本轮未改动。
`agent_or_app`、通用 `keyword`、`knowledge_network`、记录完整性、证据完整性及
排除 Agent 等不支持的组合仍走兼容路径，可能返回 partial。显式 Trace／conversation／
Interaction 标识查询也不属于本扫描器的列表契约。

扫描路径的 Tool 匹配始终使用完整授权回执 PIT 中的根，包括归档清除 SQL 回执之后。
缺少必要的 PIT 根或授权范围仍拒绝查询。成功扫描后如 PIT 清理失败，保留完整结果、
记录运行告警，并由一分钟 keep-alive 到期清理；扫描自身的错误不会被忽略。

PIT 只固定单次请求内的回执搜索，不覆盖 MariaDB 或 artifact，也不跨分页请求；
不同页各自创建 PIT，不能声称整个读取具备跨存储或跨请求的事务快照。启用
OpenSearch Security 时，需要对回执索引／别名授予
`indices:data/read/point_in_time/create`、`indices:data/read/point_in_time/delete`
及 `indices:data/read/search`。生产读取不需要列出全部 PIT 的权限；隔离验收仅使用
`indices:data/read/point_in_time/readall` 读取清单，确认自己创建的 PIT 已清理。
参见 [OpenSearch 2.19 PIT 权限说明](https://docs.opensearch.org/2.19/search-plugins/searching-data/point-in-time/#security-model)。

## 日志自动分批归档

具有归档权限的用户只需一次调用 `POST /api/observability/v1/log-archive-jobs`，
后端即按请求开始时固定的截止时间，连续处理每批最多10,000条记录。前端保持原有
一次创建请求、结束后刷新任务列表的方式，不增加连续请求循环或新调度系统。

每批独立保存任务、冻结的候选 ID、已校验归档对象及 manifest。执行顺序为：选择
完整批次、写入并校验对象、持久化 manifest 和清理候选、仅删除这些 ID、刷新并确认
这些 ID 全部消失、标记本批完成，再进入下一批。候选总量超过10,000条不再拒绝；
不完整搜索或不可信总数仍返回 `409 archive_incomplete_candidates`。同类创建和
清理重试共用数据库锁，并发操作返回 `409 archive_busy`；任何未完成清理都会阻止
新任务绕过它继续运行。

创建接口返回最后一个批次任务，`candidate_count`、manifest 和下载仅属于该批。
同次运行通过 `batch_progress.run_id` 关联，进度持久化并随任务查询返回：批次号、
选批时合格数量、本批已校验数、本批已确认清理数、运行累计已确认清理数、选批时
本批之外的剩余量和运行完成标记。清理数只累计已确认冻结 ID 全部清空的批次；
部分删除失败时尚未确认的批次不会提前计入。剩余量不是实时全局计数，也不是跨批
不可变快照。每批任务的 range 表示共同的候选时间窗，不代表该对象包含整个时间窗。

校验失败保留本批热数据；清理失败则停止并保留 `cleanup_incomplete`。重试只按
原冻结 ID 清理，不重写已校验对象，成功后沿相同 run ID 和截止时间继续后续批次。
同步请求中断时，已完成批次及待清理状态保留；解决待清理任务后，后续创建请求
可以新 run 处理剩余数据。后批失败不会回滚此前已完成批次。现有最近任务列表限制
保持不变，本轮不增加实时进度条。

迁移038增加可空 `batch_progress` 字段，兼容旧记录；使用新镜像前需应用该迁移。
迁移后账本会拒绝 pre-038 manifest，回滚需保留相同038 manifest 的兼容镜像或
另行评审数据库回滚方案。数据库锁每个活动类型占用一个连接；有限连接池小于3时
直接拒绝，以免耗尽连接；Core 默认连接池为16。

本轮仅限制每批条数，不限制字节数；完整正文及原对象写入器仍可能占用大量内存，
不等于完整流式归档或单条超大记录治理。Trace 归档冻结保持原实现，#1724 的整体
流式/OOM工作仍未完成。普通列表和详情读取路径未改动。

## 大历史归因的 capture 大小

内部历史 capture 编码在原 v1 JSON 能装入预算时，保持原字节及 hash 不变。仅因
base64 膨胀使 v1 超限时，才尝试明确版本 `internal-capture-v2-gzip-bodies`：
外层仍为 JSON，每段非空原始正文为单个 gzip 成员，不把原始 JSON 重新编码成值。
原始字节、身份、已记录缺口以及 nil／空正文均保留。

生产预算仍为64 MiB；编码后的包、metadata加累计解压正文都必须符合调用者预算。
损坏 gzip、多成员或尾随字节、错误 hash、不明确布局及未知版本仍拒绝。
大小拒绝通过 `ErrCapturePackageBudget` 与原记录缺失、损坏内容区分；压缩期间
检查取消。旧 v1 仍可读取；Revision 输入的读写严格保留 v1，不改变其持久化契约。

小 capture 保留原 hash，避免无谓重新生成。读取 v2 需要更新后的 decoder；
早于 v2 的回滚版本不能读取新压缩包。本能力限制包和解压大小，不承诺编码峰值
内存或此前 snapshot 读取的内存上限，也不扩大 EE 最终视图预算、不补造历史证据。
（#2148）

## 验证

在 Foundry 仓库根目录运行 BKN Trace 许可证头检查：

```bash
python3 bkn-trace/agent-observability/scripts/check_license_headers.py
```

在服务目录运行测试：

```bash
GOCACHE=/tmp/openbkn-go-build-cache GOMODCACHE=/tmp/openbkn-go-mod-cache go test ./...
```

## 许可证

BKN Trace 使用 [OpenBKN License](../LICENSE-OPENBKN.txt)。每个由 OpenBKN 编写且可添加注释的
源码与部署文件均带有版权声明及 `SPDX-License-Identifier: LicenseRef-OpenBKN` 标识。
