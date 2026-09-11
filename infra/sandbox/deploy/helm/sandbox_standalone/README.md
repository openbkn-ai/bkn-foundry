# Sandbox Standalone Helm Chart

This chart deploys Sandbox as a self-contained application.

Use this chart for development, demos, integration testing, or environments where Sandbox should be deployed independently from BKN Foundry. It owns its supporting services and is not constrained by the Core component values format.

For BKN Foundry component packaging, use `deploy/helm/sandbox`.

## Components

- **Control Plane**: FastAPI management service for sessions, templates, scheduling, and storage access.
- **Web Console**: React application for visual session, template, and execution management.
- **MariaDB**: Internal database for Sandbox data.
- **MinIO**: S3-compatible workspace object storage.
- **Default template metadata**: Initializes `python-basic` and `multi-language` template records.

## Prerequisites

- Kubernetes 1.24+
- Helm 3.0+
- A CNI plugin that enforces Kubernetes `NetworkPolicy`
- PV provisioner support for MariaDB and MinIO persistence

## Installing

From the repository root:

```bash
helm install sandbox deploy/helm/sandbox_standalone \
  --namespace sandbox-system \
  --create-namespace
```

With an override file:

```bash
helm install sandbox deploy/helm/sandbox_standalone \
  --namespace sandbox-system \
  --create-namespace \
  -f custom-values.yaml
```

## Default Template Images

The Control Plane seeds two default template rows into `t_sandbox_template`:

- `python-basic`
- `multi-language`

The chart exposes both image tags independently:

```yaml
image:
  defaultTemplates:
    pythonBasic:
      repository: sandbox-template-python-basic
      tag: ""
    multiLanguage:
      repository: sandbox-template-multi-language
      tag: ""
```

An empty `tag` means the Control Plane reads `/app/VERSION` at startup and uses that value as the template image tag. Set the tag only when deploying branch or custom template images.

The deprecated `image.defaultTemplate` value is still accepted as a compatibility fallback for `python-basic`.

## Key Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `namespace` | Kubernetes namespace | `sandbox-system` |
| `imagePullPolicy` | Executor Pod image pull policy | `IfNotPresent` |
| `imagePullSecrets` | Executor Pod image pull secret names | `[]` |
| `image.registry` | Shared image registry prefix | `""` |
| `image.controlPlane.repository` | Control Plane image repository | `sandbox-control-plane` |
| `image.web.repository` | Web Console image repository | `sandbox-web` |
| `image.mariadb.repository` | MariaDB image repository | `mariadb` |
| `image.minio.repository` | MinIO image repository | `quay.io/minio/minio` |
| `image.defaultTemplates.pythonBasic.tag` | Python template image tag; empty uses `/app/VERSION` | `""` |
| `image.defaultTemplates.multiLanguage.tag` | Multi-language template image tag; empty uses `/app/VERSION` | `""` |
| `mariadb.enabled` | Deploy internal MariaDB | `true` |
| `web.enabled` | Deploy Sandbox Web Console | `true` |
| `minio.enabled` | Deploy internal MinIO | `true` |
| `networkPolicy.enabled` | Apply a default-deny egress policy to dynamic executor pods | `true` |
| `networkPolicy.bkn.enabled` | Allow an optional BKN deployment through agent-retrieval port 30780 | `false` |
| `networkPolicy.additionalEgress` | Extra Kubernetes egress rules for explicitly approved dependencies | `[]` |

## Executor Egress Isolation

The policy selects only dynamic executor pods. It allows DNS, the Sandbox
Control Plane, and MinIO, and denies every other egress destination. MariaDB is
used by the Control Plane, not executors, so it is intentionally not allowed.
Standalone installs do not include BKN, so BKN access is disabled by default.
When connecting one, set both BKN URL values to agent-retrieval's
authenticated-only port 30780 and enable/configure `networkPolicy.bkn`. Never
allow its main port 30779 because that port also serves trusted `/in` routes.

Before enabling the policy on an existing installation, run an observation
period with `networkPolicy.enabled=false`, collect executor traffic with the
CNI's flow tooling, and add only verified dependencies to
`networkPolicy.additionalEgress`. Runtime package downloads and arbitrary
Internet/private-network calls are denied unless explicitly allowed. Applying
the policy affects already running executor pods. NodeLocal DNSCache users must
also allow its listener address explicitly.

## Access

```bash
kubectl port-forward svc/sandbox-control-plane 8000:8000 -n sandbox-system
kubectl port-forward svc/sandbox-web 1101:80 -n sandbox-system
kubectl port-forward svc/minio 9001:9001 -n sandbox-system
```

## Rendering

```bash
helm template sandbox deploy/helm/sandbox_standalone
```

To verify manual template versions:

```bash
helm template sandbox deploy/helm/sandbox_standalone \
  --show-only templates/configmap.yaml \
  --set image.defaultTemplates.pythonBasic.tag=dev-python \
  --set image.defaultTemplates.multiLanguage.tag=dev-multi
```

## Related Chart

See `deploy/helm/README.md` for the comparison between `sandbox` and `sandbox_standalone`.
