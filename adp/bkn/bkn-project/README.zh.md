# BKN Project

中文 | [English](README.md)

`bkn-project` 基于项目构建并发布知识网络。每次发布都作为一个新的知识网络
（`<base_id>_<version>`）导入 bkn-backend，不覆盖线上网络；发布历史以谱系
（lineage）形式保存。参见 issue #2520 和 epic #2519。

## 当前能力范围

- 发布谱系存储（`t_bkn_project_release`）及只读 API。
- 健康检查与就绪检查接口。

发布流程、git 存储和漂移检查在后续变更中提供。Helm chart 已在 `/data/git`
挂载持久卷，供 git 存储使用。

## API

前缀：`/api/bkn-project/v1`。发布相关接口需要 OAuth 令牌，与 bkn-backend
的外部接口一样通过 Hydra 校验。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/health` | 服务信息 |
| GET | `/ready` | 数据库可达时返回 200，否则返回 503 |
| GET | `/lineages/{lineage_id}/releases?limit=&cursor=` | 谱系下的发布记录，按时间倒序，游标分页 |
| GET | `/releases/{network_id}` | 产生该网络的发布记录 |

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
