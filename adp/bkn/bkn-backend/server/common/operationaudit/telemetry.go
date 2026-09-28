package operationaudit

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// PublishTelemetry counts every producer outcome while allowing one diagnostic
// log per stable reason per minute. It never stores event payloads.
type PublishTelemetry struct {
	mu      sync.Mutex
	counts  map[string]uint64
	lastLog map[string]time.Time
}

func NewPublishTelemetry() *PublishTelemetry {
	return &PublishTelemetry{counts: make(map[string]uint64), lastLog: make(map[string]time.Time)}
}

func (t *PublishTelemetry) Observe(reason string, now time.Time) (uint64, bool) {
	if t == nil {
		return 0, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counts[reason]++
	last := t.lastLog[reason]
	if !strings.HasPrefix(reason, "dropped_") || (!last.IsZero() && now.Sub(last) < time.Minute) {
		return t.counts[reason], false
	}
	t.lastLog[reason] = now
	return t.counts[reason], true
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
		result := reason
		labelReason := "none"
		if strings.HasPrefix(reason, "dropped_") {
			result = "dropped"
			labelReason = strings.TrimPrefix(reason, "dropped_")
		}
		_, _ = fmt.Fprintf(w, "audit_event_publish_total{source_id=\"bkn-backend\",result=\"%s\",reason=\"%s\"} %d\n", result, labelReason, t.counts[reason])
	}
}
