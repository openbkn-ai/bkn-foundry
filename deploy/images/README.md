# deploy/images

OCI image tarballs pre-loaded into k3s containerd before k3s starts.

k3s auto-imports any `*.tar` / `*.tar.gz` / `*.tar.zst` placed under
`/var/lib/rancher/k3s/agent/images/` at agent startup, so seeding the
host with these tarballs avoids the docker.io round-trip that breaks on
hosts where:

- DNS only returns `AAAA` records for `docker.io` / `cloudfront`
- IPv6 default route exists but packets can't reach the registry
- The kubelet/containerd Go resolver picks IPv6 first and stalls

`deploy/scripts/services/k3s.sh::_seed_k3s_system_images()` copies the
matching tarballs into `/var/lib/rancher/k3s/agent/images/` before
`curl ... | sh` runs the rancher k3s installer.

## Files

| File | Source | Why |
|------|--------|-----|
| `busybox-1.36.1.tar` | `docker.io/rancher/mirrored-library-busybox:1.36.1` | local-path-provisioner helper-pod; first-boot PVC provisioning hangs without it on broken-DNS hosts |

## Refresh

The tarballs are multi-arch (`linux/amd64` + `linux/arm64`). Re-export
from any k3s host that has the image cached:

```bash
sudo k3s ctr -n k8s.io images pull --platform linux/amd64 \
  docker.io/rancher/mirrored-library-busybox:1.36.1
sudo k3s ctr -n k8s.io images pull --platform linux/arm64 \
  docker.io/rancher/mirrored-library-busybox:1.36.1
sudo k3s ctr -n k8s.io images export \
  --platform linux/amd64 --platform linux/arm64 \
  busybox-1.36.1.tar \
  docker.io/rancher/mirrored-library-busybox:1.36.1
```

The export format is OCI image index (manifest list), which k3s loads
correctly per node architecture on startup.
