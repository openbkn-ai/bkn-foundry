// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package otelcolmetrics

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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

func (client *Client) Read(ctx context.Context) (sourcecoveragesvc.Snapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint, nil)
	if err != nil {
		return sourcecoveragesvc.Snapshot{}, fmt.Errorf("build collector metrics request: %w", err)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return sourcecoveragesvc.Snapshot{}, fmt.Errorf("request collector metrics: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return sourcecoveragesvc.Snapshot{}, fmt.Errorf("collector metrics returned status %d", response.StatusCode)
	}
	values, err := readMetrics(response.Body)
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
	snapshot, err := client.Read(ctx)
	if err != nil {
		return QueueSample{}, err
	}
	if snapshot.QueueCapacity <= 0 || snapshot.QueueSize < 0 {
		return QueueSample{}, fmt.Errorf("collector queue metrics omitted positive queue capacity")
	}
	utilization := float64(snapshot.QueueSize) / float64(snapshot.QueueCapacity)
	if utilization < 0 {
		utilization = 0
	}
	if utilization > 1 {
		utilization = 1
	}
	return QueueSample{Utilization: utilization, SampledAt: time.Now().UTC()}, nil
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
		if err != nil || value < 0 {
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
