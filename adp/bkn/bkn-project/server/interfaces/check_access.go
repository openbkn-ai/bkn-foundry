// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

//go:generate mockgen -source ../interfaces/check_access.go -destination ../interfaces/mock/mock_check_access.go
type CheckAccess interface {
	// GetCheck returns the project's check, and false when it has none.
	GetCheck(ctx context.Context, projectID string) (*Check, bool, error)
	// StartCheck writes check, running, as the project's check unless the same commit is being
	// checked already; it reports whether it did. A check of another commit is replaced, and a
	// check running since before staleBefore is taken over: it has ended without being recorded.
	StartCheck(ctx context.Context, check *Check, staleBefore int64) (bool, error)
	// FinishCheck records how a run ended while it is still the project's running check; it
	// reports whether it did, and false means a later run replaced it.
	FinishCheck(ctx context.Context, check *Check) (bool, error)
	// FailRunning marks every running check as failed with reason; checks run in the service
	// process, so checks left running after a restart will never finish.
	FailRunning(ctx context.Context, reason string, finishTime int64) (int64, error)
}
