// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"context"
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

	from, err := s.checkSwitch(ctx, s.gs, lineageID, toNetworkID)
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
		from, err := s.checkSwitch(ctx, gs, lineageID, req.ToNetworkID)
		if err != nil {
			return err
		}
		moved, err := s.moveSchedules(ctx, token, from, req.ToNetworkID)
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
		return s.ra.RecordSwitch(ctx, sw)
	})
	if err != nil {
		span.SetStatus(codes.Error, "Switch consumers failed")
		return nil, toHTTPError(ctx, "Switch consumers failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return sw, nil
}

// checkSwitch returns the network in use after checking that the lineage
// exists and that to is another network of it.
func (s *lineageService) checkSwitch(ctx context.Context, gs interfaces.GitStore, lineageID string,
	to string) (string, error) {
	if _, err := gs.ResolveRef(ctx, lineageID, branchRef(interfaces.GIT_MAIN_BRANCH)); err != nil {
		return "", err
	}
	if to == "" {
		return "", httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidSwitchTarget)
	}
	if to != lineageID {
		release, exist, err := s.ra.GetReleaseByNetworkID(ctx, to)
		if err != nil {
			return "", err
		}
		if !exist || release.LineageID != lineageID {
			return "", httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidSwitchTarget).
				WithErrorDetails(map[string]any{"to_network_id": to})
		}
	}
	from, err := s.networkInUse(ctx, lineageID)
	if err != nil {
		return "", err
	}
	if from == to {
		return "", httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_AlreadyInUse).
			WithErrorDetails(map[string]any{"network_id": to})
	}
	return from, nil
}

// networkInUse is the target of the lineage's latest switch, or its base network before any.
func (s *lineageService) networkInUse(ctx context.Context, lineageID string) (string, error) {
	latest, exist, err := s.ra.GetLatestSwitch(ctx, lineageID)
	if err != nil {
		return "", err
	}
	if !exist {
		return lineageID, nil
	}
	return latest.ToNetworkID, nil
}

// moveSchedules turns each active schedule of from off and its counterpart on
// to on, the same name and action type, made inactive first where to has none.
// A switch back finds the counterparts the earlier switch left off.
//
// No schedule may run on both networks, and a failure must leave each schedule
// running on one: every counterpart exists before anything is turned off, each
// schedule is turned off on from before its counterpart is turned on, and
// turned back on should that fail. Pairs moved before a failure stay moved; a
// retry finds them off on from and on on to, and records them as moved.
func (s *lineageService) moveSchedules(ctx context.Context, token string, from string,
	to string) ([]interfaces.SwitchedSchedule, error) {
	fromSchedules, err := s.bba.ListSchedules(ctx, token, from)
	if err != nil {
		return nil, err
	}
	toSchedules, err := s.bba.ListSchedules(ctx, token, to)
	if err != nil {
		return nil, err
	}
	counterpart := func(schedule *interfaces.ActionSchedule) *interfaces.ActionSchedule {
		for _, c := range toSchedules {
			if c.Name == schedule.Name && c.ActionTypeID == schedule.ActionTypeID {
				return c
			}
		}
		return nil
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
		}
	}

	for _, p := range pairs {
		if err := s.bba.SetScheduleStatus(ctx, token, from, p.from.ID, interfaces.SCHEDULE_STATUS_INACTIVE); err != nil {
			return nil, fmt.Errorf("turn schedule %s off on %s: %w", p.from.Name, from, err)
		}
		if p.to.Status != interfaces.SCHEDULE_STATUS_ACTIVE {
			if err := s.bba.SetScheduleStatus(ctx, token, to, p.to.ID, interfaces.SCHEDULE_STATUS_ACTIVE); err != nil {
				if restoreErr := s.bba.SetScheduleStatus(ctx, token, from, p.from.ID, interfaces.SCHEDULE_STATUS_ACTIVE); restoreErr != nil {
					otellog.LogError(ctx, fmt.Sprintf("Schedule %s is off on both %s and %s", p.from.Name, from, to), restoreErr)
				}
				return nil, fmt.Errorf("turn schedule %s on on %s: %w", p.from.Name, to, err)
			}
		}
		moved = append(moved, interfaces.SwitchedSchedule{FromID: p.from.ID, ToID: p.to.ID, Name: p.from.Name})
	}
	return moved, nil
}
