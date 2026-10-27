# Trace rolling updates

The Deployment explicitly uses `RollingUpdate`, integer `maxUnavailable: 0`
and `maxSurge: 1`. `/health/ready` gates replacement availability. Reserve one
additional Pod's resources; a failed pull, failed startup/readiness or scheduling
constraint should keep the existing healthy replica while Kubernetes waits.

On Kubernetes 1.30+ the kubelet runs a native five-second `preStop.sleep` before
SIGTERM, allowing Service endpoint removal to propagate while the old process
still serves connections. The 40-second termination grace includes that delay
and the application's existing 30-second shutdown budget. The native hook needs
no shell in the distroless image. Kubernetes 1.23–1.29 remain render-compatible
without this hook; uninterrupted successful replacement is not certified there.
On 1.30–1.33 keep the default `PodLifecycleSleepAction` feature gate enabled.
See the [Kubernetes feature gate reference](https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/).
Existing Pods do not acquire a hook retroactively: the first replacement from
an older Chart still terminates the old Pod without this delay. Plan that
transition separately; the continuity acceptance uses an already updated baseline.

This strategy does not establish cross-version database compatibility. Before
CE/EE switching, verify both images' embedded migration versions/checksums and
shared worker behavior. Use fixed images and a dedicated test database. Helm
rollback restores release resources, not database migrations. In particular an
image recognizing only `038` cannot read a database already migrated to `039`;
do not remove ledger entries to make it start (EE #445).

`make helm-lint` renders single- and multi-replica configurations and checks
strategy plus probes. Runtime acceptance must separately cover failed pulls,
failed readiness/startup, insufficient resources, success and timeout rollback.
Use real CE/EE images for schema and switch validation; an HTTP fixture alone
does not validate Enterprise entitlement, Kafka consumers or business workers.

Vega is a separate exception: its startup recovery currently marks all running
tasks interrupted without checking live process ownership. Do not apply this
Trace rollout policy to Vega until that overlap behavior is resolved. See deploy
issue #69 for the evidence, maintenance alternative and outstanding review.
