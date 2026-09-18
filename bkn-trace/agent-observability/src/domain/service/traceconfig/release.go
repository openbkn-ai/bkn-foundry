package traceconfig

import (
	"context"
	"fmt"
)

type Phase string

const (
	PhaseSucceeded      Phase = "succeeded"
	PhaseRolledBack     Phase = "rolled_back"
	PhaseRollbackFailed Phase = "rollback_failed"
)

type Service struct {
	Name     string
	Critical bool
}

type Snapshot struct {
	Service  string
	Revision int
}

type Request struct {
	Revision int
	Enabled  bool
}

type Result struct {
	Phase Phase
	Err   error
}

type ReleaseClient interface {
	Snapshot(context.Context, Service) (Snapshot, error)
	Apply(context.Context, Service, int, bool) error
	WaitReady(context.Context, Service, int, bool) error
	Rollback(context.Context, Snapshot) error
}

type ReleaseRunner struct {
	client   ReleaseClient
	services []Service
}

func NewReleaseRunner(client ReleaseClient, services []Service) *ReleaseRunner {
	return &ReleaseRunner{client: client, services: append([]Service(nil), services...)}
}

func (runner *ReleaseRunner) Run(ctx context.Context, request Request) Result {
	changed := make([]Snapshot, 0, len(runner.services))
	for _, service := range runner.services {
		if !service.Critical {
			continue
		}
		snapshot, err := runner.client.Snapshot(ctx, service)
		if err != nil {
			return runner.rollback(ctx, changed, fmt.Errorf("snapshot %s: %w", service.Name, err))
		}
		if err := runner.client.Apply(ctx, service, request.Revision, request.Enabled); err != nil {
			return runner.rollback(ctx, changed, fmt.Errorf("apply %s: %w", service.Name, err))
		}
		changed = append(changed, snapshot)
		if err := runner.client.WaitReady(ctx, service, request.Revision, request.Enabled); err != nil {
			return runner.rollback(ctx, changed, fmt.Errorf("wait ready %s: %w", service.Name, err))
		}
	}
	return Result{Phase: PhaseSucceeded}
}

func (runner *ReleaseRunner) rollback(ctx context.Context, changed []Snapshot, cause error) Result {
	for index := len(changed) - 1; index >= 0; index-- {
		if err := runner.client.Rollback(ctx, changed[index]); err != nil {
			return Result{Phase: PhaseRollbackFailed, Err: fmt.Errorf("%w; rollback %s: %v", cause, changed[index].Service, err)}
		}
	}
	return Result{Phase: PhaseRolledBack, Err: cause}
}
