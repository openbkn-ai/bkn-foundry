// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/auditpublisher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconnectPublisherRetriesUntilAvailableAndClosesOnStop(t *testing.T) {
	recorder := NewKafkaRecorder(nil, "test", NewPublishTelemetry())
	ticks := make(chan time.Time, 2)
	stop := make(chan struct{})
	done := make(chan struct{})
	var attempts, closed atomic.Int32
	publisher := &captureKafkaAuditPublisher{disposition: auditpublisher.Accepted}
	go func() {
		defer close(done)
		ReconnectPublisher(stop, ticks, recorder, func() (KafkaPublisher, func(), error) {
			if attempts.Add(1) == 1 {
				return nil, nil, errors.New("broker unavailable")
			}
			return publisher, func() { closed.Add(1) }, nil
		})
	}()
	ticks <- time.Now()
	ticks <- time.Now()
	require.Eventually(t, func() bool {
		recorder.mu.RLock()
		defer recorder.mu.RUnlock()
		return recorder.publisher == publisher
	}, time.Second, time.Millisecond)
	close(stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconnect loop did not stop")
	}
	assert.EqualValues(t, 2, attempts.Load())
	assert.EqualValues(t, 1, closed.Load())
}
