# #2056 准入预算配置持久化

**Goal:** Foundry 自身的 config generate 不再清除已评审的预算阈值、Collector 地址和现有 observability 配置。

**Architecture:** 主线 #2060 已提供结构化诊断，openbkn-deploy #18 已修复其配置生成路径；本批只补 Foundry deploy/scripts/services/config.sh 的对应缺口。保留既有配置段，不添加阈值默认值、临时 Pod 补丁、探针依赖或安全/权限/鉴权机制。

**Tech Stack:** Bash、Helm（使用既有 YAML 解析器，保留配置值与类型，允许标准化格式，不保留注释）、现有 Go 预算测试。

- [x] 实际 config generate 回归先证明观测配置丢失。
- [x] 保留原 observability 段；新安装无该段时不生成推测性阈值。
- [x] 再生成两次、行内/块式配置、字段与类型、配置文件600权限回归。
- [x] 当前配置正常/缺少阈值/不可用指标接口实际返回200或带具体原因的503；健康探针合同不改变。
- [x] 既有 Core 预算/Collector/API回归，Helm配置回读、静态检查、独立评审。
- [x] 独立PR与验收记录，不修改 #2048 或 #2061。

验收：七种真实 Core 配置/Collector 场景全部符合现有200/503合同，隔离 Kafka、MariaDB、OpenSearch 保留；采用合成 Collector 指标，不声称目标生产集群部署或真实队列压力测量。配置再生成依赖安装器已有 Helm，解析失败保留原文件；原始 YAML 格式/注释会标准化。
