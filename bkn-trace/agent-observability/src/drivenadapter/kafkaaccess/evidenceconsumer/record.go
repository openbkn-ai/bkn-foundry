package evidenceconsumer

import (
	"errors"
	"fmt"
)

type Header struct {
	Key   string
	Value string
}

type Record struct {
	Key              string
	Headers          []Header
	ProducerStreamID string
	ProducerSequence uint64
	BrokerTimestamp  string
	TimestampType    string
}

type ClosureWatermark struct {
	InstanceID           string
	Revision             uint64
	LastAcceptedSequence uint64
	ClosedAt             string
}

type PolicySnapshot struct {
	Revision           uint64
	Enabled            bool
	InstanceID         string
	RegisteredRevision uint64
	Closure            ClosureWatermark
}

type Decision struct {
	Decision string
	Reason   string
}

var errDuplicateHeader = errors.New("duplicate Kafka header")

func ParseHeaders(headers []Header) (map[string]string, error) {
	parsed := make(map[string]string, len(headers))
	for _, header := range headers {
		if _, exists := parsed[header.Key]; exists {
			return nil, errDuplicateHeader
		}
		parsed[header.Key] = header.Value
	}
	return parsed, nil
}

func ValidateLiveRecordContract(record Record) error {
	if record.TimestampType != "LogAppendTime" {
		return fmt.Errorf("timestamp_type must be LogAppendTime")
	}
	headers, err := ParseHeaders(record.Headers)
	if err != nil {
		return err
	}
	want := map[string]string{
		"content-type":              "application/json",
		"bkn-trace-schema-version":  "3.0.0",
		"capture_policy_revision":   headers["capture_policy_revision"],
		"producer_instance_id":      headers["producer_instance_id"],
		"bkn-evidence-record-class": "live",
	}
	if len(headers) != len(want) {
		return fmt.Errorf("live header set mismatch")
	}
	for key, value := range want {
		if got, exists := headers[key]; !exists || got != value || value == "" {
			return fmt.Errorf("invalid live header %s", key)
		}
	}
	if record.Key != record.ProducerStreamID {
		return fmt.Errorf("record key must equal producer_stream_id")
	}
	return nil
}
