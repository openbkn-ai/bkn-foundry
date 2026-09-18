package traceconfig

import (
	"context"
	"errors"
	"testing"
)

type fakeRollout struct {
	result Result
	calls  []Request
}

func (rollout *fakeRollout) ManagedServices() []Service {
	return []Service{{Name: "bkn-backend", Critical: true}, {Name: "agent-retrieval", Critical: true}}
}

func (rollout *fakeRollout) Run(_ context.Context, request Request) Result {
	rollout.calls = append(rollout.calls, request)
	return rollout.result
}

func TestControllerCompletesPendingReleaseAndPersistsEffectiveState(t *testing.T) {
	store := NewMemoryConfigurationStore()
	api := NewConfigurationServiceWithStore(store)
	if _, err := api.Request(context.Background(), ConfigurationRequest{Enabled: true, ExpectedRevision: 0, RequestedBy: "admin-a"}); err != nil {
		t.Fatalf("request: %v", err)
	}
	rollout := &fakeRollout{result: Result{Phase: PhaseSucceeded}}
	controller := NewController(store, rollout)

	processed, err := controller.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("process: processed=%v err=%v", processed, err)
	}
	current := api.Current(context.Background())
	if !current.EffectiveEnabled || current.LastStableRevision != 1 || current.Operation == nil || current.Operation.Phase != PhaseEnabled {
		t.Fatalf("unexpected completed state: %+v", current)
	}
	if len(current.Services) != 2 || current.Services[0].Phase != "ready" || current.Services[0].AppliedRevision != 1 {
		t.Fatalf("service progress was not persisted: %+v", current.Services)
	}
	if len(rollout.calls) != 1 || rollout.calls[0].Revision != 1 || !rollout.calls[0].Enabled {
		t.Fatalf("unexpected rollout request: %+v", rollout.calls)
	}
	if len(current.History) != 1 || current.History[0].ID != current.Operation.ID || current.History[0].CompletedAt == nil {
		t.Fatalf("completed operation was not appended to the audit ledger: %+v", current.History)
	}
	if disabled, err := api.Request(context.Background(), ConfigurationRequest{Enabled: false, ExpectedRevision: 1, RequestedBy: "admin-a"}); err != nil || disabled.Revision != 2 || disabled.Operation.Phase != PhasePending {
		t.Fatalf("terminal operation must allow next release: result=%+v err=%v", disabled, err)
	}
}

func TestControllerAuditLedgerPreservesConsecutiveOperations(t *testing.T) {
	store := NewMemoryConfigurationStore()
	api := NewConfigurationServiceWithStore(store)
	controller := NewController(store, &fakeRollout{result: Result{Phase: PhaseSucceeded}})

	first, err := api.Request(context.Background(), ConfigurationRequest{Enabled: true, ExpectedRevision: 0, RequestedBy: "admin-a"})
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	if _, err = controller.ProcessOne(context.Background()); err != nil {
		t.Fatalf("first rollout: %v", err)
	}
	second, err := api.Request(context.Background(), ConfigurationRequest{Enabled: false, ExpectedRevision: 1, RequestedBy: "admin-b"})
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	if _, err = controller.ProcessOne(context.Background()); err != nil {
		t.Fatalf("second rollout: %v", err)
	}

	current := api.Current(context.Background())
	if len(current.History) != 2 {
		t.Fatalf("want two immutable audit records, got %+v", current.History)
	}
	if current.History[0].ID != first.Operation.ID || current.History[0].RequestedBy != "admin-a" || current.History[0].Phase != PhaseEnabled {
		t.Fatalf("unexpected first audit record: %+v", current.History[0])
	}
	if current.History[1].ID != second.Operation.ID || current.History[1].RequestedBy != "admin-b" || current.History[1].Phase != PhaseDisabled {
		t.Fatalf("unexpected second audit record: %+v", current.History[1])
	}
}

func TestControllerPersistsRollbackFailureWithoutChangingEffectiveState(t *testing.T) {
	store := NewMemoryConfigurationStore()
	api := NewConfigurationServiceWithStore(store)
	accepted, _ := api.Request(context.Background(), ConfigurationRequest{Enabled: true, ExpectedRevision: 0, RequestedBy: "admin-a"})
	rollout := &fakeRollout{result: Result{Phase: PhaseRollbackFailed, Err: errors.New("release failed")}}
	controller := NewController(store, rollout)

	processed, err := controller.ProcessOne(context.Background())
	if !processed || err == nil {
		t.Fatalf("expected persisted failure, processed=%v err=%v", processed, err)
	}
	current := api.Current(context.Background())
	if current.EffectiveEnabled || current.Operation == nil || current.Operation.ID != accepted.Operation.ID || current.Operation.Phase != PhaseRollbackFailedState {
		t.Fatalf("unexpected failure state: %+v", current)
	}
}

func TestControllerRecoversInterruptedRolloutForReprocessing(t *testing.T) {
	store := NewMemoryConfigurationStore()
	api := NewConfigurationServiceWithStore(store)
	accepted, _ := api.Request(context.Background(), ConfigurationRequest{Enabled: true, ExpectedRevision: 0, RequestedBy: "admin-a"})
	_, _ = store.Update(context.Background(), func(configuration *Configuration) error {
		configuration.Operation.Phase = PhaseRollingOut
		configuration.Services = []ServiceStatus{{Name: "bkn-backend", DesiredRevision: 1, Phase: "releasing", SnapshotRevision: 4}}
		return nil
	})
	controller := NewController(store, &fakeRollout{result: Result{Phase: PhaseSucceeded}})
	if err := controller.RecoverInterrupted(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if current := api.Current(context.Background()); current.Operation == nil || current.Operation.ID != accepted.Operation.ID || current.Operation.Phase != PhasePending {
		t.Fatalf("interrupted operation was not requeued: %+v", current)
	}
	processed, err := controller.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("reprocess: processed=%v err=%v", processed, err)
	}
	rollout := controller.rollout.(*fakeRollout)
	if snapshot := rollout.calls[0].Snapshots["bkn-backend"]; snapshot.Revision != 4 {
		t.Fatalf("original pre-upgrade snapshot was not reused: %+v", rollout.calls[0].Snapshots)
	}
}

func TestControllerAllowsFailedDesiredStateToConvergeBackToEffectiveState(t *testing.T) {
	store := NewMemoryConfigurationStore()
	api := NewConfigurationServiceWithStore(store)
	_, _ = api.Request(context.Background(), ConfigurationRequest{Enabled: true, ExpectedRevision: 0, RequestedBy: "admin-a"})
	controller := NewController(store, &fakeRollout{result: Result{Phase: PhaseRolledBack, Err: errors.New("release failed")}})
	_, _ = controller.ProcessOne(context.Background())

	disabled, err := api.Request(context.Background(), ConfigurationRequest{Enabled: false, ExpectedRevision: 1, RequestedBy: "admin-a"})
	if err != nil || disabled.Revision != 2 || disabled.DesiredEnabled || disabled.Operation == nil || disabled.Operation.Phase != PhasePending {
		t.Fatalf("failed desired state must accept convergence request: result=%+v err=%v", disabled, err)
	}
}

func TestControllerRequeuesReleaseWhenShutdownCancelsHelm(t *testing.T) {
	store := NewMemoryConfigurationStore()
	api := NewConfigurationServiceWithStore(store)
	accepted, _ := api.Request(context.Background(), ConfigurationRequest{Enabled: true, ExpectedRevision: 0, RequestedBy: "admin-a"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	controller := NewController(store, &fakeRollout{result: Result{Phase: PhaseRollbackFailed, Err: context.Canceled}})

	processed, err := controller.ProcessOne(ctx)
	if !processed || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled operation, processed=%v err=%v", processed, err)
	}
	current := api.Current(context.Background())
	if current.Operation == nil || current.Operation.ID != accepted.Operation.ID || current.Operation.Phase != PhasePending || current.Operation.CompletedAt != nil {
		t.Fatalf("canceled release must be requeued for the replacement controller: %+v", current)
	}
}
