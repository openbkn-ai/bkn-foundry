package decisionlog

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
)

func TestKafkaDecisionUsesObservedFactsWithoutFakePolicyRevision(t *testing.T) {
	value, err := BuildKafkaRecord(Entry{
		AccessorID: "bkn_valid_accessor_123", ResourceType: "knowledge_network", ResourceID: "kn-1",
		Operation: "view_detail", Scope: "effective", Decision: DecisionDeny,
		Source: "check", RequestID: "req-safe-security-test", Method: "POST",
		Detail: "Bearer abcdefghijklmnop", ClientIP: "192.0.2.1",
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auditpublisher.BuildRecord(value); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(value, &record); err != nil {
		t.Fatal(err)
	}
	if record["source_id"] != "bkn-safe-security" || record["event_name"] != "authorization.decided" || record["outcome"] != "denied" {
		t.Fatalf("wrong security identity: %+v", record)
	}
	actor := record["actor"].(map[string]any)
	if actor["id"] != "anonymous" {
		t.Fatalf("caller-supplied accessor cannot become authenticated actor: %+v", actor)
	}
	if actor["effective_subject"] != "bkn_valid_accessor_123" {
		t.Fatalf("evaluated subject must remain searchable without spoofing the actor: %+v", actor)
	}
	if bytes.Contains(value, []byte("abcdefghijklmnop")) || bytes.Contains(value, []byte("192.0.2.1")) {
		t.Fatal("source must omit untrusted detail and client IP from the published record")
	}
	facts := record["facts"].(map[string]any)
	if facts["action"] != "check" || facts["decision"] != "deny" || facts["resource_scope"] != "knowledge_network:kn-1" {
		t.Fatalf("decision facts: %+v", facts)
	}
	if _, invented := facts["policy_revision"]; invented {
		t.Fatal("Safe has no trustworthy policy revision")
	}
}

func TestKafkaDecisionRedactsCredentialShapedCallerInputWithoutDroppingEvent(t *testing.T) {
	value, err := BuildKafkaRecord(Entry{
		AccessorID: "Bearer abcdefghijklmnop", ResourceType: "knowledge_network", ResourceID: "bak_123456789012_abcdefghijklmnopqrstuvwxyz1",
		Decision: DecisionDeny, Source: "check", Method: "POST",
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(value, []byte("abcdefghijklmnop")) || bytes.Contains(value, []byte("abcdefghijklmnopqrstuvwxyz1")) {
		t.Fatal("source leaked credential-shaped caller input")
	}
	if _, err := auditpublisher.BuildRecord(value); err != nil {
		t.Fatalf("redacted decision should still be published: %v", err)
	}
}
