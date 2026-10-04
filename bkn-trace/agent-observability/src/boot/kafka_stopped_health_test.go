// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package boot

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/kafkaruntime"
	kafka "github.com/segmentio/kafka-go"
	"net/http/httptest"
	"testing"
	"time"
)

type specCloseReader struct{ entered, release chan struct{} }

func (r *specCloseReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	<-ctx.Done()
	return kafka.Message{}, ctx.Err()
}
func (r *specCloseReader) CommitMessages(context.Context, ...kafka.Message) error {
	return errors.New("unused")
}
func (r *specCloseReader) Close() error { close(r.entered); <-r.release; return nil }
func TestSpecStoppedLoopMustFailHealthEvenWhileReaderCloseBlocks(t *testing.T) {
	reader := &specCloseReader{make(chan struct{}), make(chan struct{})}
	runtime, err := kafkaruntime.NewWithFactory(conf.KafkaConsumerConfig{}, conf.KafkaTopicConsumerConfig{Enabled: true}, func(context.Context, kafka.Message) error { return nil }, func(conf.KafkaConsumerConfig, conf.KafkaTopicConsumerConfig) (kafkaruntime.Reader, error) {
		return reader, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- runtime.Shutdown(context.Background()) }()
	defer func() {
		close(reader.release)
		select {
		case err := <-finished:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("shutdown did not finish")
		}
	}()
	select {
	case <-reader.entered:
	case <-time.After(time.Second):
		t.Fatal("close not reached")
	}
	h := newKafkaHealth()
	h.set("evidence", runtime)
	for _, live := range []bool{false, true} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/health", nil)
		if live {
			h.serveLiveHTTP(rec, req)
		} else {
			h.serveHTTP(rec, req)
		}
		if rec.Code != 503 {
			t.Errorf("stopped runtime reports HTTP %d live=%t state=%+v", rec.Code, live, runtime.State())
		}
	}
}
