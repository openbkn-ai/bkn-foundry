// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN

package evidenceconsumer

import (
	"context"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceadmission"
	kafka "github.com/segmentio/kafka-go"
)

func TestKafkaProcessorReturnsNilAfterDurableTerminalRejection(t *testing.T) {
	brokerTime := time.Date(2026, time.September, 25, 9, 10, 11, 123000000, time.UTC)
	instance := "spiffe://cluster/ns/openbkn/sa/bkn-backend#boot-1"
	message := kafka.Message{
		Topic: Topic, Key: []byte("bkn-backend:boot-1"), Partition: 2, Offset: 41, Time: brokerTime,
		Headers: []kafka.Header{{Key: "content-type", Value: []byte("application/json")}, {Key: "bkn-trace-schema-version", Value: []byte("3.0.0")}, {Key: "capture_policy_revision", Value: []byte("42")}, {Key: "producer_instance_id", Value: []byte(instance)}, {Key: "bkn-evidence-record-class", Value: []byte("live")}},
		Value:   []byte(`{"producer_stream_id":"bkn-backend:boot-1","producer_sequence":7}`),
	}
	rejections := &processorRejections{}
	processor, err := NewProcessor(processorAdmission{snapshot: ievidenceadmission.Snapshot{Revision: 42, Enabled: false}}, &processorLedger{}, rejections)
	if err != nil {
		t.Fatal(err)
	}
	if err := KafkaProcessor(processor)(context.Background(), message); err != nil {
		t.Fatalf("KafkaProcessor() error = %v", err)
	}
	if !rejections.called || rejections.record.Partition != 2 || rejections.record.Offset != 41 || !rejections.record.BrokerTime.Equal(brokerTime) {
		t.Fatalf("durable rejection coordinate = %+v", rejections.record)
	}
}

func TestKafkaProcessorLeavesInvalidBrokerTimeUncommitted(t *testing.T) {
	processor := &Processor{}
	run := KafkaProcessor(processor)
	err := run(context.Background(), kafka.Message{Topic: Topic, Key: []byte("stream"), Value: []byte(`{}`)})
	if err == nil {
		t.Fatal("KafkaProcessor() error = nil; missing broker timestamp must remain uncommitted")
	}
}
