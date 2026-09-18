package traceconfig

import (
	"context"
	"errors"
	"time"
)

type Rollout interface {
	Run(context.Context, Request) Result
}

type managedServiceSource interface {
	ManagedServices() []Service
}

type Controller struct {
	store   ConfigurationStore
	rollout Rollout
}

func NewController(store ConfigurationStore, rollout Rollout) *Controller {
	return &Controller{store: store, rollout: rollout}
}

// RecoverInterrupted is safe because the controller Deployment uses Recreate
// and therefore has a single writer. Helm upgrade/rollback operations are
// idempotently re-evaluated from the persisted release revisions.
func (controller *Controller) RecoverInterrupted(ctx context.Context) error {
	_, err := controller.store.Update(ctx, func(configuration *Configuration) error {
		if configuration.Operation != nil && (configuration.Operation.Phase == PhaseRollingOut || configuration.Operation.Phase == PhaseRollingBack) {
			configuration.Operation.Phase = PhasePending
			for index := range configuration.Services {
				configuration.Services[index].Phase = "pending"
			}
		}
		return nil
	})
	return err
}

func (controller *Controller) ProcessOne(ctx context.Context) (bool, error) {
	current, err := controller.store.Current(ctx)
	if err != nil {
		return false, err
	}
	if current.Operation == nil || current.Operation.Phase != PhasePending {
		return false, nil
	}
	operationID := current.Operation.ID
	claimed, err := controller.store.Update(ctx, func(configuration *Configuration) error {
		if configuration.Operation == nil || configuration.Operation.ID != operationID || configuration.Operation.Phase != PhasePending {
			return ErrOperationInProgress
		}
		configuration.Operation.Phase = PhaseRollingOut
		if source, ok := controller.rollout.(managedServiceSource); ok {
			if !serviceProgressMatchesRevision(configuration.Services, configuration.Revision) {
				configuration.Services = make([]ServiceStatus, 0, len(source.ManagedServices()))
				for _, service := range source.ManagedServices() {
					if service.Critical {
						configuration.Services = append(configuration.Services, ServiceStatus{Name: service.Name, DesiredRevision: configuration.Revision, Phase: "releasing"})
					}
				}
			} else {
				for index := range configuration.Services {
					configuration.Services[index].Phase = "releasing"
				}
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrOperationInProgress) {
			return false, nil
		}
		return false, err
	}
	result := controller.rollout.Run(ctx, Request{
		Revision: int(claimed.Revision), Enabled: claimed.DesiredEnabled,
		Snapshots: snapshotsFromStatuses(claimed.Services),
		RecordSnapshot: func(snapshotContext context.Context, snapshot Snapshot) error {
			_, err := controller.store.Update(snapshotContext, func(configuration *Configuration) error {
				if configuration.Operation == nil || configuration.Operation.ID != operationID {
					return ErrOperationInProgress
				}
				for index := range configuration.Services {
					if configuration.Services[index].Name == snapshot.Service {
						configuration.Services[index].SnapshotRevision = snapshot.Revision
						return nil
					}
				}
				return ErrOperationInProgress
			})
			return err
		},
	})
	persistContext, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelPersist()
	_, persistErr := controller.store.Update(persistContext, func(configuration *Configuration) error {
		if configuration.Operation == nil || configuration.Operation.ID != operationID {
			return ErrOperationInProgress
		}
		if ctx.Err() != nil {
			configuration.Operation.Phase = PhasePending
			configuration.Operation.Error = ""
			configuration.Operation.CompletedAt = nil
			for index := range configuration.Services {
				configuration.Services[index].Phase = "pending"
			}
			return nil
		}
		switch result.Phase {
		case PhaseSucceeded:
			configuration.EffectiveEnabled = configuration.DesiredEnabled
			configuration.LastStableRevision = configuration.Revision
			if configuration.EffectiveEnabled {
				configuration.Operation.Phase = PhaseEnabled
			} else {
				configuration.Operation.Phase = PhaseDisabled
			}
			for index := range configuration.Services {
				configuration.Services[index].AppliedRevision = configuration.Revision
				configuration.Services[index].Phase = "ready"
				configuration.Services[index].ReadyReplicas = 1
				configuration.Services[index].RequiredReplicas = 1
			}
		case PhaseRolledBack:
			configuration.Operation.Phase = PhaseFailed
			for index := range configuration.Services {
				configuration.Services[index].AppliedRevision = configuration.LastStableRevision
				configuration.Services[index].Phase = "rolled_back"
			}
		case PhaseRollbackFailed:
			configuration.Operation.Phase = PhaseRollbackFailedState
			for index := range configuration.Services {
				configuration.Services[index].Phase = "failed"
			}
		}
		completedAt := time.Now().UTC()
		configuration.Operation.CompletedAt = &completedAt
		if result.Err != nil {
			configuration.Operation.Error = "managed release failed; inspect release-controller logs with operation " + operationID
		}
		if !operationRecorded(configuration.History, operationID) {
			configuration.History = append(configuration.History, *configuration.Operation)
		}
		return nil
	})
	if persistErr != nil {
		return true, persistErr
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	return true, result.Err
}

func serviceProgressMatchesRevision(statuses []ServiceStatus, revision uint64) bool {
	if len(statuses) == 0 {
		return false
	}
	for _, status := range statuses {
		if status.DesiredRevision != revision {
			return false
		}
	}
	return true
}

func snapshotsFromStatuses(statuses []ServiceStatus) map[string]Snapshot {
	snapshots := make(map[string]Snapshot)
	for _, status := range statuses {
		if status.SnapshotRevision > 0 {
			snapshots[status.Name] = Snapshot{Service: status.Name, Revision: status.SnapshotRevision}
		}
	}
	return snapshots
}

func operationRecorded(history []Operation, operationID string) bool {
	for _, operation := range history {
		if operation.ID == operationID {
			return true
		}
	}
	return false
}
