package bkntrace

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/kafkasender"
)

type EvidencePublisherRuntime struct {
	Publisher *evidencepublisher.Publisher
	Producer  interface{ Close() error }
}

func NewEvidencePublisherRuntime() (*EvidencePublisherRuntime, error) {
	get := func(key string) string { return strings.TrimSpace(os.Getenv(key)) }
	brokers := splitBrokers(get("BKN_TRACE_KAFKA_BROKERS"))
	username, password := get("BKN_TRACE_KAFKA_USERNAME"), os.Getenv("BKN_TRACE_KAFKA_PASSWORD")
	producerID, streamID := get("BKN_TRACE_PRODUCER_ID"), get("BKN_TRACE_PRODUCER_STREAM_ID")
	identity, revision := get("BKN_TRACE_WORKLOAD_IDENTITY"), get("BKN_TRACE_CAPTURE_POLICY_REVISION")
	if len(brokers) == 0 || get("BKN_TRACE_KAFKA_SASL_MECHANISM") != "PLAIN" || username == "" || password == "" || producerID == "" || streamID == "" || identity == "" || revision == "" {
		return nil, fmt.Errorf("invalid BKN Trace Evidence Kafka configuration")
	}
	queueRecords, err := boundedEnv("BKN_TRACE_EVIDENCE_QUEUE_MAX_RECORDS", 4096, 1, 1000000)
	if err != nil {
		return nil, err
	}
	queueBytes, err := boundedEnv("BKN_TRACE_EVIDENCE_QUEUE_MAX_BYTES", 67108864, 1, 1<<30)
	if err != nil {
		return nil, err
	}
	maxRecord, err := boundedEnv("BKN_TRACE_EVIDENCE_MAX_RECORD_BYTES", 1048576, 1, 64<<20)
	if err != nil {
		return nil, err
	}
	maxAttempts, err := boundedEnv("BKN_TRACE_EVIDENCE_MAX_ATTEMPTS", 3, 1, 10)
	if err != nil {
		return nil, err
	}
	backoff, err := boundedEnv("BKN_TRACE_EVIDENCE_RETRY_BACKOFF_MS", 100, 1, 60000)
	if err != nil {
		return nil, err
	}
	producer, err := kafkasender.NewProducer(kafkasender.Config{Brokers: brokers, Mechanism: "PLAIN", Username: username, Password: password})
	if err != nil {
		return nil, err
	}
	publisher, err := evidencepublisher.New(evidencepublisher.Config{
		ProducerID: producerID, BaseStreamID: streamID, WorkloadIdentity: identity,
		ProcessBootID: uuid.NewString(), CapturePolicyRevision: revision,
		QueueMaxRecords: queueRecords, QueueMaxBytes: queueBytes, MaxRecordBytes: maxRecord,
		MaxAttempts: maxAttempts, RetryBackoff: time.Duration(backoff) * time.Millisecond,
	}, kafkasender.NewEvidence(producer))
	if err != nil {
		_ = producer.Close()
		return nil, err
	}
	return &EvidencePublisherRuntime{Publisher: publisher, Producer: producer}, nil
}

func boundedEnv(name string, fallback, min, max int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < min || parsed > max {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return parsed, nil
}

func splitBrokers(value string) []string {
	var brokers []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			brokers = append(brokers, part)
		}
	}
	return brokers
}
