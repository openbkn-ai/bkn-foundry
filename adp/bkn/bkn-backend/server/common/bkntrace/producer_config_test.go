package bkntrace

import "testing"

type closeTrackingProducer struct{ closed bool }

func (p *closeTrackingProducer) Close() error { p.closed = true; return nil }

func TestCloseEvidenceProducerClosesTransport(t *testing.T) {
	producer := &closeTrackingProducer{}
	if err := CloseEvidenceProducer(producer); err != nil {
		t.Fatal(err)
	}
	if !producer.closed {
		t.Fatal("transport was not closed")
	}
}

func TestLoadEvidencePublisherConfigDoesNotRequireStaticPolicyRevision(t *testing.T) {
	t.Setenv("BKN_TRACE_KAFKA_BROKERS", "kafka:9092")
	t.Setenv("BKN_TRACE_KAFKA_SASL_MECHANISM", "PLAIN")
	t.Setenv("BKN_TRACE_KAFKA_USERNAME", "producer")
	t.Setenv("BKN_TRACE_KAFKA_PASSWORD", "secret")
	t.Setenv("BKN_TRACE_PRODUCER_ID", "bkn-backend")
	t.Setenv("BKN_TRACE_WORKLOAD_IDENTITY", "bkn-backend")
	t.Setenv("BKN_TRACE_PRODUCER_STREAM_ID", "bkn-backend")
	t.Setenv("BKN_TRACE_CAPTURE_POLICY_REVISION", "")

	cfg, err := loadEvidencePublisherConfig()
	if err != nil {
		t.Fatalf("loadEvidencePublisherConfig() error = %v", err)
	}
	if cfg.Publisher.CapturePolicyRevision != "" {
		t.Fatalf("CapturePolicyRevision = %q; want policy from verified runtime", cfg.Publisher.CapturePolicyRevision)
	}
}
