package evidencepublisher

import (
	"context"
	"testing"
)

func TestPublisherRuntimeEnvironmentRequiresNoOAuthOrSigningConfiguration(t *testing.T) {
	for _, name := range []string{"TRACE_ADMISSION_TOKEN_URL", "TRACE_ADMISSION_CLIENT_ID", "TRACE_ADMISSION_CLIENT_SECRET", "TRACE_ADMISSION_SCOPE", "TRACE_ADMISSION_AUDIENCE", "TRACE_ADMISSION_CURRENT_KEY_ID", "TRACE_ADMISSION_CURRENT_PUBLIC_KEY", "TRACE_ADMISSION_PREVIOUS_KEY_ID", "TRACE_ADMISSION_PREVIOUS_PUBLIC_KEY"} {
		t.Setenv(name, "")
	}
	base := "http://agent-observability-internal:8081"
	t.Setenv("TRACE_ADMISSION_POLICY_URL", base+traceAdmissionPolicySuffix)
	t.Setenv("TRACE_ADMISSION_CONFIGURATION_URL", base+traceAdmissionConfigurationSuffix)
	t.Setenv("TRACE_ADMISSION_HEARTBEAT_URL", base+traceAdmissionHeartbeatSuffix)
	t.Setenv("TRACE_ADMISSION_ACK_URL_BASE", base+traceAdmissionACKSuffix)
	runtime, err := NewPublisherRuntimeFromEnvironment(context.Background(), publisherTestConfig(), &fakeSender{})
	if err != nil {
		t.Fatalf("NewPublisherRuntimeFromEnvironment() = %v", err)
	}
	defer runtime.publisher.Close(context.Background())
	if runtime.configuration.url != base+traceEvidenceConfigurationPath {
		t.Fatalf("configuration URL = %s", runtime.configuration.url)
	}
}

func TestPublisherRuntimeEnvironmentRejectsPublicConfigurationEndpoint(t *testing.T) {
	t.Setenv("TRACE_ADMISSION_POLICY_URL", "http://agent-observability-internal:8081"+traceAdmissionPolicySuffix)
	t.Setenv("TRACE_ADMISSION_CONFIGURATION_URL", "http://agent-observability:8080/api/agent-observability/v1/trace-evidence-configuration")
	if _, err := NewPublisherRuntimeFromEnvironment(context.Background(), publisherTestConfig(), &fakeSender{}); err == nil {
		t.Fatal("public configuration endpoint accepted")
	}
}
