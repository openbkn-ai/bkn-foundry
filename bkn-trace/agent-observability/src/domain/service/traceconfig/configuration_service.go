package traceconfig

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrOperationInProgress = errors.New("trace evidence operation in progress")
	ErrRevisionConflict    = errors.New("trace evidence configuration revision conflict")
)

type ConfigurationRequest struct {
	Enabled          bool
	ExpectedRevision uint64
	RequestedBy      string
}

// ConfigurationService owns the public, optimistic-locking state machine. A
// release worker advances a queued operation; callers cannot specify targets.
type ConfigurationService struct {
	store ConfigurationStore
}

func NewConfigurationService() *ConfigurationService {
	return NewConfigurationServiceWithStore(NewMemoryConfigurationStore())
}

func NewConfigurationServiceWithStore(store ConfigurationStore) *ConfigurationService {
	return &ConfigurationService{store: store}
}

func (service *ConfigurationService) Current(ctx context.Context) Configuration {
	current, err := service.CurrentResult(ctx)
	if err != nil {
		return Configuration{}
	}
	return current
}

func (service *ConfigurationService) CurrentResult(ctx context.Context) (Configuration, error) {
	return service.store.Current(ctx)
}

func (service *ConfigurationService) Request(ctx context.Context, request ConfigurationRequest) (Configuration, error) {
	return service.store.Update(ctx, func(current *Configuration) error {
		if current.Operation != nil && operationActive(current.Operation.Phase) {
			if current.DesiredEnabled == request.Enabled {
				return nil
			}
			return ErrOperationInProgress
		}
		if current.Operation != nil && current.EffectiveEnabled == request.Enabled && current.DesiredEnabled == request.Enabled {
			return nil
		}
		if request.ExpectedRevision != current.Revision {
			return ErrRevisionConflict
		}
		current.Revision++
		current.DesiredEnabled = request.Enabled
		current.Operation = &Operation{
			ID: fmt.Sprintf("tec-%d", current.Revision), Phase: PhasePending,
			RequestedAt: time.Now().UTC(), RequestedBy: request.RequestedBy,
		}
		return nil
	})
}

func operationActive(phase ConfigurationPhase) bool {
	return phase == PhasePending || phase == PhaseRollingOut || phase == PhaseRollingBack
}

// MarkSucceeded is called only by the trusted release controller after every
// critical target has loaded the requested revision and passed readiness.
func (service *ConfigurationService) MarkSucceeded(ctx context.Context, operationID string) error {
	_, err := service.store.Update(ctx, func(current *Configuration) error {
		if current.Operation == nil || current.Operation.ID != operationID {
			return ErrOperationInProgress
		}
		current.EffectiveEnabled = current.DesiredEnabled
		current.LastStableRevision = current.Revision
		current.Operation = nil
		return nil
	})
	return err
}

func cloneConfiguration(value Configuration) Configuration {
	clone := value
	clone.Services = append([]ServiceStatus(nil), value.Services...)
	clone.History = append([]Operation(nil), value.History...)
	for index := range clone.History {
		if value.History[index].CompletedAt != nil {
			completedAt := *value.History[index].CompletedAt
			clone.History[index].CompletedAt = &completedAt
		}
	}
	if value.Operation != nil {
		operation := *value.Operation
		if value.Operation.CompletedAt != nil {
			completedAt := *value.Operation.CompletedAt
			operation.CompletedAt = &completedAt
		}
		clone.Operation = &operation
	}
	return clone
}
