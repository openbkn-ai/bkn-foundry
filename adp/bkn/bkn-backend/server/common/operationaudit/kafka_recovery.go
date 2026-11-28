// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the repository root.

package operationaudit

import (
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"time"
)

// ReconnectPublisher restores an enabled publisher after boot-time connection
// failure. Requests remain fail-open and unavailable events are never replayed.
// The caller stops this owner after draining HTTP handlers and worker extensions.
func ReconnectPublisher(stop <-chan struct{}, ticks <-chan time.Time, recorder *KafkaRecorder, connect func() (KafkaPublisher, func(), error)) {
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
		if err != nil || publisher == nil || closeRuntime == nil {
			// A partially created connection must not leak when a factory fails.
			if closeRuntime != nil {
				closeRuntime()
			}
			logger.Warn("Audit Kafka publisher recovery pending; audit coverage_gap")
			continue
		}
		select {
		case <-stop:
			closeRuntime()
			return
		default:
		}
		recorder.SetPublisher(publisher)
		logger.Info("Audit Kafka publisher recovered")
		<-stop
		recorder.SetPublisher(nil)
		closeRuntime()
		return
	}
}
