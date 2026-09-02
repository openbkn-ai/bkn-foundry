# OpenBKN deployment operations

## Inputs to confirm

```text
Target host:
SSH user / authentication method:
Authorized scope: new install or upgrade
Existing deployment directory (if any):
Delivery source: customer-provided release package or approved repository URL
Kubernetes distribution: k8s or k3s
OpenBKN version or release manifest:
Access address:
API server address:
Image registry:
Data backup completed: yes/no/not applicable
```

Do not request a password in ordinary chat. Use the platform's secret input, an SSH agent, or a temporary credential. Never echo it or include it in a command line.

## Locate or obtain deployment files

The customer should provide the deployment directory when it exists. Verify it before use:

```bash
test -x <customer-directory>/deploy/deploy.sh
test -f <customer-directory>/AGENTS.md
```

If no directory is supplied, only inspect the current working directory and conventional locations such as `/opt/openbkn`, `/opt/bkn-foundry`, `/srv/bkn-foundry`, and `/root/bkn-foundry`. A candidate must contain `deploy/deploy.sh`. If discovery finds more than one candidate, report their paths and wait for the customer to choose; never select one based on name or modification time alone.

After selecting a candidate, declare it the canonical deployment root and use that exact path for the rest of the operation. Do not create a second version-suffixed root (for example, `/opt/bkn-foundry-0.1.4`) to work around a mismatch. Verify, in the same root, all of the following before any remote mutation:

```bash
test -x <root>/deploy/deploy.sh
test -f <root>/VERSION
test -f <root>/deploy/release-manifests/<version>/bkn-foundry.yaml
git -C <root> describe --tags --always --dirty  # when Git metadata exists
cat <root>/VERSION
```

The deployment script, `VERSION`, release manifest, and any local charts must be from the same approved package or Git tag. If they disagree, stop and report the exact paths and versions. Only change the canonical root after explicit approval; never silently create a parallel root.

If Git metadata is present, record the current commit and remote URL before an upgrade:

```bash
git -C <candidate-directory> rev-parse HEAD
git -C <candidate-directory> remote -v
```

First prefer a customer-provided, versioned release package. If the approved delivery method is Git, clone the approved repository and check out the requested tag or commit before running any deploy script:

```bash
git clone https://github.com/openbkn-ai/bkn-foundry.git
cd bkn-foundry
git checkout <approved-tag-or-commit>
cd deploy
```

For a production installation, report the resolved commit and the selected `release-manifests/<version>/bkn-foundry.yaml` before continuing. Do not run `git pull` blindly on a customer server; it can silently change the deployment version.

Prefer the repository's documented version entrypoint, such as `deploy.sh openbkn install --version=<version>`, when it resolves the verified manifest in the canonical root. If using `--version_file`, confirm it points inside that same root and record the reason. Never mix a script from one checkout/tag with a manifest or charts from another checkout/tag.

### Default/latest mode

When the customer explicitly says to use defaults, or supplies no `version` and no `version_file`, follow the repository's default behavior: obtain/use the official repository `main` branch and invoke `openbkn install` without `--version` or `--version_file`. The script then resolves the newest available release according to its own logic. Before mutation, state that the result is not pinned and may change as `main` or the registry changes; after installation, record the resolved product/chart versions. Keep the selected canonical root; never create a version-suffixed directory for this mode. If an existing checkout would need `git pull`, inspect the diff and get the required confirmation before changing it.

### Low-resource install override

After the read-only CPU and memory check, if either condition is true—total memory `< 8 GiB` or logical CPU cores `< 8`—add these flags to the `openbkn install` invocation:

```bash
--set resources.requests.cpu=0m \
--set resources.requests.memory=0Mi
```

Treat this as a lab-only scheduling override and report it in the confirmation and final result. It does not waive other preflight failures and does not change the host's recommended-capacity warning. If the customer explicitly provides resource settings, preserve those settings and report that the automatic override was not applied.

If Git is unavailable, do not install it automatically. Ask the customer to provide the approved release package or explicitly authorize installing Git. Extract a customer-provided package only into a customer-approved directory, then verify that it contains `deploy/deploy.sh` before continuing.

## Read-only checks

```bash
hostname -f
uname -a
nproc
free -h
df -h
kubectl config current-context
kubectl get nodes -o wide
kubectl get pods -A
helm list -A
```

On a fresh Linux host, run:

```bash
sudo bash ./preflight.sh --check-only
```

Use `--distro=k3s` consistently if the target uses k3s.

## Firewall policy

The default host-preparation policy is to disable the operating-system firewall after the customer confirms the host changes. Include this in the confirmation because it exposes services according to the surrounding network policy. Do not modify cloud security groups, hardware firewalls, or other external network controls unless the customer separately authorizes them.

Detect the active implementation first. After confirmation, use the matching command:

```bash
# firewalld
sudo systemctl stop firewalld
sudo systemctl disable firewalld

# UFW
sudo ufw disable
```

If neither implementation is detected, do not guess; report the finding and continue with the rest of the preflight results.

## Confirmation gate

Before installation or upgrade, present:

```text
将对 <host> 执行 OpenBKN <version> 的 <新装/升级>。
范围：Kubernetes/Ingress、MariaDB、Redis、Kafka、OpenSearch、OpenBKN 应用。
主机变更：<将停用 firewalld/UFW；不会修改云安全组或外部防火墙>。
数据影响：<无数据删除 / 包含数据库迁移 / 包含指定数据清理>。
回滚：<备份、旧版本 manifest、人工恢复方案>。
确认继续执行？
```

If the answer is not explicit, stop.

## Verification

```bash
sudo bash ./deploy.sh openbkn status
kubectl get nodes
kubectl get pods -A
helm list -n openbkn
kubectl get ingress -A
```

`onboard.sh` is not part of the default deployment workflow. Run it only after a separate customer request for model registration, business-user provisioning, or CLI login initialization.

Check the configured URL from the runtime config, not only the repository template. Do not print passwords in the final report.
