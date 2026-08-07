package logsvc

import (
	"context"
	"errors"
	"sync/atomic"
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

type blockingSource struct {
	id      string
	started chan<- string
	release <-chan struct{}
	active  *atomic.Int32
	peak    *atomic.Int32
}

func (source *blockingSource) ID() string { return source.id }

func (source *blockingSource) Metadata() observabilityvo.SourceStatus {
	return observabilityvo.SourceStatus{
		SourceID: source.id, Status: "healthy", Reliability: "best_effort",
		Categories: []string{observabilityvo.CategoryRuntimeSystem},
	}
}

func (source *blockingSource) Search(ctx context.Context, _ observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	active := source.active.Add(1)
	defer source.active.Add(-1)
	for {
		peak := source.peak.Load()
		if active <= peak || source.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	select {
	case source.started <- source.id:
	case <-ctx.Done():
		return observabilityvo.SourcePage{}, ctx.Err()
	}
	select {
	case <-source.release:
		return observabilityvo.SourcePage{CountAccuracy: "exact"}, nil
	case <-ctx.Done():
		return observabilityvo.SourcePage{}, ctx.Err()
	}
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

func TestListDisclosesDurableDegradedCoverageAfterSuccessfulSourceQuery(t *testing.T) {
	coverageStore := &coverageStore{coverage: observabilityvo.SourceCoverage{
		SourceID: "runtime", DeploymentID: "local", State: observabilityvo.SourceCoverageDegraded,
		Reason: "telemetry_dropped", DroppedRecords: 4,
	}}
	service := NewWithOptions([]Source{fakeSource{id: "runtime", records: []observabilityvo.LogRecord{{
		LogID: "log-1", Category: observabilityvo.CategoryRuntimeSystem, TenantID: "tenant-a",
		EventTimestamp: time.Now().UTC(), TrustLevel: "trusted", IngressPrincipal: "otel-gateway",
	}}}}, Options{
		CursorKey: []byte("coverage-test"), CoverageStore: coverageStore, CoverageDeploymentID: "local",
	})

	result, err := service.List(context.Background(), activeProfile("admin-a", "admin"), observabilityvo.LogQuery{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !result.Partial || len(result.SourceStatus) != 1 {
		t.Fatalf("expected partial result with one source status: %+v", result)
	}
	status := result.SourceStatus[0]
	if status.Status != "degraded" || status.Reason != "telemetry_dropped" || status.DroppedRecords == nil || *status.DroppedRecords != 4 {
		t.Fatalf("durable coverage degradation was hidden: %+v", status)
	}
}

func TestListReturnsLogsButMarksCoveragePartialWhenStateCannotBeRead(t *testing.T) {
	coverageStore := &coverageStore{err: errors.New("coverage database unavailable")}
	service := NewWithOptions([]Source{fakeSource{id: "runtime", records: []observabilityvo.LogRecord{{
		LogID: "log-1", Category: observabilityvo.CategoryRuntimeSystem, TenantID: "tenant-a",
		EventTimestamp: time.Now().UTC(), TrustLevel: "trusted", IngressPrincipal: "otel-gateway",
	}}}}, Options{
		CursorKey: []byte("coverage-error-test"), CoverageStore: coverageStore, CoverageDeploymentID: "local",
	})

	result, err := service.List(context.Background(), activeProfile("admin-a", "admin"), observabilityvo.LogQuery{Limit: 10})
	if err != nil || !result.Partial || len(result.SourceStatus) != 1 {
		t.Fatalf("expected available partial log result, got result=%+v err=%v", result, err)
	}
	if result.SourceStatus[0].Status != "degraded" || result.SourceStatus[0].Reason != "source_status_probe_failed" {
		t.Fatalf("coverage uncertainty was not disclosed: %+v", result.SourceStatus[0])
	}
}

type coverageStore struct {
	coverage observabilityvo.SourceCoverage
	err      error
}

func (store *coverageStore) Get(context.Context, string, string) (observabilityvo.SourceCoverage, bool, error) {
	return store.coverage, store.coverage.SourceID != "", store.err
}

func (store *coverageStore) UpsertDegraded(context.Context, observabilityvo.SourceCoverage) error {
	return nil
}

func (store *coverageStore) MarkHealthyAfterCatchUp(context.Context, string, string, uint64) error {
	return nil
}

type categorizedSource struct {
	id         string
	categories []string
	records    []observabilityvo.LogRecord
	err        error
	queries    int
}

type filteredPageSource struct {
	pages [][]observabilityvo.LogRecord
}

func (source *filteredPageSource) ID() string { return "filtered-pages" }

func (source *filteredPageSource) Search(
	_ context.Context,
	query observabilityvo.LogQuery,
) (observabilityvo.SourcePage, error) {
	page := 0
	if query.PageBefore != nil {
		page = 1
	}
	if page >= len(source.pages) {
		return observabilityvo.SourcePage{CountAccuracy: "exact"}, nil
	}
	records := validTestRecords(source.pages[page])
	return observabilityvo.SourcePage{
		Records: records, Count: int64(len(records) + boolInt(page+1 < len(source.pages))), CountAccuracy: "exact",
	}, nil
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

func TestListQueriesIndependentSourcesConcurrently(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32
	service := NewWithOptions([]Source{
		&blockingSource{id: "source-a", started: started, release: release, active: &active, peak: &peak},
		&blockingSource{id: "source-b", started: started, release: release, active: &active, peak: &peak},
	}, Options{CursorKey: []byte("test-cursor-key"), SourceTimeout: time.Second, MaxConcurrentSources: 2})

	done := make(chan error, 1)
	go func() {
		_, err := service.List(context.Background(), activeProfile("admin-a", "admin"), observabilityvo.LogQuery{})
		done <- err
	}()
	for index := 0; index < 2; index++ {
		select {
		case <-started:
		case <-time.After(250 * time.Millisecond):
			close(release)
			t.Fatal("sources were queried serially")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("concurrent source query failed: %v", err)
	}
	if peak.Load() != 2 {
		t.Fatalf("peak source concurrency = %d, want 2", peak.Load())
	}
}

func TestListDoesNotExceedConfiguredSourceConcurrency(t *testing.T) {
	started := make(chan string, 4)
	release := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32
	sources := make([]Source, 4)
	for index := range sources {
		sources[index] = &blockingSource{
			id: "source-" + time.Duration(index).String(), started: started, release: release,
			active: &active, peak: &peak,
		}
	}
	service := NewWithOptions(sources, Options{
		CursorKey: []byte("test-cursor-key"), SourceTimeout: time.Second, MaxConcurrentSources: 2,
	})

	done := make(chan error, 1)
	go func() {
		_, err := service.List(context.Background(), activeProfile("admin-a", "admin"), observabilityvo.LogQuery{})
		done <- err
	}()
	for index := 0; index < 2; index++ {
		select {
		case <-started:
		case <-time.After(250 * time.Millisecond):
			close(release)
			t.Fatal("configured source workers did not start")
		}
	}
	select {
	case sourceID := <-started:
		close(release)
		t.Fatalf("source %s exceeded the configured concurrency bound", sourceID)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("bounded source query failed: %v", err)
	}
	if peak.Load() > 2 {
		t.Fatalf("peak source concurrency = %d, want at most 2", peak.Load())
	}
}

func TestListTimesOutOneSourceAndReturnsTheHealthySource(t *testing.T) {
	started := make(chan string, 1)
	release := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32
	slow := &blockingSource{id: "slow", started: started, release: release, active: &active, peak: &peak}
	healthy := categorizedSource{
		id: "healthy", categories: []string{observabilityvo.CategoryRuntimeSystem},
		records: []observabilityvo.LogRecord{{
			LogID: "system-a", Category: observabilityvo.CategoryRuntimeSystem, EventName: "service.started",
			TenantID: "tenant-a", EventTimestamp: time.Now(), TrustLevel: "trusted", IngressPrincipal: "otel-gateway",
		}},
	}
	service := NewWithOptions([]Source{slow, &healthy}, Options{
		CursorKey: []byte("test-cursor-key"), SourceTimeout: 20 * time.Millisecond, MaxConcurrentSources: 2,
	})

	startedAt := time.Now()
	result, err := service.List(context.Background(), activeProfile("admin-a", "admin"), observabilityvo.LogQuery{})
	if err != nil {
		t.Fatalf("healthy source must survive another source timeout: %v", err)
	}
	// Keep a coarse wall-clock guard against an unbounded wait. The precise
	// 20 ms deadline is asserted by the source_timeout status below; a tighter
	// wall-clock threshold is flaky when the full suite saturates a 4C8G host.
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("source timeout did not bound request latency: %s", elapsed)
	}
	if !result.Partial || len(result.Records) != 1 || len(result.SourceStatus) != 2 {
		t.Fatalf("timeout partial result is incomplete: %+v", result)
	}
	for _, status := range result.SourceStatus {
		if status.SourceID == "slow" {
			if status.Status != "unavailable" || status.Reason != "source_timeout" || status.LatencyMS == nil {
				t.Fatalf("slow source timeout was not disclosed: %+v", status)
			}
			return
		}
	}
	t.Fatal("slow source status is missing")
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

func TestSourcesHealthCheckUsesTheConfiguredSourceTimeout(t *testing.T) {
	started := make(chan string, 1)
	release := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32
	service := NewWithOptions([]Source{
		&blockingSource{id: "slow", started: started, release: release, active: &active, peak: &peak},
		&categorizedSource{id: "healthy", categories: []string{observabilityvo.CategoryRuntimeSystem}},
	}, Options{CursorKey: []byte("test-cursor-key"), SourceTimeout: 20 * time.Millisecond, MaxConcurrentSources: 2})

	startedAt := time.Now()
	statuses, err := service.Sources(context.Background(), activeProfile("admin-a", "admin"))
	if err != nil {
		t.Fatalf("source health query failed: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("source health query was not bounded by its timeout: %s", elapsed)
	}
	if len(statuses) != 2 || statuses[0].SourceID != "slow" || statuses[0].Reason != "source_timeout" ||
		statuses[1].Status != "healthy" {
		t.Fatalf("source health status is incomplete: %+v", statuses)
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
	if source.query.TimeFrom == nil || source.query.TimeTo == nil || source.query.TimeTo.Sub(*source.query.TimeFrom) != 24*time.Hour {
		t.Fatalf("default 24-hour window was not frozen: %+v", source.query)
	}
	if source.query.ObservedBefore == nil || !source.query.ObservedBefore.Equal(*source.query.TimeTo) {
		t.Fatalf("query watermark was not pushed to the source: %+v", source.query)
	}
}

func TestListUsesSevenDayWindowForAssociatedDrilldown(t *testing.T) {
	source := &capturingSource{}
	service := New([]Source{source})
	profile := activeProfile("admin-a", "admin")

	if _, err := service.List(context.Background(), profile, observabilityvo.LogQuery{TraceID: "trace-a"}); err != nil {
		t.Fatalf("associated trace query failed: %v", err)
	}
	if source.query.TimeFrom == nil || source.query.TimeTo == nil || source.query.TimeTo.Sub(*source.query.TimeFrom) != 7*24*time.Hour {
		t.Fatalf("associated drilldown did not use the seven-day window: %+v", source.query)
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

func TestListUsesTheContractTieBreakersAcrossSources(t *testing.T) {
	timestamp := time.Now().UTC().Truncate(time.Second)
	result, err := New([]Source{
		fakeSource{id: "adapter-a", records: []observabilityvo.LogRecord{{
			LogID: "log-z", SourceID: "producer-z", SourceLogID: "log-z",
			Category: observabilityvo.CategoryRuntimeSystem, EventName: "service.started",
			TenantID: "tenant-a", EventTimestamp: timestamp, TrustLevel: "trusted", IngressPrincipal: "otel-gateway",
		}}},
		fakeSource{id: "adapter-z", records: []observabilityvo.LogRecord{{
			LogID: "log-a", SourceID: "producer-a", SourceLogID: "log-a",
			Category: observabilityvo.CategoryRuntimeSystem, EventName: "service.started",
			TenantID: "tenant-a", EventTimestamp: timestamp, TrustLevel: "trusted", IngressPrincipal: "otel-gateway",
		}}},
	}).List(context.Background(), activeProfile("admin-a", "admin"), observabilityvo.LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != 2 || result.Records[0].SourceID != "producer-a" || result.Records[1].SourceID != "producer-z" {
		t.Fatalf("same-timestamp logs must sort by source_id then source_log_id: %+v", result.Records)
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

func TestListSupportsPageNumberPaginationWithoutExposingCursors(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	source := &filteredPageSource{pages: [][]observabilityvo.LogRecord{
		{{LogID: "log-new", Category: observabilityvo.CategoryRuntimeSystem, EventName: "service.started", TenantID: "tenant-a", EventTimestamp: base, TrustLevel: "trusted", IngressPrincipal: "otel-gateway"}},
		{{LogID: "log-old", Category: observabilityvo.CategoryRuntimeSystem, EventName: "service.started", TenantID: "tenant-a", EventTimestamp: base.Add(-time.Second), TrustLevel: "trusted", IngressPrincipal: "otel-gateway"}},
	}}
	result, err := NewWithCursorKey([]Source{source}, []byte("test-cursor-signing-key")).List(
		context.Background(), activeProfile("admin-a", "admin"), observabilityvo.LogQuery{Limit: 1, Page: 2},
	)
	if err != nil || result.Page != 2 || result.PageSize != 1 || len(result.Records) != 1 || result.Records[0].LogID != "log-old" {
		t.Fatalf("unexpected numbered log page: %+v err=%v", result, err)
	}
}

func TestListAdvancesPastACompletelyFilteredSourcePage(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	filtered := make([]observabilityvo.LogRecord, 200)
	for index := range filtered {
		filtered[index] = observabilityvo.LogRecord{
			LogID: "filtered-" + time.Duration(index).String(), Category: observabilityvo.CategoryRuntimeSystem,
			EventName: "service.started", TenantID: "tenant-other", EventTimestamp: base.Add(-time.Duration(index) * time.Second),
			TrustLevel: "trusted", IngressPrincipal: "otel-gateway",
		}
	}
	visible := observabilityvo.LogRecord{
		LogID: "visible", Category: observabilityvo.CategoryRuntimeSystem, EventName: "service.started",
		TenantID: "tenant-a", EventTimestamp: base.Add(-201 * time.Second), TrustLevel: "trusted", IngressPrincipal: "otel-gateway",
	}
	service := NewWithCursorKey([]Source{&filteredPageSource{pages: [][]observabilityvo.LogRecord{filtered, {visible}}}}, []byte("test-cursor-signing-key"))
	profile := activeProfile("admin-a", "admin")
	profile.Fingerprint = "sha256:scope-a"

	first, err := service.List(context.Background(), profile, observabilityvo.LogQuery{Limit: 20})
	if err != nil || len(first.Records) != 0 || first.NextCursor == "" {
		t.Fatalf("filtered source page must remain pageable: result=%+v err=%v", first, err)
	}
	second, err := service.List(context.Background(), profile, observabilityvo.LogQuery{Limit: 20, Cursor: first.NextCursor})
	if err != nil || len(second.Records) != 1 || second.Records[0].LogID != "visible" {
		t.Fatalf("next source page was not reachable: result=%+v err=%v", second, err)
	}
}

func TestAfterSourcePositionTrustsNativeSearchAfterOrdering(t *testing.T) {
	boundary := time.Date(2026, 8, 1, 10, 0, 0, 123000000, time.UTC)
	record := observabilityvo.LogRecord{
		LogID: "log-b", SourceLogID: "source-b",
		EventTimestamp: boundary.Add(456 * time.Microsecond),
		CursorPosition: &observabilityvo.SourcePosition{
			EventTimestamp: boundary.Add(456 * time.Microsecond), LogID: "source-b",
			SearchAfter: []any{float64(1785578400123), "source-b"},
		},
	}
	position := &observabilityvo.SourcePosition{
		EventTimestamp: boundary, LogID: "source-a",
		SearchAfter: []any{float64(1785578400123), "source-a"},
	}
	if !afterSourcePosition(record, position) {
		t.Fatal("native search_after result was rejected by a mismatched source timestamp precision")
	}
}

func TestMatchesQueryUsesExclusiveTimeToBoundary(t *testing.T) {
	boundary := time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC)
	query := observabilityvo.LogQuery{TimeTo: &boundary}
	if matchesQuery(observabilityvo.LogRecord{EventTimestamp: boundary}, query) {
		t.Fatal("time_to is exclusive in the API contract and source query")
	}
	if !matchesQuery(observabilityvo.LogRecord{EventTimestamp: boundary.Add(-time.Nanosecond)}, query) {
		t.Fatal("record immediately before time_to must remain visible")
	}
}

func TestMatchesQueryExcludesRecordsObservedAfterTheQueryWatermark(t *testing.T) {
	watermark := time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC)
	query := observabilityvo.LogQuery{ObservedBefore: &watermark}
	if matchesQuery(observabilityvo.LogRecord{
		EventTimestamp:    watermark.Add(-time.Hour),
		ObservedTimestamp: watermark.Add(time.Nanosecond),
	}, query) {
		t.Fatal("a late-arriving record must not enter a cursor-stable result set")
	}
	if !matchesQuery(observabilityvo.LogRecord{
		EventTimestamp:    watermark.Add(-time.Hour),
		ObservedTimestamp: watermark,
	}, query) {
		t.Fatal("a record observed at the query watermark must remain visible")
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
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

func BenchmarkListEightSources(b *testing.B) {
	base := time.Now().UTC().Truncate(time.Second)
	sources := make([]Source, 8)
	for sourceIndex := range sources {
		records := make([]observabilityvo.LogRecord, 200)
		for recordIndex := range records {
			records[recordIndex] = validTestRecord(observabilityvo.LogRecord{
				LogID:    "log-" + time.Duration(sourceIndex*200+recordIndex).String(),
				SourceID: "source-" + time.Duration(sourceIndex).String(),
				Category: observabilityvo.CategoryRuntimeSystem, EventName: "service.started",
				TenantID: "tenant-a", EventTimestamp: base.Add(-time.Duration(recordIndex) * time.Millisecond),
				TrustLevel: "trusted", IngressPrincipal: "otel-gateway",
			})
		}
		sources[sourceIndex] = fakeSource{id: "source-" + time.Duration(sourceIndex).String(), records: records}
	}
	service := NewWithOptions(sources, Options{
		CursorKey: []byte("benchmark-cursor-key"), SourceTimeout: time.Second, MaxConcurrentSources: 4,
	})
	profile := activeProfile("admin-a", "admin")
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := service.List(context.Background(), profile, observabilityvo.LogQuery{Limit: 200}); err != nil {
			b.Fatal(err)
		}
	}
}
