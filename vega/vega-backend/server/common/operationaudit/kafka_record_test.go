// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildKafkaAuditRecord(t *testing.T) {
	entry := Entry{
		EventID: "0194f4b8-9a79-7c7f-881e-0f0a97107d22", EventTime: time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC),
		ActorID: "user-1", ActorName: "Vega Manager", ActorType: "user", AuthMethod: "oauth",
		RequestID: "req-vega-1", SourceChannel: "api", Method: "PUT", HTTPStatus: 204,
		Action: "update", TargetType: "catalog", TargetID: "catalog-1", TargetName: "Data Catalog",
		Outcome: "success", ChangedFields: []string{"name", "connector_config"},
	}

	value, err := BuildKafkaAuditRecord(entry, "test")
	require.NoError(t, err)
	var record map[string]any
	require.NoError(t, json.Unmarshal(value, &record))
	assert.Equal(t, "vega", record["source_id"])
	assert.Equal(t, "vega.operation.observed", record["event_name"])
	assert.Equal(t, "success", record["outcome"])
	assert.Equal(t, "catalog", record["target"].(map[string]any)["type"])
	assert.Equal(t, "data_resource_knowledge_network", record["scope"].(map[string]any)["business_module"])
	assert.Equal(t, []any{"name", "connector_config"}, record["facts"].(map[string]any)["changed_fields"])
	assert.NotContains(t, record["facts"].(map[string]any), "before_hash")
	assert.NotContains(t, record["facts"].(map[string]any), "after_hash")
	assert.NotContains(t, string(value), "password")
	entry.SourceChannel = "internal_api"
	value, err = BuildKafkaAuditRecord(entry, "test")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(value, &record))
	assert.Equal(t, "api", record["request_context"].(map[string]any)["source_channel"])

	entry.TargetType = "discover_schedule"
	value, err = BuildKafkaAuditRecord(entry, "test")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(value, &record))
	assert.Equal(t, "discovery_schedule", record["target"].(map[string]any)["type"])
	for _, targetType := range []string{"connector_type", "discover_task", "semantic_understanding_task"} {
		entry.TargetType = targetType
		_, err = BuildKafkaAuditRecord(entry, "test")
		assert.NoError(t, err, targetType)
	}

	entry.TargetID = strings.Repeat("x", 257)
	value, err = BuildKafkaAuditRecord(entry, "test")
	require.NoError(t, err)
	assert.NotContains(t, string(value), entry.TargetID)
	require.NoError(t, json.Unmarshal(value, &record))
	assert.Contains(t, record["target"].(map[string]any)["id"], "ref_")
	entry.TargetID = "catalog-1"
	entry.ActorName = "Bearer abcdefghijklmnop"
	entry.TargetName = "bak_123456789012_abcdefghijklmnopqrstuvwxyz1"
	value, err = BuildKafkaAuditRecord(entry, "test")
	require.NoError(t, err)
	assert.Contains(t, string(value), "Bearer abcdefghijklmnop")
	assert.Contains(t, string(value), "bak_123456789012_abcdefghijklmnopqrstuvwxyz1")
}
