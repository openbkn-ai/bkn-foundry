package traceconfigstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceconfig"
)

func TestStorePersistsOperationAcrossServiceInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace-evidence-state.json")
	api := traceconfig.NewConfigurationServiceWithStore(New(path))
	accepted, err := api.Request(context.Background(), traceconfig.ConfigurationRequest{
		Enabled: true, ExpectedRevision: 0, RequestedBy: "admin-a",
	})
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	controller := traceconfig.NewConfigurationServiceWithStore(New(path))
	if err := controller.MarkSucceeded(context.Background(), accepted.Operation.ID); err != nil {
		t.Fatalf("mark succeeded: %v", err)
	}

	restartedAPI := traceconfig.NewConfigurationServiceWithStore(New(path))
	current := restartedAPI.Current(context.Background())
	if !current.DesiredEnabled || !current.EffectiveEnabled || current.Revision != 1 || current.LastStableRevision != 1 || current.Operation != nil {
		t.Fatalf("state did not survive restart: %+v", current)
	}
}

func TestStoreDefaultsDisabledWhenFileDoesNotExist(t *testing.T) {
	current, err := New(filepath.Join(t.TempDir(), "missing.json")).Current(context.Background())
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if current.DesiredEnabled || current.EffectiveEnabled || current.Revision != 0 {
		t.Fatalf("unexpected default: %+v", current)
	}
}
