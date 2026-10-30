// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func (s *lineageService) PlanSwitch(ctx context.Context, token string, lineageID string,
	toNetworkID string) (*interfaces.SwitchPlan, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "PlanSwitch")
	defer span.End()

	from, _, err := s.checkSwitch(ctx, s.gs, lineageID, toNetworkID)
	if err != nil {
		span.SetStatus(codes.Error, "Check switch failed")
		return nil, toHTTPError(ctx, "Check switch failed", err)
	}
	schedules, err := s.bba.ListSchedules(ctx, token, from)
	if err != nil {
		span.SetStatus(codes.Error, "List schedules failed")
		return nil, toHTTPError(ctx, "List schedules failed", err)
	}
	active := []*interfaces.ActionSchedule{}
	for _, schedule := range schedules {
		if schedule.Status == interfaces.SCHEDULE_STATUS_ACTIVE {
			active = append(active, schedule)
		}
	}
	span.SetStatus(codes.Ok, "")
	return &interfaces.SwitchPlan{LineageID: lineageID, FromNetworkID: from, ToNetworkID: toNetworkID, Schedules: active}, nil
}

func (s *lineageService) SwitchConsumers(ctx context.Context, token string, lineageID string,
	req interfaces.SwitchRequest, operator interfaces.AccountInfo) (*interfaces.Switch, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SwitchConsumers")
	defer span.End()

	var sw *interfaces.Switch
	// Two switches of one lineage would read the same network in use.
	err := s.gs.WithLineageLock(ctx, lineageID, func(gs interfaces.GitStore) error {
		from, latest, err := s.checkSwitch(ctx, gs, lineageID, req.ToNetworkID)
		if err != nil {
			return err
		}
		if req.FromNetworkID != "" && req.FromNetworkID != from {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_InUseChanged).
				WithErrorDetails(map[string]any{"network_id": from})
		}
		moved, err := s.moveSchedules(ctx, token, from, req.ToNetworkID, latest)
		if err != nil {
			return err
		}
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		sw = &interfaces.Switch{
			ID:            id.String(),
			LineageID:     lineageID,
			FromNetworkID: from,
			ToNetworkID:   req.ToNetworkID,
			Operator:      operator,
			Schedules:     moved,
			CreateTime:    time.Now().UnixMilli(),
		}
		// The schedules have moved; the record must follow even if the caller is gone.
		return s.ra.RecordSwitch(context.WithoutCancel(ctx), sw)
	})
	if err != nil {
		span.SetStatus(codes.Error, "Switch consumers failed")
		httpErr := toHTTPError(ctx, "Switch consumers failed", err)
		var stranded *strandedError
		if errors.As(err, &stranded) {
			details := map[string]any{"schedules_off_on_both": stranded.names}
			if fields, ok := httpErr.(*httperrors.HTTPError).ErrorDetails.(map[string]any); ok {
				for k, v := range fields {
					details[k] = v
				}
			}
			httpErr = httpErr.(*httperrors.HTTPError).WithErrorDetails(details)
		}
		return nil, httpErr
	}
	span.SetStatus(codes.Ok, "")
	return sw, nil
}

// checkSwitch returns the network in use, and the lineage's latest switch or
// nil, after checking that the lineage exists and that to is another network
// of it. The network in use is the target of the latest switch, or the base
// network before any.
func (s *lineageService) checkSwitch(ctx context.Context, gs interfaces.GitStore, lineageID string,
	to string) (string, *interfaces.Switch, error) {
	if _, err := gs.ResolveRef(ctx, lineageID, branchRef(interfaces.GIT_MAIN_BRANCH)); err != nil {
		return "", nil, err
	}
	if to == "" {
		return "", nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidSwitchTarget)
	}
	if to != lineageID {
		release, exist, err := s.ra.GetReleaseByNetworkID(ctx, to)
		if err != nil {
			return "", nil, err
		}
		if !exist || release.LineageID != lineageID {
			return "", nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidSwitchTarget).
				WithErrorDetails(map[string]any{"to_network_id": to})
		}
	}
	latest, exist, err := s.ra.GetLatestSwitch(ctx, lineageID)
	if err != nil {
		return "", nil, err
	}
	from := lineageID
	if exist {
		from = latest.ToNetworkID
	}
	if from == to {
		return "", nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_AlreadyInUse).
			WithErrorDetails(map[string]any{"network_id": to})
	}
	return from, latest, nil
}

// strandedError is a move that turned schedules off on the network in use and
// could turn them on on neither network.
type strandedError struct {
	names []string
	err   error
}

func (e *strandedError) Error() string {
	return fmt.Sprintf("schedules %v are off on both networks: %v", e.names, e.err)
}

func (e *strandedError) Unwrap() error {
	return e.err
}

// sameWork reports whether two schedules run the same action on the same
// instances at the same times, as a copy does.
func sameWork(a *interfaces.ActionSchedule, b *interfaces.ActionSchedule) bool {
	work := func(s *interfaces.ActionSchedule) string {
		data, _ := json.Marshal([]any{s.Name, s.ActionTypeID, s.CronExpression, s.InstanceIdentities, s.DynamicParams})
		return string(data)
	}
	return work(a) == work(b)
}

// moveSchedules turns each active schedule of from off and its counterpart on
// to on, made inactive first where to has none. A schedule's counterpart is
// the one latest, a switch from to to from, moved it from; else one that does
// the same work. Each counterpart pairs with one schedule only, so same-named
// schedules never share one. A switch back finds the counterparts the earlier
// switch left off.
//
// No schedule may run on both networks, and a failure must leave each schedule
// running on one: every counterpart exists before anything is turned off, each
// schedule is turned off on from before its counterpart is turned on, and
// turned back on should that fail. Pairs moved before a failure stay moved; a
// retry finds them off on from and on on to, and records them as moved.
func (s *lineageService) moveSchedules(ctx context.Context, token string, from string,
	to string, latest *interfaces.Switch) ([]interfaces.SwitchedSchedule, error) {
	fromSchedules, err := s.bba.ListSchedules(ctx, token, from)
	if err != nil {
		return nil, err
	}
	toSchedules, err := s.bba.ListSchedules(ctx, token, to)
	if err != nil {
		return nil, err
	}
	movedFrom := map[string]string{}
	if latest != nil && latest.FromNetworkID == to && latest.ToNetworkID == from {
		for _, m := range latest.Schedules {
			movedFrom[m.ToID] = m.FromID
		}
	}
	claimed := map[string]bool{}
	claim := func(match func(*interfaces.ActionSchedule) bool) *interfaces.ActionSchedule {
		for _, c := range toSchedules {
			if !claimed[c.ID] && match(c) {
				claimed[c.ID] = true
				return c
			}
		}
		return nil
	}
	// Counterparts the latest switch recorded are claimed first, so no other
	// schedule takes them by doing the same work.
	recorded := map[string]*interfaces.ActionSchedule{}
	for _, schedule := range fromSchedules {
		if id, ok := movedFrom[schedule.ID]; ok {
			recorded[schedule.ID] = claim(func(c *interfaces.ActionSchedule) bool { return c.ID == id })
		}
	}
	counterpart := func(schedule *interfaces.ActionSchedule) *interfaces.ActionSchedule {
		if c := recorded[schedule.ID]; c != nil {
			return c
		}
		return claim(func(c *interfaces.ActionSchedule) bool { return sameWork(schedule, c) })
	}

	moved := []interfaces.SwitchedSchedule{}
	type pair struct{ from, to *interfaces.ActionSchedule }
	pairs := []pair{}
	for _, schedule := range fromSchedules {
		c := counterpart(schedule)
		switch {
		case schedule.Status == interfaces.SCHEDULE_STATUS_ACTIVE:
			if c == nil {
				created := *schedule
				created.Status = interfaces.SCHEDULE_STATUS_INACTIVE
				if created.ID, err = s.bba.CreateSchedule(ctx, token, to, &created); err != nil {
					return nil, fmt.Errorf("create schedule %s on %s: %w", schedule.Name, to, err)
				}
				c = &created
			}
			pairs = append(pairs, pair{schedule, c})
		case c != nil && c.Status == interfaces.SCHEDULE_STATUS_ACTIVE:
			// Moved by an earlier attempt that failed before the switch was recorded.
			moved = append(moved, interfaces.SwitchedSchedule{FromID: schedule.ID, ToID: c.ID, Name: schedule.Name})
		case c != nil:
			// Off on both networks: nothing to move, and an active schedule doing
			// the same work may still need this counterpart.
			delete(claimed, c.ID)
		}
	}

	// Once a schedule is off, a caller going away must not stop it being turned
	// on on one network or the other.
	statusCtx := context.WithoutCancel(ctx)
	for _, p := range pairs {
		if err := s.bba.SetScheduleStatus(statusCtx, token, from, p.from.ID, interfaces.SCHEDULE_STATUS_INACTIVE); err != nil {
			return nil, fmt.Errorf("turn schedule %s off on %s: %w", p.from.Name, from, err)
		}
		if p.to.Status != interfaces.SCHEDULE_STATUS_ACTIVE {
			if err := s.bba.SetScheduleStatus(statusCtx, token, to, p.to.ID, interfaces.SCHEDULE_STATUS_ACTIVE); err != nil {
				err = fmt.Errorf("turn schedule %s on on %s: %w", p.from.Name, to, err)
				if restoreErr := s.bba.SetScheduleStatus(statusCtx, token, from, p.from.ID, interfaces.SCHEDULE_STATUS_ACTIVE); restoreErr != nil {
					otellog.LogError(ctx, fmt.Sprintf("Schedule %s is off on both %s and %s", p.from.Name, from, to), restoreErr)
					return nil, &strandedError{names: []string{p.from.Name}, err: err}
				}
				return nil, err
			}
		}
		moved = append(moved, interfaces.SwitchedSchedule{FromID: p.from.ID, ToID: p.to.ID, Name: p.from.Name})
	}
	return moved, nil
}
