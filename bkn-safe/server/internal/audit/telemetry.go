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
	counts map[string]uint64
}

func NewPublishTelemetry() *PublishTelemetry {
	return &PublishTelemetry{counts: make(map[string]uint64)}
}

func (t *PublishTelemetry) Observe(reason string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.counts[reason]++
	t.mu.Unlock()
}

func (t *PublishTelemetry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	reasons := make([]string, 0, len(t.counts))
	for reason := range t.counts {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		result, labelReason := reason, "none"
		if strings.HasPrefix(reason, "dropped_") {
			result, labelReason = "dropped", strings.TrimPrefix(reason, "dropped_")
		}
		_, _ = fmt.Fprintf(w, "audit_event_publish_total{source_id=\"bkn-safe-admin\",result=\"%s\",reason=\"%s\"} %d\n", result, labelReason, t.counts[reason])
	}
}
