package logsvc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type fakeSource struct {
	id      string
	records []observabilityvo.LogRecord
	err     error
}

type capturingSource struct {
	query observabilityvo.LogQuery
}

func (source *capturingSource) ID() string { return "capturing" }

func (source *capturingSource) Search(_ context.Context, query observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	source.query = query
	return observabilityvo.SourcePage{CountAccuracy: "exact"}, nil
}

type fakeDetailSource struct {
	fakeSource
	record observabilityvo.LogRecord
}

func (source fakeDetailSource) Get(context.Context, string) (observabilityvo.LogRecord, bool, error) {
	return validTestRecord(source.record), source.record.LogID != "", nil
}

func (source fakeDetailSource) Metadata() observabilityvo.SourceStatus {
	categories := []string{observabilityvo.CategoryRuntimeSystem}
	if len(source.records) > 0 && source.records[0].Category != "" {
		categories = []string{source.records[0].Category}
	}
	return observabilityvo.SourceStatus{
		SourceID: source.id, Status: "healthy", Reliability: "best_effort",
		CollectionMethod: "direct_otlp", CoveredModules: []string{"context-loader"}, CountAccuracy: "exact",
		Categories: categories,
	}
}

func (source fakeSource) ID() string { return source.id }

func (source fakeSource) Search(
	context.Context,
	observabilityvo.LogQuery,
) (observabilityvo.SourcePage, error) {
	if source.err != nil {
		return observabilityvo.SourcePage{}, source.err
	}
	return observabilityvo.SourcePage{Records: validTestRecords(source.records), Count: int64(len(source.records)), CountAccuracy: "exact"}, nil
}

type categorizedSource struct {
	id         string
	categories []string
	records    []observabilityvo.LogRecord
	err        error
	queries    int
}

func (source *categorizedSource) ID() string { return source.id }

func (source *categorizedSource) Metadata() observabilityvo.SourceStatus {
	return observabilityvo.SourceStatus{
		SourceID: source.id, Status: "healthy", Reliability: "best_effort",
		CountAccuracy: "exact", Categories: append([]string(nil), source.categories...),
	}
}

func (source *categorizedSource) Search(
	_ context.Context,
	_ observabilityvo.LogQuery,
) (observabilityvo.SourcePage, error) {
	source.queries++
	if source.err != nil {
		return observabilityvo.SourcePage{}, source.err
	}
	return observabilityvo.SourcePage{
		Records: validTestRecords(source.records), Count: int64(len(source.records)), CountAccuracy: "exact",
	}, nil
}

func TestListRejectsGlobalSearchForNormalUserButAllowsOwnedTraceDrilldown(t *testing.T) {
	service := New([]Source{fakeSource{id: "otel", records: []observabilityvo.LogRecord{
		ownedBusinessLog("log-owned", "owner-a", "trace-a"),
		ownedBusinessLog("log-other", "owner-b", "trace-a"),
	}}})
	profile := activeProfile("owner-a", "normal_user")

	if _, err := service.List(context.Background(), profile, observabilityvo.LogQuery{}); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("normal user global search must be denied, got %v", err)
	}
	result, err := service.List(context.Background(), profile, observabilityvo.LogQuery{TraceID: "trace-a"})
	if err != nil {
		t.Fatalf("owned trace drilldown failed: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].LogID != "log-owned" {
		t.Fatalf("trace drilldown leaked another owner: %+v", result.Records)
	}
}

func TestListEnforcesManagedNetworkAllOf(t *testing.T) {
	service := New([]Source{fakeSource{id: "otel", records: []observabilityvo.LogRecord{
		managedBusinessLog("log-a", []string{"kn-a"}),
		managedBusinessLog("log-ab", []string{"kn-a", "kn-b"}),
	}}})
	profile := activeProfile("builder-a", "network_builder")
	profile.ManagedKnowledgeNetworkIDs = []string{"kn-a"}

	result, err := service.List(context.Background(), profile, observabilityvo.LogQuery{})
	if err != nil {
		t.Fatalf("builder search failed: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].LogID != "log-a" {
		t.Fatalf("all-of scope was not enforced: %+v", result.Records)
	}
}

func TestListReportsPartialAndFailsWhenEveryAuthorizedSourceFails(t *testing.T) {
	profile := activeProfile("admin-a", "admin")
	available := fakeSource{id: "otel", records: []observabilityvo.LogRecord{
		{LogID: "system-a", Category: observabilityvo.CategoryRuntimeSystem, EventName: "service.started", TenantID: "tenant-a", EventTimestamp: time.Now(), TrustLevel: "trusted", IngressPrincipal: "otel-gateway"},
	}}
	unavailable := fakeSource{id: "safe", err: errors.New("source unavailable")}

	result, err := New([]Source{available, unavailable}).List(context.Background(), profile, observabilityvo.LogQuery{})
	if err != nil {
		t.Fatalf("partial result should succeed: %v", err)
	}
	if !result.Partial || len(result.SourceStatus) != 2 || len(result.Records) != 1 {
		t.Fatalf("partial source state not disclosed: %+v", result)
	}

	_, err = New([]Source{unavailable}).List(context.Background(), profile, observabilityvo.LogQuery{})
	if !errors.Is(err, ErrSourcesUnavailable) {
		t.Fatalf("all failed sources must return sources unavailable, got %v", err)
	}
}

func TestDetailAndFacetsUseTheSameRecordAuthorization(t *testing.T) {
	owned := ownedBusinessLog("owned", "owner-a", "trace-a")
	other := ownedBusinessLog("other", "owner-b", "trace-a")
	owned.EventName = "knowledge.read.completed"
	other.EventName = "knowledge.read.completed"
	service := New([]Source{
		fakeDetailSource{fakeSource: fakeSource{id: "owned-source", records: []observabilityvo.LogRecord{owned, other}}, record: other},
	})
	profile := activeProfile("owner-a", "normal_user")

	if _, err := service.Get(context.Background(), profile, "other"); !errors.Is(err, ErrNotDisclosed) {
		t.Fatalf("unauthorized detail must be hidden as not disclosed, got %v", err)
	}
	facets, err := service.Facets(context.Background(), profile, observabilityvo.LogQuery{TraceID: "trace-a"}, "event_name")
	if err != nil {
		t.Fatalf("authorized facet failed: %v", err)
	}
	if len(facets.Values) != 1 || facets.Values[0].Count != 1 {
		t.Fatalf("facet leaked an unauthorized record: %+v", facets)
	}
}

func TestSourcesAndPoliciesFollowTheAccessProfile(t *testing.T) {
	service := New([]Source{fakeDetailSource{fakeSource: fakeSource{id: "otel"}}})
	admin := activeProfile("admin-a", "admin")

	sources, err := service.Sources(context.Background(), admin)
	if err != nil || len(sources) != 1 || sources[0].CollectionMethod != "direct_otlp" {
		t.Fatalf("source coverage missing: sources=%+v err=%v", sources, err)
	}
	policies, err := service.Policies(admin)
	if err != nil || len(policies) != 3 {
		t.Fatalf("admin runtime policies missing: policies=%+v err=%v", policies, err)
	}
	if _, err := service.Policies(activeProfile("user-a", "normal_user")); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("normal user policy read must be denied, got %v", err)
	}
}

func TestNotIntegratedAuthorizedSourcesAreDisclosedWithoutFalseHealth(t *testing.T) {
	service := New([]Source{
		NewNotIntegratedSource("bkn-safe-access", []string{observabilityvo.CategoryAccessUser}, []string{"oauth"}),
		NewNotIntegratedSource("bkn-safe-security", []string{observabilityvo.CategoryAuditSecurity}, []string{"authz"}),
	})
	profile := activeProfile("security-a", "security")
	statuses, err := service.Sources(context.Background(), profile)
	if err != nil || len(statuses) != 2 {
		t.Fatalf("missing authorized source coverage: statuses=%+v err=%v", statuses, err)
	}
	for _, status := range statuses {
		if status.Status != "not_integrated" || status.CollectionMethod != "not_integrated" {
			t.Fatalf("source readiness was overstated: %+v", status)
		}
	}
	if _, err := service.List(context.Background(), profile, observabilityvo.LogQuery{}); !errors.Is(err, ErrSourcesUnavailable) {
		t.Fatalf("all not-integrated sources must not return a false empty success: %v", err)
	}
}

func TestListPushesTrustedAuthorizationScopeToSources(t *testing.T) {
	source := &capturingSource{}
	service := New([]Source{source})
	profile := activeProfile("builder-a", "network_builder")
	profile.ManagedKnowledgeNetworkIDs = []string{"kn-a", "kn-b"}

	if _, err := service.List(context.Background(), profile, observabilityvo.LogQuery{}); err != nil {
		t.Fatalf("builder list failed: %v", err)
	}
	if source.query.AuthorizedTenantID != "tenant-a" || source.query.AuthorizedBusinessDomain != "domain-a" {
		t.Fatalf("trusted isolation scope was not pushed down: %+v", source.query)
	}
	if len(source.query.AuthorizedCategories) != 3 || len(source.query.AuthorizedKnowledgeNetworkIDs) != 2 {
		t.Fatalf("role and managed-network scope was not pushed down: %+v", source.query)
	}
	if source.query.TimeFrom == nil || source.query.TimeTo == nil || source.query.TimeTo.Sub(*source.query.TimeFrom) != time.Hour {
		t.Fatalf("default one-hour window was not frozen: %+v", source.query)
	}
}

func TestListRejectsTimeWindowsLongerThanSevenDays(t *testing.T) {
	now := time.Now().UTC()
	from := now.Add(-8 * 24 * time.Hour)
	_, err := New([]Source{fakeSource{id: "runtime"}}).List(
		context.Background(), activeProfile("admin-a", "admin"),
		observabilityvo.LogQuery{TimeFrom: &from, TimeTo: &now},
	)
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("oversized time window must be rejected, got %v", err)
	}
}

func TestListDoesNotQueryOrDiscloseUnauthorizedSources(t *testing.T) {
	runtimeSource := &categorizedSource{
		id: "runtime", categories: []string{observabilityvo.CategoryRuntimeSystem},
		records: []observabilityvo.LogRecord{{
			LogID: "system-a", Category: observabilityvo.CategoryRuntimeSystem, EventName: "service.started",
			TenantID: "tenant-a", EventTimestamp: time.Now(), TrustLevel: "trusted", IngressPrincipal: "otel-gateway",
		}},
	}
	auditSource := &categorizedSource{
		id: "admin-audit", categories: []string{observabilityvo.CategoryAuditAdmin},
		err: errors.New("must not be queried"),
	}

	result, err := New([]Source{runtimeSource, auditSource}).List(
		context.Background(), activeProfile("admin-a", "admin"), observabilityvo.LogQuery{},
	)
	if err != nil {
		t.Fatalf("authorized runtime query failed: %v", err)
	}
	if auditSource.queries != 0 {
		t.Fatalf("unauthorized source was queried %d times", auditSource.queries)
	}
	if result.Partial || len(result.SourceStatus) != 1 || result.SourceStatus[0].SourceID != "runtime" {
		t.Fatalf("unauthorized source leaked through status: %+v", result)
	}
}

func TestListExcludesUntrustedUnknownAndCategoryMismatchedRecords(t *testing.T) {
	now := time.Now().UTC()
	base := observabilityvo.LogRecord{
		Category: observabilityvo.CategoryRuntimeSystem, EventName: "service.started",
		TenantID: "tenant-a", EventTimestamp: now, TrustLevel: "trusted", IngressPrincipal: "otel-gateway",
	}
	trusted := base
	trusted.LogID = "trusted"
	untrusted := base
	untrusted.LogID, untrusted.TrustLevel = "untrusted", "untrusted"
	unknown := base
	unknown.LogID, unknown.EventName = "unknown", "plugin.custom.event"
	mismatch := base
	mismatch.LogID, mismatch.EventName = "mismatch", "knowledge.read.completed"

	result, err := New([]Source{fakeSource{id: "runtime", records: []observabilityvo.LogRecord{
		trusted, untrusted, unknown, mismatch,
	}}}).List(context.Background(), activeProfile("admin-a", "admin"), observabilityvo.LogQuery{})
	if err != nil {
		t.Fatalf("query logs: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].LogID != "trusted" {
		t.Fatalf("quarantined records escaped the query projection: %+v", result.Records)
	}
}

func TestListUsesSignedCursorAndRejectsTamperingOrScopeChanges(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	source := fakeSource{id: "runtime", records: []observabilityvo.LogRecord{
		{LogID: "log-3", Category: observabilityvo.CategoryRuntimeSystem, EventName: "service.started", TenantID: "tenant-a", EventTimestamp: base, TrustLevel: "trusted", IngressPrincipal: "otel-gateway"},
		{LogID: "log-2", Category: observabilityvo.CategoryRuntimeSystem, EventName: "service.started", TenantID: "tenant-a", EventTimestamp: base.Add(-time.Second), TrustLevel: "trusted", IngressPrincipal: "otel-gateway"},
		{LogID: "log-1", Category: observabilityvo.CategoryRuntimeSystem, EventName: "service.started", TenantID: "tenant-a", EventTimestamp: base.Add(-2 * time.Second), TrustLevel: "trusted", IngressPrincipal: "otel-gateway"},
	}}
	service := NewWithCursorKey([]Source{source}, []byte("test-cursor-signing-key"))
	profile := activeProfile("admin-a", "admin")
	profile.Fingerprint = "sha256:scope-a"

	first, err := service.List(context.Background(), profile, observabilityvo.LogQuery{Limit: 2})
	if err != nil || len(first.Records) != 2 || first.NextCursor == "" {
		t.Fatalf("first page missing stable cursor: result=%+v err=%v", first, err)
	}
	second, err := service.List(context.Background(), profile, observabilityvo.LogQuery{Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Records) != 1 || second.Records[0].LogID != "log-1" {
		t.Fatalf("second page is unstable: result=%+v err=%v", second, err)
	}

	tampered := first.NextCursor[:len(first.NextCursor)-1] + "x"
	if _, err := service.List(context.Background(), profile, observabilityvo.LogQuery{Limit: 2, Cursor: tampered}); !errors.Is(err, ErrCursorInvalid) {
		t.Fatalf("tampered cursor must be rejected, got %v", err)
	}
	profile.Fingerprint = "sha256:scope-b"
	if _, err := service.List(context.Background(), profile, observabilityvo.LogQuery{Limit: 2, Cursor: first.NextCursor}); !errors.Is(err, ErrCursorStale) {
		t.Fatalf("scope-changed cursor must be stale, got %v", err)
	}
}

func activeProfile(subject, role string) evidencevo.AccessProfile {
	return evidencevo.AccessProfile{
		TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: subject,
		Roles: []string{role}, AccountActive: true, TenantActive: true,
	}
}

func ownedBusinessLog(id, owner, traceID string) observabilityvo.LogRecord {
	return observabilityvo.LogRecord{
		LogID: id, Category: observabilityvo.CategoryRuntimeBusiness, EventName: "knowledge.read.completed", TenantID: "tenant-a",
		BusinessDomain: "domain-a", EffectiveSubjectID: owner, TraceID: traceID, EventTimestamp: time.Now(),
		TrustLevel: "trusted", IngressPrincipal: "otel-gateway",
	}
}

func managedBusinessLog(id string, networks []string) observabilityvo.LogRecord {
	return observabilityvo.LogRecord{
		LogID: id, Category: observabilityvo.CategoryRuntimeBusiness, EventName: "knowledge.read.completed", TenantID: "tenant-a",
		BusinessDomain: "domain-a", EffectiveSubjectID: "other-a", KnowledgeNetworkIDs: networks,
		EventTimestamp: time.Now(), TrustLevel: "trusted", IngressPrincipal: "otel-gateway",
	}
}

func validTestRecords(records []observabilityvo.LogRecord) []observabilityvo.LogRecord {
	result := make([]observabilityvo.LogRecord, len(records))
	for index, record := range records {
		result[index] = validTestRecord(record)
	}
	return result
}

func validTestRecord(record observabilityvo.LogRecord) observabilityvo.LogRecord {
	if record.SchemaVersion == "" {
		record.SchemaVersion = "1.0.0"
	}
	if record.SourceID == "" {
		record.SourceID = "test-source"
	}
	if record.SourceLogID == "" {
		record.SourceLogID = record.LogID
	}
	if record.ObservedTimestamp.IsZero() {
		record.ObservedTimestamp = record.EventTimestamp
	}
	if record.SeverityNumber == 0 {
		record.SeverityNumber, record.SeverityText = 9, "INFO"
	}
	if record.Outcome == "" {
		record.Outcome = "success"
	}
	if record.ServiceName == "" {
		record.ServiceName = "test-service"
	}
	if record.Environment == "" {
		record.Environment = "test"
	}
	return record
}
