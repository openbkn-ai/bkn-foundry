// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/action_target"
)

const eventKN = "kn_001"

var eventExecutor = interfaces.AccountInfo{ID: "u-subject", Type: "user"}

func licenseEventActions(t *testing.T, yes bool) {
	t.Helper()
	previous := action_target.EventActionsLicensed
	action_target.EventActionsLicensed = func() bool { return yes }
	t.Cleanup(func() { action_target.EventActionsLicensed = previous })
}

func recordEventAction() interfaces.ActionType {
	return interfaces.ActionType{
		ATID: "po_overdue_recognize", ATName: "超期订单", ActionType: "add", ObjectTypeID: "purchase_order",
		OperationConfig: &interfaces.ActionOperationConfig{Type: interfaces.ActionOperationEvent, EventClass: "po_overdue"},
		Condition:       &cond.CondCfg{Name: "due_time", Operation: "<", ValueFrom: interfaces.ActionValueFromCheckTime, Value: "-PT48H"},
		Schedule:        interfaces.Schedule{Type: "CRON", Expression: "0 8 * * *", Timezone: "Asia/Shanghai"},
		RuleVersion:     "a1b2c3", ExecutionSubject: &eventExecutor,
	}
}

var eventSubject = interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
	OTID: "purchase_order", OTName: "采购订单", PrimaryKeys: []string{"po_id"}, DisplayKey: "po_name",
	DataProperties: []cond.DataProperty{{Name: "po_id", Type: "integer"}, {Name: "po_name", Type: "string"}, {Name: "due_time", Type: "datetime"}},
}}

func overdueOrder(id int) map[string]any {
	key := json.Number(fmt.Sprint(id))
	return map[string]any{
		"po_id": key, "po_name": fmt.Sprintf("PO %d", id), "due_time": "2026-11-20T00:00:00+08:00",
		interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY: map[string]any{"po_id": key},
		interfaces.SYSTEM_PROPERTY_DISPLAY:           fmt.Sprintf("PO %d", id),
	}
}

type eventMocks struct {
	models      *omock.MockOntologyManagerAccess
	objects     *omock.MockObjectTypeService
	queries     *omock.MockQueryAuthorizationService
	rules       *omock.MockEventRuleAccess
	logs        *omock.MockActionLogsService
	permissions *actionPermissionStub
}

func newEventScheduler(t *testing.T) (*actionSchedulerService, eventMocks) {
	t.Helper()
	licenseEventActions(t, true)
	ctrl := gomock.NewController(t)
	m := eventMocks{
		models: omock.NewMockOntologyManagerAccess(ctrl), objects: omock.NewMockObjectTypeService(ctrl),
		queries: omock.NewMockQueryAuthorizationService(ctrl), rules: omock.NewMockEventRuleAccess(ctrl),
		logs: omock.NewMockActionLogsService(ctrl), permissions: &actionPermissionStub{},
	}
	m.models.EXPECT().GetObjectType(gomock.Any(), eventKN, interfaces.MAIN_BRANCH, "purchase_order").Return(eventSubject, true, nil).AnyTimes()
	m.queries.EXPECT().AuthorizeObjectTypeQuery(gomock.Any(), eventKN, interfaces.MAIN_BRANCH, "purchase_order").Return(nil).AnyTimes()
	return &actionSchedulerService{omAccess: m.models, ots: m.objects, queries: m.queries, eventRules: m.rules,
		logsService: m.logs, permissions: m.permissions}, m
}

func asExecutor() context.Context {
	return context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, eventExecutor)
}

func scheduledCheck() *interfaces.ActionExecutionRequest {
	return &interfaces.ActionExecutionRequest{KNID: eventKN, ActionTypeID: "po_overdue_recognize",
		TriggerType: interfaces.TriggerTypeScheduled, Internal: true}
}

func refusal(t *testing.T, err error, status int, detail string) {
	t.Helper()
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != status || !strings.Contains(fmt.Sprint(httpErr.ErrorDetails), detail) {
		t.Fatalf("want %d %q, got %v", status, detail, err)
	}
}

func TestARecordEventActionRunsOnlyOnItsSchedule(t *testing.T) {
	service, m := newEventScheduler(t)
	m.models.EXPECT().GetActionType(gomock.Any(), eventKN, interfaces.MAIN_BRANCH, "po_overdue_recognize").
		Return(recordEventAction(), map[string]any{}, true, nil).AnyTimes()

	external := scheduledCheck()
	external.Internal = false
	_, err := service.ExecuteAction(asExecutor(), external)
	refusal(t, err, http.StatusForbidden, "runs only on its schedule")

	manual := scheduledCheck()
	manual.TriggerType = interfaces.TriggerTypeManual
	_, err = service.ExecuteAction(asExecutor(), manual)
	refusal(t, err, http.StatusForbidden, "runs only on its schedule")

	targeted := scheduledCheck()
	targeted.InstanceIdentities = []map[string]any{{"po_id": 1}}
	_, err = service.ExecuteAction(asExecutor(), targeted)
	refusal(t, err, http.StatusBadRequest, "_instance_identities must be empty")

	_, err = service.ExecuteAction(context.Background(), scheduledCheck())
	refusal(t, err, http.StatusBadRequest, "execution subject")

	m.permissions.err = httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden)
	_, err = service.ExecuteAction(asExecutor(), scheduledCheck())
	refusal(t, err, http.StatusForbidden, "")
	if len(m.permissions.requirements) != 1 || m.permissions.requirements[0].ResourceID != eventKN+"/purchase_order" ||
		m.permissions.requirements[0].Operation != interfaces.PermissionOperationQueryData {
		t.Fatalf("a check needs to read the subject's objects, got %+v", m.permissions.requirements)
	}
	// Checking an execution in advance asks the same.
	m.permissions.err = nil
	if err := service.CheckActionExecution(asExecutor(), scheduledCheck()); err != nil {
		t.Fatalf("check: %v", err)
	}
}

func TestAnUnlicensedRecordEventActionIsUnknown(t *testing.T) {
	service, m := newEventScheduler(t)
	licenseEventActions(t, false)
	m.models.EXPECT().GetActionType(gomock.Any(), eventKN, interfaces.MAIN_BRANCH, "po_overdue_recognize").
		Return(recordEventAction(), map[string]any{}, true, nil)
	_, err := service.ExecuteAction(asExecutor(), scheduledCheck())
	refusal(t, err, http.StatusBadRequest, `unsupported action operation "event"`)

	action := recordEventAction()
	_, err = service.TrialAction(asExecutor(), eventKN, interfaces.MAIN_BRANCH, &action)
	refusal(t, err, http.StatusBadRequest, `unsupported action operation "event"`)
}

func TestRecordEventActionHonorsGovernanceScope(t *testing.T) {
	request := scheduledCheck()
	managed, err := parseManagedActionScopes(request.KNID + "/" + request.ActionTypeID)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		managed    managedActionScopes
		gate       interfaces.ActionGovernanceGate
		configErr  error
		wantStatus int
	}{
		{
			name:       "static scope requires governance even without a saved policy",
			managed:    managed,
			gate:       &dynamicScopeGateStub{},
			wantStatus: http.StatusConflict,
		},
		{
			name:       "saved policy requires governance without a static scope",
			gate:       &dynamicScopeGateStub{required: true},
			wantStatus: http.StatusConflict,
		},
		{
			name:       "policy lookup unavailable",
			gate:       &dynamicScopeGateStub{lookupErr: errors.New("policy unavailable")},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "invalid scope configuration",
			configErr:  errors.New("invalid action governance scope"),
			wantStatus: http.StatusServiceUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, m := newEventScheduler(t)
			service.managedActions = tt.managed
			service.governanceGate = tt.gate
			service.governanceConfigError = tt.configErr
			m.models.EXPECT().GetActionType(gomock.Any(), request.KNID, interfaces.MAIN_BRANCH, request.ActionTypeID).
				Return(recordEventAction(), nil, true, nil)

			// The execution log and event ledger mocks expect no writes.
			response, err := service.ExecuteAction(asExecutor(), scheduledCheck())
			if response != nil || status(err) != tt.wantStatus {
				t.Fatalf("expected %d before creating an execution, got %v, %v", tt.wantStatus, response, err)
			}
		})
	}
}

func TestAScheduledCheckRecordsItsHits(t *testing.T) {
	service, m := newEventScheduler(t)
	service.governanceGate = &dynamicScopeGateStub{}
	m.models.EXPECT().GetActionType(gomock.Any(), eventKN, interfaces.MAIN_BRANCH, "po_overdue_recognize").
		Return(recordEventAction(), map[string]any{"id": "po_overdue_recognize"}, true, nil)
	var created *interfaces.ActionExecution
	m.logs.EXPECT().CreateExecution(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, execution *interfaces.ActionExecution) error {
		created = execution
		return nil
	})
	m.logs.EXPECT().MarkExecutionRunning(gomock.Any(), eventKN, gomock.Any()).Return(nil)
	m.logs.EXPECT().GetExecutionStatus(gomock.Any(), eventKN, gomock.Any()).Return(interfaces.ExecutionStatusRunning, nil).Times(2)
	m.objects.EXPECT().ScanObjectsByObjectTypeID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(interfaces.Objects{Datas: []map[string]any{overdueOrder(1), overdueOrder(2)}}, nil)
	var check *interfaces.EventRuleCheckRequest
	var checkedAs interfaces.AccountInfo
	m.rules.EXPECT().RecordRuleCheck(gomock.Any(), eventKN, gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, _, executionID string, request *interfaces.EventRuleCheckRequest) (*interfaces.EventRuleCheckResult, error) {
			if executionID != created.ID {
				t.Errorf("the check names its execution: %s, want %s", executionID, created.ID)
			}
			checkedAs, _ = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
			check = request
			return &interfaces.EventRuleCheckResult{Events: []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`{}`)},
				Check: map[string]any{"opened": 2}}, nil
		})
	var stored []interfaces.ObjectExecutionResult
	m.logs.EXPECT().AppendResults(gomock.Any(), eventKN, gomock.Any(), 0, gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, _ int, results []interfaces.ObjectExecutionResult) error {
			stored = append(stored, results...)
			return nil
		})
	finished := make(chan *interfaces.ExecutionOutcome, 1)
	m.logs.EXPECT().FinishExecution(gomock.Any(), eventKN, gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, outcome *interfaces.ExecutionOutcome) error {
			finished <- outcome
			return nil
		})

	response, err := service.ExecuteAction(asExecutor(), scheduledCheck())
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != interfaces.ExecutionStatusPending || response.ExecutionID != created.ID {
		t.Fatalf("response = %+v", response)
	}
	if created.TriggerType != interfaces.TriggerTypeScheduled || created.ExecutionMode != interfaces.ExecutionModeOnce ||
		created.TotalCount != 1 || created.Executor != eventExecutor || created.ObjectTypeID != "purchase_order" ||
		len(created.PermissionSnapshot) != 1 {
		t.Fatalf("execution = %+v", created)
	}
	var outcome *interfaces.ExecutionOutcome
	select {
	case outcome = <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the check did not finish")
	}
	if outcome.Status != interfaces.ExecutionStatusCompleted || outcome.SuccessCount != 1 || outcome.TargetCount == nil || *outcome.TargetCount != 2 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if checkedAs != eventExecutor {
		t.Errorf("the ledger is called as the executor, got %+v", checkedAs)
	}
	if check.EventType != "po_overdue" || check.ActionTypeID != "po_overdue_recognize" || check.RuleVersion != "a1b2c3" ||
		check.Status != "ok" || check.Covered != nil || len(check.Hits) != 2 || check.Hits[0].Key != "1" || check.Hits[0].Name != "PO 1" {
		t.Fatalf("check = %+v", check)
	}
	if len(stored) != 1 || len(stored[0].Targets) != 2 || stored[0].Targets[1].Display != "PO 2" ||
		stored[0].Result.(map[string]any)["event_count"] != 2 || stored[0].Parameters["hit_count"] != 2 {
		t.Fatalf("results = %+v", stored)
	}
}

type checkRun struct {
	stored  []interfaces.ObjectExecutionResult
	outcome *interfaces.ExecutionOutcome
}

func runCheck(t *testing.T, service *actionSchedulerService, m eventMocks) *checkRun {
	t.Helper()
	run := &checkRun{}
	action := recordEventAction()
	execution := &interfaces.ActionExecution{ID: "019a0000-0000-7000-8000-000000000001", KNID: eventKN, ActionTypeID: action.ATID,
		Executor: eventExecutor, StartTime: time.Now().UnixMilli(),
		PermissionSnapshot: []interfaces.PermissionRequirement{{ResourceType: interfaces.PermissionResourceTypeObjectType,
			ResourceID: eventKN + "/purchase_order", Operation: interfaces.PermissionOperationQueryData}}}
	m.logs.EXPECT().MarkExecutionRunning(gomock.Any(), eventKN, execution.ID).Return(nil)
	m.logs.EXPECT().GetExecutionStatus(gomock.Any(), eventKN, execution.ID).Return(interfaces.ExecutionStatusRunning, nil).AnyTimes()
	m.logs.EXPECT().AppendResults(gomock.Any(), eventKN, execution.ID, 0, gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, _ int, results []interfaces.ObjectExecutionResult) error {
			run.stored = append(run.stored, results...)
			return nil
		})
	m.logs.EXPECT().FinishExecution(gomock.Any(), eventKN, execution.ID, gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, outcome *interfaces.ExecutionOutcome) error {
			run.outcome = outcome
			return nil
		})
	service.executeEventAsync(execution, &action)
	return run
}

func TestAFailedSelectionLeavesTheEventsAlone(t *testing.T) {
	service, m := newEventScheduler(t)
	m.objects.EXPECT().ScanObjectsByObjectTypeID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(interfaces.Objects{}, errors.New("opensearch is down"))
	// The ledger mock expects no call: a failed selection must not reach it.
	run := runCheck(t, service, m)
	if run.outcome.Status != interfaces.ExecutionStatusFailed || run.outcome.FailedCount != 1 {
		t.Fatalf("outcome = %+v", run.outcome)
	}
	if !strings.HasPrefix(run.stored[0].ErrorMessage, "select targets: ") || !strings.Contains(run.stored[0].ErrorMessage, "opensearch is down") {
		t.Fatalf("error = %q", run.stored[0].ErrorMessage)
	}
}

func TestALedgerRefusalFailsTheCheck(t *testing.T) {
	service, m := newEventScheduler(t)
	m.objects.EXPECT().ScanObjectsByObjectTypeID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(interfaces.Objects{Datas: []map[string]any{overdueOrder(1)}}, nil)
	m.rules.EXPECT().RecordRuleCheck(gomock.Any(), eventKN, gomock.Any(), gomock.Any()).Return(nil, &httperrors.HTTPError{
		HTTPCode: http.StatusConflict, BaseError: httperrors.BaseError{
			ErrorCode:    "BknBackend.KnEvent.RecognitionMismatch",
			Description:  "the event class is recognized by another action type",
			ErrorDetails: "po_overdue is recognized by po_overdue_v2",
		}})
	run := runCheck(t, service, m)
	if run.outcome.Status != interfaces.ExecutionStatusFailed || *run.outcome.TargetCount != 1 {
		t.Fatalf("outcome = %+v", run.outcome)
	}
	reason := run.stored[0].ErrorMessage
	if !strings.HasPrefix(reason, "record the check: BknBackend.KnEvent.RecognitionMismatch") ||
		!strings.Contains(reason, "po_overdue_v2") || !strings.Contains(reason, "HTTP 409") {
		t.Fatalf("the failure keeps the ledger's reason: %q", reason)
	}
}

func TestATruncatedCheckCoversOnlyWhatItJudged(t *testing.T) {
	action := recordEventAction()
	check := eventRuleCheck(&action, &action_target.Selection{
		Targets: []action_target.EventTarget{{EventHit: interfaces.EventHit{Key: "1", Name: "PO 1"}}},
		Covered: []string{"1"}, Limited: true, Message: strings.Repeat("m", 2000),
	})
	encoded, _ := json.Marshal(check)
	if !strings.Contains(string(encoded), `"covered":["1"]`) || len([]rune(check.Message)) != eventCheckMessageMax {
		t.Fatalf("check = %s", encoded)
	}
	full, _ := json.Marshal(eventRuleCheck(&action, &action_target.Selection{}))
	if !strings.Contains(string(full), `"covered":null`) || !strings.Contains(string(full), `"hits":[]`) || strings.Contains(string(full), `"message"`) {
		t.Fatalf("a complete check covers everything and says so: %s", full)
	}
}

func TestATrialShowsTheFirstHits(t *testing.T) {
	service, m := newEventScheduler(t)
	orders := make([]map[string]any, 0, 250)
	for i := 1; i <= 250; i++ {
		orders = append(orders, overdueOrder(i))
	}
	m.objects.EXPECT().ScanObjectsByObjectTypeID(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, query *interfaces.ObjectQueryBaseOnObjectType, _ int) (interfaces.Objects, error) {
			// The draft branch is read as the draft version of main, and so as the caller.
			pin := interfaces.KNVersionPinFromContext(ctx)
			if query.Branch != interfaces.MAIN_BRANCH || pin.QueryString() != "&kn_version=draft" || !pin.UsesCallerIdentity() {
				t.Errorf("a trial reads the version its branch names, got %q %q", query.Branch, pin.QueryString())
			}
			return interfaces.Objects{Datas: orders}, nil
		})
	action := recordEventAction()
	trial, err := service.TrialAction(asExecutor(), eventKN, "draft", &action)
	if err != nil {
		t.Fatal(err)
	}
	if trial.Event.Subject != "purchase_order" || trial.Event.HitCount != 250 || len(trial.Event.Hits) != interfaces.EventTrialHitsMax ||
		trial.Event.Limited || trial.Event.RowFilterApplied == nil {
		t.Fatalf("trial = %+v", trial.Event)
	}

	tool := recordEventAction()
	tool.OperationConfig = &interfaces.ActionOperationConfig{Type: "tool"}
	_, err = service.TrialAction(asExecutor(), eventKN, "draft", &tool)
	refusal(t, err, http.StatusBadRequest, "only a record-event action")

	invalid := recordEventAction()
	invalid.Schedule.Timezone = ""
	_, err = service.TrialAction(asExecutor(), eventKN, "draft", &invalid)
	refusal(t, err, http.StatusBadRequest, "timezone")
}

func TestATrialBranchNamesAVersion(t *testing.T) {
	for branch, want := range map[string]string{"": "", "main": "", "draft": "&kn_version=draft", "v3": "&kn_version=3"} {
		ctx, err := trialVersion(context.Background(), branch)
		if err != nil || interfaces.KNVersionPinFromContext(ctx).QueryString() != want {
			t.Errorf("%q: %q, %v", branch, interfaces.KNVersionPinFromContext(ctx).QueryString(), err)
		}
	}
	for _, branch := range []string{"feature", "v", "v0", "v03", "vx"} {
		if _, err := trialVersion(context.Background(), branch); err == nil {
			t.Errorf("%q is not a branch that can be tried", branch)
		}
	}
	pinned, _ := interfaces.NewKNVersionPin("2", "")
	if _, err := trialVersion(interfaces.WithKNVersionPin(context.Background(), pinned), "draft"); err == nil {
		t.Errorf("a version named twice is refused")
	}
	unpinned, _ := interfaces.NewKNVersionPin("", "")
	if _, err := trialVersion(interfaces.WithKNVersionPin(context.Background(), unpinned), "draft"); err != nil {
		t.Errorf("a request without kn_version carries an empty pin: %v", err)
	}
}

// A check gives the ledger each object's identity, which the handling action of an event of the
// object runs on.
func TestACheckGivesTheIdentityOfEachObject(t *testing.T) {
	action := recordEventAction()
	check := eventRuleCheck(&action, &action_target.Selection{Targets: []action_target.EventTarget{{
		Identity: map[string]any{"id": json.Number("9007199254740993")},
		EventHit: interfaces.EventHit{Key: "9007199254740993", Name: "PO"},
	}}})
	encoded, _ := json.Marshal(check.Hits)
	if !strings.Contains(string(encoded), `"identity":{"id":9007199254740993}`) {
		t.Fatalf("hits = %s", encoded)
	}
}
