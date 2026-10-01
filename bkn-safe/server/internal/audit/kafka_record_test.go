package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildKafkaAdminRecordPreservesCommittedTargetWithoutDetail(t *testing.T) {
	entry := Entry{
		ActorID: "verified-admin", ActorNameSnapshot: "Administrator", ActorType: "user", AuthMethod: "oauth",
		RequestID: "req-safe-role-1", SourceChannel: "api", Method: "PUT",
		Resource: "roles", Action: "update", TargetID: "role-1", TargetName: "Operators",
		Status: 200, Detail: `{"password":"never-publish","requested_permission":"write"}`,
	}
	value, err := BuildKafkaAdminRecord(entry, "test")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(value), "never-publish") || strings.Contains(string(value), "requested_permission") {
		t.Fatalf("private request Detail leaked into Kafka Audit: %s", value)
	}
	var record map[string]any
	if err := json.Unmarshal(value, &record); err != nil {
		t.Fatal(err)
	}
	if record["source_id"] != "bkn-safe-admin" || record["event_name"] != "safe.admin.operation.observed" || record["outcome"] != "success" {
		t.Fatalf("wrong committed admin event: %+v", record)
	}
	actor := record["actor"].(map[string]any)
	if actor["display_name_snapshot"] != "Administrator" {
		t.Fatalf("actor name snapshot = %+v", actor)
	}
	target := record["target"].(map[string]any)
	if target["type"] != "role" || target["id"] != "role-1" {
		t.Fatalf("committed target = %+v", target)
	}
	if target["name"] != "Operators" {
		t.Fatalf("target name snapshot = %+v", target)
	}
	facts := record["facts"].(map[string]any)
	if facts["action"] != "update" || facts["decision"] != "allowed" {
		t.Fatalf("admin facts = %+v", facts)
	}
	if _, exists := facts["changed_fields"]; exists {
		t.Fatalf("request body is not a committed field diff: %+v", facts)
	}
}

func TestBuildKafkaAdminRecordBoundsDisplaySnapshots(t *testing.T) {
	entry := Entry{
		ActorID: "verified-admin", ActorNameSnapshot: strings.Repeat("操", 300), ActorType: "user", AuthMethod: "oauth",
		RequestID: "req-safe-role-long-name", SourceChannel: "api", Method: "PUT",
		Resource: "roles", Action: "update", TargetID: "role-1", TargetName: strings.Repeat("作", 600), Status: 200,
	}
	value, err := BuildKafkaAdminRecord(entry, "test")
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(value, &record); err != nil {
		t.Fatal(err)
	}
	actor := record["actor"].(map[string]any)
	if got := []rune(actor["display_name_snapshot"].(string)); len(got) != 256 {
		t.Fatalf("actor display snapshot length=%d, want 256", len(got))
	}
	target := record["target"].(map[string]any)
	if got := []rune(target["name"].(string)); len(got) != 512 {
		t.Fatalf("target name snapshot length=%d, want 512", len(got))
	}
}

func TestBuildKafkaAdminRecordRejectsUncommittedAttempt(t *testing.T) {
	entry := Entry{ActorID: "verified-admin", ActorType: "user", AuthMethod: "oauth",
		RequestID: "req-safe-failed-1", SourceChannel: "api", Method: "POST",
		Resource: "users", Action: "create", Status: 400}
	if _, err := BuildKafkaAdminRecord(entry, "test"); err == nil {
		t.Fatal("uncommitted request must not enter the business audit stream")
	}
}

func TestBuildKafkaAdminRecordRejectsMissingActor(t *testing.T) {
	entry := Entry{ActorType: "user", AuthMethod: "oauth", RequestID: "req-safe-missing-actor", SourceChannel: "api", Method: "POST", Resource: "users", Action: "create", TargetID: "user-1", TargetName: "用户 A", Status: 201}
	if _, err := BuildKafkaAdminRecord(entry, "test"); err == nil {
		t.Fatal("admin fact without a resolved actor must not enter the business audit stream")
	}
}

func TestBuildKafkaAdminRecordRejectsMissingActorSnapshot(t *testing.T) {
	entry := Entry{ActorID: "verified-admin", ActorType: "user", AuthMethod: "oauth", RequestID: "req-safe-missing-actor-name", SourceChannel: "api", Method: "POST", Resource: "users", Action: "create", TargetID: "user-1", TargetName: "用户 A", Status: 201}
	if _, err := BuildKafkaAdminRecord(entry, "test"); err == nil {
		t.Fatal("admin fact without an actor snapshot must not enter the business audit stream")
	}
}

func TestBuildKafkaAdminRecordRejectsMissingTargetSnapshot(t *testing.T) {
	entry := Entry{ActorID: "verified-admin", ActorNameSnapshot: "Administrator", ActorType: "user", AuthMethod: "oauth", RequestID: "req-safe-missing-target-name", SourceChannel: "api", Method: "POST", Resource: "users", Action: "create", TargetID: "user-1", Status: 201}
	if _, err := BuildKafkaAdminRecord(entry, "test"); err == nil {
		t.Fatal("admin fact without a target snapshot must not enter the business audit stream")
	}
}

func TestBuildKafkaAdminRecordCoversSafeManagementResources(t *testing.T) {
	resources := map[string]string{
		"users": "user", "roles": "role", "departments": "department",
		"role-bindings": "role_binding", "object-grants": "object_grant",
		"enterprise-object-grants": "object_grant", "permission-requests": "permission_request",
		"api-keys": "api_key", "oauth": "oauth_access_origin", "clients": "oauth_client",
		"license": "license", "policies": "authorization_policy",
		"resource-parents": "resource_parent", "property-levels": "property_level",
		"property-grants": "property_grant", "row-filter-policies": "row_filter_policy",
		"profile": "user",
	}
	for resource, targetType := range resources {
		t.Run(resource, func(t *testing.T) {
			entry := Entry{ActorID: "verified-admin", ActorNameSnapshot: "Administrator", ActorType: "user", AuthMethod: "oauth",
				RequestID: "req-safe-management", SourceChannel: "api", Method: "POST",
				Resource: resource, Action: "create", TargetName: "business target", Status: 201}
			value, err := BuildKafkaAdminRecord(entry, "test")
			if err != nil {
				t.Fatal(err)
			}
			var record map[string]any
			if err := json.Unmarshal(value, &record); err != nil {
				t.Fatal(err)
			}
			target := record["target"].(map[string]any)
			if target["type"] != targetType || target["id"] != targetType+":req-safe-management" {
				t.Fatalf("request target = %+v", target)
			}
		})
	}
}

func TestBuildKafkaAdminRecordKeepsSystemLicenseEventWithoutInventedRequest(t *testing.T) {
	value, err := BuildKafkaAdminRecord(Entry{
		ActorID: "system:license", Method: "SYSTEM", Resource: "license",
		Action: "license.state-change", Status: 200, Detail: "contains private license state",
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(value, &record); err != nil {
		t.Fatal(err)
	}
	actor := record["actor"].(map[string]any)
	if actor["type"] != "service_account" || actor["id"] != "system:license" {
		t.Fatalf("system actor = %+v", actor)
	}
	target := record["target"].(map[string]any)
	if target["type"] != "license" || target["id"] != "license:cluster" {
		t.Fatalf("license target = %+v", target)
	}
	correlation := record["correlation"].(map[string]any)
	if _, present := correlation["request_id"]; present {
		t.Fatalf("invented request correlation = %+v", correlation)
	}
	if _, present := record["http_status"]; present {
		t.Fatal("system event invented an HTTP status")
	}
	requestContext := record["request_context"].(map[string]any)
	if requestContext["transport"] != "non_http" {
		t.Fatalf("system transport = %+v", requestContext)
	}
	if requestContext["source_channel"] != "unknown" {
		t.Fatalf("system source was invented: %+v", requestContext)
	}
	if facts := record["facts"].(map[string]any); facts["action"] != "license_state_change" {
		t.Fatalf("license action is not canonical: %+v", facts)
	}
	if strings.Contains(string(value), "private license state") {
		t.Fatal("license detail leaked")
	}
}

func TestBuildKafkaAdminRecordClassifiesLicenseRenewFailureWithoutFakeHTTPCode(t *testing.T) {
	value, err := BuildKafkaAdminRecord(Entry{
		ActorID: "system:license", Method: "SYSTEM", Resource: "license",
		Action: "license.renew-failed", Status: 200,
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(value, &record); err != nil {
		t.Fatal(err)
	}
	if record["outcome"] != "failure" {
		t.Fatalf("license renewal failure reported as %v", record["outcome"])
	}
	if record["failure_code"] != "LICENSE_RENEW_FAILED" {
		t.Fatalf("non-HTTP failure reason = %v", record["failure_code"])
	}
	if _, present := record["http_status"]; present {
		t.Fatal("system failure invented an HTTP status")
	}
	if facts := record["facts"].(map[string]any); facts["action"] != "license_renew_failed" {
		t.Fatalf("failure action = %+v", facts)
	}
}
