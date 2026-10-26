# BKN Project

中文 | [English](README.md)

`bkn-project` 基于项目构建并发布知识网络。每次发布都作为一个新的知识网络
（`<base_id>_<version>`）导入 bkn-backend，不覆盖线上网络；发布历史以谱系
（lineage）形式保存。参见 issue #2520 和 epic #2519。

## 当前能力范围

- 发布谱系存储（`t_bkn_project_release`）及只读 API。
- git 谱系：每个谱系一个裸仓库，位于 `/data/git`（Helm chart 中为持久卷）。
  `main` 对应线上网络；各项目在自己的分支上工作，并通过语义三方合并把
  `main` 同步进分支。
- 线上网络与 `main` 之间的漂移检查。
- 健康检查与就绪检查接口。

发布流程在后续变更中提供。

## API

前缀：`/api/bkn-project/v1`。除 `/health` 和 `/ready` 外的接口都需要 OAuth
令牌，与 bkn-backend 的外部接口一样通过 Hydra 校验。从 bkn-backend 导出时使用
调用方的令牌，因此 bkn-backend 的权限同样生效。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/health` | 服务信息 |
| GET | `/ready` | 数据库可达时返回 200，否则返回 503 |
| GET | `/lineages/{lineage_id}/releases?limit=&cursor=` | 谱系下的发布记录，按时间倒序，游标分页 |
| GET | `/releases/{network_id}` | 产生该网络的发布记录 |
| POST | `/lineages` | 请求体 `{"kn_id"}`。导出线上网络并在 `main` 上创建谱系；返回 201 `{"lineage_id","commit"}`，已存在返回 409 |
| POST | `/lineages/{lineage_id}/branches` | 请求体 `{"name","from"}`（`from` 默认 `main`）；返回 201 `{"name","commit"}`，已存在返回 409 |
| PUT | `/lineages/{lineage_id}/branches/{branch}/definitions?message=` | 请求体为 bkn-backend 导出格式的 BKN tar 包（`application/octet-stream`，最大 64 MiB），替换分支上的定义；返回 `{"commit","changed":{"added","changed","removed"}}`。对 `main` 返回 403 |
| POST | `/lineages/{lineage_id}/branches/{branch}/sync` | 把 `main` 同步进分支；返回 `{"result":"up_to_date"\|"fast_forward"\|"merged","commit","summary"}`，有冲突时返回 409，冲突列表在 `error_details` 中 |
| POST | `/lineages/{lineage_id}/drift?commit=` | 请求体 `{"kn_id"}`。比较线上网络与 `main`；返回 `{"lineage_id","kn_id","added","changed","removed","commit"}`。`commit=true` 时把线上定义提交到 `main` |

## 本地开发

```bash
cd server
go build ./...
go vet ./...
I18N_MODE_UT=true go test ./...
```

配置文件：`server/config/bkn-project-config.yaml`。表结构由 data-migrator
根据 `migrations/bkn-project/` 创建。

使用 `make generate-mock` 重新生成 mock（需要 `go.uber.org/mock` 的 `mockgen`）。
