// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package otelcolmetrics

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sourcecoveragesvc"
)

const (
	refusedLogsMetric   = "otelcol_receiver_refused_log_records_total"
	failedLogsMetric    = "otelcol_exporter_send_failed_log_records_total"
	queueSizeMetric     = "otelcol_exporter_queue_size"
	queueCapacityMetric = "otelcol_exporter_queue_capacity"
)

type Client struct {
	endpoint   string
	httpClient *http.Client
}

type QueueSample struct {
	Utilization float64
	SampledAt   time.Time
}

func New(endpoint string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), httpClient: httpClient}
}

func (client *Client) readValues(ctx context.Context) (map[string]int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build collector metrics request: %w", err)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request collector metrics: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("collector metrics returned status %d", response.StatusCode)
	}
	values, err := readMetrics(response.Body)
	if err != nil {
		return nil, err
	}
	return values, nil
}

func (client *Client) Read(ctx context.Context) (sourcecoveragesvc.Snapshot, error) {
	values, err := client.readValues(ctx)
	if err != nil {
		return sourcecoveragesvc.Snapshot{}, err
	}
	return sourcecoveragesvc.Snapshot{
		RefusedLogs: values[refusedLogsMetric], FailedLogs: values[failedLogsMetric],
		QueueSize: values[queueSizeMetric], QueueCapacity: values[queueCapacityMetric],
	}, nil
}

// ReadQueueSample reuses the Collector metrics endpoint already used by the
// source-coverage monitor. A missing/zero queue capacity is not a healthy
// zero; it is an unavailable admission source and must fail closed.
func (client *Client) ReadQueueSample(ctx context.Context) (QueueSample, error) {
	values, err := client.readValues(ctx)
	if err != nil {
		return QueueSample{}, err
	}
	size, hasSize := values[queueSizeMetric]
	capacity, hasCapacity := values[queueCapacityMetric]
	if !hasSize {
		return QueueSample{}, queueMetricError("metric_missing", queueSizeMetric)
	}
	if !hasCapacity {
		return QueueSample{}, queueMetricError("metric_missing", queueCapacityMetric)
	}
	if capacity <= 0 {
		return QueueSample{}, queueMetricError("metric_invalid", queueCapacityMetric)
	}
	utilization := float64(size) / float64(capacity)
	if utilization < 0 {
		utilization = 0
	}
	if utilization > 1 {
		utilization = 1
	}
	return QueueSample{Utilization: utilization, SampledAt: time.Now().UTC()}, nil
}

func queueMetricError(reason, field string) error {
	return &capturepolicysvc.AdmissionBudgetError{Reason: reason, Metric: "trace_collector_queue", Fields: []string{field}, Cause: fmt.Errorf("collector queue metric %s is missing or invalid", field)}
}

func readMetrics(body interface{ Read([]byte) (int, error) }) (map[string]int64, error) {
	values := map[string]int64{}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if label := strings.IndexByte(name, '{'); label >= 0 {
			name = name[:label]
		}
		if !isCoverageMetric(name) {
			continue
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value >= float64(math.MaxInt64) {
			if name == queueSizeMetric || name == queueCapacityMetric {
				return nil, queueMetricError("metric_invalid", name)
			}
			continue
		}
		values[name] += int64(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read collector metrics: %w", err)
	}
	return values, nil
}

func isCoverageMetric(name string) bool {
	switch name {
	case refusedLogsMetric, failedLogsMetric, queueSizeMetric, queueCapacityMetric:
		return true
	default:
		return false
	}
}
