package bkntrace

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/kafkasender"
)

type evidencePublisherConfig struct {
	Publisher evidencepublisher.Config
	Brokers   []string
	Mechanism string
	Username  string
	Password  string
}

type EvidencePublisherRuntime struct {
	Publisher *evidencepublisher.Publisher
	Producer  interface{ Close() error }
}

func CloseEvidenceProducer(producer interface{ Close() error }) error {
	if producer == nil {
		return nil
	}
	return producer.Close()
}

func newEvidencePublisher() (*EvidencePublisherRuntime, error) {
	cfg, err := loadEvidencePublisherConfig()
	if err != nil {
		return nil, err
	}
	producer, err := kafkasender.NewProducer(kafkasender.Config{Brokers: cfg.Brokers, Mechanism: cfg.Mechanism, Username: cfg.Username, Password: cfg.Password})
	if err != nil {
		return nil, err
	}
	publisher, err := evidencepublisher.New(cfg.Publisher, kafkasender.NewEvidence(producer))
	if err != nil {
		_ = producer.Close()
		return nil, err
	}
	return &EvidencePublisherRuntime{Publisher: publisher, Producer: producer}, nil
}

func NewEvidencePublisher() (*evidencepublisher.Publisher, error) {
	runtime, err := newEvidencePublisher()
	if err != nil {
		return nil, err
	}
	return runtime.Publisher, nil
}

func NewEvidencePublisherRuntime() (*EvidencePublisherRuntime, error) { return newEvidencePublisher() }

func loadEvidencePublisherConfig() (evidencePublisherConfig, error) {
	get := func(key string) string { return strings.TrimSpace(os.Getenv(key)) }
	brokers := splitNonEmpty(get("BKN_TRACE_KAFKA_BROKERS"))
	mechanism := get("BKN_TRACE_KAFKA_SASL_MECHANISM")
	username, password := get("BKN_TRACE_KAFKA_USERNAME"), os.Getenv("BKN_TRACE_KAFKA_PASSWORD")
	producerID, identity, streamID := get("BKN_TRACE_PRODUCER_ID"), get("BKN_TRACE_WORKLOAD_IDENTITY"), get("BKN_TRACE_PRODUCER_STREAM_ID")
	revision := get("BKN_TRACE_CAPTURE_POLICY_REVISION")
	if len(brokers) == 0 || mechanism != "PLAIN" || username == "" || password == "" || producerID == "" || identity == "" || streamID == "" || revision == "" {
		return evidencePublisherConfig{}, errors.New("invalid BKN Trace Evidence Kafka configuration")
	}
	if parsed, err := strconv.ParseUint(revision, 10, 64); err != nil || parsed == 0 {
		return evidencePublisherConfig{}, fmt.Errorf("invalid BKN_TRACE_CAPTURE_POLICY_REVISION")
	}
	queueMaxRecords, err := boundedPositive(getInt(get("BKN_TRACE_EVIDENCE_QUEUE_MAX_RECORDS"), 4096), 1, 1000000)
	if err != nil {
		return evidencePublisherConfig{}, err
	}
	queueMaxBytes, err := boundedPositive(getInt(get("BKN_TRACE_EVIDENCE_QUEUE_MAX_BYTES"), 67108864), 1, 1<<30)
	if err != nil {
		return evidencePublisherConfig{}, err
	}
	maxRecordBytes, err := boundedPositive(getInt(get("BKN_TRACE_EVIDENCE_MAX_RECORD_BYTES"), 1048576), 1, 64<<20)
	if err != nil {
		return evidencePublisherConfig{}, err
	}
	maxAttempts, err := boundedPositive(getInt(get("BKN_TRACE_EVIDENCE_MAX_ATTEMPTS"), 3), 1, 10)
	if err != nil {
		return evidencePublisherConfig{}, err
	}
	retryBackoffMs, err := boundedPositive(getInt(get("BKN_TRACE_EVIDENCE_RETRY_BACKOFF_MS"), 100), 1, 60000)
	if err != nil {
		return evidencePublisherConfig{}, err
	}
	return evidencePublisherConfig{
		Publisher: evidencepublisher.Config{
			ProducerID: producerID, BaseStreamID: streamID, WorkloadIdentity: identity,
			ProcessBootID: uuid.NewString(), CapturePolicyRevision: revision,
			QueueMaxRecords: queueMaxRecords, QueueMaxBytes: queueMaxBytes,
			MaxRecordBytes: maxRecordBytes, MaxAttempts: maxAttempts,
			RetryBackoff: time.Duration(retryBackoffMs) * time.Millisecond,
		},
		Brokers: brokers, Mechanism: mechanism, Username: username, Password: password,
	}, nil
}

func getInt(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return -1
	}
	return parsed
}

func boundedPositive(value, min, max int) (int, error) {
	if value < min || value > max {
		return 0, fmt.Errorf("invalid bounded Evidence publisher setting: %d", value)
	}
	return value, nil
}

func splitNonEmpty(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
