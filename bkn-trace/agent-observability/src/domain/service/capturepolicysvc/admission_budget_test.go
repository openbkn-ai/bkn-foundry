// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package capturepolicysvc

import (
	"context"
	"errors"
	"testing"
	"time"
)

type admissionMetricSourceFunc func(context.Context) (AdmissionMeasurement, error)

func (f admissionMetricSourceFunc) Read(ctx context.Context) (AdmissionMeasurement, error) {
	return f(ctx)
}

func admissionMetricSources(now time.Time) []AdmissionMeasurementSource {
	measurements := []AdmissionMeasurement{
		{Metric: "trace_opensearch_capacity", Source: "opensearch", SampleTime: now, Value: 0.4, Fresh: true},
		{Metric: "trace_opensearch_heap", Source: "opensearch", SampleTime: now, Value: 0.5, Fresh: true},
		{Metric: "trace_collector_queue", Source: "collector", SampleTime: now, Value: 0.2, Fresh: true},
		{Metric: "trace_storage_connection_pool", Source: "mariadb", SampleTime: now, Value: 0.3, Fresh: true},
	}
	sources := make([]AdmissionMeasurementSource, 0, len(measurements))
	for _, measurement := range measurements {
		measurement := measurement
		sources = append(sources, admissionMetricSourceFunc(func(context.Context) (AdmissionMeasurement, error) { return measurement, nil }))
	}
	return sources
}

func admissionBudgetThresholds() AdmissionBudgetThresholds {
	return AdmissionBudgetThresholds{OpenSearchCapacity: 0.8, OpenSearchHeap: 0.8, CollectorQueue: 0.8, StoragePool: 0.8}
}

func TestAdmissionBudgetProviderRequiresAllFourMetricSources(t *testing.T) {
	if _, err := NewAdmissionBudgetProvider("default", admissionBudgetThresholds(), admissionMetricSources(time.Now().UTC())[:3]...); !errors.Is(err, ErrAdmissionBudgetUnavailable) {
		t.Fatalf("missing source error = %v, want ErrAdmissionBudgetUnavailable", err)
	}
}

func TestAdmissionBudgetProviderBuildsFrozenFourMetricBudget(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	provider, err := NewAdmissionBudgetProvider("default", admissionBudgetThresholds(), admissionMetricSources(now)...)
	if err != nil {
		t.Fatal(err)
	}
	provider.now = func() time.Time { return now }
	budget, err := provider.ReadAdmissionBudget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if budget.ContractVersion != "AdmissionBudgetV1" || len(budget.Measurements) != 4 || !budget.FreshUntil.After(now) {
		t.Fatalf("unexpected frozen budget: %+v", budget)
	}
	if err := ValidateAdmissionBudgetForEnable(budget, now); err != nil {
		t.Fatalf("valid budget rejected: %v", err)
	}
}

func TestAdmissionBudgetProviderRejectsStaleOrMissingMetric(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	sources := admissionMetricSources(now)
	sources[2] = admissionMetricSourceFunc(func(context.Context) (AdmissionMeasurement, error) {
		return AdmissionMeasurement{Metric: "trace_collector_queue", Source: "collector", SampleTime: now, Value: 0.2, Fresh: false}, nil
	})
	provider, err := NewAdmissionBudgetProvider("default", admissionBudgetThresholds(), sources...)
	if err != nil {
		t.Fatal(err)
	}
	provider.now = func() time.Time { return now }
	if _, err := provider.ReadAdmissionBudget(context.Background()); !errors.Is(err, ErrAdmissionBudgetUnavailable) {
		t.Fatalf("stale metric error = %v, want unavailable", err)
	}
}

func TestValidateAdmissionBudgetRejectsThresholdBreach(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	provider, err := NewAdmissionBudgetProvider("default", admissionBudgetThresholds(), admissionMetricSources(now)...)
	if err != nil {
		t.Fatal(err)
	}
	provider.now = func() time.Time { return now }
	budget, err := provider.ReadAdmissionBudget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	budget.Measurements[0].Value = 0.9
	if err := ValidateAdmissionBudgetForEnable(budget, now); !errors.Is(err, ErrAdmissionBudgetExceeded) {
		t.Fatalf("threshold breach error = %v, want ErrAdmissionBudgetExceeded", err)
	}
}
