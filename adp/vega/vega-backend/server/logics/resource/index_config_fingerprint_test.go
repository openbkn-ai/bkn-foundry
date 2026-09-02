// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestIndexConfigFingerprint(t *testing.T) {
	base := fingerprintTestResource(false)
	contract, err := BuildIndexConfigContract(base)
	require.NoError(t, err)
	fingerprint, err := IndexConfigFingerprint(contract)
	require.NoError(t, err)
	assert.Len(t, fingerprint, 64)

	reordered := fingerprintTestResource(true)
	reordered.SourceMetadata = map[string]any{"changed": true}
	reordered.Description = "display-only change"
	reordered.SchemaDefinition[0].DisplayName = "display-only field change"
	reordered.SchemaDefinition[0].Features[0].Description = "display-only feature change"
	reorderedContract, err := BuildIndexConfigContract(reordered)
	require.NoError(t, err)
	reorderedFingerprint, err := IndexConfigFingerprint(reorderedContract)
	require.NoError(t, err)
	assert.Equal(t, fingerprint, reorderedFingerprint)

	changes := []struct {
		name   string
		mutate func(*interfaces.Resource)
	}{
		{"field name", func(resource *interfaces.Resource) { resource.SchemaDefinition[0].Name = "renamed" }},
		{"field original name", func(resource *interfaces.Resource) { resource.SchemaDefinition[0].OriginalName = "renamed_id" }},
		{"field original type", func(resource *interfaces.Resource) { resource.SchemaDefinition[0].OriginalType = "integer" }},
		{"field type", func(resource *interfaces.Resource) { resource.SchemaDefinition[0].Type = interfaces.DataType_String }},
		{"feature ref property", func(resource *interfaces.Resource) { resource.SchemaDefinition[1].Features[0].RefProperty = "summary" }},
		{"feature mapping config", func(resource *interfaces.Resource) {
			resource.SchemaDefinition[1].Features[1].Config["ignore_above"] = 512
		}},
		{"effective analyzer", func(resource *interfaces.Resource) { resource.IndexConfig.DefaultFulltextAnalyzer = "english" }},
		{"effective model ID", func(resource *interfaces.Resource) { resource.IndexConfig.DefaultEmbeddingModel = "model-b" }},
		{"incremental field order", func(resource *interfaces.Resource) {
			resource.IndexConfig.IncrementalFields = []string{"id", "created_at"}
		}},
		{"field added", func(resource *interfaces.Resource) {
			resource.SchemaDefinition = append(resource.SchemaDefinition, &interfaces.Property{Name: "extra", Type: interfaces.DataType_String})
		}},
	}

	for _, tt := range changes {
		t.Run(tt.name, func(t *testing.T) {
			changed := fingerprintTestResource(false)
			tt.mutate(changed)
			changedContract, err := BuildIndexConfigContract(changed)
			require.NoError(t, err)
			changedFingerprint, err := IndexConfigFingerprint(changedContract)
			require.NoError(t, err)
			assert.NotEqual(t, fingerprint, changedFingerprint)
		})
	}

	t.Run("self ref_property is normalized", func(t *testing.T) {
		selfReferenced := fingerprintTestResource(false)
		selfReferenced.SchemaDefinition[1].Features[0].RefProperty = selfReferenced.SchemaDefinition[1].Name
		selfContract, err := BuildIndexConfigContract(selfReferenced)
		require.NoError(t, err)
		selfFingerprint, err := IndexConfigFingerprint(selfContract)
		require.NoError(t, err)
		assert.Equal(t, fingerprint, selfFingerprint)
	})

	t.Run("unused default changes do not affect explicit feature overrides", func(t *testing.T) {
		explicit := fingerprintTestResource(false)
		explicit.SchemaDefinition[1].Features[0].Config["embedding_model"] = "feature-model"
		explicit.SchemaDefinition[1].Features[2].Config["analyzer"] = "feature-analyzer"
		first, err := ResourceIndexConfigFingerprint(explicit)
		require.NoError(t, err)

		explicit.IndexConfig.DefaultEmbeddingModel = "unused-model"
		explicit.IndexConfig.DefaultFulltextAnalyzer = "unused-analyzer"
		second, err := ResourceIndexConfigFingerprint(explicit)
		require.NoError(t, err)
		assert.Equal(t, first, second)
	})

	t.Run("invalid resource fails closed", func(t *testing.T) {
		_, err := BuildIndexConfigContract(nil)
		assert.Error(t, err)

		invalid := fingerprintTestResource(false)
		invalid.SchemaDefinition = append(invalid.SchemaDefinition, nil)
		_, err = BuildIndexConfigContract(invalid)
		assert.Error(t, err)

		duplicate := fingerprintTestResource(false)
		duplicate.SchemaDefinition = append(duplicate.SchemaDefinition, &interfaces.Property{Name: "id", Type: interfaces.DataType_Integer})
		_, err = BuildIndexConfigContract(duplicate)
		assert.Error(t, err)
	})
}

func TestBuildTaskIndexConfigFingerprintMatchesResourceSnapshot(t *testing.T) {
	resource := fingerprintTestResource(false)
	fields, err := SnapshotBuildTaskIndexConfigFields(resource)
	require.NoError(t, err)

	taskFingerprint, err := BuildTaskIndexConfigFingerprint(&interfaces.BuildTaskIndexConfig{
		IndexConfigContract: interfaces.IndexConfigContract{
			PrimaryKeyFields:  append([]string(nil), resource.IndexConfig.PrimaryKeyFields...),
			IncrementalFields: append([]string(nil), resource.IndexConfig.IncrementalFields...),
			Fields:            fields,
		},
	})
	require.NoError(t, err)
	resourceFingerprint, err := ResourceIndexConfigFingerprint(resource)
	require.NoError(t, err)
	assert.Equal(t, resourceFingerprint, taskFingerprint)

	resource.SchemaDefinition[1].OriginalType = "text"
	changedFingerprint, err := ResourceIndexConfigFingerprint(resource)
	require.NoError(t, err)
	assert.NotEqual(t, taskFingerprint, changedFingerprint)
}

func TestBuildTaskIndexConfigFingerprintSupportsUpgradedSnapshot(t *testing.T) {
	resource := fingerprintTestResource(false)
	encodedSchema, err := json.Marshal(resource.SchemaDefinition)
	require.NoError(t, err)
	var fields []interfaces.IndexConfigFieldContract
	require.NoError(t, json.Unmarshal(encodedSchema, &fields))

	taskFingerprint, err := BuildTaskIndexConfigFingerprint(&interfaces.BuildTaskIndexConfig{
		IndexConfigContract: interfaces.IndexConfigContract{
			PrimaryKeyFields:  append([]string(nil), resource.IndexConfig.PrimaryKeyFields...),
			IncrementalFields: append([]string(nil), resource.IndexConfig.IncrementalFields...),
			Fields:            fields,
		},
		Features: map[string]interfaces.BuildTaskFieldIndexFeature{
			"title": {
				Vector:   &interfaces.SmallModel{ModelID: "model-a"},
				Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "standard"},
			},
		},
	})
	require.NoError(t, err)
	resourceFingerprint, err := ResourceIndexConfigFingerprint(resource)
	require.NoError(t, err)
	assert.Equal(t, resourceFingerprint, taskFingerprint)
}

func fingerprintTestResource(reordered bool) *interfaces.Resource {
	id := &interfaces.Property{
		Name:         "id",
		OriginalName: "customer_id",
		OriginalType: "bigint",
		Type:         interfaces.DataType_Integer,
	}
	title := &interfaces.Property{
		Name:         "title",
		OriginalName: "customer_title",
		OriginalType: "varchar",
		Type:         interfaces.DataType_String,
		Features: []interfaces.PropertyFeature{
			{
				FeatureName: "title_vector",
				FeatureType: interfaces.PropertyFeatureType_Vector,
				RefProperty: "",
				Config:      map[string]any{},
			},
			{
				FeatureName: "title_keyword",
				FeatureType: interfaces.PropertyFeatureType_Keyword,
				Config: map[string]any{
					"ignore_above": 256,
					"normalizer":   map[string]any{"type": "custom", "filters": []any{"lowercase"}},
				},
			},
			{
				FeatureName: "title_fulltext",
				FeatureType: interfaces.PropertyFeatureType_Fulltext,
				Config:      map[string]any{},
			},
		},
	}
	createdAt := &interfaces.Property{
		Name:         "created_at",
		OriginalName: "created_at",
		OriginalType: "timestamp",
		Type:         interfaces.DataType_Timestamp,
	}

	if reordered {
		title.Features[0], title.Features[2] = title.Features[2], title.Features[0]
		title.Features[1].Config = map[string]any{
			"normalizer":   map[string]any{"filters": []any{"lowercase"}, "type": "custom"},
			"ignore_above": 256,
		}
		return &interfaces.Resource{
			SchemaDefinition: []*interfaces.Property{title, createdAt, id},
			IndexConfig: &interfaces.ResourceIndexConfig{
				PrimaryKeyFields:        []string{"id"},
				IncrementalFields:       []string{"created_at", "id"},
				DefaultFulltextAnalyzer: "standard",
				DefaultEmbeddingModel:   "model-a",
			},
		}
	}

	return &interfaces.Resource{
		SchemaDefinition: []*interfaces.Property{id, title, createdAt},
		IndexConfig: &interfaces.ResourceIndexConfig{
			PrimaryKeyFields:        []string{"id"},
			IncrementalFields:       []string{"created_at", "id"},
			DefaultFulltextAnalyzer: "standard",
			DefaultEmbeddingModel:   "model-a",
		},
	}
}
