package evidencepublisher

import (
	"context"
	"strings"
	"testing"
)

func TestPublisherRuntimeEnvironmentRequiresNoOAuthOrSigningConfiguration(t *testing.T) {
	for _, name := range []string{"TRACE_ADMISSION_TOKEN_URL", "TRACE_ADMISSION_CLIENT_ID", "TRACE_ADMISSION_CLIENT_SECRET", "TRACE_ADMISSION_SCOPE", "TRACE_ADMISSION_AUDIENCE", "TRACE_ADMISSION_CURRENT_KEY_ID", "TRACE_ADMISSION_CURRENT_PUBLIC_KEY", "TRACE_ADMISSION_PREVIOUS_KEY_ID", "TRACE_ADMISSION_PREVIOUS_PUBLIC_KEY"} {
		t.Setenv(name, "")
	}
	base := "http://agent-observability-internal:8081"
	// Keep the deployment URLs literal so endpoint-contract drift fails this test.
	t.Setenv("TRACE_ADMISSION_POLICY_URL", "http://agent-observability-internal:8081/api/agent-observability/v1/internal/trace-evidence/policy")
	t.Setenv("TRACE_ADMISSION_CONFIGURATION_URL", "http://agent-observability-internal:8081/api/agent-observability/v1/internal/trace-evidence/configuration")
	t.Setenv("TRACE_ADMISSION_HEARTBEAT_URL", "http://agent-observability-internal:8081/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat")
	t.Setenv("TRACE_ADMISSION_ACK_URL_BASE", "http://agent-observability-internal:8081/api/agent-observability/v1/internal/trace-evidence/operations")
	runtime, err := NewPublisherRuntimeFromEnvironment(context.Background(), publisherTestConfig(), &fakeSender{})
	if err != nil {
		t.Fatalf("NewPublisherRuntimeFromEnvironment() = %v", err)
	}
	defer runtime.publisher.Close(context.Background())
	if runtime.configuration.url != base+traceEvidenceConfigurationPath {
		t.Fatalf("configuration URL = %s", runtime.configuration.url)
	}
}

func TestPublisherRuntimeEnvironmentDiagnosesInvalidEndpoints(t *testing.T) {
	base := "http://agent-observability-internal:8081"
	endpoints := []struct{ name, suffix string }{
		{"TRACE_ADMISSION_POLICY_URL", traceAdmissionPolicySuffix},
		{"TRACE_ADMISSION_CONFIGURATION_URL", traceAdmissionConfigurationSuffix},
		{"TRACE_ADMISSION_HEARTBEAT_URL", traceAdmissionHeartbeatSuffix},
		{"TRACE_ADMISSION_ACK_URL_BASE", traceAdmissionACKSuffix},
	}
	for _, endpoint := range endpoints {
		for _, invalid := range []struct{ name, value, stage, reason string }{
			{"empty", "", "parse", "invalid_url"},
			{"malformed", "http://user:private-password@host/%zz", "parse", "invalid_url"},
			{"relative", endpoint.suffix, "validate", "missing_scheme"},
			{"host_missing", "http:///" + strings.TrimPrefix(endpoint.suffix, "/"), "validate", "missing_host"},
			{"wrong_path", base + "/wrong", "validate", "path_suffix_mismatch"},
			{"query", "http://user:private-password@agent-observability-internal:8081" + endpoint.suffix + "?token=private-token", "validate", "query_not_allowed"},
		} {
			t.Run(endpoint.name+"/"+invalid.name, func(t *testing.T) {
				for _, valid := range endpoints {
					t.Setenv(valid.name, base+valid.suffix)
				}
				t.Setenv(endpoint.name, invalid.value)
				_, err := NewPublisherRuntimeFromEnvironment(context.Background(), publisherTestConfig(), &fakeSender{})
				if err == nil {
					t.Fatal("invalid endpoint accepted")
				}
				for _, want := range []string{"TRACE_ADMISSION_ENDPOINT_INVALID", "variable=" + endpoint.name, "stage=" + invalid.stage, "reason=" + invalid.reason, "expected_suffix=\"" + endpoint.suffix + "\""} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("error %q does not contain %q", err, want)
					}
				}
				for _, secret := range []string{"private-password", "private-token"} {
					if strings.Contains(err.Error(), secret) {
						t.Fatalf("error exposes URL credentials: %s", err)
					}
				}
				if invalid.reason == "path_suffix_mismatch" && !strings.Contains(err.Error(), "path=\"/wrong\"") {
					t.Fatalf("error does not report the actual path: %s", err)
				}
			})
		}
	}
}

func TestPublisherRuntimeEnvironmentDiagnosesACKBaseMismatch(t *testing.T) {
	base := "http://agent-observability-internal:8081"
	t.Setenv("TRACE_ADMISSION_POLICY_URL", base+traceAdmissionPolicySuffix)
	t.Setenv("TRACE_ADMISSION_CONFIGURATION_URL", base+traceAdmissionConfigurationSuffix)
	t.Setenv("TRACE_ADMISSION_HEARTBEAT_URL", base+traceAdmissionHeartbeatSuffix)
	t.Setenv("TRACE_ADMISSION_ACK_URL_BASE", "http://other:8081"+traceAdmissionACKSuffix)
	_, err := NewPublisherRuntimeFromEnvironment(context.Background(), publisherTestConfig(), &fakeSender{})
	if err == nil || !strings.Contains(err.Error(), "variable=TRACE_ADMISSION_ACK_URL_BASE stage=validate reason=base_mismatch") || !strings.Contains(err.Error(), "TRACE_ADMISSION_HEARTBEAT_URL") {
		t.Fatalf("unexpected ACK base mismatch diagnostic: %v", err)
	}
}

func TestPublisherRuntimeEnvironmentRejectsPublicConfigurationEndpoint(t *testing.T) {
	t.Setenv("TRACE_ADMISSION_POLICY_URL", "http://agent-observability-internal:8081"+traceAdmissionPolicySuffix)
	t.Setenv("TRACE_ADMISSION_CONFIGURATION_URL", "http://agent-observability:8080/api/agent-observability/v1/trace-evidence-configuration")
	if _, err := NewPublisherRuntimeFromEnvironment(context.Background(), publisherTestConfig(), &fakeSender{}); err == nil {
		t.Fatal("public configuration endpoint accepted")
	}
}
