// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import (
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

// ReconnectPublisher retries only an initially unavailable broker. HTTP
// requests continue through a nil publisher and report an explicit gap until
// reconnection; the loop owns and closes the recovered runtime on shutdown.
func ReconnectPublisher(
	stop <-chan struct{}, ticks <-chan time.Time, recorder *KafkaRecorder,
	connect func() (KafkaPublisher, func(), error),
) {
	for {
		select {
		case <-stop:
			return
		case _, ok := <-ticks:
			if !ok {
				return
			}
		}
		publisher, closeRuntime, err := connect()
		if err != nil {
			logger.Warnf("Vega Audit Kafka publisher recovery pending: %v", err)
			continue
		}
		if publisher == nil || closeRuntime == nil {
			logger.Warn("Vega Audit Kafka publisher recovery returned empty runtime")
			continue
		}
		recorder.SetPublisher(publisher)
		logger.Info("Vega Audit Kafka publisher recovered")
		<-stop
		recorder.SetPublisher(nil)
		closeRuntime()
		return
	}
}
