// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package kafkaruntime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	kafka "github.com/segmentio/kafka-go"
)

type fakeReader struct {
	messages chan kafka.Message
	mu       sync.Mutex
	order    []string
	closed   chan struct{}
}

func newFakeReader() *fakeReader {
	return &fakeReader{messages: make(chan kafka.Message, 1), closed: make(chan struct{})}
}
func (f *fakeReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	select {
	case message := <-f.messages:
		return message, nil
	case <-ctx.Done():
		return kafka.Message{}, ctx.Err()
	}
}
func (f *fakeReader) CommitMessages(ctx context.Context, _ ...kafka.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.order = append(f.order, "commit")
	return nil
}
func (f *fakeReader) Close() error {
	select {
	case <-f.closed:
	default:
		close(f.closed)
	}
	return nil
}
func (f *fakeReader) ordered() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.order...)
}

func TestRuntimeDurableProcessorCompletesBeforeSynchronousOffsetCommit(t *testing.T) {
	reader := newFakeReader()
	processed := make(chan struct{})
	config := conf.KafkaConsumerConfig{}
	topic := conf.KafkaTopicConsumerConfig{Enabled: true, Topic: "openbkn.audit.v1", Group: "audit-group"}
	runtime, err := NewWithFactory(config, topic, func(context.Context, kafka.Message) error {
		reader.mu.Lock()
		reader.order = append(reader.order, "ledger")
		reader.mu.Unlock()
		close(processed)
		return nil
	}, func(conf.KafkaConsumerConfig, conf.KafkaTopicConsumerConfig) (Reader, error) { return reader, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	reader.messages <- kafka.Message{Topic: topic.Topic, Partition: 1, Offset: 4}
	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Fatal("record was not processed")
	}
	deadline := time.After(time.Second)
	for len(reader.ordered()) < 2 {
		select {
		case <-deadline:
			t.Fatal("offset was not committed")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	order := reader.ordered()
	if len(order) != 2 || order[0] != "ledger" || order[1] != "commit" {
		t.Fatalf("processing/offset order = %v", order)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestLogAppendTimeStartupSettingMustBeExplicitAndExact(t *testing.T) {
	for _, test := range []struct {
		name    string
		entries []sarama.ConfigEntry
		wantErr bool
	}{
		{name: "exact LogAppendTime", entries: []sarama.ConfigEntry{{Name: "message.timestamp.type", Value: "LogAppendTime"}}},
		{name: "CreateTime", entries: []sarama.ConfigEntry{{Name: "message.timestamp.type", Value: "CreateTime"}}, wantErr: true},
		{name: "missing timestamp config", entries: []sarama.ConfigEntry{{Name: "retention.ms", Value: "1000"}}, wantErr: true},
		{name: "unreadable empty config", entries: nil, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := verifyLogAppendTimeSetting(test.entries)
			if (err != nil) != test.wantErr {
				t.Fatalf("verify setting error = %v, wantErr=%t", err, test.wantErr)
			}
		})
	}
}

func TestRuntimeNeverCommitsWhenProcessorHasNoTerminalDecision(t *testing.T) {
	reader := newFakeReader()
	runtime, err := NewWithFactory(conf.KafkaConsumerConfig{}, conf.KafkaTopicConsumerConfig{Enabled: true, Topic: "openbkn.audit.v1", Group: "audit-group"}, func(context.Context, kafka.Message) error {
		return errors.New("ledger unavailable")
	}, func(conf.KafkaConsumerConfig, conf.KafkaTopicConsumerConfig) (Reader, error) { return reader, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	reader.messages <- kafka.Message{Topic: "openbkn.audit.v1", Partition: 0, Offset: 2}
	deadline := time.Now().Add(time.Second)
	for runtime.State().Reason != "ledger_decision_pending" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := runtime.State(); got.Ready || got.Reason != "ledger_decision_pending" {
		t.Fatalf("unexpected failure state: %+v", got)
	}
	if commits := reader.ordered(); len(commits) != 0 {
		t.Fatalf("offset advanced without ledger terminal decision: %v", commits)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownDrainsAcceptedRecordAndCommitBeforeReturning(t *testing.T) {
	reader := newFakeReader()
	processing := make(chan struct{})
	release := make(chan struct{})
	processCalls := atomic.Int32{}
	runtime, err := NewWithFactory(conf.KafkaConsumerConfig{}, conf.KafkaTopicConsumerConfig{Enabled: true, Topic: "openbkn.audit.v1", Group: "audit-group"}, func(context.Context, kafka.Message) error {
		if processCalls.Add(1) == 1 {
			close(processing)
			<-release
		}
		reader.mu.Lock()
		reader.order = append(reader.order, "ledger")
		reader.mu.Unlock()
		return nil
	}, func(conf.KafkaConsumerConfig, conf.KafkaTopicConsumerConfig) (Reader, error) { return reader, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	reader.messages <- kafka.Message{Topic: "openbkn.audit.v1", Partition: 0, Offset: 7}
	select {
	case <-processing:
	case <-time.After(time.Second):
		t.Fatal("record was not accepted for processing")
	}
	shutdown := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		shutdown <- runtime.Shutdown(ctx)
	}()
	select {
	case err := <-shutdown:
		t.Fatalf("shutdown returned before accepted record drained: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	// A queued message must remain unprocessed after shutdown stops polling.
	reader.messages <- kafka.Message{Topic: "openbkn.audit.v1", Partition: 0, Offset: 8}
	close(release)
	select {
	case err := <-shutdown:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after record processing was released")
	}
	if order := reader.ordered(); len(order) != 2 || order[0] != "ledger" || order[1] != "commit" {
		t.Fatalf("accepted record was not durably processed before commit: %v", order)
	}
	if got := processCalls.Load(); got != 1 {
		t.Fatalf("processed %d records after shutdown stopped polling, want only the in-flight record", got)
	}
}

func TestShutdownDeadlineReturnsWithoutCommittingInFlightRecord(t *testing.T) {
	reader := newFakeReader()
	processing := make(chan struct{})
	release := make(chan struct{})
	runtime, err := NewWithFactory(conf.KafkaConsumerConfig{}, conf.KafkaTopicConsumerConfig{Enabled: true, Topic: "openbkn.audit.v1", Group: "audit-group"}, func(context.Context, kafka.Message) error {
		close(processing)
		<-release // Simulate a processor that cannot be interrupted by cancellation.
		return nil
	}, func(conf.KafkaConsumerConfig, conf.KafkaTopicConsumerConfig) (Reader, error) { return reader, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	reader.messages <- kafka.Message{Topic: "openbkn.audit.v1", Partition: 0, Offset: 8}
	select {
	case <-processing:
	case <-time.After(time.Second):
		t.Fatal("record was not accepted for processing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = runtime.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	if time.Since(started) > 250*time.Millisecond {
		t.Fatal("shutdown exceeded its deadline while waiting for blocked processor")
	}
	close(release)
	select {
	case <-runtime.done:
	case <-time.After(time.Second):
		t.Fatal("runtime did not exit after processor was released")
	}
	if commits := reader.ordered(); len(commits) != 0 {
		t.Fatalf("offset committed after shutdown deadline: %v", commits)
	}
}
