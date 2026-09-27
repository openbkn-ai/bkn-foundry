package bkntrace

import "testing"

func TestEvidencePublisherConfigDoesNotRequireStaticPolicyRevision(t *testing.T) {
	t.Setenv("BKN_TRACE_KAFKA_BROKERS", "kafka.resource.svc.cluster.local:9092")
	t.Setenv("BKN_TRACE_KAFKA_SASL_MECHANISM", "PLAIN")
	t.Setenv("BKN_TRACE_KAFKA_USERNAME", "operator")
	t.Setenv("BKN_TRACE_KAFKA_PASSWORD", "test-password")
	t.Setenv("BKN_TRACE_PRODUCER_ID", "agent-operator-integration")
	t.Setenv("BKN_TRACE_WORKLOAD_IDENTITY", "agent-operator-integration")
	t.Setenv("BKN_TRACE_PRODUCER_STREAM_ID", "agent-operator-integration")
	t.Setenv("BKN_TRACE_CAPTURE_POLICY_REVISION", "")
	config, err := loadEvidencePublisherConfig()
	if err != nil {
		t.Fatalf("dynamic publisher config rejected without static revision: %v", err)
	}
	if config.Publisher.CapturePolicyRevision != "" {
		t.Fatalf("static revision unexpectedly required: %q", config.Publisher.CapturePolicyRevision)
	}
}
