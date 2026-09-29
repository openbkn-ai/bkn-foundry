package audit

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// PublishTelemetry exposes only aggregate producer outcomes. It never retains
// Audit payloads, actor IDs, request IDs or target IDs.
type PublishTelemetry struct {
	mu     sync.Mutex
	counts map[publishOutcomeKey]uint64
}

type publishOutcomeKey struct {
	sourceID string
	reason   string
}

func NewPublishTelemetry() *PublishTelemetry {
	return &PublishTelemetry{counts: make(map[publishOutcomeKey]uint64)}
}

func (t *PublishTelemetry) Observe(reason string) {
	t.ObserveForSource("bkn-safe-admin", reason)
}

// ObserveForSource records a bounded producer outcome. sourceID must be a
// registered static source identifier, never a request-derived value.
func (t *PublishTelemetry) ObserveForSource(sourceID, reason string) {
	if t == nil {
		return
	}
	if sourceID == "" {
		sourceID = "unknown"
	}
	t.mu.Lock()
	t.counts[publishOutcomeKey{sourceID: sourceID, reason: reason}]++
	t.mu.Unlock()
}

func (t *PublishTelemetry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	keys := make([]publishOutcomeKey, 0, len(t.counts))
	for key := range t.counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].sourceID != keys[j].sourceID {
			return keys[i].sourceID < keys[j].sourceID
		}
		return keys[i].reason < keys[j].reason
	})
	for _, key := range keys {
		reason := key.reason
		result, labelReason := reason, "none"
		if strings.HasPrefix(reason, "dropped_") {
			result, labelReason = "dropped", strings.TrimPrefix(reason, "dropped_")
		}
		_, _ = fmt.Fprintf(w, "audit_event_publish_total{source_id=\"%s\",result=\"%s\",reason=\"%s\"} %d\n", key.sourceID, result, labelReason, t.counts[key])
	}
}
