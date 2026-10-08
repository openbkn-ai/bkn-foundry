# Internal Trace Admission Implementation Plan

**Goal:** 根据用户确认，内部 Trace Admission 不鉴权、不签名。
**Architecture:** policy、heartbeat、publisher/gateway ACK 仅在内部 8081 listener 提供；workload identity 来自内部协议字段。保留 revision、有效期、状态收敛、请求结构验证。调用方需要的 configuration GET 同步提供内部只读路由，公开用户配置读写仍保留原鉴权。
**Tech Stack:** Go、Python、Helm、Bash。

- [x] 服务端：匿名内部接口回归测试；移除服务主体与策略签名；测试公开路由不可访问这些接口。
- [x] Go Publisher：匿名 unsigned policy/control/configuration 回归测试；删除 OAuth/key 配置；保留有效期和状态检查。
- [x] bkn-agent / Collector：同样简化协议、配置、Chart 和测试。
- [x] 四个 Go workload Chart：无需 OAuth/signing Secret 的 enabled publisher render 回归测试，删除配置。
- [x] 安装器：无需 Admission credential 的完整 publisher profile 回归测试，移除强制注册配置。
- [x] 接口文档和设计更新；各模块 test/vet/build/Helm 验证及整体协议核对。

无旧客户端兼容层。按用户授权提交分支并创建 PR；不执行实际部署或 Secret 删除。

## 验证记录

- agent-observability：完整 `go test ./... -count=1`，`go vet ./...`，`go build ./...` 通过。
- comm-go：`go test ./bkntrace/...`、`go vet ./bkntrace/...`、publisher race test 和完整 build 通过。全量 tests 依赖 MYSQL/DM/KDB 数据库环境变量，当前环境未提供。
- 四个 Go workload：Trace 包测试/vet、完整 `go build ./...` 通过。
- Collector：完整模块 tests，Helm lint、身份/构建契约测试通过。
- bkn-agent：策略/Kafka 40 项 tests、Helm lint 与渲染通过。
- 内部四接口真实客户端/handler 集成测试：`bkn-trace/tests/internal-control` 的 `make ci` 通过，已加入 CI。
- 部署：四个 workload enabled publisher 渲染、六类工作负载 NetworkPolicy allowlist、无凭据 admission profile、原安装 profile 116 项检查通过。
- `git diff --check`、OpenBKN license headers、Swagger contract drift 通过。
- 与 CI 相同版本的 golangci-lint v2.12.2：agent-observability 0 issues。

### 环境验证限制

未执行真实 Kubernetes 部署或 CNI 网络拒绝测试。Docker daemon 未启动，Collector 镜像配置验证未执行。GitHub CI 结果在 PR 中跟踪。独立 openbkn-deploy 安装器未在当前工作区，修改仅覆盖本仓库部署脚本和 Chart。
