// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package capturepolicysvc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	ErrAdmissionBudgetUnavailable = errors.New("admission budget is unavailable")
	ErrAdmissionBudgetExceeded    = errors.New("admission budget exceeded")
)

type AdmissionBudgetThresholds struct {
	OpenSearchCapacity float64
	OpenSearchHeap     float64
	CollectorQueue     float64
	StoragePool        float64
}

func (t AdmissionBudgetThresholds) Validate() error {
	values := map[string]float64{
		"trace_opensearch_capacity":     t.OpenSearchCapacity,
		"trace_opensearch_heap":         t.OpenSearchHeap,
		"trace_collector_queue":         t.CollectorQueue,
		"trace_storage_connection_pool": t.StoragePool,
	}
	for metric, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > 1 {
			return fmt.Errorf("%w: threshold for %s must be in (0,1]", ErrAdmissionBudgetUnavailable, metric)
		}
	}
	return nil
}

type AdmissionMeasurementSource interface {
	Read(context.Context) (AdmissionMeasurement, error)
}

type AdmissionMeasurementSourceFunc func(context.Context) (AdmissionMeasurement, error)

func (f AdmissionMeasurementSourceFunc) Read(ctx context.Context) (AdmissionMeasurement, error) {
	return f(ctx)
}

// AdmissionBudgetProvider combines existing health sources. It performs no
// adaptive calculation: the four normalized values and their deployment/SLO
// thresholds are copied into the frozen AdmissionBudgetV1 read model.
type AdmissionBudgetProvider struct {
	profile    string
	thresholds AdmissionBudgetThresholds
	now        func() time.Time
	sources    []AdmissionMeasurementSource
}

func NewAdmissionBudgetProvider(profile string, thresholds AdmissionBudgetThresholds, sources ...AdmissionMeasurementSource) (*AdmissionBudgetProvider, error) {
	if profile == "" {
		return nil, fmt.Errorf("%w: admission budget profile is required", ErrAdmissionBudgetUnavailable)
	}
	if err := thresholds.Validate(); err != nil {
		return nil, err
	}
	if len(sources) != 4 {
		return nil, fmt.Errorf("%w: four admission metric sources are required", ErrAdmissionBudgetUnavailable)
	}
	return &AdmissionBudgetProvider{profile: profile, thresholds: thresholds, now: func() time.Time { return time.Now().UTC() }, sources: sources}, nil
}

func (p *AdmissionBudgetProvider) ReadAdmissionBudget(ctx context.Context) (AdmissionBudget, error) {
	if p == nil || len(p.sources) != 4 {
		return AdmissionBudget{}, ErrAdmissionBudgetUnavailable
	}
	now := p.now().UTC()
	measurements := make([]AdmissionMeasurement, 0, len(p.sources))
	seen := make(map[string]struct{}, len(p.sources))
	for _, source := range p.sources {
		if source == nil {
			return AdmissionBudget{}, ErrAdmissionBudgetUnavailable
		}
		measurement, err := source.Read(ctx)
		if err != nil {
			return AdmissionBudget{}, fmt.Errorf("%w: %v", ErrAdmissionBudgetUnavailable, err)
		}
		if _, exists := seen[measurement.Metric]; exists {
			return AdmissionBudget{}, fmt.Errorf("%w: duplicate metric %q", ErrAdmissionBudgetExceeded, measurement.Metric)
		}
		seen[measurement.Metric] = struct{}{}
		if !isAdmissionBudgetMetric(measurement.Metric) {
			return AdmissionBudget{}, fmt.Errorf("%w: metric %s is not part of AdmissionBudgetV1", ErrAdmissionBudgetExceeded, measurement.Metric)
		}
		if err := validateAdmissionMeasurement(measurement, now); err != nil {
			return AdmissionBudget{}, fmt.Errorf("%w: %v", ErrAdmissionBudgetExceeded, err)
		}
		measurement.Threshold = p.thresholdFor(measurement.Metric)
		if measurement.Threshold <= 0 {
			return AdmissionBudget{}, fmt.Errorf("%w: threshold for %s is missing", ErrAdmissionBudgetUnavailable, measurement.Metric)
		}
		measurements = append(measurements, measurement)
	}
	for _, metric := range []string{"trace_opensearch_capacity", "trace_opensearch_heap", "trace_collector_queue", "trace_storage_connection_pool"} {
		if _, ok := seen[metric]; !ok {
			return AdmissionBudget{}, fmt.Errorf("%w: metric %s is missing", ErrAdmissionBudgetExceeded, metric)
		}
	}
	return AdmissionBudget{ContractVersion: "AdmissionBudgetV1", Profile: p.profile, SampledAt: now, FreshUntil: now.Add(30 * time.Second), Measurements: measurements}, nil
}

func isAdmissionBudgetMetric(metric string) bool {
	switch metric {
	case "trace_opensearch_capacity", "trace_opensearch_heap", "trace_collector_queue", "trace_storage_connection_pool":
		return true
	default:
		return false
	}
}

func (p *AdmissionBudgetProvider) thresholdFor(metric string) float64 {
	switch metric {
	case "trace_opensearch_capacity":
		return p.thresholds.OpenSearchCapacity
	case "trace_opensearch_heap":
		return p.thresholds.OpenSearchHeap
	case "trace_collector_queue":
		return p.thresholds.CollectorQueue
	case "trace_storage_connection_pool":
		return p.thresholds.StoragePool
	default:
		return 0
	}
}

func validateAdmissionMeasurement(measurement AdmissionMeasurement, now time.Time) error {
	if measurement.Metric == "" || measurement.Source == "" || measurement.SampleTime.IsZero() || !measurement.Fresh || measurement.SampleTime.After(now) || math.IsNaN(measurement.Value) || math.IsInf(measurement.Value, 0) || measurement.Value < 0 || measurement.Value > 1 {
		return fmt.Errorf("%w: metric %q is missing a fresh normalized sample", ErrAdmissionBudgetUnavailable, measurement.Metric)
	}
	return nil
}

func ValidateAdmissionBudgetForEnable(budget AdmissionBudget, now time.Time) error {
	if budget.ContractVersion != "AdmissionBudgetV1" || budget.Profile == "" || budget.SampledAt.IsZero() || budget.FreshUntil.IsZero() || !budget.FreshUntil.After(now) || len(budget.Measurements) != 4 {
		return ErrAdmissionBudgetExceeded
	}
	allowed := map[string]struct{}{
		"trace_opensearch_capacity":     {},
		"trace_opensearch_heap":         {},
		"trace_collector_queue":         {},
		"trace_storage_connection_pool": {},
	}
	seen := make(map[string]struct{}, len(budget.Measurements))
	for _, measurement := range budget.Measurements {
		if _, ok := allowed[measurement.Metric]; !ok {
			return fmt.Errorf("%w: unknown metric %q", ErrAdmissionBudgetExceeded, measurement.Metric)
		}
		if _, ok := seen[measurement.Metric]; ok {
			return fmt.Errorf("%w: duplicate metric %q", ErrAdmissionBudgetExceeded, measurement.Metric)
		}
		seen[measurement.Metric] = struct{}{}
		if err := validateAdmissionMeasurement(measurement, now); err != nil {
			return fmt.Errorf("%w: %v", ErrAdmissionBudgetExceeded, err)
		}
		if math.IsNaN(measurement.Threshold) || math.IsInf(measurement.Threshold, 0) || measurement.Threshold <= 0 || measurement.Threshold > 1 {
			return fmt.Errorf("%w: invalid threshold for %s", ErrAdmissionBudgetExceeded, measurement.Metric)
		}
		if measurement.Value > measurement.Threshold {
			return fmt.Errorf("%w: %s value %.4f exceeds threshold %.4f", ErrAdmissionBudgetExceeded, measurement.Metric, measurement.Value, measurement.Threshold)
		}
	}
	if len(seen) != len(allowed) {
		return ErrAdmissionBudgetExceeded
	}
	return nil
}
