# install-status 快照集群内自动刷新

- Issue: [#204](https://github.com/openbkn-ai/bkn-foundry/issues/204)
- 分支: `feature/204-install-status-auto-snapshot`
- 涉及: `deploy/conf/install-status/`、`deploy/scripts/services/status.sh`

## 1. 背景

install-status 页面数据分两层：Ready/健康由 pod 内 refresher 容器实时刷新；版本/组件清单/依赖端点是 `publish-status` 时落进 configmap 的静态快照，超 24h 页面弹横幅要求手动重跑。快照里唯一真正会"过期"的是**版本号**——而 refresher dump 的 `workloads.json` 里每个 Deployment 的镜像 tag 就是真实运行版本，数据已在手边，只差合并。

## 2. 方案

refresher 循环内新增一步。实现语言选 **jq**（1.7.1）——refresher 实际镜像
portainer/kubectl-shell **没有 python**，只有 jq/busybox（早期误判 python 可用，
是探到了已删除的 install-status-rt 容器；VM 实测修正）：

```
kubectl get pods      → /live/pods.json          （不变）
kubectl get deploy,sts → /live/workloads.json     （不变）
jq -f /scripts/merge.jq：
    /data/install-status.json   （期望清单，configmap 卷挂载，零 RBAC）
  + /live/workloads.json        （实际状态）
  → 版本列 = 镜像 tag（versionSource=live）、ready 同步、generatedAt=now
  → 原子写 /live/install-status.live.json（失败即弃 tmp，保留上一版）
```

nginx 的 `/install-status.json` 改 `try_files`：优先 live 版，refresher 挂掉自动退回静态快照——退回后 generatedAt 变旧、原横幅弹出，**横幅语义自动转为 refresher 失联告警**，逻辑零改动。

`_status_apply_endpoint` 同时：把 merge.py 加入 install-status-nginx configmap；清理历史遗留的 `install-status-rt` 全套资源（早期实时化迭代产物，与本体重复，`--ignore-not-found` 幂等删除）。

## 3. 边界与不做的事

- **零新增 RBAC**：期望清单走 configmap 卷挂载（pod 声明，不经 API）；实际版本用现有 deploy/sts 读权限。不读 helm release secrets（全 ns secrets 读权限爆炸半径不可接受，见 issue 评论）。
- merge.py 任何异常 exit 0,不影响既有 pods/workloads 两个文件；上一版 live 文件保留。
- 期望清单（该装哪些组件）仍只在 publish 时更新——它只随 deploy.sh 升级变化，而升级流程本来就会重新 publish，闭环。
- 安装主机 cron 方案不采用、不产品化；VM 上的临时 cron 在本方案部署后删除。

## 4. 验收

1. 不跑 publish-status，`/install-status.json` 的 generatedAt 每分钟自动前进，横幅不出现。
2. 版本列显示真实镜像 tag（含 `kubectl set image` 热修后的值），带 live 标。
3. 杀掉 refresher 容器 → 降级为静态快照，横幅恢复出现（失联告警语义）。
4. publish 后集群内不存在任何 install-status-rt 资源。
5. VM + 139.75 两台实测。
