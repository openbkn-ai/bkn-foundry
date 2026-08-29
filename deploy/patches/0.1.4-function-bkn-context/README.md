# OpenBKN 0.1.4 Function / BKN Context Compatibility Patch

This Helm patch enables a third-party Agent to discover and call published
Function Toolbox tools through Context Loader MCP. A Function receives only its
business parameters and reads permitted Knowledge Network data in the sandbox
using the managed caller identity. It is intended for the supply ontology sample
and is generic: it does not install the sample, data, toolbox, Skills, or any
Knowledge Network.

## Compatibility and scope

- Base product: OpenBKN `0.1.4` installed by Helm/Kubernetes.
- Changed Deployments: `agent-retrieval` and `agent-operator-integration` only.
- No data migration, no database DDL, and no BKN import/export occurs.
- The Function Runtime and Sandbox Context Loader address injection already ship
  in the `0.1.4` base release; do not replace Sandbox images for this patch.
- The patch image tag must be supplied from the signed release record. Never use
  a local development tag in a customer cluster.

## Before changing the cluster

1. Record the two currently running image references, especially if your
   installation uses a private registry:

   ```bash
   kubectl -n openbkn get deployment agent-retrieval agent-operator-integration \
     -o jsonpath='{range .items[*]}{.metadata.name}{"="}{.spec.template.spec.containers[0].image}{"\n"}{end}'
   ```

2. Obtain the published patch image registry, tag, and digest from the release
   record. Both service images must use the same patch tag.
3. Ensure the existing Helm releases are named `agent-retrieval` and
   `agent-operator-integration` in the target namespace.

## Preview and install

```bash
cd deploy/patches/0.1.4-function-bkn-context

./apply.sh --dry-run \
  --registry <patch-image-registry> \
  --tag <published-patch-tag>

./apply.sh --yes \
  --namespace openbkn \
  --registry <patch-image-registry> \
  --tag <published-patch-tag>
```

For air-gapped installations, first mirror the two published images to the
customer registry, then pass that registry to `--registry`. The Helm charts stay
on the normal `0.1.4-release` version; only their image values are replaced.

## Verify

```bash
./verify.sh --namespace openbkn
```

Kubernetes readiness is necessary but not sufficient. With an account that can
query the imported supply Knowledge Network, an Agent must then use Context
Loader MCP to:

1. start a managed Interaction;
2. call `list_published_toolboxes` and `list_published_tools` to discover the
   published supply toolbox and the actual tool IDs;
3. call `execute_published_tool` for **标准交期** with
   `{"material_code":"606-000989"}` and the managed `bkn_context`;
4. verify `exit_code=0` and `leadtime_days=14`, then finish the Interaction.

This validates the full MCP → Toolbox → Function sandbox → Knowledge Network
path without a token, service address, snapshot, or `resolved_context` in the
Function arguments.

## Roll back

Use the exact original registry and tag recorded before installation:

```bash
./rollback.sh --yes \
  --namespace openbkn \
  --registry <original-image-registry> \
  --tag <original-image-tag>
```

The standard release uses `0.1.4-release`, but a private or previously patched
installation may use another tag. Rollback changes only the same two Deployments
and does not remove imported sample data or persisted business data.

## Release record

The release owner must publish the following immutable details alongside this
directory before customers install it:

| Item | Required value |
| --- | --- |
| Patch version | Semantic patch version, e.g. `0.1.4-supply-sample-p1` |
| Source commit | Commit on `patch/0.1.4-function-bkn-context` |
| `agent-retrieval` digest | `sha256:…` for amd64 and arm64 manifest list |
| `agent-operator-integration` digest | `sha256:…` for amd64 and arm64 manifest list |
| Verification evidence | Unit tests and MCP smoke result |

### Publishing procedure

Review this patch as a PR against `release/0.1.4`, but do **not** merge it in a
way that overwrites the existing `0.1.4-release` image tag. After approval,
create the immutable tag `v0.1.4-supply-sample-p1` on the approved patch commit.
The repository's existing release workflows build multi-architecture images from
that tag. Copy `release-record.template.yaml` to the delivery record and replace
every placeholder with the published digest and fresh MCP smoke evidence.
