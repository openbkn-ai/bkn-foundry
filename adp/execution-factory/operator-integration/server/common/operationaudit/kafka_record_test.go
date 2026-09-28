package operationaudit

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildKafkaRecordPreservesManagementAttemptWithoutInventingChangedState(t *testing.T) {
	eventID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{
		EventID: eventID.String(), EventTime: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
		ActorID: "user-1", ActorName: "Operator", ActorType: "user", AuthMethod: "oauth",
		RequestID: "req-1", SourceChannel: "api", Method: "POST", HTTPStatus: 403,
		Action: "create", TargetType: "skill", TargetID: "skill-1", TargetName: "Skill One",
		Outcome: "denied", FailureCode: "http_403",
	}
	value, err := BuildKafkaRecord(entry, "test")
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(value, &record); err != nil {
		t.Fatal(err)
	}
	if record["event_id"] != entry.EventID || record["event_name"] != "execution_factory.operation.observed" || record["source_id"] != "execution-factory" {
		t.Fatalf("wrong management event: %+v", record)
	}
	if record["outcome"] != "denied" || record["http_status"] != float64(403) {
		t.Fatalf("wrong result: %+v", record)
	}
	facts := record["facts"].(map[string]any)
	if len(facts) != 1 || facts["action"] != "create" {
		t.Fatalf("invented change facts: %+v", facts)
	}
}

func TestBuildKafkaRecordKeepsUnauthenticatedDenialAnonymous(t *testing.T) {
	eventID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{
		EventID: eventID.String(), EventTime: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
		ActorID: "anonymous", ActorType: "anonymous", AuthMethod: "unknown",
		RequestID: "req-unauthenticated", SourceChannel: "api", Method: "POST", HTTPStatus: 401,
		Action: "create", TargetType: "operator", TargetID: "operator:req-unauthenticated",
		Outcome: "denied", FailureCode: "http_401",
	}
	value, err := BuildKafkaRecord(entry, "test")
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(value, &record); err != nil {
		t.Fatal(err)
	}
	actor := record["actor"].(map[string]any)
	if actor["id"] != "anonymous" || actor["type"] != "anonymous" || actor["auth_method"] != "unknown" {
		t.Fatalf("unauthenticated actor = %+v", actor)
	}
}

func TestBuildKafkaRecordAcceptsRequestLevelCompoundImport(t *testing.T) {
	eventID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{
		EventID: eventID.String(), EventTime: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
		ActorID: "user-1", ActorType: "user", AuthMethod: "oauth", RequestID: "req-import-1",
		SourceChannel: "api", Method: "POST", HTTPStatus: 201, Action: "import",
		TargetType: "import_batch", TargetID: "import_batch:req-import-1", Outcome: "success",
	}
	if _, err := BuildKafkaRecord(entry, "test"); err != nil {
		t.Fatalf("request-level import Audit rejected: %v", err)
	}
}

func TestBuildKafkaRecordAllowsUnknownPartialBundleWithoutFailureCode(t *testing.T) {
	eventID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{
		EventID: eventID.String(), EventTime: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
		ActorID: "user-1", ActorType: "user", AuthMethod: "oauth", RequestID: "req-bundle-1",
		SourceChannel: "api", Method: "POST", HTTPStatus: 200, Action: "create",
		TargetType: "capability_bundle", TargetID: "capability_bundle:req-bundle-1", Outcome: "unknown",
	}
	value, err := BuildKafkaRecord(entry, "test")
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(value, &record); err != nil {
		t.Fatal(err)
	}
	if _, exists := record["failure_code"]; exists {
		t.Fatalf("partial bundle must not invent an HTTP failure: %+v", record)
	}
}

func TestBuildKafkaRecordPreservesUnknownPrivateChannelAndCategoryTarget(t *testing.T) {
	eventID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{
		EventID: eventID.String(), EventTime: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
		ActorID: "anonymous", ActorType: "anonymous", AuthMethod: "unknown",
		RequestID: "req-private-category", SourceChannel: "unknown", Method: "PUT", HTTPStatus: 200,
		Action: "update", TargetType: "operator_category", TargetID: "operator_category:req-private-category", Outcome: "success",
	}
	value, err := BuildKafkaRecord(entry, "test")
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(value, &record); err != nil {
		t.Fatal(err)
	}
	context := record["request_context"].(map[string]any)
	if context["source_channel"] != "unknown" {
		t.Fatalf("unverified private caller channel = %v, want unknown", context["source_channel"])
	}
	if record["target"].(map[string]any)["type"] != "operator_category" {
		t.Fatalf("private category target = %+v", record["target"])
	}
}
