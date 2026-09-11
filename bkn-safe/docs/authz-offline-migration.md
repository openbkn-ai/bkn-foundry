# 四档权限一次性离线迁移

本手册用于首次切换到 `authz-edition-boundaries-v1` 权限存储合同。它不是常规版本升级步骤；后续版本未改变权限兼容合同时，不得重复设计或启用在线双读、双写和按租户灰度。

## 安全边界

- 必须在产品整体维护窗口执行；网关、外部 API、Studio、MCP、定时任务、行动执行、同步 Worker 和所有会读写权限或创建受保护资源的实例均停止接收流量。
- 执行前备份完整的 `safe` 数据库；存在 `ee_permobject_rules` 时必须包含 EE 私有表及生命周期审计表。
- dry-run 不写数据库。管理员必须审阅完整报告，`effect=deny` 行优先单独复核。
- 真实数据库的 apply 和 EE 激活属于权限语义变更，必须另行提交 What / Blast radius / Rollback，并取得 Owner 确认。
- 任一步失败都保持维护模式，不启动混合版本实例。恢复方式是还原数据库备份并运行旧二进制。

## 1. 停止流量并核对实例

先关闭产品外部入口，再将所有相关 Deployment、CronJob 和临时 Worker 停止。资源名称随发行清单变化，操作人员必须从当前 release manifest 生成清单，不能只停止 bkn-safe。

至少覆盖：

- bkn-safe、bkn-backend、ontology-query、Vega；
- action execution、schedule、sync、agent/context-loader 等后台执行与查询组件；
- Studio、CLI、MCP 和外部 API 的网关入口；
- 任何旧版本或新版本的临时 Pod。

停止后确认没有 Running 的相关业务 Pod，也没有待执行的 CronJob/Job。维护期间不得重新打开入口。

## 2. 备份

使用部署环境规定的数据库备份工具生成一致性备份，并记录备份标识、时间和恢复命令。备份完成前不得执行 apply。

## 3. 准备证据 manifest

从 [authz-migration-dry-run.json](examples/authz-migration-dry-run.json) 复制工作文件：

- `lifecycle_evidence` 只接收知识网络所有者 `authorize` 和行动类创建者实例 `execute` 的权威生命周期事实；缺少证据的 Core 行保留为 `legacy`，不会根据 operation 集合推断完整包或系统来源。
- `ee.assembly.was_assembled` 必须依据发行/装配记录填写。曾装配 EE 但私有表缺失会中止；纯 Community 缺表记录为 `absent`，迁移不会创建 EE 表。
- `ee.assembly.evidence_ref` 在 apply 时必填。纯 Community 也必须引用发行清单、装配台账或等价审核记录，明确证明“未装配”，不能以字段缺省代替结论。
- `ee.rule_evidence` 必须同时给出正式发布写入证据和升级前实际参与判权的证据。表存在、许可证档位或字段合法本身都不是激活证据。

示例文件默认表示纯 Community，`rule_evidence` 为空。只有 dry-run 确认 EE 表为 `present_with_rows` 时，才按实际规则填写，例如：

```json
{
  "grant_id": "historical-ee-grant-id",
  "expected_subject_type": "user",
  "published_writer_evidence": "published-writer-audit-reference",
  "runtime_usage_evidence": "historical-runtime-decision-reference"
}
```

该对象加入 `ee.rule_evidence` 数组；不得把占位 ID 直接用于维护窗口。

EE 主体类型由 `safe.users`、`safe.roles` 和可信 Casbin 角色成员关系重新确认。部门、主体缺失、类型冲突、禁用用户或无真实成员的角色不能激活。

## 4. dry-run

在挂载 bkn-safe 配置、数据库 Secret 和 manifest 的一次性维护 Pod 中运行：

```sh
/opt/bkn-safe/authz-migrate \
  --mode dry-run \
  --config /etc/bkn-safe/config.yaml \
  --manifest /work/authz-migration.json \
  > /work/authz-migration-report.json
```

保存报告原件。必须检查：

- Core 每条策略的当前/计划来源、稳定 `grant_id`、清理项和异常；
- 没有历史 operation 集合被转换为 `full_business_access`，也没有自动补 `execute`；
- BKN 子资源 `authorize` 和无业务执行点的 BKN `task_manage` 仅列为清理，不扩大到知识网络；
- 不存在 operation 为 `execute_action` 的持久化行；若存在，必须修正数据来源并重新 dry-run，工具不会转换它；
- EE 表状态为 `absent`、`present_empty` 或 `present_with_rows`，与装配记录一致；
- EE 每行的主体类型、三类分类、effect、operation、有效期、激活资格和异常；
- 所有 deny 行完成单独复核。

任何异常都不得进入 apply。

## 5. 管理员确认 EE 激活

默认不激活任何历史 EE 行。需要激活时，把 dry-run 报告中的 `enterprise.inventory_digest` 原样写入 manifest，并增加：

```json
{
  "confirmed_grant_ids": ["待激活的稳定 grant_id"],
  "inventory_digest": "dry-run 输出的 64 位摘要",
  "operator_id": "管理员主体 ID",
  "evidence_ref": "变更单或审批记录",
  "confirmed_at": "2026-09-11T08:00:00Z"
}
```

该对象放在 `ee.activation`。摘要不一致、确认信息不全、规则已过期/撤销、主体不可信或角色没有真实成员关系都会中止，不会部分激活。

## 6. apply

再次确认维护模式、备份可恢复且 manifest 已审批，然后运行：

```sh
/opt/bkn-safe/authz-migrate \
  --mode apply \
  --config /etc/bkn-safe/config.yaml \
  --manifest /work/authz-migration.json \
  > /work/authz-migration-applied.json
```

apply 不接受省略 `--manifest` 的调用；manifest 缺少 `ee.assembly.evidence_ref` 时也会在连接数据库前失败。

工具按以下顺序执行：

1. 同时完成 Core 与 EE 只读预检；
2. 幂等写入 Core 来源、稳定 grant 和经批准的清理/系统派生规则；
3. 对存在的 EE 表写入分类，只激活管理员明确确认的行；
4. 重新对账数量、来源、主体、effect、operation、有效期和激活结果；
5. 两侧全部通过后写入带校验和的当前迁移标记。

重复执行同一 manifest 不得新增 grant、删除有效策略或重复写激活审计。

## 7. 新版本冒烟与性能门禁

保持外部入口关闭，一次性部署全部相关新版本服务。bkn-safe 在接受监听流量前校验迁移标记；标记缺失、版本不符或校验和异常时拒绝启动。

至少执行并留存以下结果：

- Core legacy allow/deny、父子资源回落和历史行动实例 `execute`；
- EE 用户 allow、deny、永久和到期规则；
- EE 角色 allow/deny，测试用户必须具有真实可信角色成员关系；
- Community/Professional 下 EE 层不参与，恢复 Enterprise/Industry 后已激活且未过期规则恢复；
- 休眠、异常、部门和错误主体分类均不进入运行时；主体错分触发失败关闭；
- 代表性的 `100 resources × 5 operations` 列表判权，记录 EE SQL 次数和 P95。若存在逐资源/operation 往返或未达到发布基线，先完成批量 provider 优化，不开放流量。

全部冒烟、分类和激活结果成功后，才允许统一退出维护模式。禁止先开放一部分资源或一部分服务。

## 8. 失败恢复

任何迁移、标记、启动、冒烟或性能检查失败时：

1. 保持网关和后台流量关闭；
2. 停止所有新版本实例；
3. 恢复步骤 2 的数据库备份；
4. 部署旧版本全部相关服务；
5. 验证旧权限结果后再决定是否恢复外部流量。

不得在失败库上手工补写成功标记，也不得跳过 bkn-safe 启动门禁。
