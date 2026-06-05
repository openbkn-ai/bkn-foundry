# ISF 替换 —— VM 上线 Runbook(2026-06-05)

> 代码 + 部署产物已全部就绪(见 cutover-plan-2026-06-04.md §E 与各 commit)。
> 本 runbook 是 **VM 上的操作步骤**:发布 → 部署 hydra+bkn-safe → seed → 影子(E2)
> → 逐服务翻权威(E3)→ introspect 重指(E4)→ 退役 ISF(G)。
>
> **环境**:`parallels@10.211.55.4`(ubuntu24 aarch64,docker + k3s + helm)。命名空间沿用
> 现有部署 ns(下文记作 `$NS`)。**所有部署/验证在 VM 上跑,不要在本机 Mac 跑。**
>
> **核心安全网**:每个服务的切换都是 `AUTHZ_PROVIDER`/`DIRECTORY_PROVIDER` env,
> **回退 = 把 env 翻回空(=ISF)再 rollout restart**。ISF 全程在线直到 G 验证无差。

---

## 0. 前置

- [ ] 确认在 VM 上:`ssh parallels@10.211.55.4`,`kubectl get ns`,`helm version`。
- [ ] 管理员账号 `admin/eisoo.com123`、测试账号 `test/111111`(沿用现有 ISF 用户做对照)。
- [ ] 记录回退基线:`helm list -n $NS`、各服务当前镜像 tag。

## 1. CI 发布 bkn-safe 镜像 + bkn-safe/hydra chart 到 OCI

工作流:`.github/workflows/release-adp-bkn-safe.yml`(push `bkn-safe/**` 或 workflow_dispatch + publish=true)。
- [ ] 触发并确认产出:
  - 镜像 `ghcr.io/openbkn-ai/bkn-safe:<ver>`
  - chart `oci://ghcr.io/openbkn-ai/charts/bkn-safe:<ver>`、`.../hydra:<ver>`
- [ ] `<ver>` 与 `release-manifests/0.1.0/bkn-foundry.yaml` 里 hydra/bkn-safe 的 `version` 对齐
      (manifest 现写 `0.1.0`;按 CI 实际产出版本改 manifest 或用 `--version`)。

## 2. 供给 PostgreSQL(hydra 后端,**硬前置**)

hydra 不能跑 MariaDB(CAST AS JSON / MDEV-26448)。需要一个 PG:
- [ ] 选型:外挂 PG,或 ns 内起一个(可用 `bitnami/postgresql` 或自管 Deployment)。
- [ ] 建库:`hydra` 库 + 账号;记下 DSN `postgres://USER:PWD@HOST:5432/hydra?sslmode=disable`。
- [ ] 把 DSN 写进 hydra chart values `config.dsn`(部署时 `--set` 或 values 覆盖)。

## 3. 部署 hydra + bkn-safe(E1)

> bkn-safe 用现有 MariaDB(`config.db.*`,`SAFE_DB_TYPE=MySQL` 走 proton-rds);hydra 用 §2 的 PG。
> 生产关掉 bundledDeps(`bundledDeps.enabled=false`)。

- [ ] **hydra**:
  ```sh
  helm upgrade --install hydra oci://ghcr.io/openbkn-ai/charts/hydra --version <ver> -n $NS \
    --set namespace=$NS \
    --set config.dsn="postgres://USER:PWD@PGHOST:5432/hydra?sslmode=disable" \
    --set config.publicIssuer="https://<外部域名>" \
    --set config.urls.login="https://<外部域名>/login" \
    --set config.urls.consent="https://<外部域名>/consent" \
    --set config.urls.deviceVerification="https://<外部域名>/device" \
    --set config.secretsSystem="<32+字节强随机>" \
    --set config.tlsAllowTerminationFrom="<ingress/pod CIDR>"
  ```
  - 验证:migrate Job `Completed`;`kubectl get pods -n $NS | grep hydra` Running;
    `curl -s http://hydra:4444/.well-known/openid-configuration`(集群内)返回 metadata。
- [ ] **bkn-safe**:
  ```sh
  helm upgrade --install bkn-safe oci://ghcr.io/openbkn-ai/charts/bkn-safe --version <ver> -n $NS \
    --set namespace=$NS \
    --set bundledDeps.enabled=false \
    --set config.db.host=<现有MariaDB> --set config.db.password=<...> --set config.db.name=safe \
    --set config.hydra.adminURL="http://hydra:4445" \
    --set config.hydra.publicURL="http://hydra:4444" \
    --set config.seedOnStart=true
  ```
  - 验证:`kubectl logs deploy/bkn-safe -n $NS` 见 seed 完成;`/health/ready` OK。

## 4. Seed + 建用户分角色(重定义,不迁 ISF 表)

- [ ] `SAFE_SEED_ON_START=true` 已灌:9 角色(UUID 保号)+ 13 资源类型 + 角色→资源授权
      (roles.json/catalog.json/grants.json)。验证:`POST /api/safe/v1/authz/check` 对超级管理员通配返回 allowed。
- [ ] 建本地用户(bcrypt)+ 绑角色(`POST /api/safe/v1/authz/role-bindings`):
      至少建一个与 ISF `admin` 对应的超级管理员、一个 `test` 普通用户,**角色按重定义**(不照搬 ISF 策略表)。
- [ ] 用 `bkn-safe/dev/validate-safe-api.sh`(指向 VM 的 bkn-safe)过一遍 19 项 API。

## 5. E2 —— 影子比对(ISF 仍权威)

逐服务设 **shadow**(只观测,不改判定),`BKN_SAFE_URL` 指 bkn-safe ClusterIP:
- [ ] 改 chart values(或 `--set`)→ rollout:
  ```sh
  # 例:vega。其余:bkn-backend / agent-backend / mf-model-manager / mf-model-api / agent-operator-integration
  helm upgrade vega-backend ... \
    --set bknSafe.authzProvider=shadow \
    --set bknSafe.directoryProvider=shadow \    # 有 directory 适配器的服务才设
    --set bknSafe.url="http://bkn-safe:3000"
  kubectl rollout restart deploy/vega-backend -n $NS
  ```
  > exec-factory 只有 authz(无 directory),不设 directoryProvider。
- [ ] 跑真实流量(或回放),收日志:
  ```sh
  kubectl logs deploy/<svc> -n $NS | grep '\[authz-shadow\] DIFF'
  ```
- [ ] **判读**:DIFF 应只来自“重定义”预期差(角色/策略重新定义),不应有“同输入不同判定”的意外。
      不清零、逐条解释。directory 侧同理(名称解析/部门归属一致)。

## 6. E3 —— 逐服务翻权威(bkn-safe authoritative)

DIFF 清白后,**逐个**服务翻:
- [ ] `--set bknSafe.authzProvider=bkn-safe`(+ `directoryProvider=bkn-safe`)→ rollout restart。
- [ ] 翻一个、观察一个(功能 + 错误率)。**回退** = 翻回 `shadow` 或空 + restart(秒级、无数据迁移)。
- [ ] 顺序建议:先样板(vega/bkn 的 names + 一个 authz 服务)→ DA → mf-model → exec-factory。

## 7. E4 —— introspect 重指新 hydra

各服务校验 token 走 hydra-admin introspect。把各服务的 hydra-admin endpoint 配置
(per-cluster values)指向 §3 的新 hydra(`http://hydra:4445`):
- [ ] 改各服务 OAuth/hydra 配置值 → rollout。验证:已签发 token introspect 仍返回含 ext 字段
      (visitor_type/login_ip/udid/account_type/client_type)——旧 lib 无 nil 检查,5 字段必须齐。
- [ ] OAuth client:把现有应用的 client 在新 hydra 重建/迁移(client_id/secret/redirect_uri/grant)。

## 8. G —— 退役 ISF

三层(authz/directory/introspect)全翻 + 对账无差,稳定观察期后:
- [ ] manifest 去掉 `isf` 依赖(`dependencies` 里那段)或关 `auth.enabled`;`deploy.sh kweaver-core` 重出。
- [ ] `helm uninstall` ISF 11 release(authentication/authorization/user-management/sharemgnt/
      policy-management/audit-log/eacp/isfweb/isfwebthrift/hydra(fork)/isf-data-migrator)。
- [ ] 保留 ISF 的 MariaDB 数据一段时间(只读),确认无回退需求再清。
- [ ] (低优先)DA `f_is_data_flow_agent` 列:确认线上无数据后单独迁移 drop(见 dataflow-removal-scope §5.7)。

## 回退矩阵(任何阶段)

| 出问题 | 回退 |
|---|---|
| 某服务翻 bkn-safe 后异常 | `bknSafe.authzProvider/directoryProvider` 翻回 `shadow` 或空 + rollout restart(秒级) |
| introspect 重指后 token 失效 | hydra endpoint 配置翻回 ISF hydra-fork + rollout |
| bkn-safe/hydra 本身故障 | 服务 env 全空 = 纯 ISF;ISF 未退役,直接回落 |
| 已退役 ISF 后才发现问题 | 由 §8 “保留 ISF 数据/charts” 兜底重装(故 G 要在稳定观察期后) |

## 验证脚本 / 资产

- `bkn-safe/dev/validate-safe-api.sh`、`validate-e2e.sh`(改 endpoint 指 VM)。
- shadow 离线对账:`bkn-safe/cmd/authz-shadow`(ISF↔bkn-safe 批量 diff)。
- 影子日志标记:`[authz-shadow] DIFF`(Go 服务)/ `[authz-shadow] DIFF`(mf-model Py)。
