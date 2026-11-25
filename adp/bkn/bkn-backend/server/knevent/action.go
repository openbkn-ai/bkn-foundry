// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	corelogics "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// eventAction is the event module's part in a save of a record-event action type, the recognition
// action of an event class: it checks the action type against its class, works out the rule
// version and the account the checks run as, and runs it once when a person saves it.
type eventAction struct {
	store *store
	// objects reads the class in the transaction that saves the action type.
	objects interfaces.ObjectTypeAccess
	// actions reads the stored action type.
	actions interfaces.ActionTypeAccess
	// tools reads the function a recognition action calls; nil reads none.
	tools interfaces.CallerToolAccess
	// trial runs the action type once; nil runs none.
	trial interfaces.ActionTrialAccess
	// mainChanged makes the platform's schedules match the record-event action types of main.
	mainChanged func(ctx context.Context, knID string) error
	available   func() bool
}

var _ interfaces.ActionTypeOperation = (*eventAction)(nil)

func (a *eventAction) Available() bool { return a.available() }

func invalidAction(ctx context.Context, format string, args ...any) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
		WithErrorDetails(fmt.Sprintf(format, args...))
}

func actionInternalError(ctx context.Context, err error) error {
	if httpErr, ok := err.(*httperrors.HTTPError); ok {
		return httpErr
	}
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionType_InternalError).
		WithErrorDetails(err.Error())
}

// Prepare checks a recognition action against its class and fills what the platform keeps of it:
// its rule version and its execution subject. The caller has checked the action type on its own
// and against its bound object type.
func (a *eventAction) Prepare(ctx context.Context, tx *sql.Tx, at *interfaces.ActionType, batch *interfaces.BatchIDIndex) error {
	classID := at.OperationConfig.EventClass
	class, err := a.class(ctx, tx, at, batch)
	if err != nil {
		return err
	}
	switch {
	case class == nil:
		return invalidAction(ctx, "event class %s is not an object type of the network", classID)
	case !class.HasKind(interfaces.ObjectTypeKindEvent) || class.Event == nil:
		return invalidAction(ctx, "%s is not an event class", classID)
	case class.Event.Source != interfaces.KnEventSourceRule:
		return invalidAction(ctx, "the events of class %s are reported: only a class whose source is rule has a record-event action type", classID)
	case class.Event.Subject != at.ObjectTypeID:
		return invalidAction(ctx, "event class %s is about %s, but the action type is bound to %s",
			classID, class.Event.Subject, at.ObjectTypeID)
	}
	others, err := a.store.recognitionActions(ctx, tx, at.KNID, at.Branch, classID)
	if err != nil {
		return actionInternalError(ctx, err)
	}
	others = slices.DeleteFunc(others, func(id string) bool { return id == at.ATID })
	if len(others) > 0 {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("event class %s already has the record-event action type %s: a class has one", classID, others[0]))
	}
	version, err := a.ruleVersion(ctx, at)
	if err != nil {
		return err
	}
	at.RuleVersion = version
	if err := a.keepSubject(ctx, at); err != nil {
		return err
	}
	if corelogics.ActionTrialWanted(ctx) && a.trial != nil {
		trial, err := a.trial.TrialEventAction(ctx, at.Branch, at)
		if err != nil {
			return err
		}
		at.Trial = trial
	}
	return nil
}

// class is the event class of the action type, from the definitions saved with it or from the
// branch as the transaction sees it.
func (a *eventAction) class(ctx context.Context, tx *sql.Tx, at *interfaces.ActionType, batch *interfaces.BatchIDIndex) (*interfaces.ObjectType, error) {
	classID := at.OperationConfig.EventClass
	if batch != nil && batch.ObjectTypes[classID] != nil {
		return batch.ObjectTypes[classID], nil
	}
	found, err := a.objects.GetObjectTypesByIDs(ctx, tx, at.KNID, at.Branch, []string{classID})
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_GetObjectTypesByIDsFailed).WithErrorDetails(err.Error())
	}
	if len(found) == 0 {
		return nil, nil
	}
	return found[0], nil
}

// ruleVersion names what the action type finds: its bound object type, its condition or targets,
// and for a function, the version of the function. A check under a new version finds what has
// long been wrong as a backlog, not as news.
func (a *eventAction) ruleVersion(ctx context.Context, at *interfaces.ActionType) (string, error) {
	basis := struct {
		ObjectType string                         `json:"object_type"`
		Condition  *interfaces.ActionCondCfg      `json:"condition,omitempty"`
		Targets    *interfaces.ActionEventTargets `json:"targets,omitempty"`
		Function   int64                          `json:"function,omitzero"`
	}{ObjectType: at.ObjectTypeID, Condition: at.Condition, Targets: at.OperationConfig.Targets}
	if targets := at.OperationConfig.Targets; targets != nil && targets.Function != nil && a.tools != nil {
		tool, err := a.tools.GetCallerTool(ctx, targets.Function.BoxID, targets.Function.ToolID)
		switch {
		case err == nil && tool != nil:
			basis.Function = tool.UpdateTime
		case corelogics.ActionTrialWanted(ctx):
			return "", invalidAction(ctx, "the function %s/%s cannot be read: %v", targets.Function.BoxID, targets.Function.ToolID, err)
		default:
			// An import may bring the network before its functions; the trial of a save through
			// the API tells a person when the function is missing.
			logger.Warnf("record-event action type %s/%s: read function %s/%s: %v",
				at.KNID, at.ATID, targets.Function.BoxID, targets.Function.ToolID, err)
		}
	}
	data, err := json.Marshal(basis)
	if err != nil {
		return "", actionInternalError(ctx, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12], nil
}

// keepSubject decides whom the checks run as. They run as the account that last changed what
// the action type finds or when it runs; a save that changes neither, such as a new name, keeps
// the account. What the caller sent is not taken.
func (a *eventAction) keepSubject(ctx context.Context, at *interfaces.ActionType) error {
	at.ExecutionSubject = nil
	if at.ATID != "" && a.actions != nil {
		stored, err := a.actions.GetActionTypesByIDs(ctx, at.KNID, at.Branch, []string{at.ATID})
		if err != nil {
			return actionInternalError(ctx, err)
		}
		if len(stored) > 0 && stored[0] != nil {
			before := stored[0]
			if interfaces.IsEventAction(before) && before.ExecutionSubject != nil && before.ExecutionSubject.ID != "" &&
				before.RuleVersion == at.RuleVersion && sameSchedule(before.Schedule, at.Schedule) {
				kept := *before.ExecutionSubject
				at.ExecutionSubject = &kept
				return nil
			}
		}
	}
	account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if account.ID == "" || account.Type == "" {
		return invalidAction(ctx, "a record-event action type runs as the account that saves it, and this save has none")
	}
	at.ExecutionSubject = &interfaces.AccountInfo{ID: account.ID, Type: account.Type}
	return nil
}

func sameSchedule(a, b interfaces.Schedule) bool {
	return a.Type == b.Type && a.Expression == b.Expression && a.Timezone == b.Timezone
}

// MainChanged makes the platform's schedules of the network match its record-event action types.
// A failure is left to the worker, which does the same on a timer.
func (a *eventAction) MainChanged(ctx context.Context, knID string) {
	if a.mainChanged == nil {
		return
	}
	syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := a.mainChanged(syncCtx, knID); err != nil {
		logger.Warnf("sync the record-event schedules of network %s: %v", knID, err)
	}
}

// recognizedBy refuses a change of a class that its record-event action type depends on.
func (k *eventKind) recognizedBy(ctx context.Context, tx *sql.Tx, class *interfaces.ObjectType, why string) error {
	actions, err := k.store.recognitionActions(ctx, tx, class.KNID, class.Branch, class.OTID)
	if err != nil {
		return classInternalError(ctx, err)
	}
	if len(actions) == 0 {
		return nil
	}
	return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KnEvent_RecognizedByAction).
		WithErrorDetails(map[string]string{"event_type": class.OTID, "action_type_id": actions[0], "change": why})
}
