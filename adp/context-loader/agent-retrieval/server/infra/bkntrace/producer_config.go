package bkntrace

import (
	"context"
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
	Runtime  *evidencepublisher.PublisherRuntime
	Producer interface{ Close() error }
}

type evidencePublisherConfig struct {
	Publisher evidencepublisher.Config
	Brokers   []string
	Username  string
	Password  string
}

func NewEvidencePublisherRuntime() (*EvidencePublisherRuntime, error) {
	config, err := loadEvidencePublisherConfig()
	if err != nil {
		return nil, err
	}
	producer, err := kafkasender.NewProducer(kafkasender.Config{Brokers: config.Brokers, Mechanism: "PLAIN", Username: config.Username, Password: config.Password})
	if err != nil {
		return nil, err
	}
	runtime, err := evidencepublisher.NewPublisherRuntimeFromEnvironment(context.Background(), config.Publisher, kafkasender.NewEvidence(producer))
	if err != nil {
		_ = producer.Close()
		return nil, err
	}
	return &EvidencePublisherRuntime{Runtime: runtime, Producer: producer}, nil
}

func loadEvidencePublisherConfig() (evidencePublisherConfig, error) {
	get := func(key string) string { return strings.TrimSpace(os.Getenv(key)) }
	queueMaxRecords, err := positiveEnvInt("BKN_TRACE_EVIDENCE_QUEUE_MAX_RECORDS")
	if err != nil {
		return evidencePublisherConfig{}, err
	}
	queueMaxBytes, err := positiveEnvInt("BKN_TRACE_EVIDENCE_QUEUE_MAX_BYTES")
	if err != nil {
		return evidencePublisherConfig{}, err
	}
	maxRecordBytes, err := positiveEnvInt("BKN_TRACE_EVIDENCE_MAX_RECORD_BYTES")
	if err != nil {
		return evidencePublisherConfig{}, err
	}
	maxAttempts, err := positiveEnvInt("BKN_TRACE_EVIDENCE_MAX_ATTEMPTS")
	if err != nil {
		return evidencePublisherConfig{}, err
	}
	retryBackoffMS, err := positiveEnvInt("BKN_TRACE_EVIDENCE_RETRY_BACKOFF_MS")
	if err != nil {
		return evidencePublisherConfig{}, err
	}
	brokers := splitBrokers(get("BKN_TRACE_KAFKA_BROKERS"))
	if len(brokers) == 0 || get("BKN_TRACE_KAFKA_SASL_MECHANISM") != "PLAIN" || get("BKN_TRACE_KAFKA_USERNAME") == "" || os.Getenv("BKN_TRACE_KAFKA_PASSWORD") == "" || get("BKN_TRACE_PRODUCER_ID") == "" || get("BKN_TRACE_WORKLOAD_IDENTITY") == "" || get("BKN_TRACE_PRODUCER_STREAM_ID") == "" {
		return evidencePublisherConfig{}, fmt.Errorf("invalid BKN Trace Evidence Kafka configuration")
	}
	return evidencePublisherConfig{Publisher: evidencepublisher.Config{
		ProducerID: get("BKN_TRACE_PRODUCER_ID"), BaseStreamID: get("BKN_TRACE_PRODUCER_STREAM_ID"),
		WorkloadIdentity: get("BKN_TRACE_WORKLOAD_IDENTITY"), ProcessBootID: uuid.NewString(),
		QueueMaxRecords: queueMaxRecords,
		QueueMaxBytes:   queueMaxBytes, MaxRecordBytes: maxRecordBytes, MaxAttempts: maxAttempts,
		RetryBackoff: time.Duration(retryBackoffMS) * time.Millisecond,
	}, Brokers: brokers, Username: get("BKN_TRACE_KAFKA_USERNAME"), Password: os.Getenv("BKN_TRACE_KAFKA_PASSWORD")}, nil
}

func positiveEnvInt(name string) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func splitBrokers(value string) []string {
	var result []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
