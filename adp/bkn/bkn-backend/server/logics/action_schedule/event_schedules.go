// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_schedule

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// A record-event action type runs on the schedule its definition carries. The platform keeps one
// schedule row for each such action type of the running version, the main branch: the worker runs
// it as it runs a user's schedule, and the schedule API shows it read-only. The row follows the
// action type: SyncEventSchedules adds, changes and removes rows to match the main branch. It runs
// after a save on main, a publish and a switch of the running version, and on a timer in case one
// of them was missed.

// platformScheduleID is the ID of the row kept for an action type: the same on every replica, so
// two replicas that add it at once write one row.
func platformScheduleID(knID, actionTypeID string) string {
	sum := sha256.Sum256([]byte(knID + "\x00" + actionTypeID))
	return "at-" + hex.EncodeToString(sum[:])[:36]
}

// SyncEventSchedules makes the platform's schedule rows of the network, or of every network when
// knID is empty, match the record-event action types of its main branch. A row whose expression
// changes runs next on the new expression; an unchanged row keeps its next run.
func SyncEventSchedules(ctx context.Context, asa interfaces.ActionScheduleAccess, ata interfaces.ActionTypeAccess,
	knID string, now time.Time) error {
	actions, err := ata.ListActionTypes(ctx, interfaces.ActionTypesQueryParams{
		KNID: knID, Branch: interfaces.MAIN_BRANCH, OperationKind: interfaces.ActionOperationEvent,
	})
	if err != nil {
		return fmt.Errorf("list record-event action types: %w", err)
	}
	rows, err := asa.ListSchedules(ctx, interfaces.ActionScheduleQueryParams{
		KNID: knID, Source: interfaces.ActionScheduleSourceActionType,
	})
	if err != nil {
		return fmt.Errorf("list platform schedules: %w", err)
	}
	kept := make(map[string]*interfaces.ActionSchedule, len(rows))
	for _, row := range rows {
		kept[row.ID] = row
	}
	var errs []error
	for _, action := range actions {
		want, ok := wantedSchedule(action, now)
		if !ok {
			continue
		}
		row := kept[want.ID]
		delete(kept, want.ID)
		switch {
		case row == nil:
			if err := asa.CreateSchedule(ctx, nil, want); err != nil {
				if _, duplicate := common.DatabaseUniqueConstraint(err); !duplicate {
					errs = append(errs, fmt.Errorf("add schedule of %s/%s: %w", action.KNID, action.ATID, err))
				}
			}
		case row.CronExpression != want.CronExpression || row.Name != want.Name ||
			row.ExecutionSubject != want.ExecutionSubject || row.Status != want.Status:
			change := *want
			if row.CronExpression == want.CronExpression && row.NextRunTime > 0 {
				change.NextRunTime = row.NextRunTime
			}
			if err := asa.UpdateSchedule(ctx, nil, &change); err != nil {
				errs = append(errs, fmt.Errorf("update schedule of %s/%s: %w", action.KNID, action.ATID, err))
			}
		}
	}
	if len(kept) > 0 {
		ids := make([]string, 0, len(kept))
		for id := range kept {
			ids = append(ids, id)
		}
		if err := asa.DeleteSchedules(ctx, nil, ids); err != nil {
			errs = append(errs, fmt.Errorf("remove platform schedules: %w", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("sync record-event schedules: %v", errs)
	}
	return nil
}

// wantedSchedule is the row an action type needs, or false when it cannot run: a schedule that
// does not read, or no execution subject. Such an action type was saved before the checks that
// prevent this, or written around them; it is not run until it is saved again.
func wantedSchedule(action *interfaces.ActionType, now time.Time) (*interfaces.ActionSchedule, bool) {
	schedule, err := interfaces.ParseEventSchedule(action.Schedule)
	if err != nil {
		logger.Warnf("record-event action type %s/%s has no schedule to run: %v", action.KNID, action.ATID, err)
		return nil, false
	}
	if action.ExecutionSubject == nil || action.ExecutionSubject.ID == "" || action.ExecutionSubject.Type == "" {
		logger.Warnf("record-event action type %s/%s has no execution subject", action.KNID, action.ATID)
		return nil, false
	}
	subject := interfaces.AccountInfo{ID: action.ExecutionSubject.ID, Type: action.ExecutionSubject.Type}
	at := now.UnixMilli()
	return &interfaces.ActionSchedule{
		ID:                 platformScheduleID(action.KNID, action.ATID),
		Name:               action.ATName,
		KNID:               action.KNID,
		Branch:             interfaces.MAIN_BRANCH,
		ActionTypeID:       action.ATID,
		CronExpression:     schedule.Expression,
		InstanceIdentities: []map[string]any{},
		DynamicParams:      map[string]any{},
		Status:             interfaces.ScheduleStatusActive,
		NextRunTime:        schedule.Next(now).UnixMilli(),
		Creator:            subject,
		CreateTime:         at,
		Updater:            subject,
		UpdateTime:         at,
		ExecutionSubject:   subject,
		Source:             interfaces.ActionScheduleSourceActionType,
	}, true
}
