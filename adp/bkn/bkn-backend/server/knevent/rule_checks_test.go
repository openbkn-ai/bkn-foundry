// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// toolsStub answers for one rule tool and records who called it with what.
type toolsStub struct {
	mu        sync.Mutex
	tool      *interfaces.CallerTool
	getErr    error
	reply     *interfaces.CallerToolReply
	runErr    error
	accounts  []string
	arguments []map[string]any
}

func (t *toolsStub) seen(ctx context.Context) {
	account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.accounts = append(t.accounts, account.Type+":"+account.ID)
}

func (t *toolsStub) GetCallerTool(ctx context.Context, _, _ string) (*interfaces.CallerTool, error) {
	t.seen(ctx)
	return t.tool, t.getErr
}

func (t *toolsStub) ExecuteCallerTool(ctx context.Context, _, _ string, arguments map[string]any, _ time.Duration) (*interfaces.CallerToolReply, error) {
	t.seen(ctx)
	t.mu.Lock()
	t.arguments = append(t.arguments, arguments)
	t.mu.Unlock()
	return t.reply, t.runErr
}

func cypherTool() *interfaces.CallerTool {
	return &interfaces.CallerTool{
		ToolID: "overdue", Status: interfaces.EXEC_TOOL_STATUS_ENABLED, UpdateTime: 7,
		ScriptType: interfaces.EXEC_FUNCTION_SCRIPT_CYPHER, KnID: "kn1", ResultShape: interfaces.EXEC_FUNCTION_RESULT_TABLE,
		Inputs: []interfaces.CallerToolInput{{Name: "today", Type: "string", Required: true}, {Name: "days", Type: "number"}},
	}
}

func tableReply(body string) *interfaces.CallerToolReply {
	return &interfaces.CallerToolReply{StatusCode: http.StatusOK, Body: json.RawMessage(body)}
}

const twoHits = `{"columns":[{"name":"key"},{"name":"name"},{"name":"due_date"}],
	"entries":[{"key":"o1","name":"Order 1","due_date":"2026-11-10"},{"key":"o2","name":"Order 2","due_date":"2026-11-12"}],"limited":false}`

func ruleTool() *interfaces.KnEventRuleTool {
	return &interfaces.KnEventRuleTool{BoxID: "box", ToolID: "overdue", Parameters: map[string]json.RawMessage{
		"today": json.RawMessage(`{"check_time":"date"}`), "days": json.RawMessage(`3`),
	}}
}

func ruleInput() interfaces.KnEventTypeInput {
	return interfaces.KnEventTypeInput{
		Code: "late", Name: "Late order", Source: interfaces.KnEventSourceRule,
		RuleTool: ruleTool(), CheckSchedule: &interfaces.KnEventCheckSchedule{Cron: "0 9 * * *"},
	}
}

// at is 2026-11-15 08:00 in Shanghai.
var at = time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)

func checkingService(tools *toolsStub) *service {
	s := newTestService(&permissionsStub{})
	s.tools = tools
	s.now = func() int64 { return at.UnixMilli() }
	return s
}

func errorCode(t *testing.T, err error) string {
	t.Helper()
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error %v is not an HTTP error", err)
	}
	return httpErr.ErrorCode
}

func TestSaveRefusesARuleToolItCannotUse(t *testing.T) {
	forbidden := interfaces.NewDependencyError("execution-factory", "get_caller_tool", interfaces.DependencyForbidden, http.StatusForbidden)
	missing := interfaces.NewDependencyError("execution-factory", "get_caller_tool", interfaces.DependencyNotFound, http.StatusNotFound)
	unpublished := interfaces.NewDependencyError("execution-factory", "execute_caller_tool", interfaces.DependencyInvalidBinding, http.StatusBadRequest)
	tool := func(change func(*interfaces.CallerTool)) *interfaces.CallerTool {
		definition := cypherTool()
		change(definition)
		return definition
	}
	for _, test := range []struct {
		name   string
		tools  *toolsStub
		input  func(*interfaces.KnEventTypeInput)
		status int
		code   string
		// called is whether the tool was tried.
		called bool
	}{
		{name: "box forbidden", tools: &toolsStub{getErr: forbidden}, status: 403, code: httperrors.PublicError_Forbidden, called: true},
		{name: "tool missing", tools: &toolsStub{getErr: missing}, status: 400, code: berrors.BknBackend_KnEvent_RuleToolUnavailable, called: true},
		{name: "tool disabled", tools: &toolsStub{tool: tool(func(d *interfaces.CallerTool) { d.Status = "disabled" })},
			status: 400, code: berrors.BknBackend_KnEvent_RuleToolUnavailable, called: true},
		{name: "python function", tools: &toolsStub{tool: tool(func(d *interfaces.CallerTool) { d.ScriptType = "python"; d.ResultShape = "" })},
			status: 400, code: berrors.BknBackend_KnEvent_RuleToolNotCypherTable, called: true},
		{name: "value shape", tools: &toolsStub{tool: tool(func(d *interfaces.CallerTool) { d.ResultShape = "value" })},
			status: 400, code: berrors.BknBackend_KnEvent_RuleToolNotCypherTable, called: true},
		{name: "another network", tools: &toolsStub{tool: tool(func(d *interfaces.CallerTool) { d.KnID = "kn2" })},
			status: 400, code: berrors.BknBackend_KnEvent_RuleToolNotCypherTable, called: true},
		{name: "parameter not an input", tools: &toolsStub{tool: cypherTool()}, called: true,
			input:  func(i *interfaces.KnEventTypeInput) { i.RuleTool.Parameters["limit"] = json.RawMessage(`5`) },
			status: 400, code: berrors.BknBackend_KnEvent_InvalidParameter},
		{name: "required input missing", tools: &toolsStub{tool: cypherTool()}, called: true,
			input:  func(i *interfaces.KnEventTypeInput) { delete(i.RuleTool.Parameters, "today") },
			status: 400, code: berrors.BknBackend_KnEvent_InvalidParameter},
		{name: "box not published", tools: &toolsStub{tool: cypherTool(), runErr: unpublished}, called: true,
			status: 400, code: berrors.BknBackend_KnEvent_RuleToolUnavailable},
		{name: "statement refused for the caller", called: true,
			tools:  &toolsStub{tool: cypherTool(), reply: &interfaces.CallerToolReply{StatusCode: 403, Error: "no access to sales_order"}},
			status: 403, code: httperrors.PublicError_Forbidden},
		{name: "statement fails", called: true,
			tools:  &toolsStub{tool: cypherTool(), reply: &interfaces.CallerToolReply{StatusCode: 400, Error: "unknown label sales"}},
			status: 400, code: berrors.BknBackend_KnEvent_InvalidParameter},
		{name: "no key column", called: true,
			tools:  &toolsStub{tool: cypherTool(), reply: tableReply(`{"columns":[{"name":"id"}],"entries":[{"id":"o1"}]}`)},
			status: 400, code: berrors.BknBackend_KnEvent_InvalidParameter},
		{name: "checks too often", tools: &toolsStub{tool: cypherTool()},
			input:  func(i *interfaces.KnEventTypeInput) { i.CheckSchedule.Cron = "* * * * *" },
			status: 400, code: berrors.BknBackend_KnEvent_InvalidParameter},
		{name: "unknown time zone", tools: &toolsStub{tool: cypherTool()},
			input:  func(i *interfaces.KnEventTypeInput) { i.CheckSchedule.Timezone = "Mars/Olympus" },
			status: 400, code: berrors.BknBackend_KnEvent_InvalidParameter},
		{name: "no schedule", tools: &toolsStub{tool: cypherTool()},
			input:  func(i *interfaces.KnEventTypeInput) { i.CheckSchedule = nil },
			status: 400, code: berrors.BknBackend_KnEvent_InvalidParameter},
		{name: "report type", tools: &toolsStub{tool: cypherTool()},
			input:  func(i *interfaces.KnEventTypeInput) { i.Source = interfaces.KnEventSourceReport },
			status: 400, code: berrors.BknBackend_KnEvent_InvalidParameter},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := ruleInput()
			if test.input != nil {
				test.input(&input)
			}
			_, err := checkingService(test.tools).CreateEventType(userContext(), "kn1", input)
			if statusOf(t, err) != test.status || errorCode(t, err) != test.code {
				t.Fatalf("got %v, want %d %s", err, test.status, test.code)
			}
			if called := len(test.tools.accounts) > 0; called != test.called {
				t.Fatalf("tool called %v, want %v", test.tools.accounts, test.called)
			}
		})
	}
}

func TestStatementErrorsComeBackAsTheToolSaidThem(t *testing.T) {
	tools := &toolsStub{tool: cypherTool(), reply: &interfaces.CallerToolReply{StatusCode: 400,
		Body: json.RawMessage(`{"error_code":"BknBackend.Cypher.Syntax","description":"syntax error","error_details":"line 1"}`)}}
	_, err := checkingService(tools).CreateEventType(userContext(), "kn1", ruleInput())
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatal(err)
	}
	details, _ := httpErr.ErrorDetails.(map[string]any)
	if details["status"] != 400 || details["error"] != "syntax error: line 1" {
		t.Fatalf("details %#v", httpErr.ErrorDetails)
	}
}

func TestSaveTriesTheRuleAsTheCaller(t *testing.T) {
	tools := &toolsStub{tool: cypherTool(), reply: tableReply(strings.Replace(twoHits, `"limited":false`, `"limited":true`, 1))}
	s := checkingService(tools)
	input := ruleInput()
	check, err := validateChecks(&input, at)
	if err != nil {
		t.Fatal(err)
	}
	eventType := interfaces.KnEventType{KNID: "kn1", Code: "late", RuleTool: input.RuleTool, CheckSchedule: input.CheckSchedule}
	if err := s.prepareChecks(userContext(), &eventType, *check); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tools.accounts, []string{"user:u1", "user:u1"}) {
		t.Fatalf("called as %v, want the caller", tools.accounts)
	}
	if got := tools.arguments[0]; got["today"] != "2026-11-15" || got["days"] != json.Number("3") {
		t.Fatalf("tried with %v", got)
	}
	want := interfaces.KnEventType{
		KNID: "kn1", Code: "late", RuleTool: input.RuleTool,
		CheckSchedule: &interfaces.KnEventCheckSchedule{Cron: "0 9 * * *", Timezone: "Asia/Shanghai"},
		CheckSubject:  &interfaces.KnEventCheckSubject{ID: "u1", Type: "user", Name: "Alice"},
		RuleVersion:   ruleVersion(*input.RuleTool, 7),
		// The next 09:00 in Shanghai.
		NextCheckAt: time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC).UnixMilli(),
		Trial:       &interfaces.KnEventRuleTrial{Hits: 2, Limited: true},
	}
	if !reflect.DeepEqual(eventType, want) {
		t.Fatalf("prepared %+v\nwant %+v", eventType, want)
	}
}

func TestSaveWithoutAnAccountIsRefused(t *testing.T) {
	tools := &toolsStub{tool: cypherTool(), reply: tableReply(twoHits)}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{})
	if _, err := checkingService(tools).CreateEventType(ctx, "kn1", ruleInput()); statusOf(t, err) != http.StatusBadRequest {
		t.Fatalf("no account: %v", err)
	}
	if len(tools.accounts) != 0 {
		t.Fatalf("tool called as %v", tools.accounts)
	}
}

func TestUpdateKeepsTheChecksUnlessTheyChange(t *testing.T) {
	input := ruleInput()
	if _, err := validateChecks(&input, at); err != nil {
		t.Fatal(err)
	}
	subject := &interfaces.KnEventCheckSubject{ID: "u0", Type: "user"}
	current := interfaces.KnEventType{
		KNID: "kn1", Code: "late", Name: "Late order", Source: interfaces.KnEventSourceRule,
		RuleTool: ruleTool(), CheckSchedule: &interfaces.KnEventCheckSchedule{Cron: "0 9 * * *", Timezone: "Asia/Shanghai"},
		CheckSubject: subject, RuleVersion: "v-platform", NextCheckAt: 42, CheckFailures: 2,
	}
	if err := normalizeRuleTool(current.RuleTool); err != nil {
		t.Fatal(err)
	}

	// A new name only: checked as before, by the same subject.
	renamed := ruleInput()
	renamed.Name, renamed.RuleVersion = "Overdue order", "ignored"
	after, changed, err := updated(current, renamed, at)
	if err != nil || changed != nil {
		t.Fatalf("rename: changed %v, %v", changed, err)
	}
	if after.Name != "Overdue order" || after.CheckSubject != subject || after.RuleVersion != "v-platform" ||
		after.NextCheckAt != 42 || after.CheckFailures != 2 {
		t.Fatalf("rename changed the checks: %+v", after)
	}

	// Same schedule written differently is the same schedule.
	explicit := ruleInput()
	explicit.CheckSchedule = &interfaces.KnEventCheckSchedule{Cron: " 0 9 * * * ", Timezone: "Asia/Shanghai"}
	if _, changed, err := updated(current, explicit, at); err != nil || changed != nil {
		t.Fatalf("same schedule: changed %v, %v", changed, err)
	}

	// Another schedule is tried again.
	moved := ruleInput()
	moved.CheckSchedule.Cron = "0 10 * * *"
	if _, changed, err := updated(current, moved, at); err != nil || changed == nil {
		t.Fatalf("new schedule: changed %v, %v", changed, err)
	}

	// No rule tool: the applications check again, with their version.
	manual := interfaces.KnEventTypeInput{Name: "Late order", RuleVersion: "v-app"}
	after, changed, err = updated(current, manual, at)
	if err != nil || changed != nil {
		t.Fatalf("remove: %v", err)
	}
	if after.RuleTool != nil || after.CheckSchedule != nil || after.CheckSubject != nil ||
		after.NextCheckAt != 0 || after.CheckFailures != 0 || after.RuleVersion != "v-app" {
		t.Fatalf("removing the tool left %+v", after)
	}
}

func subjectType() interfaces.KnEventType {
	tool := ruleTool()
	_ = normalizeRuleTool(tool)
	return interfaces.KnEventType{
		KNID: "kn1", Code: "late", Name: "Late order", Source: interfaces.KnEventSourceRule,
		RuleTool: tool, CheckSchedule: &interfaces.KnEventCheckSchedule{Cron: "0 9 * * *", Timezone: "Asia/Shanghai"},
		CheckSubject: &interfaces.KnEventCheckSubject{ID: "u0", Type: "user"}, RuleVersion: "old",
	}
}

func TestACheckRunsAsItsSubject(t *testing.T) {
	tools := &toolsStub{tool: cypherTool(), reply: tableReply(twoHits)}
	s := checkingService(tools)
	eventType := subjectType()
	// The caller of a check asked for is not who it runs as.
	input := s.runCheck(subjectContext(userContext(), *eventType.CheckSubject), eventType, at)
	if !reflect.DeepEqual(tools.accounts, []string{"user:u0", "user:u0"}) {
		t.Fatalf("ran as %v, want the subject", tools.accounts)
	}
	if input.Status != interfaces.KnEventCheckOK || input.RuleVersion != ruleVersion(*eventType.RuleTool, 7) ||
		len(input.Hits) != 2 || input.Covered != nil || input.Message != "" {
		t.Fatalf("check %+v", input)
	}
	if input.Hits[0].Attributes["due_date"].Label != "2026-11-10" {
		t.Fatalf("hit %+v", input.Hits[0])
	}
}

func TestACheckThatCannotDecideSaysWhy(t *testing.T) {
	forbidden := interfaces.NewDependencyError("execution-factory", "execute_caller_tool", interfaces.DependencyForbidden, http.StatusForbidden)
	down := interfaces.NewDependencyError("execution-factory", "execute_caller_tool", interfaces.DependencyUnavailable, http.StatusBadGateway)
	for _, test := range []struct {
		name  string
		tools *toolsStub
		want  string
	}{
		{"subject refused the box", &toolsStub{tool: cypherTool(), runErr: forbidden}, checkText("NoPermission", nil, "")},
		{"subject refused the data", &toolsStub{tool: cypherTool(), reply: &interfaces.CallerToolReply{StatusCode: 403, Error: "x"}},
			checkText("NoPermission", nil, "")},
		{"tool disabled", &toolsStub{tool: func() *interfaces.CallerTool { d := cypherTool(); d.Status = "disabled"; return d }()},
			checkText("ToolUnavailable", nil, "")},
		{"tool no longer Cypher", &toolsStub{tool: func() *interfaces.CallerTool { d := cypherTool(); d.ScriptType = "python"; return d }()},
			checkText("ToolUnavailable", nil, "")},
		{"statement timed out", &toolsStub{tool: cypherTool(), reply: &interfaces.CallerToolReply{StatusCode: 504, Error: "did not finish within 90 seconds"}},
			checkText("Failed", map[string]any{"error": "did not finish within 90 seconds"}, "")},
		{"execution factory down", &toolsStub{tool: cypherTool(), runErr: down}, checkText("Failed", map[string]any{"error": down.Error()}, "")},
		{"no key column", &toolsStub{tool: cypherTool(), reply: tableReply(`{"columns":[{"name":"id"}],"entries":[]}`)},
			checkText("NoKeyColumn", nil, "")},
	} {
		t.Run(test.name, func(t *testing.T) {
			eventType := subjectType()
			input := checkingService(test.tools).runCheck(subjectContext(context.Background(), *eventType.CheckSubject), eventType, at)
			if input.Status != interfaces.KnEventCheckUnknown || input.Message != test.want || input.Hits != nil {
				t.Fatalf("check %+v, want unknown with %q", input, test.want)
			}
			if test.want == "" {
				t.Fatal("the message has no text")
			}
		})
	}
}

func TestATruncatedResultCoversOnlyWhatItReturned(t *testing.T) {
	tools := &toolsStub{tool: cypherTool(), reply: tableReply(strings.Replace(twoHits, `"limited":false`, `"limited":true`, 1))}
	eventType := subjectType()
	input := checkingService(tools).runCheck(subjectContext(context.Background(), *eventType.CheckSubject), eventType, at)
	if input.Status != interfaces.KnEventCheckOK || !reflect.DeepEqual(input.Covered, []string{"o1", "o2"}) ||
		input.Message != checkText("Limited", nil, "") {
		t.Fatalf("check %+v", input)
	}
}

// typeRow is a stored event type as the store reads it.
func typeRow(eventType interfaces.KnEventType) *sqlmock.Rows {
	tool := ""
	if eventType.RuleTool != nil {
		encoded, _ := json.Marshal(eventType.RuleTool)
		tool = string(encoded)
	}
	var check interfaces.KnEventCheckSchedule
	if eventType.CheckSchedule != nil {
		check = *eventType.CheckSchedule
	}
	var subject interfaces.KnEventCheckSubject
	if eventType.CheckSubject != nil {
		subject = *eventType.CheckSubject
	}
	return sqlmock.NewRows(typeColumns).AddRow(
		eventType.KNID, eventType.Code, eventType.Name, eventType.Source, "", "", 0.0, "{}", eventType.RuleVersion,
		"u0", "user", 1, "u0", "user", 1, "ot", "ds",
		tool, check.Cron, check.Timezone, subject.ID, subject.Type, eventType.NextCheckAt, eventType.CheckFailures,
	)
}

func TestApplicationsCannotCheckARuleThePlatformChecks(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT f_last_seq FROM " + headTable).WillReturnRows(sqlmock.NewRows([]string{"f_last_seq"}).AddRow(0))
	mock.ExpectQuery("FROM " + typeTable).WillReturnRows(typeRow(subjectType()))
	mock.ExpectRollback()
	s := checkingService(&toolsStub{})
	s.store = newStore(db)

	_, err = s.RecordRuleCheck(userContext(), "kn1", interfaces.KnEventRuleCheckInput{EventType: "late", Status: "ok"})
	if statusOf(t, err) != http.StatusConflict || errorCode(t, err) != berrors.BknBackend_KnEvent_CheckedByPlatform {
		t.Fatalf("got %v, want 409", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoredChecksReadBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stored := subjectType()
	stored.NextCheckAt, stored.CheckFailures = 1234, 3
	mock.ExpectQuery("FROM " + typeTable).WillReturnRows(typeRow(stored))
	got, found, err := newStore(db).getType(context.Background(), "kn1", "late")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if !sameChecks(got, stored) || got.NextCheckAt != 1234 || got.CheckFailures != 3 || got.RuleVersion != "old" {
		t.Fatalf("read back %+v", got)
	}
	values, err := checkColumnsOf(interfaces.KnEventType{})
	if err != nil {
		t.Fatal(err)
	}
	if values["f_rule_tool"] != nil || values["f_check_cron"] != "" || values["f_next_check_at"] != int64(0) {
		t.Fatalf("a type without checks stores %v", values)
	}
}
