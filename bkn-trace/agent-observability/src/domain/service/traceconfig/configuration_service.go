package traceconfig

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	mu      sync.Mutex
	current Configuration
	nextID  uint64
}

func NewConfigurationService() *ConfigurationService { return &ConfigurationService{} }

func (service *ConfigurationService) Current(_ context.Context) Configuration {
	service.mu.Lock()
	defer service.mu.Unlock()
	return cloneConfiguration(service.current)
}

func (service *ConfigurationService) Request(_ context.Context, request ConfigurationRequest) (Configuration, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.current.Operation != nil {
		if service.current.DesiredEnabled == request.Enabled {
			return cloneConfiguration(service.current), nil
		}
		return Configuration{}, ErrOperationInProgress
	}
	if request.ExpectedRevision != service.current.Revision {
		return Configuration{}, ErrRevisionConflict
	}
	service.nextID++
	service.current.Revision++
	service.current.DesiredEnabled = request.Enabled
	service.current.Operation = &Operation{
		ID: fmt.Sprintf("tec-%d", service.nextID), Phase: PhasePending,
		RequestedAt: time.Now().UTC(), RequestedBy: request.RequestedBy,
	}
	return cloneConfiguration(service.current), nil
}

// MarkSucceeded is called only by the trusted release controller after every
// critical target has loaded the requested revision and passed readiness.
func (service *ConfigurationService) MarkSucceeded(_ context.Context, operationID string) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.current.Operation == nil || service.current.Operation.ID != operationID {
		return ErrOperationInProgress
	}
	service.current.EffectiveEnabled = service.current.DesiredEnabled
	service.current.LastStableRevision = service.current.Revision
	service.current.Operation = nil
	return nil
}

func cloneConfiguration(value Configuration) Configuration {
	clone := value
	clone.Services = append([]ServiceStatus(nil), value.Services...)
	if value.Operation != nil {
		operation := *value.Operation
		clone.Operation = &operation
	}
	return clone
}
