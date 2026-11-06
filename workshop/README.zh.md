# Workshop

中文 | [English](README.md)

Workshop 是 OpenBKN 的应用工坊服务：应用与发布版本、构建器 AI 对话、事件与任务，以及图探索的分析画布，都存在这个服务里。服务属于付费版，业务实现在私有仓库 openbkn-ee（镜像 `workshop-ee`）；本目录是社区版骨架。

## 本目录包含

- 进程骨架（`server/app`）：读取配置、连接数据库、安装许可门控，在 `Boot` 与 `Run` 之间由付费版用 `RegisterRoutes` 装配业务路由。
- 公共 HTTP 层（`server/driveradapters`）：健康检查 `/api/workshop/v1/health`、就绪检查 `/api/workshop/v1/ready`（数据库可达）、内部健康检查 `/api/workshop/in/v1/health`；`Authenticate`（经 Hydra 校验令牌）与 `RequireEdition`（未达档位时与社区版一样返回 404）供业务路由使用。
- 没有数据库迁移：Workshop 的表由付费版镜像在启动时自己创建与升级，骨架不建表。

这里不发布镜像和 Helm chart：付费版镜像 `workshop-ee` 及其 chart 在 openbkn-ee 构建，由企业版安装器作为企业版独有服务部署。骨架单独构建时只有上面的探针，业务路由一律返回 404。

## 配置

- 配置文件 `config/workshop-config.yaml`；依赖服务写在 `depServices`：`rds`、`hydra-admin`、`bkn-agent`、`bkn-backend`（知识网络事件）。
- `BKN_SAFE_URL`：bkn-safe 地址，必填。
- 端口 13022。公开路由 `/api/workshop/v1` 进 Ingress；内部路由 `/api/workshop/in/v1` 只在集群内。

## 开发

```bash
cd workshop
make test
```
