package helmrelease

import (
	"context"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceconfig"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/traceconfigmanifest"
)

type recordedCommand struct {
	name string
	args []string
}
type fakeCommands struct {
	output []byte
	calls  []recordedCommand
}

func (fake *fakeCommands) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	fake.calls = append(fake.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	return fake.output, nil
}

func TestClientBuildsHelmCommandOnlyFromManagedManifest(t *testing.T) {
	commands := &fakeCommands{}
	manifest := traceconfigmanifest.Manifest{Version: 1, Services: []traceconfigmanifest.Service{{
		Name: "bkn-backend", Release: "bkn-backend", Chart: "/charts/bkn-backend", Critical: true,
		TraceValue: "config.otel.trace.enabled", EvidenceValues: []string{"bknTrace.producerOutbox.enabled", "bknTrace.producerOutbox.workerEnabled"},
	}}}
	client, err := New(commands, "openbkn", manifest)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if err := client.Apply(context.Background(), traceconfig.Service{Name: "bkn-backend", Critical: true}, 7, true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	joined := strings.Join(commands.calls[0].args, " ")
	for _, expected := range []string{"upgrade --install bkn-backend /charts/bkn-backend", "--namespace openbkn", "config.otel.trace.enabled=true", "bknTrace.producerOutbox.enabled=true", "bknTrace.producerOutbox.workerEnabled=true", "traceEvidence.revision=7", "--wait"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %q in %s", expected, joined)
		}
	}
}

func TestClientRejectsServiceOutsideManagedManifest(t *testing.T) {
	manifest := traceconfigmanifest.Manifest{Version: 1, Services: []traceconfigmanifest.Service{{
		Name: "bkn-backend", Release: "bkn-backend", Chart: "/charts/bkn-backend", Critical: true, TraceValue: "config.otel.trace.enabled",
	}}}
	client, err := New(&fakeCommands{}, "openbkn", manifest)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if err := client.Apply(context.Background(), traceconfig.Service{Name: "attacker-release", Critical: true}, 1, true); err == nil {
		t.Fatal("unmanaged service must be rejected")
	}
}

func TestClientSnapshotsAndRollsBackExactRevision(t *testing.T) {
	commands := &fakeCommands{output: []byte(`{"version":12}`)}
	manifest := traceconfigmanifest.Manifest{Version: 1, Services: []traceconfigmanifest.Service{{Name: "bkn-backend", Release: "bkn-backend", Chart: "/charts/bkn-backend", Critical: true, TraceValue: "config.otel.trace.enabled"}}}
	client, _ := New(commands, "openbkn", manifest)
	snapshot, err := client.Snapshot(context.Background(), traceconfig.Service{Name: "bkn-backend", Critical: true})
	if err != nil || snapshot.Revision != 12 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if err := client.Rollback(context.Background(), snapshot); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := strings.Join(commands.calls[1].args, " "); !strings.Contains(got, "rollback bkn-backend 12 --namespace openbkn --wait") {
		t.Fatalf("unexpected rollback: %s", got)
	}
}

func TestClientRequiresHelmDeployedStatusForReadiness(t *testing.T) {
	commands := &fakeCommands{output: []byte(`{"info":{"status":"failed"}}`)}
	manifest := traceconfigmanifest.Manifest{Version: 1, Services: []traceconfigmanifest.Service{{Name: "bkn-backend", Release: "bkn-backend", Chart: "/charts/bkn-backend", Critical: true, TraceValue: "config.otel.trace.enabled"}}}
	client, _ := New(commands, "openbkn", manifest)
	if err := client.WaitReady(context.Background(), traceconfig.Service{Name: "bkn-backend", Critical: true}, 1, true); err == nil {
		t.Fatal("failed Helm status must not be ready")
	}
}
