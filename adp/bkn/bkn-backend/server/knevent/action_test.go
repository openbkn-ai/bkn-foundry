// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/mock/gomock"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
	corelogics "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

type toolsStub struct {
	tool *interfaces.CallerTool
	err  error
}

func (s toolsStub) GetCallerTool(context.Context, string, string) (*interfaces.CallerTool, error) {
	return s.tool, s.err
}

func (s toolsStub) ExecuteCallerTool(context.Context, string, string, map[string]any, time.Duration) (*interfaces.CallerToolReply, error) {
	return nil, errors.New("not used")
}

type trialStub struct {
	calls   int
	account interfaces.AccountInfo
	branch  string
	trial   *interfaces.ActionEventTrial
	err     error
}

func (s *trialStub) TrialEventAction(ctx context.Context, branch string, _ *interfaces.ActionType) (*interfaces.ActionEventTrial, error) {
	s.calls++
	s.account, _ = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	s.branch = branch
	return s.trial, s.err
}

var recognitionQuery = regexp.QuoteMeta("SELECT f_id FROM t_action_type WHERE f_kn_id = ?")

// recognition is a record-event action type of late_order, about order.
func recognition() *interfaces.ActionType {
	return &interfaces.ActionType{
		ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
			ATID: "late_order_check", ATName: "识别订单超期", ActionType: interfaces.ACTION_TYPE_ADD,
			ObjectTypeID: "order",
			Condition: &interfaces.ActionCondCfg{
				Field: "due", Operation: "<", ValueFrom: "check_time", Value: "-PT48H",
			},
			OperationConfig: &interfaces.ActionOperationConfig{Type: interfaces.ActionOperationEvent, EventClass: "late_order"},
			Schedule:        interfaces.Schedule{Type: interfaces.ActionScheduleCron, Expression: "0 8 * * *", Timezone: "Asia/Shanghai"},
		},
		KNID: "kn1", Branch: interfaces.MAIN_BRANCH,
	}
}

type actionFixture struct {
	action  *eventAction
	db      sqlmock.Sqlmock
	objects *bmock.MockObjectTypeAccess
	actions *bmock.MockActionTypeAccess
	trial   *trialStub
}

func newActionFixture(t *testing.T) *actionFixture {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctrl := gomock.NewController(t)
	f := &actionFixture{
		db: mock, objects: bmock.NewMockObjectTypeAccess(ctrl), actions: bmock.NewMockActionTypeAccess(ctrl),
		trial: &trialStub{trial: &interfaces.ActionEventTrial{Subject: "order", Hits: []map[string]any{}}},
	}
	f.action = &eventAction{
		store: newStore(db), objects: f.objects, actions: f.actions, trial: f.trial,
		available: func() bool { return true },
	}
	return f
}

// classIs answers the lookup of late_order with class, or with nothing.
func (f *actionFixture) classIs(class *interfaces.ObjectType) {
	found := []*interfaces.ObjectType{}
	if class != nil {
		found = append(found, class)
	}
	f.objects.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"late_order"}).
		Return(found, nil)
}

func (f *actionFixture) recognizedBy(ids ...string) {
	rows := sqlmock.NewRows([]string{"f_id"})
	for _, id := range ids {
		rows.AddRow(id)
	}
	f.db.ExpectQuery(recognitionQuery).WithArgs("kn1", interfaces.MAIN_BRANCH, interfaces.ActionOperationEvent, "late_order").
		WillReturnRows(rows)
}

func (f *actionFixture) stored(actions ...*interfaces.ActionType) {
	f.actions.EXPECT().GetActionTypesByIDs(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, []string{"late_order_check"}).
		Return(actions, nil)
}

func savedBy(id string) context.Context {
	return context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: id, Type: "user"})
}

func TestPrepareRefusesAnActionTypeItsClassCannotTake(t *testing.T) {
	report := newClass("late_order")
	report.Event.Source = interfaces.KnEventSourceReport
	plain := newClass("late_order")
	plain.Kinds, plain.Event = nil, nil
	otherSubject := newClass("late_order")
	otherSubject.Event.Subject = "customer"
	for name, class := range map[string]*interfaces.ObjectType{
		"no class": nil, "not an event class": plain, "a reported class": report, "another subject": otherSubject,
	} {
		t.Run(name, func(t *testing.T) {
			f := newActionFixture(t)
			f.classIs(class)
			err := f.action.Prepare(savedBy("u1"), nil, recognition(), nil)
			if statusOf(t, err) != http.StatusBadRequest {
				t.Fatalf("%v, want 400", err)
			}
		})
	}
}

func TestPrepareTakesTheClassSavedWithTheActionType(t *testing.T) {
	f := newActionFixture(t)
	f.recognizedBy()
	f.stored()
	batch := &interfaces.BatchIDIndex{ObjectTypes: map[string]*interfaces.ObjectType{"late_order": newClass("late_order")}}
	if err := f.action.Prepare(savedBy("u1"), nil, recognition(), batch); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRefusesASecondActionTypeOfTheClass(t *testing.T) {
	f := newActionFixture(t)
	f.classIs(newClass("late_order"))
	f.recognizedBy("late_order_check", "late_order_check_old")
	err := f.action.Prepare(savedBy("u1"), nil, recognition(), nil)
	if statusOf(t, err) != http.StatusConflict {
		t.Fatalf("%v, want 409", err)
	}
}

func prepared(t *testing.T, ctx context.Context, at *interfaces.ActionType, stored ...*interfaces.ActionType) *interfaces.ActionType {
	t.Helper()
	f := newActionFixture(t)
	f.classIs(newClass("late_order"))
	f.recognizedBy("late_order_check")
	f.stored(stored...)
	if err := f.action.Prepare(ctx, nil, at, nil); err != nil {
		t.Fatal(err)
	}
	return at
}

func TestRuleVersionFollowsWhatTheActionTypeFinds(t *testing.T) {
	first := prepared(t, savedBy("u1"), recognition())
	if len(first.RuleVersion) != 12 {
		t.Fatalf("rule version %q, want 12 hex characters", first.RuleVersion)
	}
	renamed := recognition()
	renamed.ATName = "另一个名字"
	renamed.Schedule.Expression = "0 9 * * *"
	if prepared(t, savedBy("u1"), renamed).RuleVersion != first.RuleVersion {
		t.Fatal("a new name or schedule finds the same objects: the rule version stays")
	}
	changed := recognition()
	changed.Condition.Value = "-PT24H"
	if prepared(t, savedBy("u1"), changed).RuleVersion == first.RuleVersion {
		t.Fatal("a new condition is a new rule version")
	}
}

func TestRuleVersionOfAFunctionFollowsTheFunction(t *testing.T) {
	version := func(updated int64) string {
		f := newActionFixture(t)
		f.action.tools = toolsStub{tool: &interfaces.CallerTool{UpdateTime: updated}}
		f.classIs(newClass("late_order"))
		f.recognizedBy()
		f.stored()
		at := recognition()
		at.Condition = nil
		at.OperationConfig.Targets = &interfaces.ActionEventTargets{Function: &interfaces.ActionFunctionTargets{BoxID: "b", ToolID: "t"}}
		if err := f.action.Prepare(savedBy("u1"), nil, at, nil); err != nil {
			t.Fatal(err)
		}
		return at.RuleVersion
	}
	if version(1) == version(2) {
		t.Fatal("a changed function is a new rule version")
	}
}

func TestAFunctionThatCannotBeReadRefusesASaveThroughTheAPI(t *testing.T) {
	at := recognition()
	at.Condition = nil
	at.OperationConfig.Targets = &interfaces.ActionEventTargets{Function: &interfaces.ActionFunctionTargets{BoxID: "b", ToolID: "t"}}
	f := newActionFixture(t)
	f.action.tools = toolsStub{err: errors.New("no such tool")}
	f.classIs(newClass("late_order"))
	f.recognizedBy()
	err := f.action.Prepare(corelogics.WithActionTrial(savedBy("u1")), nil, at, nil)
	if statusOf(t, err) != http.StatusBadRequest {
		t.Fatalf("%v, want 400", err)
	}
}

func TestChecksRunAsWhoeverLastChangedWhatTheyFind(t *testing.T) {
	before := prepared(t, savedBy("u1"), recognition())
	if before.ExecutionSubject == nil || before.ExecutionSubject.ID != "u1" {
		t.Fatalf("subject %+v, want the saver of a new action type", before.ExecutionSubject)
	}

	renamed := recognition()
	renamed.ATName = "改个名字"
	renamed.ExecutionSubject = &interfaces.AccountInfo{ID: "intruder", Type: "user"}
	if got := prepared(t, savedBy("u2"), renamed, before).ExecutionSubject; got == nil || got.ID != "u1" {
		t.Fatalf("subject %+v, want u1 kept: a rename changes nothing the checks do", got)
	}

	rescheduled := recognition()
	rescheduled.Schedule.Timezone = "UTC"
	if got := prepared(t, savedBy("u2"), rescheduled, before).ExecutionSubject; got == nil || got.ID != "u2" {
		t.Fatalf("subject %+v, want u2, who changed when the checks run", got)
	}

	changed := recognition()
	changed.Condition.Value = "-PT1H"
	if got := prepared(t, savedBy("u2"), changed, before).ExecutionSubject; got == nil || got.ID != "u2" {
		t.Fatalf("subject %+v, want u2, who changed what the checks find", got)
	}
}

func TestASaveWithoutAnAccountIsRefused(t *testing.T) {
	f := newActionFixture(t)
	f.classIs(newClass("late_order"))
	f.recognizedBy()
	f.stored()
	err := f.action.Prepare(context.Background(), nil, recognition(), nil)
	if statusOf(t, err) != http.StatusBadRequest {
		t.Fatalf("%v, want 400", err)
	}
}

func TestTrialRunsOnlyForASaveThroughTheAPI(t *testing.T) {
	f := newActionFixture(t)
	f.classIs(newClass("late_order"))
	f.recognizedBy()
	f.stored()
	at := recognition()
	if err := f.action.Prepare(savedBy("u1"), nil, at, nil); err != nil {
		t.Fatal(err)
	}
	if f.trial.calls != 0 || at.Trial != nil {
		t.Fatal("an import runs no trial")
	}

	f = newActionFixture(t)
	f.classIs(newClass("late_order"))
	f.recognizedBy()
	f.stored()
	at = recognition()
	if err := f.action.Prepare(corelogics.WithActionTrial(savedBy("u1")), nil, at, nil); err != nil {
		t.Fatal(err)
	}
	if f.trial.calls != 1 || f.trial.account.ID != "u1" || f.trial.branch != interfaces.MAIN_BRANCH || at.Trial != f.trial.trial {
		t.Fatalf("trial %+v, action trial %+v", f.trial, at.Trial)
	}

	f = newActionFixture(t)
	f.trial.err = httperrors.NewHTTPError(context.Background(), http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter)
	f.classIs(newClass("late_order"))
	f.recognizedBy()
	f.stored()
	err := f.action.Prepare(corelogics.WithActionTrial(savedBy("u1")), nil, recognition(), nil)
	if statusOf(t, err) != http.StatusBadRequest {
		t.Fatalf("%v, want the trial's 400", err)
	}
}

func TestAClassItsActionTypeRecordsKeepsItsSubjectAndIsNotDeleted(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	kind := &eventKind{store: newStore(db), available: func() bool { return true }}
	mock.ExpectQuery(recognitionQuery).WillReturnRows(sqlmock.NewRows([]string{"f_id"}).AddRow("late_order_check"))
	err = kind.recognizedBy(userContext(), nil, newClass("late_order"), "delete")
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusConflict ||
		httpErr.ErrorCode != berrors.BknBackend_KnEvent_RecognizedByAction {
		t.Fatalf("%v, want 409 %s", err, berrors.BknBackend_KnEvent_RecognizedByAction)
	}
	mock.ExpectQuery(recognitionQuery).WillReturnRows(sqlmock.NewRows([]string{"f_id"}))
	if err := kind.recognizedBy(userContext(), nil, newClass("late_order"), "delete"); err != nil {
		t.Fatal(err)
	}
}
