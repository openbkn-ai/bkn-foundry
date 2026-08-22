// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package coremetrics

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icoremetrics"
)

var metricKinds = map[string]string{
	icoremetrics.ConversationsTotal:              "counter",
	icoremetrics.InteractionsTotal:               "counter",
	icoremetrics.SessionRejectionsTotal:          "counter",
	icoremetrics.InteractionsAbandonedTotal:      "counter",
	icoremetrics.SessionStoreErrorsTotal:         "counter",
	icoremetrics.SessionTransitionConflictsTotal: "counter",
	icoremetrics.EvidenceIngestTotal:             "counter",
	icoremetrics.EvidenceHashConflictsTotal:      "counter",
	icoremetrics.ProjectionErrorsTotal:           "counter",
	icoremetrics.ProjectionLagSeconds:            "gauge",
	icoremetrics.ProjectionReady:                 "gauge",
	icoremetrics.AssemblyLagSeconds:              "gauge",
}

type Registry struct {
	counters sync.Map
	gauges   sync.Map
}

func New() *Registry {
	return &Registry{}
}

func (r *Registry) Increment(name string) {
	r.Add(name, 1)
}

func (r *Registry) Add(name string, delta uint64) {
	if metricKinds[name] != "counter" {
		return
	}
	value, _ := r.counters.LoadOrStore(name, &atomic.Uint64{})
	value.(*atomic.Uint64).Add(delta)
}

func (r *Registry) Set(name string, value float64) {
	if metricKinds[name] != "gauge" {
		return
	}
	stored, _ := r.gauges.LoadOrStore(name, &atomic.Uint64{})
	stored.(*atomic.Uint64).Store(math.Float64bits(value))
}

func (r *Registry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	names := make([]string, 0, len(metricKinds))
	for name := range metricKinds {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_, _ = fmt.Fprintf(w, "# TYPE %s %s\n%s %s\n", name, metricKinds[name], name, r.value(name))
	}
}

func (r *Registry) value(name string) string {
	if metricKinds[name] == "counter" {
		if value, ok := r.counters.Load(name); ok {
			return fmt.Sprintf("%d", value.(*atomic.Uint64).Load())
		}
		return "0"
	}
	if value, ok := r.gauges.Load(name); ok {
		return fmt.Sprintf("%g", math.Float64frombits(value.(*atomic.Uint64).Load()))
	}
	return "0"
}

var _ http.Handler = (*Registry)(nil)
