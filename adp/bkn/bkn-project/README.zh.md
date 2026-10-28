# BKN Project

中文 | [English](README.md)

`bkn-project` 基于项目构建并发布知识网络。每次发布都作为一个新的知识网络
（`<base_id>_<version>`）导入 bkn-backend，不覆盖线上网络；发布历史以谱系
（lineage）形式保存。参见 issue #2520 和 epic #2519。

## 当前能力范围

- 发布谱系存储（`t_bkn_project_release`）。
- git 谱系：每个谱系一个裸仓库，位于 `/data/git`（Helm chart 中为持久卷）。
  `main` 对应线上网络；各项目在自己的分支上工作，并通过语义三方合并把
  `main` 同步进分支。
- 线上网络与 `main` 之间的漂移检查。
- 把项目分支发布为新网络 `<lineage_id>_v<major>_<minor>_<patch>`，名称为
  `<基础名称> · <版本>`（ID 与名称均截断到 128 个字符），并在 git 中打标签 `v<version>`。
- 项目（`t_bkn_project_project`）：每个项目在所属网络谱系的分支
  `project/<id>` 上修改该网络；同一网络的项目共用一个谱系。能看到该网络的
  调用方才能看到项目。
- 本体草稿（`t_bkn_project_draft`）：按规则根据项目数据目录的表结构起草本体。
  实体表起草为对象类，主键用全表统计核对；没有声明主键的表，取一个全表唯一且无
  空值的标识类字段作为推断主键。代码表起草为枚举；关联表起草为多对多关系；声明的
  外键、以及按对方主键命名的字段（含带角色前缀的，如 parent_lot_id）起草为关系，
  用全表命中率核对。无法计数的推断关系作为"未核对"保留；取值很少的字段（少于
  `draft.minInferredDistinct`，默认 10）推断出的关系只推荐不采用。推荐而未采用的关系在
  `proposed_because` 中说明原因：`not_counted`、`below_suggest_hit_rate`、
  `below_adopt_hit_rate`、`few_values` 或 `end_not_adopted`（一端的对象类还在等人
  决定）。个人信息字段（隐私关键词按词匹配）排除。优先使用数据目录语义
  理解给出的名称和描述。以调用方身份读取 vega-backend，只取回计数和代码表取值。
- 健康检查与就绪检查接口。

## API

前缀：`/api/bkn-project/v1`。除 `/health` 和 `/ready` 外的接口都需要 OAuth
令牌，与 bkn-backend 的外部接口一样通过 Hydra 校验。从 bkn-backend 导出时使用
调用方的令牌，因此 bkn-backend 的权限同样生效。

所有谱系都按 BKN 3.0 保存：导出时向 bkn-backend 请求 `bkn_spec=3.0`，`main`、
项目分支和发布读回的内容都遵循 3.0 规则，草稿写出的值类型只有 3.0 能容纳。
bkn-backend 无法按 3.0 导出的网络以 400 拒绝，`error_details` 指出要修正的内容，
不会建出任何东西：行动参数未通过网络检查时为 `BknProject.Lineage.NetworkNeedsCheck`，
有定义按 3.0 规则读不回时为 `BknProject.Lineage.NetworkNotReadableAsV3`。以 2.x
开始的谱系在提交漂移时转为 3.0，其分支在同步时转为 3.0。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/health` | 服务信息 |
| GET | `/ready` | 数据库可达时返回 200，否则返回 503 |
| GET | `/lineages/{lineage_id}/releases?limit=&cursor=` | 谱系下的发布记录，按时间倒序，游标分页。最新一次发布的 `state` 为 `live`，更早的为 `archived`；切换使用方不改变状态 |
| GET | `/releases/{network_id}` | 产生该网络的发布记录 |
| POST | `/lineages/{lineage_id}/releases` | 请求体 `{"branch","version","project_id","notes"}`。发布该分支：返回 201 `{"release","network_id","tag","commit"}`。409 `BranchBehind`（请先同步分支）、`Drift`（路径见 `error_details`，请提交漂移并同步）、`VersionExists`，或 `CapabilitiesSkipped`（网络已导入但未记录发布；从同一分支头以同一版本重新发布会覆盖该网络） |
| POST | `/lineages` | 请求体 `{"kn_id"}`。导出线上网络并在 `main` 上创建谱系；返回 201 `{"lineage_id","commit"}`，已存在返回 409 |
| POST | `/lineages/{lineage_id}/branches` | 请求体 `{"name","from"}`（`from` 默认 `main`）；返回 201 `{"name","commit"}`，已存在返回 409 |
| PUT | `/lineages/{lineage_id}/branches/{branch}/definitions?message=` | 请求体为 bkn-backend 导出格式的 BKN tar 包（`application/octet-stream`，最大 64 MiB），替换分支上的定义；返回 `{"commit","changed":{"added","changed","removed"}}`。对 `main` 返回 403 |
| POST | `/lineages/{lineage_id}/branches/{branch}/sync` | 把 `main` 同步进分支；返回 `{"result":"up_to_date"\|"fast_forward"\|"merged","commit","summary"}`，有冲突时返回 409，冲突列表在 `error_details` 中 |
| POST | `/lineages/{lineage_id}/drift?commit=` | 请求体 `{"kn_id"}`。比较线上网络与 `main`；返回 `{"lineage_id","kn_id","added","changed","removed","commit"}`。`commit=true` 时把线上定义提交到 `main` |
| GET | `/lineages/{lineage_id}/switch-plan?to=` | 把谱系的使用方切换到网络 `to` 会做什么：返回 `{"lineage_id","from_network_id","to_network_id","schedules"}`，即当前所用网络上正在运行的调度。智能体、MCP、workshop、eval 及 `to` 上的权限由各自负责人切换。400 `InvalidSwitchTarget`（不是该谱系的网络），409 `AlreadyInUse` |
| POST | `/lineages/{lineage_id}/switches` | 请求体 `{"to_network_id"}`。在目标网络上启用每个运行中的调度（以切换人身份执行），在当前网络上停用，然后记录这次切换：返回 201 和切换记录。回退就是切回之前的网络，上次停用的对应调度会重新启用 |
| POST | `/projects` | 请求体 `{"name","kn_id"\|"new_kn":{"name","id"},"catalog_ids"}`，`kn_id`（调用方可见的已有网络）和 `new_kn`（新建网络）必须且只能给一个。传入某次发布产生的网络时，项目并入其原网络的谱系。网络没有谱系时先创建谱系，再创建分支 `project/<id>`；返回 201 和项目。调用方为负责人 |
| GET | `/projects?archived=&name=&limit=&cursor=` | 调用方可见网络下的项目，按时间倒序，游标分页。`archived` 为 `false`（默认）、`true` 或 `all`；`name` 按包含匹配。每条带 `other_projects`：同一网络下其他未归档项目的数量 |
| GET | `/projects/{project_id}` | 项目详情；调用方看不到其网络时同样返回 404 |
| POST | `/projects/{project_id}:archive`、`:unarchive` | 归档（默认列表不显示）或取消归档，只有负责人可以操作（否则 403），不影响已发布版本 |
| PUT | `/projects/{project_id}/catalogs` | 替换项目起草所用的数据目录。请求体 `{"catalog_ids"}`（必填，可为空数组，自动去重）。只有负责人可以操作（否则 403）。草稿保持不变，重新起草时读取新的目录。400 `Project.InvalidRequestBody`、`Project.InvalidCatalogIDs` |
| POST | `/projects/{project_id}/ontology/draft:generate` | 后台根据项目数据目录的表起草本体：返回 202 和状态为 `drafting` 的草稿。可选请求体 `{"discard_decisions": true}` 表示同意丢弃当前草稿上已做的决定。400 `Draft.NoCatalogs`；409：正在起草时 `Draft.Drafting`，草稿已有决定且未同意丢弃时 `Draft.HasDecisions`，项目已归档时 `Draft.ProjectArchived`，项目的数据目录已不存在时 `Draft.CatalogMissing`（vega-backend 返回 404；`error_details` 列出这些目录；否则起草会在其中读不到任何表）。调用方没有目录详情权限（403）时，仍按其能读的表起草 |
| GET | `/projects/{project_id}/ontology/draft` | 草稿：`status`（`drafting`、`ready`，或 `failed` 并带 `error`：`interrupted`、`timeout` 或 vega-backend 的返回）和 `items`：`object_types`、`relation_types`、`value_types`、`excluded`、`unreadable`，每条带证据和是否默认采用；以及 `decisions`，即人做过的修改，按时间先后排列。还没有草稿时 404 `Draft.NotFound` |
| PATCH | `/projects/{project_id}/ontology/draft/items/{kind}/{item_id}` | 修改已就绪草稿中的一项；`kind` 为 `object_types`、`relation_types` 或 `value_types`。请求体：修改所基于的草稿 `update_time`，以及 `adopted`、`name`（1–128 个字符）、`description`（最长 1000）中的任意几项。每处修改都记为一条决定，带被替换的值；不采用某个对象类时，连到它的关系类一并改为不采用。返回草稿。400 `Draft.MissingUpdateTime`、`Draft.EmptyEdit`、`Draft.NeedsIdentity`（对象类还没选定唯一标识就采用）、`Draft.InvalidName`、`Draft.InvalidDescription`、`Draft.EndpointNotAdopted`（关系类两端的对象类没有都采用）；404 `Draft.ItemNotFound`；409 `Draft.Stale`（`update_time` 之后草稿已被修改，需重新加载）、`Draft.NotReady`、`Draft.ProjectArchived` |
| PUT | `/projects/{project_id}/ontology/draft/object_types/{item_id}/identity` | 选定对象类的唯一标识属性。请求体：`update_time` 和 `properties`，即一个或多个不重复的属性，类型须为 `string`、`text`、`integer` 或 `unsigned integer`。单属性的标识会以调用者身份做全表计数，有重复或空值也照样接受，计数写入 `key`（`source` 为 `chosen`）；复合标识不计数。清除 `needs_identity`；是否采用仍需单独修改。400 `Draft.MissingUpdateTime`、`Draft.InvalidIdentity`；404 `Draft.ItemNotFound`；409 同修改草稿项 |
| POST | `/projects/{project_id}/ontology/draft:restore` | 把被隐私规则排除的字段恢复为属性，字段信息以调用者身份从 vega-backend 重新读取。请求体：`update_time`、`object_type`、`field`。400 `Draft.MissingUpdateTime`、`Draft.NotRestorable`（因类型无法映射而排除）；404 `Draft.ItemNotFound`；409 `Draft.FieldNotFound`（字段已不在表中，或无权读取），以及同修改草稿项的情形 |
| POST | `/projects/{project_id}/ontology/draft:confirm` | 确认就绪的草稿：把已采纳的项写成 BKN 3.0 定义，提交到项目分支，与分支已有内容并存；返回草稿，`items.confirmation` 记录 `commit`、`by`、`time`、`files`、`skipped_value_types`。`items.confirmation.update_time` 等于草稿的 `update_time` 时为已确认；之后任何编辑都会推进草稿的更新时间，再次确认会替换或删除上次确认写入的文件（`files`），分支上的其他内容不会被替换。重新起草会保留 `items.confirmation`（不再算已确认），之后的确认仍能替换或删除这些文件。确认的 commit 随后在后台校验（`ontology/check`）。请求体 `{"update_time"}`。仅负责人：403 `Draft.NotOwner`。400 `Draft.MissingUpdateTime`、`Draft.NothingAdopted`、`Draft.InvalidItem`；409 `Draft.HasPending`（`error_details`：仍需选定唯一标识的已采纳对象类、两端未全部采纳的已采纳关系）、`Draft.Stale`、`Draft.NotReady`、`Draft.ProjectArchived`、`Lineage.DefinitionsExist`（分支上已有同路径的定义，列出）、`Lineage.BranchNotV3`（先同步分支）、`Lineage.LineageNotV3`（main 也是 2.x：先提交漂移，再同步分支）；与分支合并后不符合 BKN 校验时 400 `Lineage.InvalidDefinitions`（`error_details`：`table`、`column`、`code`、`message`）。被拒的确认不改变草稿：处理原因后可用同一个 `update_time` 再次确认。BKN 3.0 不接受的值类型（如基础类型为 `text`）不写入，列在 `skipped_value_types`，引用它的属性保留基础类型 |
| GET | `/projects/{project_id}/ontology/check` | 上次确认的校验。确认后在后台自动运行，以确认人的身份计数：`commit`、`status`（`running`、`passed`、`issues`，或 `failed`；服务重启中断时 `error` 为 `interrupted`）、`attempt`、`runner`、`start_time`、`finish_time` 和 `results`：`keys`（每个对象类的唯一标识：`rows`、`duplicates`、`nulls`）、`relations`（每个关系的 `joins`，每连到一端一条：`rows`、`nulls`、`orphans`、`hit_rate`）、`excluded_mapped`（映射到隐私规则排除字段的属性）。每一项带 `state`：`counted`；`not_counted` 并带 `reason`（`cross_catalog`、`several_fields`、`unmapped`、`no_key`）；或 `failed`（`query_failed`、`timeout`）。`issues` 表示唯一标识有重复或缺失，或映射了排除字段；孤儿记录只报告，不判定。一次失败后自动重试一次，只重算失败项。结果只作提示，不会撤销确认。还没有校验时 404 `Check.NotFound` |
| POST | `/projects/{project_id}/ontology/check:run` | 以调用者身份重新校验上次确认的全部内容，返回 202 和校验（`running`）。仅负责人：403 `Check.NotOwner`。409 `Draft.ProjectArchived`、`Check.NothingConfirmed`、`Check.Running`（这次确认正在校验）、`Check.ConfirmAgain`（确认后草稿又改过，而那次确认从未校验过：请再确认一次） |

## 本地开发

```bash
cd server
go build ./...
go vet ./...
I18N_MODE_UT=true go test ./...
```

配置文件：`server/config/bkn-project-config.yaml`。表结构由 data-migrator
根据 `migrations/bkn-project/` 创建。

起草规则在 `draft` 下（括号内为默认值）：`codeTableMaxRows`（500）和
`codeTableMaxColumns`（3）界定代码表；`adoptHitRate`（0.95）和 `suggestHitRate`
（0.8）按命中率给关系分档；`privacyKeywords` 按字段名或注释匹配个人信息；
`queryTimeout`（60 秒）限制每条计数查询，`timeout`（10 分钟）限制整次起草。起草在
服务进程内运行：重启时仍在起草中的草稿置为失败，原因为 `interrupted`。

使用 `make generate-mock` 重新生成 mock（需要 `go.uber.org/mock` 的 `mockgen`）。
