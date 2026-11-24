// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

var (
	actionOperationsMu sync.RWMutex
	actionOperations   = map[string]interfaces.ActionTypeOperation{}
)

// RegisterActionTypeOperation registers the module that provides an operation category of action
// types. A nil operation unregisters the category.
func RegisterActionTypeOperation(name string, operation interfaces.ActionTypeOperation) {
	actionOperationsMu.Lock()
	defer actionOperationsMu.Unlock()
	if operation == nil {
		delete(actionOperations, name)
		return
	}
	actionOperations[name] = operation
}

// ActionTypeOperationNamed is the registered operation category, or nil.
func ActionTypeOperationNamed(name string) interfaces.ActionTypeOperation {
	actionOperationsMu.RLock()
	defer actionOperationsMu.RUnlock()
	return actionOperations[name]
}

// ActionTypeOperationAvailable reports whether the action type's operation category may run: the
// categories of the core always may, a module's when it is registered and licensed.
func ActionTypeOperationAvailable(actionType *interfaces.ActionType) bool {
	if actionType == nil || actionType.OperationConfig == nil {
		return true
	}
	switch actionType.OperationConfig.Type {
	case interfaces.ActionOperationObject, interfaces.ActionOperationRelation, interfaces.ActionOperationTool:
		return true
	}
	operation := ActionTypeOperationNamed(actionType.OperationConfig.Type)
	return operation != nil && operation.Available()
}

// MainChanged tells the operation categories that the main branch of the network changed.
func MainChanged(ctx context.Context, knID string) {
	actionOperationsMu.RLock()
	operations := make([]interfaces.ActionTypeOperation, 0, len(actionOperations))
	for _, operation := range actionOperations {
		operations = append(operations, operation)
	}
	actionOperationsMu.RUnlock()
	for _, operation := range operations {
		operation.MainChanged(ctx, knID)
	}
}

type actionTrialKey struct{}

// WithActionTrial is ctx for a save through the API, which runs a record-event action type once
// before it is saved; an import does not.
func WithActionTrial(ctx context.Context) context.Context {
	return context.WithValue(ctx, actionTrialKey{}, true)
}

// ActionTrialWanted reports whether the save runs a trial.
func ActionTrialWanted(ctx context.Context) bool {
	wanted, _ := ctx.Value(actionTrialKey{}).(bool)
	return wanted
}
