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

func (source fakeSource) ID() string { return source.id }

func (source fakeSource) Search(
	context.Context,
	observabilityvo.LogQuery,
) (observabilityvo.SourcePage, error) {
	if source.err != nil {
		return observabilityvo.SourcePage{}, source.err
	}
	return observabilityvo.SourcePage{Records: source.records, Count: int64(len(source.records)), CountAccuracy: "exact"}, nil
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
		{LogID: "system-a", Category: observabilityvo.CategoryRuntimeSystem, TenantID: "tenant-a", EventTimestamp: time.Now()},
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

func activeProfile(subject, role string) evidencevo.AccessProfile {
	return evidencevo.AccessProfile{
		TenantID: "tenant-a", BusinessDomain: "domain-a", EffectiveSubjectID: subject,
		Roles: []string{role}, AccountActive: true, TenantActive: true,
	}
}

func ownedBusinessLog(id, owner, traceID string) observabilityvo.LogRecord {
	return observabilityvo.LogRecord{
		LogID: id, Category: observabilityvo.CategoryRuntimeBusiness, TenantID: "tenant-a",
		BusinessDomain: "domain-a", EffectiveSubjectID: owner, TraceID: traceID, EventTimestamp: time.Now(),
	}
}

func managedBusinessLog(id string, networks []string) observabilityvo.LogRecord {
	return observabilityvo.LogRecord{
		LogID: id, Category: observabilityvo.CategoryRuntimeBusiness, TenantID: "tenant-a",
		BusinessDomain: "domain-a", EffectiveSubjectID: "other-a", KnowledgeNetworkIDs: networks,
		EventTimestamp: time.Now(),
	}
}
