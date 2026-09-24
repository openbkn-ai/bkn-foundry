package bkntrace

import "testing"

func TestLoadEvidencePublisherConfigRejectsMissingFrozenInputs(t *testing.T) {
	t.Setenv("BKN_TRACE_KAFKA_BROKERS", "kafka:9092")
	t.Setenv("BKN_TRACE_KAFKA_SASL_MECHANISM", "PLAIN")
	t.Setenv("BKN_TRACE_KAFKA_USERNAME", "producer")
	t.Setenv("BKN_TRACE_KAFKA_PASSWORD", "secret")
	t.Setenv("BKN_TRACE_PRODUCER_ID", "ontology-query")
	t.Setenv("BKN_TRACE_WORKLOAD_IDENTITY", "ontology-query")
	t.Setenv("BKN_TRACE_PRODUCER_STREAM_ID", "ontology-query")
	t.Setenv("BKN_TRACE_CAPTURE_POLICY_REVISION", "")

	if _, err := loadEvidencePublisherConfig(); err == nil {
		t.Fatal("loadEvidencePublisherConfig() error = nil, want missing policy revision error")
	}
}

func TestCloseEvidenceProducerClosesTransport(t *testing.T) {
	producer := &closeTrackingProducer{}
	if err := CloseEvidenceProducer(producer); err != nil {
		t.Fatal(err)
	}
	if !producer.closed {
		t.Fatal("transport was not closed")
	}
}

type closeTrackingProducer struct{ closed bool }

func (p *closeTrackingProducer) Close() error {
	p.closed = true
	return nil
}
