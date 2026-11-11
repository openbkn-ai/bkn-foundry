// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

type networksStub map[string]bool

func (n networksStub) CheckKNExistByID(_ context.Context, knID, branch string) (string, bool, error) {
	if branch != interfaces.MAIN_BRANCH {
		return "", false, errors.New("events read the main branch only")
	}
	return knID, n[knID], nil
}

// permissionsStub records the operations checked and denies those listed.
type permissionsStub struct {
	mu      sync.Mutex
	checked []string
	deny    bool
}

func (p *permissionsStub) CheckPermission(ctx context.Context, resource interfaces.PermissionResource, ops []string) error {
	if resource.Type != interfaces.RESOURCE_TYPE_KN {
		return errors.New("events check knowledge-network permissions")
	}
	p.mu.Lock()
	p.checked = append(p.checked, resource.ID+":"+strings.Join(ops, ","))
	p.mu.Unlock()
	if p.deny {
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
	}
	return nil
}

type namesStub struct {
	name string
	err  error
}

func (n namesStub) GetAccountNames(_ context.Context, accounts []*interfaces.AccountInfo) error {
	for _, account := range accounts {
		account.Name = n.name
	}
	return n.err
}

// newTestService has no store: a test that reaches it fails loudly.
func newTestService(permissions *permissionsStub) *service {
	return &service{
		store:       newStore(nil),
		permissions: permissions,
		networks:    networksStub{"kn1": true},
		names:       namesStub{name: "Alice"},
		now:         func() int64 { return 100 },
		newID:       func() string { return "id" },
	}
}

func userContext() context.Context {
	return context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "u1", Type: "user"})
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error %v is not an HTTP error", err)
	}
	return httpErr.HTTPCode
}

// calls reach every method of the service; each stops before the store.
var calls = []struct {
	name      string
	operation string
	call      func(*service, context.Context, string) error
}{
	{"list types", "view_detail", func(s *service, ctx context.Context, kn string) error {
		_, err := s.ListEventTypes(ctx, kn)
		return err
	}},
	{"get type", "view_detail", func(s *service, ctx context.Context, kn string) error {
		_, err := s.GetEventType(ctx, kn, "t")
		return err
	}},
	{"create type", "modify", func(s *service, ctx context.Context, kn string) error {
		_, err := s.CreateEventType(ctx, kn, interfaces.KnEventTypeInput{Code: "t", Name: "T", Source: "rule"})
		return err
	}},
	{"update type", "modify", func(s *service, ctx context.Context, kn string) error {
		_, err := s.UpdateEventType(ctx, kn, "t", interfaces.KnEventTypeInput{Name: "T"})
		return err
	}},
	{"delete type", "modify", func(s *service, ctx context.Context, kn string) error {
		return s.DeleteEventType(ctx, kn, "t")
	}},
	{"list events", "query_data", func(s *service, ctx context.Context, kn string) error {
		_, err := s.ListEvents(ctx, kn, interfaces.KnEventFilter{})
		return err
	}},
	{"get event", "query_data", func(s *service, ctx context.Context, kn string) error {
		_, err := s.GetEvent(ctx, kn, "e")
		return err
	}},
	{"report", "query_data", func(s *service, ctx context.Context, kn string) error {
		_, err := s.ReportEvent(ctx, kn, interfaces.KnEventReportInput{EventType: "t", Title: "x"})
		return err
	}},
	{"change", "query_data", func(s *service, ctx context.Context, kn string) error {
		_, err := s.ChangeEvent(ctx, kn, "e", "verify", interfaces.KnEventReasonInput{})
		return err
	}},
	{"rule check", "query_data", func(s *service, ctx context.Context, kn string) error {
		_, err := s.RecordRuleCheck(ctx, kn, interfaces.KnEventRuleCheckInput{EventType: "t", Status: "ok"})
		return err
	}},
	{"list checks", "query_data", func(s *service, ctx context.Context, kn string) error {
		_, err := s.ListRuleChecks(ctx, kn)
		return err
	}},
}

func TestEveryCallChecksTheNetworkThenItsPermission(t *testing.T) {
	for _, test := range calls {
		t.Run(test.name, func(t *testing.T) {
			permissions := &permissionsStub{}
			err := test.call(newTestService(permissions), userContext(), "missing")
			if statusOf(t, err) != http.StatusNotFound || len(permissions.checked) != 0 {
				t.Fatalf("missing network: %v, checked %v", err, permissions.checked)
			}
			permissions = &permissionsStub{deny: true}
			err = test.call(newTestService(permissions), userContext(), "kn1")
			if statusOf(t, err) != http.StatusForbidden {
				t.Fatalf("denied: %v", err)
			}
			if len(permissions.checked) != 1 || permissions.checked[0] != "kn1:"+test.operation {
				t.Fatalf("checked %v, want kn1:%s", permissions.checked, test.operation)
			}
		})
	}
}

func TestChangeFeedChecksOnlyTheNetwork(t *testing.T) {
	permissions := &permissionsStub{deny: true}
	_, err := newTestService(permissions).ListChanges(context.Background(), "missing", 0, 0)
	if statusOf(t, err) != http.StatusNotFound || len(permissions.checked) != 0 {
		t.Fatalf("change feed: %v, checked %v", err, permissions.checked)
	}
}

func TestInvalidInputIsRefusedBeforeTheStore(t *testing.T) {
	ctx := userContext()
	tooManyHits := make([]interfaces.KnEventRuleHit, interfaces.KnEventRuleHitsMax+1)
	for index := range tooManyHits {
		tooManyHits[index].Key = "k"
	}
	attributes := map[string]interfaces.KnEventAttribute{}
	for index := 0; index <= interfaces.KnEventAttributesMax; index++ {
		attributes[strings.Repeat("a", index+1)] = interfaces.KnEventAttribute{}
	}
	for _, test := range []struct {
		name string
		want int
		call func(*service) error
	}{
		{"code pattern", 400, func(s *service) error {
			_, err := s.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: "Late Order", Name: "n", Source: "rule"})
			return err
		}},
		{"code length", 400, func(s *service) error {
			_, err := s.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: strings.Repeat("a", 65), Name: "n", Source: "rule"})
			return err
		}},
		{"name required", 400, func(s *service) error {
			_, err := s.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: "a", Name: "  ", Source: "rule"})
			return err
		}},
		{"source", 400, func(s *service) error {
			_, err := s.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: "a", Name: "n", Source: "manual"})
			return err
		}},
		{"merge hours", 400, func(s *service) error {
			_, err := s.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: "a", Name: "n", Source: "report", MergeHours: -1})
			return err
		}},
		{"rule size", 413, func(s *service) error {
			rule := []byte(`"` + strings.Repeat("x", interfaces.KnEventRuleMaxBytes) + `"`)
			_, err := s.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: "a", Name: "n", Source: "rule", Rule: rule})
			return err
		}},
		{"title required", 400, func(s *service) error {
			_, err := s.ReportEvent(ctx, "kn1", interfaces.KnEventReportInput{EventType: "t", Title: " "})
			return err
		}},
		{"event type required", 400, func(s *service) error {
			_, err := s.ReportEvent(ctx, "kn1", interfaces.KnEventReportInput{Title: "x"})
			return err
		}},
		{"attributes count", 400, func(s *service) error {
			_, err := s.ReportEvent(ctx, "kn1", interfaces.KnEventReportInput{EventType: "t", Title: "x", ObjectAttributes: attributes})
			return err
		}},
		{"attribute key length", 400, func(s *service) error {
			_, err := s.ReportEvent(ctx, "kn1", interfaces.KnEventReportInput{EventType: "t", Title: "x",
				ObjectAttributes: map[string]interfaces.KnEventAttribute{"c": {Key: strings.Repeat("k", 251)}}})
			return err
		}},
		{"reason length", 400, func(s *service) error {
			_, err := s.ChangeEvent(ctx, "kn1", "e", "reject", interfaces.KnEventReasonInput{Reason: strings.Repeat("r", 513)})
			return err
		}},
		{"check status", 400, func(s *service) error {
			_, err := s.RecordRuleCheck(ctx, "kn1", interfaces.KnEventRuleCheckInput{EventType: "t", Status: "maybe"})
			return err
		}},
		{"hit key", 400, func(s *service) error {
			_, err := s.RecordRuleCheck(ctx, "kn1", interfaces.KnEventRuleCheckInput{EventType: "t", Status: "ok",
				Hits: []interfaces.KnEventRuleHit{{Name: "no key"}}})
			return err
		}},
		{"hit count", 413, func(s *service) error {
			_, err := s.RecordRuleCheck(ctx, "kn1", interfaces.KnEventRuleCheckInput{EventType: "t", Status: "ok", Hits: tooManyHits})
			return err
		}},
		{"list status", 400, func(s *service) error {
			_, err := s.ListEvents(ctx, "kn1", interfaces.KnEventFilter{Statuses: []string{"open"}})
			return err
		}},
		{"list cursor", 400, func(s *service) error {
			_, err := s.ListEvents(ctx, "kn1", interfaces.KnEventFilter{Cursor: "not a cursor"})
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := statusOf(t, test.call(newTestService(&permissionsStub{}))); got != test.want {
				t.Fatalf("status %d, want %d", got, test.want)
			}
		})
	}
}

func TestCursorRoundTrip(t *testing.T) {
	cursor, err := decodeCursor(encodeCursor(&interfaces.KnEvent{RecordedAt: 1730000000000, ID: "0192-ab:c"}))
	if err != nil || cursor.recordedAt != 1730000000000 || cursor.id != "0192-ab:c" {
		t.Fatalf("cursor %+v, %v", cursor, err)
	}
}

func TestActorNameIsBestEffort(t *testing.T) {
	s := newTestService(&permissionsStub{})
	if actor := s.actor(userContext()); actor.Name != "Alice" || actor.ID != "u1" {
		t.Fatalf("actor %+v", actor)
	}
	s.names = namesStub{name: "partial", err: errors.New("directory down")}
	if actor := s.actor(userContext()); actor.Name != "" || actor.ID != "u1" {
		t.Fatalf("a directory outage gave %+v", actor)
	}
	s.names = namesStub{name: "-"}
	if actor := s.actor(userContext()); actor.Name != "" {
		t.Fatalf("an unknown account is named %q", actor.Name)
	}
}

func TestRuleEventTitleFollowsTheLanguage(t *testing.T) {
	s := newTestService(&permissionsStub{})
	for lang, want := range map[i18n.Language]string{i18n.SimplifiedChinese: "低库存：SKU 1", i18n.AmericanEnglish: "低库存: SKU 1"} {
		o := s.op(i18n.WithLanguage(userContext(), lang))
		if got := o.title("低库存", "SKU 1"); got != want {
			t.Fatalf("%s title %q, want %q", lang, got, want)
		}
	}
}
