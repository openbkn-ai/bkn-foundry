# Internal Trace control contract integration test

Runs the actual Go evidence publisher clients against the actual
agent-observability HTTP handlers and unsigned policy snapshot builder.
The test verifies policy and configuration reads, publisher heartbeat registration,
and publisher acknowledgement, without OAuth credentials or signing keys.

A custom HTTP transport dispatches through httptest.ResponseRecorder;
the persistence boundary uses an in-memory writer. No listening socket, database,
Kafka broker, or Kubernetes cluster is required.

Run from this directory:

    go test -v ./...

The module uses a relative replacement for the checked-out agent-observability
module and pins comm-go to a published version. For local comm-go changes, include
both this module and comm-go in the repository-root go.work (see
[comm-go local development](../../../comm-go/README.md#本地联调)). Use GOWORK=off
to validate the pinned comm-go version independently.
It checks the handler/client contract; network routing and deployment isolation
are covered separately by the Helm tests.
