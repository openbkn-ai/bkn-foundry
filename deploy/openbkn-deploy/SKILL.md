---
name: openbkn-deploy
description: Deploy or upgrade OpenBKN on a customer-authorized Linux server through the repository's deploy scripts, with preflight checks, explicit confirmation, secret handling, and post-deployment verification.
metadata:
  short-description: Safely deploy OpenBKN for an authorized customer
---

# OpenBKN deployment

Use this skill when a customer asks to install, upgrade, verify, or diagnose an OpenBKN deployment using this repository's `deploy/` directory.

## Scope and authorization

- Act only on a server the customer explicitly identifies and authorizes.
- Before any remote mutation, show the target host, SSH user, selected version, deployment mode, components, and expected impact; require explicit confirmation.
- Never assume that providing credentials authorizes an upgrade, data cleanup, reset, uninstall, or production change.
- Never put passwords, private keys, tokens, or generated credentials in this skill, Git, command arguments, shell history, or normal logs. Prefer an SSH key or a temporary credential supplied through a secure secret mechanism.
- Never run `k8s reset`, delete PVCs, uninstall data services, run trace cleanup, or perform a database/schema migration without a separate confirmation that states what changes, blast radius, and rollback/backup plan.

## Standard workflow

1. Collect only the required deployment inputs: target host, SSH method, sudo/root access, access address, Kubernetes distribution (`k8s` or `k3s`), requested version, registry, whether this is a new install or upgrade, the delivery source (release package or repository URL), and the deployment directory if it already exists.
2. Inspect the target without changing it: OS, CPU, memory, disk, hostname, time sync, open ports, container runtime, Kubernetes context, `kubectl`, and `helm` availability.
3. Locate and verify the deployment directory. Prefer the directory supplied by the customer. If absent, do a limited read-only search for a directory containing `deploy/deploy.sh`; if there are zero or multiple candidates, stop and ask the customer to identify the intended directory. Verify the selected directory's source and version before using it.
   - Establish one canonical deployment root for the entire operation. Once selected or created, reuse it for every preflight, manifest, download, and deploy command.
   - Never create a parallel directory such as `/opt/bkn-foundry-<version>` to resolve a version mismatch. If the selected root's source, commit/tag, `VERSION`, or release manifest does not match the requested release, stop and report the mismatch; replace it only with explicit customer approval or use an explicitly supplied release package in the same approved root.
   - The deployment script, `VERSION`, release manifest, and charts must come from the same verified source/tag. Record the canonical root, source URL, commit/tag, script version, and manifest path before mutation.
4. If no verified deployment directory exists, obtain the customer-approved release package or clone the approved repository at the requested tag/commit. If Git is unavailable, use the release package; do not install Git or download an unapproved package without confirmation. Work from the resulting `deploy/` directory.
5. Run the repository preflight in check-only mode first. When the customer confirms host preparation, the default deployment policy is to disable the host firewall; state this explicitly in the confirmation, identify the detected firewall implementation, and report the change after it is made. Do not change cloud security groups or external network firewalls without separate authorization.
6. For a new install, use the pinned release manifest when one is requested or available. Prefer the repository's documented version entrypoint (for example, `--version=<version>`) when it resolves the same verified manifest; use `--version_file` only when needed and record why. Do not use `--latest` in production unless explicitly authorized. Do not mix a script from one source/tag with a manifest or charts from another.
   - If the target has less than 8 GiB total memory or fewer than 8 logical CPU cores, append both `--set resources.requests.cpu=0m` and `--set resources.requests.memory=0Mi` to the `openbkn install` command, unless the customer explicitly supplied different resource overrides. Report this low-resource override as a lab-only warning; it does not make the host meet the recommended production capacity.
   - Default/latest mode is distinct: when the customer explicitly chooses the default or omits both `version` and `version_file`, the repository's documented behavior is to use the official `main` branch and let the script resolve/install the newest available release. Omit both version flags, state that this is a moving-target latest install, and record the resolved version after installation. This is allowed only with explicit customer authorization; never silently substitute it for a requested pinned release.
7. For an upgrade, verify backups, current Helm releases, PVCs, runtime configuration, current version, and release notes. Read the applicable upgrade instructions before running any migration or cleanup script.
8. Execute the existing deployment scripts rather than reimplementing Helm commands. Do not run `onboard.sh` as part of the default installation; leave post-install business initialization to the customer as a separate, explicitly requested operation:

   ```bash
   sudo bash ./preflight.sh --check-only
   sudo bash ./deploy.sh openbkn install \
     --registry=swr \
     --access_address=https://HOST \
     --api_server_address=API_IP
   sudo bash ./deploy.sh openbkn status
   ```

9. Verify Kubernetes nodes, Pods, Helm releases, Ingress, service health, access URL, and the recorded runtime configuration. Report failures with the exact component and next safe diagnostic command.

## Upgrade rules

- Treat `git pull` as a code/config change: inspect the diff and identify the target version before installing.
- Use `--version_file=./release-manifests/<version>/bkn-foundry.yaml` for reproducible production upgrades.
- Do not run legacy Trace cleanup merely because the document mentions it; confirm that the target upgrade requires it, take a backup, quiesce writes, and obtain explicit confirmation.
- Do not assume changing a ConfigMap or `OPENSEARCH_INITIAL_ADMIN_PASSWORD` changes an already-initialized OpenSearch password on an existing PVC. Distinguish application connection settings from the OpenSearch internal admin credential.
- Do not run `onboard.sh` unless the customer separately requests post-install initialization such as model registration, business-user provisioning, or CLI login setup.
- Use `--force-upgrade` only when a deliberate configuration or access-address change requires it, and explain the expected rollout.

## Failure handling

- Stop on authentication, target mismatch, backup failure, ambiguous version, or unexpected destructive prompt.
- Diagnose with read-only commands first (`kubectl get`, `kubectl describe`, `kubectl logs`, `helm list`, `helm status`).
- Do not “fix” failures by deleting Pods, PVCs, namespaces, or the cluster unless the customer separately confirms the exact action.
- At completion, provide the deployed version, access URL, admin credential delivery location (never print the secret), health summary, commands run, and remaining warnings.

For detailed command selection and confirmation wording, read [references/operations.md](references/operations.md).
