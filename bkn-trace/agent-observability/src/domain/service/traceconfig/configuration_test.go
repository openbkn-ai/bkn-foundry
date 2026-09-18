package traceconfig

import (
	"context"
	"errors"
	"testing"
)

func TestServiceDefaultsDisabledAndQueuesOneOperation(t *testing.T) {
	service := NewConfigurationService()
	current := service.Current(context.Background())
	if current.DesiredEnabled || current.EffectiveEnabled || current.Revision != 0 {
		t.Fatalf("unexpected default configuration: %+v", current)
	}

	accepted, err := service.Request(context.Background(), ConfigurationRequest{Enabled: true, ExpectedRevision: 0, RequestedBy: "admin-a"})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if accepted.Revision != 1 || accepted.EffectiveEnabled || accepted.Operation == nil || accepted.Operation.Phase != PhasePending {
		t.Fatalf("unexpected accepted configuration: %+v", accepted)
	}
	if _, err := service.Request(context.Background(), ConfigurationRequest{Enabled: false, ExpectedRevision: 1, RequestedBy: "admin-a"}); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("expected active operation conflict, got %v", err)
	}
}

func TestServiceRejectsStaleRevisionAndIsIdempotentForSameTarget(t *testing.T) {
	service := NewConfigurationService()
	accepted, err := service.Request(context.Background(), ConfigurationRequest{Enabled: true, ExpectedRevision: 0, RequestedBy: "admin-a"})
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	repeated, err := service.Request(context.Background(), ConfigurationRequest{Enabled: true, ExpectedRevision: 0, RequestedBy: "admin-a"})
	if err != nil || repeated.Operation == nil || repeated.Operation.ID != accepted.Operation.ID {
		t.Fatalf("same target must reuse active operation: result=%+v err=%v", repeated, err)
	}
	if err := service.MarkSucceeded(context.Background(), accepted.Operation.ID); err != nil {
		t.Fatalf("mark succeeded: %v", err)
	}
	if _, err := service.Request(context.Background(), ConfigurationRequest{Enabled: true, ExpectedRevision: 9, RequestedBy: "admin-a"}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}
}

func TestServiceUsesSharedStoreAcrossAPIAndControllerInstances(t *testing.T) {
	store := NewMemoryConfigurationStore()
	api := NewConfigurationServiceWithStore(store)
	accepted, err := api.Request(context.Background(), ConfigurationRequest{Enabled: true, ExpectedRevision: 0, RequestedBy: "admin-a"})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	controller := NewConfigurationServiceWithStore(store)
	if err := controller.MarkSucceeded(context.Background(), accepted.Operation.ID); err != nil {
		t.Fatalf("controller completion: %v", err)
	}
	current := api.Current(context.Background())
	if !current.EffectiveEnabled || current.LastStableRevision != 1 || current.Operation != nil {
		t.Fatalf("controller result was not persisted for API reader: %+v", current)
	}
}
