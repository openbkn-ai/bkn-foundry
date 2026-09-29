package accesslog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildKafkaRecordProjectsLoginFailureWithoutLegacyAccessFields(t *testing.T) {
	value, err := BuildKafkaRecord(Entry{
		ActorNameSnapshot: "untrusted-account-name",
		AuthMethod:        "password",
		SourceChannel:     "web",
		Action:            "login",
		Outcome:           "failure",
		FailureCode:       "invalid_credentials",
		RequestID:         "req-safe-access-failure",
		ClientIP:          "192.0.2.10",
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(value), "untrusted-account-name") || strings.Contains(string(value), "192.0.2.10") {
		t.Fatalf("legacy access detail leaked into Kafka record: %s", value)
	}
	var record map[string]any
	if err := json.Unmarshal(value, &record); err != nil {
		t.Fatal(err)
	}
	if record["source_id"] != "bkn-safe-access" || record["category"] != "access.user" || record["event_name"] != "login.failed" || record["outcome"] != "failure" {
		t.Fatalf("wrong access Kafka record: %+v", record)
	}
	facts, ok := record["facts"].(map[string]any)
	if !ok || facts["action"] != "login" || facts["result"] != "failure" {
		t.Fatalf("access facts = %+v, want login failure", facts)
	}
	target, ok := record["target"].(map[string]any)
	if !ok || target["type"] != "session" || target["id"] != "session:req-safe-access-failure" {
		t.Fatalf("access target = %+v, want request-scoped session", target)
	}
}
