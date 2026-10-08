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

The module uses relative replacements for the checked-out comm-go and
agent-observability modules, so it validates changes to both sides together.
It checks the handler/client contract; network routing and deployment isolation
are covered separately by the Helm tests.
