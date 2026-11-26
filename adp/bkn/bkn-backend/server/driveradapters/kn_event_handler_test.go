// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// knEventServiceStub records what the handler passed on.
type knEventServiceStub struct {
	interfaces.KnEventServiceV1
	account interfaces.AccountInfo
	knID    string
	action  string
	reason  string
	filter  interfaces.KnEventFilter
	stats   interfaces.KnEventStatsFilter
	after   int64
	limit   int
	hits    int
	err     error
	// executionID and actionTypeID are what a rule check was sent for.
	executionID  string
	actionTypeID string
}

func (s *knEventServiceStub) remember(ctx context.Context, knID string) {
	s.knID = knID
	if account, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo); ok {
		s.account = account
	}
}

func (s *knEventServiceStub) ListEvents(ctx context.Context, knID string, filter interfaces.KnEventFilter) (interfaces.KnEventList, error) {
	s.remember(ctx, knID)
	s.filter = filter
	return interfaces.KnEventList{Entries: []interfaces.KnEvent{}}, s.err
}

func (s *knEventServiceStub) ChangeEvent(ctx context.Context, knID, _ string, action string, input interfaces.KnEventReasonInput) (interfaces.KnEventsResult, error) {
	s.remember(ctx, knID)
	s.action = action
	s.reason = input.Reason
	return interfaces.KnEventsResult{Events: []interfaces.KnEvent{{ID: "e1"}}}, s.err
}

func (s *knEventServiceStub) RecordRuleCheck(ctx context.Context, knID string, input interfaces.KnEventRuleCheckInput) (interfaces.KnEventRuleCheckResult, error) {
	s.remember(ctx, knID)
	s.hits = len(input.Hits)
	s.executionID = interfaces.ExecutionIDOf(ctx)
	s.actionTypeID = input.ActionTypeID
	return interfaces.KnEventRuleCheckResult{Events: []interfaces.KnEvent{}}, s.err
}

func (s *knEventServiceStub) CountEvents(ctx context.Context, knID string, filter interfaces.KnEventStatsFilter) (interfaces.KnEventStats, error) {
	s.remember(ctx, knID)
	s.stats = filter
	return interfaces.KnEventStats{Entries: []interfaces.KnEventStatEntry{}}, s.err
}

func (s *knEventServiceStub) ListChanges(ctx context.Context, knID string, after int64, limit int) (interfaces.KnEventChangeList, error) {
	s.remember(ctx, knID)
	s.after = after
	s.limit = limit
	return interfaces.KnEventChangeList{Entries: []interfaces.KnEventChange{}, LastSeq: after}, s.err
}

func newKnEventEngine(service interfaces.KnEventServiceV1) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterKnEventRoutes(engine, &common.AppSetting{}, service, nil)
	return engine
}

func serveKnEvent(engine *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

var knEventUser = map[string]string{
	interfaces.HTTP_HEADER_ACCOUNT_ID:   "u1",
	interfaces.HTTP_HEADER_ACCOUNT_TYPE: "user",
}

// The event routes are mounted next to the core routes and the paid image's
// object-metric routes; gin panics at startup if their paths conflict.
func TestKnEventRoutesCoexistWithCoreRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	appSetting := &common.AppSetting{}
	(&restHandler{appSetting: appSetting}).RegisterPublic(engine)
	RegisterObjectMetricRoutes(engine, appSetting, nil, nil)
	RegisterKnEventRoutes(engine, appSetting, &knEventServiceStub{}, nil)
	if got := serveKnEvent(engine, http.MethodGet, "/api/bkn-backend/in/v1/knowledge-networks/kn1/events", "", knEventUser).Code; got != http.StatusOK {
		t.Fatalf("event list next to the core routes: status %d", got)
	}
}

func TestKnEventInternalRoutesForwardCallerIdentity(t *testing.T) {
	stub := &knEventServiceStub{}
	engine := newKnEventEngine(stub)
	recorder := serveKnEvent(engine, http.MethodGet,
		"/api/bkn-backend/in/v1/knowledge-networks/kn1/events?event_type=a,%20b&status=verified&object_key=PO-1&from=5&to=9&limit=20&cursor=c1",
		"", knEventUser)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	if stub.knID != "kn1" || stub.account.ID != "u1" || stub.account.Type != "user" {
		t.Fatalf("identity not forwarded: kn=%q account=%+v", stub.knID, stub.account)
	}
	want := interfaces.KnEventFilter{EventTypes: []string{"a", "b"}, Statuses: []string{"verified"}, ObjectKey: "PO-1",
		From: 5, To: 9, Cursor: "c1", Limit: 20}
	if !reflect.DeepEqual(stub.filter, want) {
		t.Fatalf("filter %+v, want %+v", stub.filter, want)
	}
	for _, query := range []string{"from=-1", "to=x"} {
		if got := serveKnEvent(engine, http.MethodGet, "/api/bkn-backend/in/v1/knowledge-networks/kn1/events?"+query, "", knEventUser).Code; got != http.StatusBadRequest {
			t.Fatalf("events?%s: status %d, want 400", query, got)
		}
	}
}

func TestKnEventStatsRoute(t *testing.T) {
	stub := &knEventServiceStub{}
	engine := newKnEventEngine(stub)
	path := "/api/bkn-backend/in/v1/knowledge-networks/kn1/event-stats"
	recorder := serveKnEvent(engine, http.MethodGet, path+"?event_type=a&event_type=b&object_key=PO-1&from=5&to=9", "", knEventUser)
	if recorder.Code != http.StatusOK || stub.knID != "kn1" || stub.account.ID != "u1" {
		t.Fatalf("status %d kn %q account %+v: %s", recorder.Code, stub.knID, stub.account, recorder.Body.String())
	}
	if want := (interfaces.KnEventStatsFilter{EventTypes: []string{"a", "b"}, ObjectKey: "PO-1", From: 5, To: 9}); !reflect.DeepEqual(stub.stats, want) {
		t.Fatalf("filter %+v, want %+v", stub.stats, want)
	}
	var body struct {
		Entries []interfaces.KnEventStatEntry `json:"entries"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Entries == nil {
		t.Fatalf("body %s, want an entries array", recorder.Body.String())
	}
	if got := serveKnEvent(engine, http.MethodGet, path+"?to=-3", "", knEventUser).Code; got != http.StatusBadRequest {
		t.Fatalf("negative end: status %d, want 400", got)
	}
}

// Event classes are object types now: the event type routes are gone.
func TestKnEventTypeRoutesAreGone(t *testing.T) {
	engine := newKnEventEngine(&knEventServiceStub{})
	for _, prefix := range []string{"/api/bkn-backend/v1", "/api/bkn-backend/in/v1"} {
		for _, path := range []string{"/knowledge-networks/kn1/event-types", "/knowledge-networks/kn1/event-types/late_order"} {
			if got := serveKnEvent(engine, http.MethodGet, prefix+path, "", knEventUser).Code; got != http.StatusNotFound {
				t.Fatalf("%s%s: status %d, want 404", prefix, path, got)
			}
		}
	}
}

// The gate stands before every event route, the internal change feed included, and before the
// caller is authenticated.
func TestKnEventGateStandsBeforeEveryRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	stub := &knEventServiceStub{}
	RegisterKnEventRoutes(engine, &common.AppSetting{}, stub, func(c *gin.Context) {
		c.AbortWithStatus(http.StatusTeapot)
	})
	for _, route := range [][2]string{
		{http.MethodGet, "/api/bkn-backend/v1/knowledge-networks/kn1/events"},
		{http.MethodPost, "/api/bkn-backend/v1/knowledge-networks/kn1/rule-checks"},
		{http.MethodGet, "/api/bkn-backend/in/v1/knowledge-networks/kn1/event-stats"},
		{http.MethodPost, "/api/bkn-backend/in/v1/knowledge-networks/kn1/events/e1/close"},
		{http.MethodGet, "/api/bkn-backend/in/v1/knowledge-networks/kn1/event-changes"},
	} {
		if got := serveKnEvent(engine, route[0], route[1], "{}", nil).Code; got != http.StatusTeapot {
			t.Fatalf("%s %s: status %d, want the gate's", route[0], route[1], got)
		}
	}
	if stub.knID != "" {
		t.Fatalf("the service was reached for %q", stub.knID)
	}
}

func TestKnEventActionRoute(t *testing.T) {
	stub := &knEventServiceStub{}
	engine := newKnEventEngine(stub)
	path := "/api/bkn-backend/in/v1/knowledge-networks/kn1/events/e1/"

	if got := serveKnEvent(engine, http.MethodPost, path+"archive", "{}", knEventUser).Code; got != http.StatusNotFound {
		t.Fatalf("unknown action: status %d, want 404", got)
	}
	if got := serveKnEvent(engine, http.MethodPost, path+"verify", "", knEventUser).Code; got != http.StatusOK || stub.action != "verify" {
		t.Fatalf("verify without a body: status %d action %q", got, stub.action)
	}
	if got := serveKnEvent(engine, http.MethodPost, path+"close", `{"reason":"done"}`, knEventUser).Code; got != http.StatusOK || stub.reason != "done" {
		t.Fatalf("close: status %d reason %q", got, stub.reason)
	}
	if got := serveKnEvent(engine, http.MethodPost, path+"close", `{"reason":`, knEventUser).Code; got != http.StatusBadRequest {
		t.Fatalf("broken body: status %d, want 400", got)
	}
}

func TestKnEventServiceErrorsKeepTheirStatus(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusConflict} {
		stub := &knEventServiceStub{err: httperrors.NewHTTPError(context.Background(), status, berrors.BknBackend_KnEvent_ActionNotAllowed)}
		engine := newKnEventEngine(stub)
		got := serveKnEvent(engine, http.MethodPost, "/api/bkn-backend/in/v1/knowledge-networks/kn1/events/e1/reject", `{"reason":"x"}`, knEventUser).Code
		if got != status {
			t.Fatalf("status %d, want %d", got, status)
		}
	}
}

func TestKnEventRuleCheckBodyLimit(t *testing.T) {
	stub := &knEventServiceStub{}
	engine := newKnEventEngine(stub)
	path := "/api/bkn-backend/in/v1/knowledge-networks/kn1/rule-checks"
	body := `{"event_type":"t","status":"ok","hits":[{"key":"` + strings.Repeat("x", knEventBodyLimit) + `"}]}`
	if got := serveKnEvent(engine, http.MethodPost, path, body, knEventUser).Code; got != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: status %d, want 413", got)
	}
	if got := serveKnEvent(engine, http.MethodPost, path, `{"event_type":"t","status":"ok","hits":[{"key":"a"},{"key":"b"}]}`, knEventUser).Code; got != http.StatusOK || stub.hits != 2 {
		t.Fatalf("rule check: status %d hits %d", got, stub.hits)
	}
}

// A misspelt field is refused rather than stored as its default.
func TestKnEventBodiesRefuseUnknownFields(t *testing.T) {
	stub := &knEventServiceStub{}
	engine := newKnEventEngine(stub)
	base := "/api/bkn-backend/in/v1/knowledge-networks/kn1/"
	for path, body := range map[string]string{
		"rule-checks":     `{"event_type":"t","status":"ok","hit":[{"key":"a"}]}`,
		"events/e1/close": `{"reasn":"done"}`,
	} {
		if got := serveKnEvent(engine, http.MethodPost, base+path, body, knEventUser).Code; got != http.StatusBadRequest {
			t.Fatalf("%s with an unknown field: status %d, want 400", path, got)
		}
	}
	if got := serveKnEvent(engine, http.MethodPost, base+"events/e1/verify", "", knEventUser).Code; got != http.StatusOK {
		t.Fatalf("verify without a body: status %d, want 200", got)
	}
}

func TestKnEventChangesNeedNoIdentity(t *testing.T) {
	stub := &knEventServiceStub{}
	engine := newKnEventEngine(stub)
	path := "/api/bkn-backend/in/v1/knowledge-networks/kn1/event-changes"
	recorder := serveKnEvent(engine, http.MethodGet, path+"?after=41&limit=7", "", nil)
	if recorder.Code != http.StatusOK || stub.after != 41 || stub.limit != 7 || stub.account.ID != "" {
		t.Fatalf("status %d after %d limit %d account %+v", recorder.Code, stub.after, stub.limit, stub.account)
	}
	if got := serveKnEvent(engine, http.MethodGet, path+"?after=-1", "", nil).Code; got != http.StatusBadRequest {
		t.Fatalf("negative cursor: status %d, want 400", got)
	}
	if got := serveKnEvent(engine, http.MethodGet, "/api/bkn-backend/v1/knowledge-networks/kn1/event-changes", "", nil).Code; got != http.StatusNotFound {
		t.Fatalf("public change feed: status %d, want 404", got)
	}
}

// A record-event action type sends its checks through the internal API, with the execution they
// belong to.
func TestKnEventRuleCheckOfAnActionExecution(t *testing.T) {
	stub := &knEventServiceStub{}
	engine := newKnEventEngine(stub)
	path := "/api/bkn-backend/in/v1/knowledge-networks/kn1/rule-checks"
	body := `{"event_type":"t","action_type_id":"at1","status":"ok","hits":[{"key":"a","detail":{"metric":{"value":1}}}]}`
	headers := map[string]string{interfaces.HTTP_HEADER_EXECUTION_ID: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"}
	for key, value := range knEventUser {
		headers[key] = value
	}
	if got := serveKnEvent(engine, http.MethodPost, path, body, headers).Code; got != http.StatusOK {
		t.Fatalf("status %d", got)
	}
	if stub.executionID != headers[interfaces.HTTP_HEADER_EXECUTION_ID] || stub.actionTypeID != "at1" {
		t.Fatalf("execution %q action type %q", stub.executionID, stub.actionTypeID)
	}
	headers[interfaces.HTTP_HEADER_EXECUTION_ID] = "not an id"
	if got := serveKnEvent(engine, http.MethodPost, path, body, headers).Code; got != http.StatusBadRequest {
		t.Fatalf("a malformed execution ID: status %d, want 400", got)
	}
}

// Only the platform runs record-event action types: the public API does not take their checks.
func TestKnEventPublicRuleCheckTakesNoActionType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &knEventServiceStub{}
	handler := &restHandler{kes: stub}
	engine := gin.New()
	engine.POST("/rule-checks", func(c *gin.Context) {
		handler.recordKnEventRuleCheck(c, context.WithValue(context.Background(), knEventPublicKey{}, true))
	})
	if got := serveKnEvent(engine, http.MethodPost, "/rule-checks", `{"event_type":"t","action_type_id":"at1","status":"ok","hits":[]}`, nil).Code; got != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", got)
	}
	if got := serveKnEvent(engine, http.MethodPost, "/rule-checks", `{"event_type":"t","status":"ok","hits":[]}`, nil).Code; got != http.StatusOK {
		t.Fatalf("a check without an action type: status %d, want 200", got)
	}
}
