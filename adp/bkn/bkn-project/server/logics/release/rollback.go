// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// rollbackConsumers are the consumers whose owners switch them back.
var rollbackConsumers = []string{
	interfaces.ROLLBACK_CONSUMER_AGENTS, interfaces.ROLLBACK_CONSUMER_MCP,
	interfaces.ROLLBACK_CONSUMER_WORKSHOP, interfaces.ROLLBACK_CONSUMER_EVAL,
}

// lineageReleases are the lineage's releases, newest first.
func (s *projectReleaseService) lineageReleases(ctx context.Context, lineageID string) ([]*interfaces.Release, error) {
	all := []*interfaces.Release{}
	query := interfaces.ReleasesQuery{LineageID: lineageID, Limit: interfaces.MAX_RELEASE_LIMIT}
	for {
		page, err := s.ra.ListReleases(ctx, query)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < query.Limit {
			return all, nil
		}
		last := page[len(page)-1]
		query.Cursor = &interfaces.ReleaseCursor{CreateTime: last.CreateTime, ID: last.ID}
	}
}

// networkOf names a network of the lineage: the release that created it, or the base network.
func networkOf(release *interfaces.Release, lineageID string) interfaces.RollbackNetwork {
	if release == nil {
		return interfaces.RollbackNetwork{NetworkID: lineageID, Base: true}
	}
	return interfaces.RollbackNetwork{NetworkID: release.NetworkID, Version: release.Version}
}

// definitionsRef is the commit holding a network's definitions: its release's tag, or the lineage's
// first commit for the base network.
func (s *projectReleaseService) definitionsRef(ctx context.Context, lineageID string, n interfaces.RollbackNetwork) (string, error) {
	if n.Base {
		return s.gs.RootCommit(ctx, lineageID)
	}
	return "refs/tags/v" + n.Version, nil
}

// rollbackSignature stands for the network in use, the target, the latest switch and the schedules
// the rollback would move.
func rollbackSignature(from string, to string, latest *interfaces.Switch, schedules []*interfaces.ActionSchedule) string {
	hash := sha256.New()
	switchID := ""
	if latest != nil {
		switchID = latest.ID
	}
	fmt.Fprintf(hash, "%s\n%s\n%s\n", from, to, switchID)
	ids := []string{}
	for _, sc := range schedules {
		ids = append(ids, sc.ID)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(hash, "%s\n", id)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// planRollback works out the rollback of entry's lineage. Only reading the plan's refusals tells
// whether it may go.
func (s *projectReleaseService) planRollback(ctx context.Context, token string,
	entry *interfaces.ProjectEntry) (*interfaces.RollbackPlan, error) {
	lineageID := entry.KNID
	plan := &interfaces.RollbackPlan{
		ProjectID: entry.ID,
		Schedules: []*interfaces.ActionSchedule{},
		Consumers: rollbackConsumers,
		Questions: releaseQuestions(nil, nil, nil, interfaces.PathDiff{}),
		Refusals:  []interfaces.ReleaseRefusal{},
	}
	latest, switched, err := s.ra.GetLatestSwitch(ctx, lineageID)
	if err != nil {
		return nil, internalError(ctx, "Get switch failed", err)
	}
	if !switched {
		latest = nil
	}
	inUse := lineageID
	if latest != nil {
		inUse = latest.ToNetworkID
	}
	releases, err := s.lineageReleases(ctx, lineageID)
	if err != nil {
		return nil, internalError(ctx, "List releases failed", err)
	}
	at := -1
	for i, r := range releases {
		if r.NetworkID == inUse {
			at = i
		}
	}
	if at < 0 {
		// The base network is in use: there is nothing before it.
		plan.From = networkOf(nil, lineageID)
		plan.Refusals = append(plan.Refusals, interfaces.ReleaseRefusal{Code: perrors.BknProject_Rollback_NoPrevious})
		plan.Signature = rollbackSignature(inUse, "", latest, nil)
		return plan, nil
	}
	plan.From = networkOf(releases[at], lineageID)
	var previous *interfaces.Release
	if at+1 < len(releases) {
		previous = releases[at+1]
	}
	to := networkOf(previous, lineageID)
	plan.To = &to
	if releases[at].ProjectID != entry.ID {
		plan.Refusals = append(plan.Refusals, interfaces.ReleaseRefusal{
			Code: perrors.BknProject_Rollback_NotProjectRelease, Details: map[string]any{"project_id": releases[at].ProjectID},
		})
		plan.Signature = rollbackSignature(inUse, to.NetworkID, latest, nil)
		return plan, nil
	}

	switchPlan, err := s.ls.PlanSwitch(ctx, token, lineageID, to.NetworkID)
	if err != nil {
		return nil, err
	}
	plan.Schedules = switchPlan.Schedules

	var questions *interfaces.ScenarioQuestions
	if s.rs != nil {
		if questions, err = s.rs.ScenarioQuestions(ctx, entry.ID); err != nil {
			return nil, internalError(ctx, "Get scenario questions failed", err)
		}
	}
	fromRef, err := s.definitionsRef(ctx, lineageID, plan.From)
	if err != nil {
		return nil, internalError(ctx, "Resolve definitions failed", err)
	}
	toRef, err := s.definitionsRef(ctx, lineageID, to)
	if err != nil {
		return nil, internalError(ctx, "Resolve definitions failed", err)
	}
	fromFiles, err := s.gs.ReadTree(ctx, lineageID, fromRef)
	if err != nil {
		return nil, internalError(ctx, "Read definitions failed", err)
	}
	toFiles, err := s.gs.ReadTree(ctx, lineageID, toRef)
	if err != nil {
		return nil, internalError(ctx, "Read definitions failed", err)
	}
	diff, err := s.gs.Diff(ctx, lineageID, fromRef, toRef)
	if err != nil {
		return nil, internalError(ctx, "Diff definitions failed", err)
	}
	// As a release review places them, with the target in place of the branch and the network in use
	// in place of what is live.
	plan.Questions = releaseQuestions(questions, toFiles, fromFiles, *diff)
	plan.Signature = rollbackSignature(inUse, to.NetworkID, latest, plan.Schedules)
	return plan, nil
}

// PlanRollback reports what rolling the project's lineage back would do, read by anyone who can read
// the project.
func (s *projectReleaseService) PlanRollback(ctx context.Context, token string,
	projectID string) (*interfaces.RollbackPlan, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "PlanRollback")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	plan, err := s.planRollback(ctx, token, entry)
	if err != nil {
		span.SetStatus(codes.Error, "Plan rollback failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return plan, nil
}

// Rollback switches the consumers back, for the owner, only as the plan read says. It only switches:
// schedules move and the switch is recorded; nothing is imported, and no data or definitions are
// written.
func (s *projectReleaseService) Rollback(ctx context.Context, token string, projectID string,
	req interfaces.ProjectRollback, caller interfaces.AccountInfo) (*interfaces.Switch, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Rollback")
	defer span.End()

	if req.Signature == "" {
		span.SetStatus(codes.Error, "No precheck")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Rollback_PrecheckRequired)
	}
	entry, err := s.ownedProject(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Project not rollbackable")
		return nil, err
	}
	plan, err := s.planRollback(ctx, token, entry)
	if err != nil {
		span.SetStatus(codes.Error, "Plan rollback failed")
		return nil, err
	}
	if len(plan.Refusals) > 0 {
		span.SetStatus(codes.Error, "Rollback refused")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, plan.Refusals[0].Code).
			WithErrorDetails(plan.Refusals[0].Details)
	}
	if req.Signature != plan.Signature {
		span.SetStatus(codes.Error, "Plan outdated")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Rollback_PlanOutdated)
	}
	sw, err := s.ls.SwitchConsumers(ctx, token, entry.KNID, interfaces.SwitchRequest{ToNetworkID: plan.To.NetworkID}, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Switch consumers failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return sw, nil
}
