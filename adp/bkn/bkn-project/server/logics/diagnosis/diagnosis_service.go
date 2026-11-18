// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package diagnosis diagnoses a project's knowledge network as a whole: the draft service counts the
// facts, Ask AI judges them by the ontology rules in the diagnose pass of the Ontology Assistant, and
// this package checks and ranks the suggestions, keeps the latest diagnosis of each project and
// applies a suggestion with the draft's own operations. Without Ask AI the suggestions are the
// server's own, from the counts alone.
package diagnosis

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/draft"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/project"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/requirement"
)

const (
	// agentWait bounds Ask AI's judgment: the Ontology Assistant's own limit (timeout_s 180, set for
	// the diagnose pass by migration 0.2.2/21) and a little for the stream to end. The run is in the
	// background; an answer on a page keeps the draft service's shorter wait.
	agentWait = 3*time.Minute + 15*time.Second
	// staleAfter is how long a run may take before another may replace it: the scan, Ask AI and a
	// minute to record it.
	staleAfterExtra = time.Minute
)

var (
	dgsOnce sync.Once
	dgs     interfaces.DiagnosisService
)

type diagnosisService struct {
	setting common.DraftSetting
	dga     interfaces.DiagnosisAccess
	drs     interfaces.DraftService
	rqs     interfaces.RequirementService
	rqa     interfaces.RequirementAccess
	ps      interfaces.ProjectService
	ba      interfaces.BknAgentAccess
	// run diagnoses in the background; tests replace it to run in line.
	run       func(fn func())
	agentWait time.Duration
	now       func() time.Time
}

func NewDiagnosisService(appSetting *common.AppSetting) interfaces.DiagnosisService {
	dgsOnce.Do(func() {
		dgs = &diagnosisService{
			setting:   appSetting.DraftSetting,
			dga:       logics.DGA,
			drs:       draft.NewDraftService(appSetting),
			rqs:       requirement.NewRequirementService(),
			rqa:       logics.RQA,
			ps:        project.NewProjectService(),
			ba:        logics.BAA,
			run:       func(fn func()) { go fn() },
			agentWait: agentWait,
			now:       time.Now,
		}
	})
	return dgs
}

func internalError(ctx context.Context, operation string, err error) error {
	otellog.LogError(ctx, operation, err)
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, perrors.BknProject_Draft_InternalError).
		WithErrorDetails(err.Error())
}

func (s *diagnosisService) staleBefore() int64 {
	timeout := s.setting.DiagnosisTimeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	return s.now().Add(-(timeout + s.agentWait + staleAfterExtra)).UnixMilli()
}

// inputs are what a run reads while the request is still open: the draft, the scenario questions and
// the requirement concepts. The counts and Ask AI follow in the background.
type inputs struct {
	draft     *interfaces.Draft
	questions *interfaces.ScenarioQuestions
	concepts  []conceptInput
}

func (s *diagnosisService) Run(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) (*interfaces.DiagnosisRun, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "RunDiagnosis")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	if entry.Archived {
		span.SetStatus(codes.Error, "Project archived")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_ProjectArchived)
	}
	if entry.Owner.ID != caller.ID {
		span.SetStatus(codes.Error, "Not the owner")
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Diagnosis_NotOwner)
	}
	d, err := s.drs.GetDraft(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, err
	}
	if d.Status != interfaces.DRAFT_STATUS_READY {
		span.SetStatus(codes.Error, "Draft not ready")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_NotReady)
	}
	in := inputs{draft: d}
	if in.questions, err = s.rqs.ScenarioQuestions(ctx, projectID); err != nil {
		span.SetStatus(codes.Error, "Read questions failed")
		return nil, internalError(ctx, "Read scenario questions failed", err)
	}
	if in.concepts, err = s.pendingConcepts(ctx, token, projectID, caller); err != nil {
		span.SetStatus(codes.Error, "Read concepts failed")
		return nil, err
	}

	runID, err := uuid.NewV7()
	if err != nil {
		return nil, internalError(ctx, "New diagnosis run", err)
	}
	diagnosis := &interfaces.Diagnosis{ProjectID: projectID, State: interfaces.DIAGNOSIS_STATE_RUNNING,
		RunID: runID.String(), DraftUpdateTime: d.UpdateTime, Runner: caller, StartTime: s.now().UnixMilli(),
		Components: [][]string{}, Suggestions: []*interfaces.DiagnosisSuggestion{}}
	started, err := s.dga.StartDiagnosis(ctx, diagnosis, s.staleBefore())
	if err != nil {
		span.SetStatus(codes.Error, "Start diagnosis failed")
		return nil, internalError(ctx, "Start diagnosis failed", err)
	}
	if !started {
		span.SetStatus(codes.Error, "Diagnosis running")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Diagnosis_Running)
	}
	// The reply is the run as started; the run goes on writing its own. It outlives the request,
	// keeping the trace but not the request's cancellation.
	reply := &interfaces.DiagnosisRun{State: diagnosis.State, RunID: diagnosis.RunID}
	background := context.WithoutCancel(ctx)
	s.run(func() { s.diagnose(background, token, diagnosis, in, caller) })
	span.SetStatus(codes.Ok, "")
	return reply, nil
}

// diagnose counts the facts, asks AI, and records the outcome while the run is still the project's.
func (s *diagnosisService) diagnose(ctx context.Context, token string, diagnosis *interfaces.Diagnosis, in inputs,
	caller interfaces.AccountInfo) {
	facts, err := s.drs.NetworkFacts(ctx, token, diagnosis.ProjectID, caller)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Network facts of project %s: %v", diagnosis.ProjectID, err))
		diagnosis.State, diagnosis.Error = interfaces.DIAGNOSIS_STATE_FAILED, interfaces.DIAGNOSIS_ERROR_DRAFT_UNAVAILABLE
	} else {
		j := newJudge(s.setting, facts, in.questions, in.concepts)
		var out *agentOutput
		if s.ba != nil {
			out, err = s.askAgent(ctx, caller, j.agentInput())
			if err != nil {
				otellog.LogWarn(ctx, fmt.Sprintf("Diagnosis of project %s without Ask AI: %v", diagnosis.ProjectID, err))
			}
		}
		diagnosis.Suggestions, diagnosis.AI = j.suggestions(out)
		diagnosis.Metrics, diagnosis.Components = j.metrics(), facts.Components
		diagnosis.State, diagnosis.DraftUpdateTime = interfaces.DIAGNOSIS_STATE_READY, facts.Draft.UpdateTime
	}
	diagnosis.ComputedAt = s.now().UnixMilli()
	diagnosis.FinishTime = diagnosis.ComputedAt
	if saved, err := s.dga.SaveDiagnosis(ctx, diagnosis); err != nil || !saved {
		otellog.LogWarn(ctx, fmt.Sprintf("Record diagnosis of project %s: saved %t, %v", diagnosis.ProjectID, saved, err))
	}
}

func (s *diagnosisService) Get(ctx context.Context, token string, projectID string) (*interfaces.Diagnosis, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetDiagnosis")
	defer span.End()

	if _, err := s.ps.GetProject(ctx, token, projectID); err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	diagnosis, exist, err := s.dga.GetDiagnosis(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get diagnosis failed")
		return nil, internalError(ctx, "Get diagnosis failed", err)
	}
	if !exist {
		span.SetStatus(codes.Error, "Diagnosis not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Diagnosis_NotFound)
	}
	if diagnosis.State == interfaces.DIAGNOSIS_STATE_RUNNING && diagnosis.StartTime < s.staleBefore() {
		// The service stopped before the run recorded its outcome.
		diagnosis.State, diagnosis.Error = interfaces.DIAGNOSIS_STATE_FAILED, interfaces.DIAGNOSIS_ERROR_INTERRUPTED
	}
	if diagnosis.Components == nil {
		diagnosis.Components = [][]string{}
	}
	if diagnosis.Suggestions == nil {
		diagnosis.Suggestions = []*interfaces.DiagnosisSuggestion{}
	}
	if diagnosis.State == interfaces.DIAGNOSIS_STATE_READY {
		d, err := s.drs.GetDraft(ctx, token, projectID)
		var httpErr *httperrors.HTTPError
		switch {
		case err == nil:
			// Only changes made outside the diagnosis's own apply and dismiss make it stale: those
			// move the update time it holds for along.
			diagnosis.Stale = d.UpdateTime != diagnosis.DraftUpdateTime
			markDismissed(diagnosis.Suggestions, &d.Items)
		case errors.As(err, &httpErr) && httpErr.HTTPCode == http.StatusNotFound:
			diagnosis.Stale = true
		default:
			span.SetStatus(codes.Error, "Get draft failed")
			return nil, err
		}
	}
	span.SetStatus(codes.Ok, "")
	return diagnosis, nil
}

// markDismissed marks the suggestions a person dismissed, as the draft records it.
func markDismissed(suggestions []*interfaces.DiagnosisSuggestion, items *interfaces.DraftItems) {
	dismissed := dismissedIDs(items)
	for _, sg := range suggestions {
		if dismissed[sg.ID] && sg.Status == interfaces.DIAGNOSIS_STATUS_OPEN {
			sg.Status = interfaces.DIAGNOSIS_STATUS_DISMISSED
		}
	}
}

// dismissedIDs are the suggestions the draft records as dismissed.
func dismissedIDs(items *interfaces.DraftItems) map[string]bool {
	dismissed := map[string]bool{}
	for _, d := range items.Decisions {
		if d.Kind == interfaces.DRAFT_KIND_DIAGNOSIS && d.Action == interfaces.DRAFT_DECISION_DISMISS {
			dismissed[d.ItemID] = true
		}
	}
	return dismissed
}
