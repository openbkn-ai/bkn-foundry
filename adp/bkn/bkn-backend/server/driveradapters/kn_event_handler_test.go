// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	after   int64
	limit   int
	hits    int
	err     error
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
	return interfaces.KnEventRuleCheckResult{Events: []interfaces.KnEvent{}}, s.err
}

func (s *knEventServiceStub) ListEventTypes(ctx context.Context, knID string) ([]interfaces.KnEventType, error) {
	s.remember(ctx, knID)
	return nil, s.err
}

func (s *knEventServiceStub) ListChanges(ctx context.Context, knID string, after int64, limit int) (interfaces.KnEventChangeList, error) {
	s.remember(ctx, knID)
	s.after = after
	s.limit = limit
	return interfaces.KnEventChangeList{Entries: []interfaces.KnEventChange{}, LastSeq: after}, s.err
}

func newKnEventEngine(service interfaces.KnEventServiceV1, gate gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterKnEventRoutes(engine, &common.AppSetting{}, service, gate)
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

// The paid image mounts the event routes next to the core and object-metric
// routes; gin panics at startup if their paths conflict.
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

func TestKnEventRoutesHiddenBelowTier(t *testing.T) {
	hidden := func(c *gin.Context) {
		c.AbortWithStatus(http.StatusNotFound)
	}
	stub := &knEventServiceStub{}
	engine := newKnEventEngine(stub, hidden)
	for _, path := range []string{
		"/api/bkn-backend/v1/knowledge-networks/kn1/events",
		"/api/bkn-backend/in/v1/knowledge-networks/kn1/events",
		"/api/bkn-backend/in/v1/knowledge-networks/kn1/event-changes",
	} {
		if got := serveKnEvent(engine, http.MethodGet, path, "", knEventUser).Code; got != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404 below the tier", path, got)
		}
	}
	if stub.knID != "" {
		t.Fatal("service reached below the tier")
	}
}

func TestKnEventInternalRoutesForwardCallerIdentity(t *testing.T) {
	stub := &knEventServiceStub{}
	engine := newKnEventEngine(stub, nil)
	recorder := serveKnEvent(engine, http.MethodGet,
		"/api/bkn-backend/in/v1/knowledge-networks/kn1/events?event_type=a,%20b&status=verified&object_key=PO-1&limit=20&cursor=c1",
		"", knEventUser)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	if stub.knID != "kn1" || stub.account.ID != "u1" || stub.account.Type != "user" {
		t.Fatalf("identity not forwarded: kn=%q account=%+v", stub.knID, stub.account)
	}
	want := interfaces.KnEventFilter{EventTypes: []string{"a", "b"}, Statuses: []string{"verified"}, ObjectKey: "PO-1", Cursor: "c1", Limit: 20}
	if strings.Join(stub.filter.EventTypes, ",") != "a,b" || stub.filter.Statuses[0] != "verified" ||
		stub.filter.ObjectKey != want.ObjectKey || stub.filter.Cursor != want.Cursor || stub.filter.Limit != want.Limit {
		t.Fatalf("filter %+v, want %+v", stub.filter, want)
	}
}

func TestKnEventTypeListIsNeverNull(t *testing.T) {
	engine := newKnEventEngine(&knEventServiceStub{}, nil)
	recorder := serveKnEvent(engine, http.MethodGet, "/api/bkn-backend/in/v1/knowledge-networks/kn1/event-types", "", knEventUser)
	var body struct {
		Entries []interfaces.KnEventType `json:"entries"`
		Total   int                      `json:"total"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Entries == nil || body.Total != 0 {
		t.Fatalf("body %s, want an empty entries array", recorder.Body.String())
	}
}

func TestKnEventActionRoute(t *testing.T) {
	stub := &knEventServiceStub{}
	engine := newKnEventEngine(stub, nil)
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
		engine := newKnEventEngine(stub, nil)
		got := serveKnEvent(engine, http.MethodPost, "/api/bkn-backend/in/v1/knowledge-networks/kn1/events/e1/reject", `{"reason":"x"}`, knEventUser).Code
		if got != status {
			t.Fatalf("status %d, want %d", got, status)
		}
	}
}

func TestKnEventRuleCheckBodyLimit(t *testing.T) {
	stub := &knEventServiceStub{}
	engine := newKnEventEngine(stub, nil)
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
	engine := newKnEventEngine(stub, nil)
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
	engine := newKnEventEngine(stub, nil)
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
