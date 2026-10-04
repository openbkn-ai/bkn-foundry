// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package boot

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/kafkaruntime"
	kafka "github.com/segmentio/kafka-go"
)

func TestConsumerOutageKeepsLifecycleServingButReportsEvidenceUnavailable(t *testing.T) {
	runtime, err := kafkaruntime.NewWithFactory(conf.KafkaConsumerConfig{}, conf.KafkaTopicConsumerConfig{Enabled: true}, func(context.Context, kafka.Message) error { return nil }, func(conf.KafkaConsumerConfig, conf.KafkaTopicConsumerConfig) (kafkaruntime.Reader, error) {
		return failingHealthReader{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	deadline := time.Now().Add(time.Second)
	for (runtime.State().Reason == "starting" || runtime.State().Reason == "polling") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	health := newKafkaHealth()
	health.set("evidence", runtime)
	response := httptest.NewRecorder()
	health.serveHTTP(response, httptest.NewRequest("GET", "/health/ready", nil))
	if response.Code != 200 {
		t.Fatalf("consumer outage removed lifecycle from routing: status=%d", response.Code)
	}
	var body struct {
		Ready          bool                          `json:"ready"`
		ConsumersReady bool                          `json:"consumers_ready"`
		Consumers      map[string]kafkaruntime.State `json:"consumers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Ready || body.ConsumersReady || body.Consumers["evidence"].Ready {
		t.Fatalf("consumer failure hidden: %+v", body)
	}
	live := httptest.NewRecorder()
	health.serveLiveHTTP(live, httptest.NewRequest("GET", "/health/live", nil))
	if live.Code != 200 {
		t.Fatalf("recovering consumer triggers whole-pod restart: %d", live.Code)
	}
}
