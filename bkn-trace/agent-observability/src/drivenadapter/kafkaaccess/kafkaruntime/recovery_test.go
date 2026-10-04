// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package kafkaruntime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	kafka "github.com/segmentio/kafka-go"
)

type recoveringReader struct {
	*fakeReader
	fetchFailures  atomic.Int32
	commitFailures atomic.Int32
	commitCalls    atomic.Int32
}

func (r *recoveringReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	if r.fetchFailures.CompareAndSwap(1, 0) {
		return kafka.Message{}, errors.New("broker temporarily unavailable")
	}
	return r.fakeReader.FetchMessage(ctx)
}

func (r *recoveringReader) CommitMessages(ctx context.Context, messages ...kafka.Message) error {
	r.commitCalls.Add(1)
	if r.commitFailures.CompareAndSwap(1, 0) {
		return errors.New("commit temporarily unavailable")
	}
	return r.fakeReader.CommitMessages(ctx, messages...)
}

func TestRuntimeRecoversWithoutRestartOrSkippingUnconfirmedRecord(t *testing.T) {
	for _, stage := range []string{"fetch", "process", "commit"} {
		t.Run(stage, func(t *testing.T) {
			reader := &recoveringReader{fakeReader: newFakeReader()}
			if stage == "fetch" {
				reader.fetchFailures.Store(1)
			}
			if stage == "commit" {
				reader.commitFailures.Store(1)
			}
			var processCalls atomic.Int32
			firstFailure := make(chan struct{})
			runtime, err := NewWithFactory(conf.KafkaConsumerConfig{}, conf.KafkaTopicConsumerConfig{Enabled: true}, func(_ context.Context, message kafka.Message) error {
				if message.Partition != 2 || message.Offset != 41 {
					t.Errorf("retried different message: %+v", message)
				}
				if processCalls.Add(1) == 1 && stage == "process" {
					close(firstFailure)
					return errors.New("receipt confirmation transaction unavailable")
				}
				return nil
			}, func(conf.KafkaConsumerConfig, conf.KafkaTopicConsumerConfig) (Reader, error) { return reader, nil })
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
			reader.messages <- kafka.Message{Topic: "evidence", Partition: 2, Offset: 41}
			if stage == "process" {
				select {
				case <-firstFailure:
				case <-time.After(time.Second):
					t.Fatal("processor not called")
				}
				if reader.commitCalls.Load() != 0 {
					t.Fatal("offset committed before confirmation")
				}
			}
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for len(reader.ordered()) == 0 || !runtime.State().Ready {
				select {
				case <-runtime.done:
					t.Fatal("recoverable error stopped consumer; needs whole-pod restart")
				case <-deadline.C:
					t.Fatal("consumer did not recover")
				case <-ticker.C:
				}
			}
			wantProcess := int32(1)
			if stage == "process" {
				wantProcess = 2
			}
			if got := processCalls.Load(); got != wantProcess {
				t.Fatalf("process calls=%d want %d", got, wantProcess)
			}
			wantCommit := int32(1)
			if stage == "commit" {
				wantCommit = 2
			}
			if got := reader.commitCalls.Load(); got != wantCommit {
				t.Fatalf("commit calls=%d want %d", got, wantCommit)
			}
			if got := runtime.State(); !got.Ready || got.Reason != "polling" {
				t.Fatalf("did not clear degraded state: %+v", got)
			}
		})
	}
}
