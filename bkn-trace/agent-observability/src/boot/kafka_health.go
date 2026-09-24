// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package boot

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/kafkaruntime"
)

type kafkaHealth struct {
	mu        sync.RWMutex
	consumers map[string]*kafkaruntime.Runtime
}

func newKafkaHealth() *kafkaHealth {
	return &kafkaHealth{consumers: map[string]*kafkaruntime.Runtime{}}
}

func (h *kafkaHealth) set(name string, runtime *kafkaruntime.Runtime) {
	h.mu.Lock()
	h.consumers[name] = runtime
	h.mu.Unlock()
}

func (h *kafkaHealth) serveHTTP(w http.ResponseWriter, _ *http.Request) {
	h.mu.RLock()
	states := make(map[string]kafkaruntime.State, len(h.consumers))
	ready := true
	for name, runtime := range h.consumers {
		state := runtime.State()
		states[name] = state
		if state.Enabled && !state.Ready {
			ready = false
		}
	}
	h.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ready": ready, "consumers": states})
}

func (h *kafkaHealth) serveLiveHTTP(w http.ResponseWriter, _ *http.Request) {
	h.mu.RLock()
	live := true
	for _, runtime := range h.consumers {
		state := runtime.State()
		if state.Enabled && (state.Reason == "fetch_failed" || state.Reason == "ledger_decision_pending" || state.Reason == "offset_commit_failed") {
			live = false
		}
	}
	h.mu.RUnlock()
	if !live {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_, _ = w.Write([]byte("{\"live\":" + strconv.FormatBool(live) + "}"))
}
