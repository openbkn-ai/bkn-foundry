// Package common common operator manage
// @file status_manage.go
// @description: Unified turntable manager.
package common

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

var statusTransitions = map[interfaces.BizStatus][]interfaces.BizStatus{
	// Transition from unpublished state.
	interfaces.BizStatusUnpublish: {
		interfaces.BizStatusPublished,
		interfaces.BizStatusUnpublish,
	},

	// Transition from published state.
	interfaces.BizStatusPublished: {
		interfaces.BizStatusOffline,
		interfaces.BizStatusEditing,
	},

	// Transition from Published to Editing status.
	interfaces.BizStatusEditing: {
		interfaces.BizStatusEditing,
		interfaces.BizStatusPublished,
	},

	// Transition from delisted status.
	interfaces.BizStatusOffline: {
		interfaces.BizStatusPublished,
		interfaces.BizStatusUnpublish,
	},
}

// CheckStatusTransition checks whether the status can be transitioned.
func CheckStatusTransition(fromState, toState interfaces.BizStatus) bool {
	allowedTargetStates, exists := statusTransitions[fromState]
	if !exists {
		return false
	}
	for _, allowedTargetState := range allowedTargetStates {
		if allowedTargetState == toState {
			return true
		}
	}
	return false
}

// Allowed state transitions under edit operations.
var editStatusTrans = map[interfaces.BizStatus]interfaces.BizStatus{
	interfaces.BizStatusUnpublish: interfaces.BizStatusUnpublish,
	interfaces.BizStatusPublished: interfaces.BizStatusEditing,
	interfaces.BizStatusEditing:   interfaces.BizStatusEditing,
	interfaces.BizStatusOffline:   interfaces.BizStatusUnpublish,
}

var deletableStatus = map[interfaces.BizStatus]bool{
	interfaces.BizStatusUnpublish: true,
	interfaces.BizStatusOffline:   true,
}

// GetEditStatusTrans Gets the status transitions allowed under editing operations.
func GetEditStatusTrans(ctx context.Context, fromState interfaces.BizStatus) (interfaces.BizStatus, error) {
	targetState, exists := editStatusTrans[fromState]
	if !exists {
		return "", errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtMCPUnSupportEdit, "current mcp does not support editing")
	}
	return targetState, nil
}

// CanDelete determines whether the current business status allows entry into the deletion process.
func CanDelete(fromState interfaces.BizStatus) bool {
	return deletableStatus[fromState]
}
