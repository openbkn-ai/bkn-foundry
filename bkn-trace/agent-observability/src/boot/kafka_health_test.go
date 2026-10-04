// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package boot

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/kafkaruntime"
	kafka "github.com/segmentio/kafka-go"
)

type failingHealthReader struct{}

func (failingHealthReader) FetchMessage(context.Context) (kafka.Message, error) {
	return kafka.Message{}, errors.New("broker unavailable")
}
func (failingHealthReader) CommitMessages(context.Context, ...kafka.Message) error { return nil }
func (failingHealthReader) Close() error                                           { return nil }

func TestConsumerNotStartedMakesReadinessAndLivenessFail(t *testing.T) {
	runtime, err := kafkaruntime.NewWithFactory(conf.KafkaConsumerConfig{}, conf.KafkaTopicConsumerConfig{Enabled: true, Topic: "openbkn.audit.v1", Group: "audit"}, func(context.Context, kafka.Message) error { return nil }, func(conf.KafkaConsumerConfig, conf.KafkaTopicConsumerConfig) (kafkaruntime.Reader, error) {
		return failingHealthReader{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	health := newKafkaHealth()
	health.set("audit", runtime)
	defer func() { _ = runtime.Shutdown(context.Background()) }()
	ready := httptest.NewRecorder()
	health.serveHTTP(ready, httptest.NewRequest("GET", "/health/ready", nil))
	if ready.Code != 503 {
		t.Fatalf("readiness status = %d", ready.Code)
	}
	live := httptest.NewRecorder()
	health.serveLiveHTTP(live, httptest.NewRequest("GET", "/health/live", nil))
	if live.Code != 503 {
		t.Fatalf("liveness status = %d", live.Code)
	}
}
