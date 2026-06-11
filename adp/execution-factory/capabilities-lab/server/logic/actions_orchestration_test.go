package logic

import (
	"reflect"
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/model"
)

func TestOperatorInfoForCapability(t *testing.T) {
	got := operatorInfoForCapability(&model.Capability{
		Group: &model.Group{Category: "data_query"},
	})

	want := map[string]interface{}{
		"operator_type":  "basic",
		"execution_mode": "sync",
		"category":       "data_query",
		"source":         "custom",
		"is_data_source": false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("operatorInfoForCapability() = %#v, want %#v", got, want)
	}
}

func TestOperatorInfoForCapabilityDefaults(t *testing.T) {
	got := operatorInfoForCapability(&model.Capability{})

	want := map[string]interface{}{
		"operator_type":  "basic",
		"execution_mode": "sync",
		"category":       "other_category",
		"source":         "custom",
		"is_data_source": false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("operatorInfoForCapability() = %#v, want %#v", got, want)
	}
}

func TestOperatorExecuteControlToMap(t *testing.T) {
	got := operatorExecuteControlToMap(model.OperatorExecuteControl{
		Timeout: 30000,
		RetryPolicy: model.OperatorRetryPolicy{
			MaxAttempts:   5,
			InitialDelay:  1000,
			MaxDelay:      8000,
			BackoffFactor: 2,
			RetryConditions: model.OperatorRetryConditions{
				StatusCode: []int{500, 502, 503},
				ErrorCodes: []string{"TIMEOUT"},
			},
		},
	})

	want := map[string]interface{}{
		"timeout": int64(30000),
		"retry_policy": map[string]interface{}{
			"max_attempts":   int64(5),
			"initial_delay":  int64(1000),
			"max_delay":      int64(8000),
			"backoff_factor": int64(2),
			"retry_conditions": map[string]interface{}{
				"status_code": []int{500, 502, 503},
				"error_codes": []string{"TIMEOUT"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("operatorExecuteControlToMap() = %#v, want %#v", got, want)
	}
}

func TestOperatorExecuteControlToMapEmpty(t *testing.T) {
	if got := operatorExecuteControlToMap(model.OperatorExecuteControl{}); got != nil {
		t.Fatalf("operatorExecuteControlToMap() = %#v, want nil", got)
	}
}

func TestEnsureCurrentVersionEntry(t *testing.T) {
	capability := &model.Capability{
		Version:    "current-version",
		Status:     "published",
		UpdateTime: 12345,
	}

	got := ensureCurrentVersionEntry(capability, nil)
	want := []model.VersionEntry{{
		Version:    "current-version",
		Status:     "published",
		UpdateTime: 12345,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ensureCurrentVersionEntry() = %#v, want %#v", got, want)
	}

	existing := []model.VersionEntry{{Version: "current-version", Status: "published"}}
	got = ensureCurrentVersionEntry(capability, existing)
	if !reflect.DeepEqual(got, existing) {
		t.Fatalf("ensureCurrentVersionEntry() duplicated current version: %#v", got)
	}
}
