// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package boot

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

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

func TestKafkaFailureMakesReadinessAndLivenessFail(t *testing.T) {
	runtime, err := kafkaruntime.NewWithFactory(conf.KafkaConsumerConfig{}, conf.KafkaTopicConsumerConfig{Enabled: true, Topic: "openbkn.audit.v1", Group: "audit"}, func(context.Context, kafka.Message) error { return nil }, func(conf.KafkaConsumerConfig, conf.KafkaTopicConsumerConfig) (kafkaruntime.Reader, error) {
		return failingHealthReader{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	health := newKafkaHealth()
	health.set("audit", runtime)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Shutdown(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for runtime.State().Reason != "fetch_failed" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if runtime.State().Reason != "fetch_failed" {
		t.Fatalf("runtime did not surface fetch failure: %+v", runtime.State())
	}
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
