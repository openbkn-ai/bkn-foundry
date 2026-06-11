package observability

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

type Metrics struct {
	requests sync.Map
}

type requestCounter struct {
	value atomic.Uint64
}

func (m *Metrics) IncRequest(method, route, status string) {
	key := fmt.Sprintf("%s|%s|%s", method, route, status)
	counter, _ := m.requests.LoadOrStore(key, &requestCounter{})
	counter.(*requestCounter).value.Add(1)
}

func (m *Metrics) RenderPrometheus() string {
	type metricRow struct {
		method string
		route  string
		status string
		value  uint64
	}

	rows := make([]metricRow, 0)
	m.requests.Range(func(key, value any) bool {
		parts := strings.Split(key.(string), "|")
		if len(parts) != 3 {
			return true
		}

		rows = append(rows, metricRow{
			method: parts[0],
			route:  parts[1],
			status: parts[2],
			value:  value.(*requestCounter).value.Load(),
		})
		return true
	})

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].route != rows[j].route {
			return rows[i].route < rows[j].route
		}
		if rows[i].method != rows[j].method {
			return rows[i].method < rows[j].method
		}
		return rows[i].status < rows[j].status
	})

	var builder strings.Builder
	builder.WriteString("# HELP capabilities_lab_http_requests_total Total HTTP requests handled by capabilities-lab.\n")
	builder.WriteString("# TYPE capabilities_lab_http_requests_total counter\n")
	for _, row := range rows {
		builder.WriteString(fmt.Sprintf(
			"capabilities_lab_http_requests_total{method=%q,route=%q,status=%q} %d\n",
			row.method,
			row.route,
			row.status,
			row.value,
		))
	}

	return builder.String()
}
