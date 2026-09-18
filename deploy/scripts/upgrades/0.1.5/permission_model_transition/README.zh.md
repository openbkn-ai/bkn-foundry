# OpenBKN 0.1.5 权限模型一次性迁移

中文 | [English](README.md)

本目录是 OpenBKN 0.1.4 → 0.1.5 权限模型首次切换的唯一运维入口。本迁移需要整体维护窗口、可审阅的 dry-run、Enterprise 历史规则显式确认以及多数据库一致性备份，因此不接入通用 `data-migrator` 的自动 Helm Hook。

全新安装由 bkn-safe seed 直接写入当前迁移标记；后续版本未改变授权存储合同时，不再执行本迁移。

## 固定步骤

统一入口按顺序执行并在首个失败处停止：

1. `bkn-data`：校验或迁移空分支、权威资源父级关系、托管代理账号、Resource/Tool Box/MCP/Skill 授权来源、物化代理策略、BKN 代理映射与同步版本。Skill 授权按尽力而为处理：授权人无权执行的已挂载 Skill 列入报告的 `skipped_skill_grants`，不会导致该网络迁移失败；Skill 挂载也不改变网络的模型版本。
2. `vega-data`：读取权威的 Catalog 与 Resource 元数据，对账全部 `resource → catalog` 父子关系，并为每个非内置 Catalog 登记规范的创建者业务权限包与授权权限。
3. `authorization`：调用本目录下的 `authz_migrate`，完成 Core 来源与稳定 grant 分类、Enterprise 规则对账和显式激活，并在全部成功后写入带校验和的迁移标记。

BKN 步骤不再删除或重建 caller 权限，也不会写入 `task_manage`。历史 Core allow/deny 全部交给授权步骤分类和保留。

Vega 步骤只写 bkn-safe，其改动由前置 BKN 步骤生成的 Safe 备份覆盖；Vega 数据库只读。

## 环境要求

- Python 3.9+、PyMySQL 1.1.0；
- `mariadb-dump` 或 `mysqldump`，以及足够保存 BKN、Safe 完整逻辑备份的空间；
- 目标集群的 `kubectl` 权限；
- 仓库内置的、适用于 Linux 部署环境的 `authz_migrate/authz-migrate` 可执行程序；
- BKN、Vega 和 Safe 数据库访问权限。

数据步骤优先读取 `BKN_DB_*`、`VEGA_DB_*`、`SAFE_DB_*`，其次读取标准 `MARIADB_*`，最后使用本地默认值。密码文件可通过各数据库前缀的 `*_PASSWORD_FILE` 及对应 MariaDB 环境变量提供。如脚本旁目录不适合保存备份，设置 `OPENBKN_MIGRATION_BACKUP_DIR`。

## 执行流程

复制 `manifest.example.json` 到受保护的工作目录，并把全部占位内容替换为权威的发行、生命周期和 Enterprise 证据。

发行内容已携带授权迁移程序，执行迁移不需要 Go。仅在有意修改其 Go 源码时才重新构建：

```bash
./authz_migrate/build.sh
```

先执行无写入检查：

```bash
./migrate.py dry-run \
  --source-version 0.1.4 \
  --manifest /work/authz-migration.json \
  --authz-config /etc/bkn-safe/config.yaml \
  --report-dir /work/reports/dry-run
```

审核 `01-bkn-data.json`、`02-vega-data.json`、`03-authorization.json` 和 `summary.json`。Vega 创建者权限包只能依据权威的 `t_catalog.f_creator` 生命周期元数据生成，不能从历史访问权限推断。需要激活 EE 历史规则时，把精确的 inventory digest 和管理员确认写入 manifest。authorization 步骤不得根据历史 operation 集合推断 `full_business_access`，也不得自动增加 operation。

关闭外部网关并停用相关 CronJob/Worker，然后停止已登记的业务 Deployment：

```bash
./migrate.py stop \
  --namespace openbkn \
  --state-file /work/workloads.tsv
```

使用新的空报告目录执行迁移：

```bash
./migrate.py apply \
  --source-version 0.1.4 \
  --manifest /work/authz-migration.json \
  --authz-config /etc/bkn-safe/config.yaml \
  --state-file /work/workloads.tsv \
  --namespace openbkn \
  --report-dir /work/reports/apply
```

`apply` 会通过目标集群再次确认全部登记的 Deployment 仍处于停止状态。BKN 步骤在首次写入前创建并校验 BKN、Safe 完整逻辑备份，然后提交 BKN/代理数据；授权步骤随后执行。任一步失败时，后续步骤不再运行，业务服务保持停止。

审核 apply 报告后，在外部入口仍关闭的情况下启动新版本、完成授权冒烟，再开放流量：

```bash
./migrate.py start \
  --namespace openbkn \
  --state-file /work/workloads.tsv
```

## 失败恢复

任一步失败都不得启动业务服务。使用 `01-bkn-data.json` 中记录的命令同时恢复 BKN、Safe 备份，部署全部旧版本二进制，验证旧权限行为后才能开放流量。禁止手工伪造成功标记或在部分迁移的数据上继续执行。

## 聚焦测试

```bash
python3 -m unittest -v test_bkn_data.py vega/test_vega_data.py test_migrate.py
./test_service_control.sh
(cd authz_migrate && go test -p=1 ./...)
```
