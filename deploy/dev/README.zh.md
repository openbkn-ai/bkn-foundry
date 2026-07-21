# macOS 开发路径（`dev/mac.sh`、kind）

**读者：** **macOS 开发者**作快速验证的**可选**路径。**生产环境与主文档以 Linux 为准** —— 请先看 [`deploy/README.zh.md`](../README.zh.md)、[`help/zh/install.md`](../../help/zh/install.md)；英文见 [`deploy/README.md`](../README.md)、[`help/en/install.md`](../../help/en/install.md)。

中文 | [English](README.md)

本机用 **kind** 起 Kubernetes，与 Linux `deploy.sh` 使用同一套 Helm Chart；宿主机不跑 **`preflight` / k3s / kubeadm**。

> ⚠️ **macOS 上不要用 `sudo`。** 本节所有命令一律用普通 `bash`。Docker Desktop / `kind` / `$HOME`（含安装配置与 `bkn` token）都属于当前用户，`sudo` 会把它们重定向到 `/var/root` 并割裂安装与 onboard。`deploy.sh` 已识别 `Darwin` 并跳过 root 检查，`sudo` 在 Mac 上无任何额外作用。

### 克隆仓库（先做好）

脚本与清单在仓库目录内；**`mac.sh` 不能脱离仓库单独使用**。请先 **[clone openbkn-ai/bkn-foundry](https://github.com/openbkn-ai/bkn-foundry)**，并切换到实际部署所用分支，然后 **`cd` 进入 `deploy/`**：

```bash
git clone https://github.com/openbkn-ai/bkn-foundry.git
cd bkn-foundry/deploy   # 在此目录执行 bash ./dev/mac.sh ...（与 deploy.sh 同层）
```

从产品包解压时，路径中须有 **`deploy/`** 目录，布局与上文一致即可。

### 架构（Apple Silicon / arm64）

**Apple Silicon** 上 kind 节点默认为 **linux/arm64**。镜像来自 [`dev/conf/mac-config.yaml`](conf/mac-config.yaml) 的 **`image.registry`**（由 [`mac-config.yaml.example`](conf/mac-config.yaml.example) 复制）；须 **arm64 可用**（多架构 manifest 或 arm64 标签）。仅 **amd64** 的镜像易导致 *exec format error*。Intel Mac 上节点多为 **amd64**（除非另行指定平台）。

### 访问地址（HTTP 与自动 host）

- **HTTP 与 HTTPS：**HTTPS 加密并校验服务端；HTTP 不加密，开发环境常见；浏览器对 HTTP 的「不安全」提示属预期。
- **自动 IP：**`mac-config.yaml` 常用 **`accessAddress.scheme: http`**，且可**省略 `host`**（见示例）。**`bkn-foundry install`** 会探测本机局域网 IP 并写入 values。若仅本机访问，可设 **`accessAddress.host: localhost`**。

### 操作流程

在 **`deploy/`** 下执行；用 **bash** 调用（如 `bash ./dev/mac.sh ...`）。**`bkn-foundry` / `core`** 封装默认装**全量（含必选的 bkn-safe）**；旧的无认证 `--minimum` 模式已移除。

| 步骤 | 命令 | 是否必需？ |
|------|------|------------|
| 1 | `bash ./dev/mac.sh doctor` | 建议 |
| 2 | `bash ./dev/mac.sh doctor --fix`（或 `-y doctor --fix`） | 缺工具时 |
| 3 | `bash ./dev/mac.sh cluster up` | **安装前必须** |
| 4 | `bash ./dev/mac.sh data-services install` | **可选** — 仅单独装/刷新数据层；**`bkn-foundry install` 会先跑同一套捆绑安装**（`OPENBKN_SKIP_DATA_SERVICES_BUNDLE=true` 可跳过） |
| 5 | `bash ./dev/mac.sh bkn-foundry download` | **可选**（本地 chart 缓存） |
| 6 | `bash ./dev/mac.sh bkn-foundry install` | **必须** — 部署 Core（全量含 bkn-safe）；默认**先装捆绑 data-services** |
| 7 | `bash ./dev/mac.sh onboard` | **可选**（需 `bkn` CLI；`-y` 少交互） |

其它与 Linux **`deploy.sh`** 相同（须集群就绪、[`CONFIG_YAML_PATH`](conf/mac-config.yaml) 等与安装一致）：`bash ./dev/mac.sh isf install|download|uninstall|status`，`bash ./dev/mac.sh etrino …`（Vega；**`vega`** 为 **`etrino`** 别名）。ISF 对 DB/配置要求常更高。**未接入 `mac.sh`：**`bkn-dip`。

**最短路径：**`cluster up` → `bkn-foundry install`（**先装数据层**）。若 **`OPENBKN_SKIP_DATA_SERVICES_BUNDLE=true`**，须自备 DB/Kafka 等可达实例，或先执行 **`data-services install`**。

**暂歇省资源（不删集群）：**退出 **Docker Desktop** 即可。kind 依赖 Docker，等于停掉本地集群，**不是** `cluster down`（不会 `kind delete`）。再用时重新打开 Docker。

若 **Docker 要一直开着**，可以只停 kind **节点**容器（效果同样是集群不可用，未执行 `kind delete`）：

```bash
CLUSTER="${KIND_CLUSTER_NAME:-bkn-dev}"
docker stop $(docker ps -q --filter "label=io.x-k8s.kind.cluster=${CLUSTER}")
```

恢复：再执行 `docker start $(docker ps -aq --filter "label=io.x-k8s.kind.cluster=${CLUSTER}")`（`CLUSTER` 同上）。

**卸载（删除集群）：**可先 **`bash ./dev/mac.sh data-services uninstall`**（卸数据层 Helm，保留 kind），再 **`bash ./dev/mac.sh cluster down`**（执行 `kind delete cluster`，销毁集群）。

**配置：**[`mac-config.yaml.example`](conf/mac-config.yaml.example) → **`mac-config.yaml`**；**已被 .gitignore**，避免口令入库；按需调整 **`accessAddress`**、**`image.registry`**。

另见：[`mac.sh`](mac.sh) 顶部注释、`bash ./dev/mac.sh -h`。

### 推荐资源 & 已知问题

- **资源（Docker Desktop / colima）**：建议给虚拟机 **≥ 10 CPU**、**≥ 14 GB 内存**、**60 GB 磁盘**。给少了的风险：
  - **8 GB** Docker 内存对「kind + 数据层 + Core」一般 **明显不够**（现象：Pod **RESTARTS** 很多、长期 **`0/1 Running`**、Docker/虚拟机休眠后更严重）。**~10 GB 可当作能试的底线**；低于约 **12 GiB** 时 **`mac.sh doctor` 会告警**（可用 `MAC_DOCTOR_MIN_MEM_GB` 覆盖）；要稳态仍建议下文 **14–16 GB**。
  - 全量栈 CPU 请求合计约 **4 核**：4 CPU 的 VM 无调度余量（会 `Insufficient cpu`），6 CPU 才有富余，故建议 **≥ 6**。
  - `--memory 12`（GB）实际分配 **11.66 GiB**（GB→GiB 换算损失），**低于 doctor 的 12 GiB 阈值**会报内存不足；建议直接 `--memory 14`。例：`colima start --cpu 10 --memory 14 --disk 60`。
  - **不要安装中途 resize**：Helm 部署完后停/起 VM 会触发下面的 Redis ACL bug。

- **代理污染**（`cluster up` 之前）：kind 容器会继承宿主 `HTTP_PROXY` / `HTTPS_PROXY`。若代理地址是 `127.0.0.1:<port>` 或实际未启动，kind 节点拉镜像会报 `proxyconnect tcp: connect: connection refused`，宿主上 `curl http://localhost/...` 也会返 502（curl 走了死代理）。每次 `mac.sh` 之前先：
  ```bash
  unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY all_proxy ALL_PROXY
  ```
  后面验证 ingress 时也建议加 `--noproxy '*'` 给 curl 兜底。

- **Redis pod `CrashLoopBackOff` 且报 `WRONGPASS invalid username-password pair`**（通常发生在 VM/节点重启后，或 Redis `helm upgrade` 之后）：`redis` 镜像内的 `/config-init.sh` 给 `monitor-user` 写死了一个固定 SHA256 且 else 分支只 `sed` 替换不补行；同时 `sentinel`/`exporter` sidecar 在运行期会跑 `ACL SETUSER` + `ACL SAVE`，把盘上 ACL 写成跟 Secret 不一致的 hash。盘上文件不会自愈，用一条命令修复：
  ```bash
  bash ./deploy.sh redis fix-acl
  ```
  脚本会删掉 PVC 上的 `/data/conf/{users,sentinel-users}.acl` 并删 Pod，让 init 容器走 "if not exists" 分支从 ConfigMap 重新拷正确 hash 的 ACL。依赖 Redis 的应用（`agent-operator-integration`、`coderunner` 等）会在下一次 backoff 后自愈，或 `kubectl delete pod` 加速。如果不方便用 `deploy.sh`，对应的手动等价命令：
  ```bash
  kubectl exec -n resource redis-0 -c redis -- \
    rm -f /data/conf/users.acl /data/conf/sentinel-users.acl
  kubectl delete pod -n resource redis-0
  ```

- **`onboard --config` 必须用 `=`**：`bash ./dev/mac.sh -y onboard --config=conf/models.yaml`。空格形式 `--config conf/...` 会被底层 `onboard.sh` 拒绝并报 `Unknown: --config`。

- **kind 镜像在 Docker Desktop 的 "Images" 面板看不到**：kind 节点在节点容器内独立跑了一份 `containerd`，跟宿主 Docker 不共享存储。bkn 应用镜像都在那里，不在 Docker Desktop 的镜像列表里——但仍然占 Docker Desktop 的磁盘配额（全栈 ~15–25 GB）。查看 / 预加载：
  ```bash
  docker exec bkn-dev-control-plane crictl images        # 列出 kind 节点内的镜像
  kind load docker-image <img:tag> --name bkn-dev        # 把宿主已有镜像推进 kind
  ```

- **`mac.sh isf install` 会自动把整套切到 HTTPS**：ISF（hydra/oauth2）的 issuer 必须是 https，所以 install 流程会自动：(1) 把 `mac-config.yaml` 的 `accessAddress` 改成 `https/443`，(2) 用 openssl 生 self-signed 证书并落到 Secret `bkn-ingress-tls`，(3) 对已装的 `bkn-foundry` release 做 `helm upgrade` 让它们读到新 https `accessAddress`，(4) 装 ISF 并给 ingress patch TLS。全新场景大约 10 min。浏览器会提示自签证书风险，确认一次即可。**不装 ISF 的话不用动**——默认全量安装（含 bkn-safe）也跑在 HTTP 上，只有 ISF 才强制切 https。

- **装完后的快速验证**（代理已 unset、Core pod 全 Ready 后）：
  ```bash
  curl --noproxy '*' http://localhost/                       # → 200，Sandbox Control Plane JSON
  curl --noproxy '*' http://localhost/api/bkn-backend/v1     # → 404（路径在，无对应处理函数）
  ```
  安装器历史输出里的 `curl http://<局域网 IP>/api/v1/health` 没有任何 ingress 路由匹配，请改用上面这些路径或文档化的 `/api/...` 业务路径。

### 故障排除

- **`cluster up` 报 Docker API / `docker.sock`：**多为 **CLI 已装但引擎未起**。请先启动 **Docker Desktop**，`docker info` 通过后重试。**`doctor --fix`** 不会拉起守护进程。
- **`bkn-core-data-migrator` / Job `BackoffLimitExceeded`：**确认数据层就绪（一般由 **`bkn-foundry install` 自动安装**；否则 **`data-services install`**）。确认 **`depServices.rds`** 指向集群内 MariaDB；必要时 `helm uninstall bkn-core-data-migrator -n <namespace>` 后再装 Core。

### Onboard 与 `openbkn`（全量安装）

**`bash ./dev/mac.sh onboard`** 调用 **`onboard.sh`**（`CONFIG_YAML_PATH` 多为 **`dev/conf/mac-config.yaml`**）。**全量（bkn-safe）** 安装会用 **`-u`/`-p`** 执行 **`openbkn auth login`**。种子 admin 首次登录强制改密；**`onboard.sh` 会自动清除** —— 在凭据登录前把密码经自助 `/api/safe/v1/auth/change-password` 端点 bounce 一遍,所以无人值守 onboard 不会卡住。**终端里脚本提示仍为英文。**

| 方式 | 命令要点 |
|------|----------|
| 凭据（默认） | `openbkn auth login https://<访问地址> -u admin -p '<密码>' -k` |
| 浏览器 / device | `openbkn auth login https://<访问地址> -k`，**不要**加 `-u`/`-p` |
| 首登改密 | `openbkn auth change-password https://<访问地址> -u admin -k`，**必须写 URL** |

**务必在命令中写出平台访问基址**；省略时 CLI 使用 **`openbkn auth list`** 里当前激活会话的地址，易连到其它环境，**并非** Helm 误读 `accessAddress`。

详见 [`help/zh/install.md`](../../help/zh/install.md)、[`help/en/install.md`](../../help/en/install.md)。
