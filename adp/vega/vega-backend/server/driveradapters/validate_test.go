// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

var testSortTypes = map[string]string{
	"name":        "f_name",
	"create_time": "f_create_time",
}

func TestParseTaskStatuses(t *testing.T) {
	ctx := context.Background()
	isValid := func(status string) bool {
		return status == "pending" || status == "running"
	}

	tests := []struct {
		name    string
		values  []string
		want    []string
		wantErr bool
	}{
		{name: "absent", want: []string{}},
		{name: "single value", values: []string{"pending"}, want: []string{"pending"}},
		{
			name:   "multiple values are trimmed and deduplicated",
			values: []string{" pending ", "running", "pending"},
			want:   []string{"pending", "running"},
		},
		{name: "unknown value", values: []string{"unknown"}, wantErr: true},
		{name: "explicit empty value", values: []string{""}, wantErr: true},
		{name: "comma-separated value", values: []string{"pending,running"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTaskStatuses(ctx, tt.values, isValid, verrors.VegaBackend_InvalidParameter_Format)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateName(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "valid name", value: "test-catalog"},
		{name: "empty name", value: "", wantErr: true},
		{name: "max length name", value: strings.Repeat("a", interfaces.NAME_MAX_LENGTH)},
		{name: "exceeds max length", value: strings.Repeat("a", interfaces.NAME_MAX_LENGTH+1), wantErr: true},
		{name: "UTF-8 max length name", value: strings.Repeat("中", interfaces.NAME_MAX_LENGTH)},
		{name: "UTF-8 exceeds max length", value: strings.Repeat("中", interfaces.NAME_MAX_LENGTH+1), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateName(ctx, tt.value)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateID(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "empty ID", value: ""},
		{name: "valid ID", value: "test-id_123"},
		{name: "max length ID", value: strings.Repeat("a", 40)},
		{name: "exceeds max length", value: strings.Repeat("a", 41), wantErr: true},
		{name: "invalid character", value: "test.id", wantErr: true},
		{name: "starts with underscore", value: "_test_id", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateID(ctx, tt.value)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateCatalogRequestID(t *testing.T) {
	ctx := context.Background()

	t.Run("invalid ID", func(t *testing.T) {
		err := ValidateCatalogRequest(ctx, &interfaces.CatalogRequest{
			ID:   strings.Repeat("a", 41),
			Name: "test-catalog",
		})
		require.Error(t, err)
	})

	t.Run("valid ID", func(t *testing.T) {
		err := ValidateCatalogRequest(ctx, &interfaces.CatalogRequest{
			ID:   "test-catalog_1",
			Name: "test-catalog",
		})
		require.NoError(t, err)
	})
}

func TestValidateResourceRequestID(t *testing.T) {
	ctx := context.Background()

	t.Run("invalid ID", func(t *testing.T) {
		err := ValidateResourceRequest(ctx, &interfaces.ResourceRequest{
			ID:   "test.resource",
			Name: "test-resource",
		})
		require.Error(t, err)
	})

	t.Run("valid ID", func(t *testing.T) {
		err := ValidateResourceRequest(ctx, &interfaces.ResourceRequest{
			ID:   "test-resource_1",
			Name: "test-resource",
		})
		require.NoError(t, err)
	})
}

func TestValidateCreateResourceCategory(t *testing.T) {
	ctx := context.Background()

	t.Run("allows creatable categories", func(t *testing.T) {
		require.NoError(t, validateCreateResourceCategory(ctx, interfaces.ResourceCategoryDataset))
		require.NoError(t, validateCreateResourceCategory(ctx, interfaces.ResourceCategoryLogicView))
	})

	t.Run("rejects discover-owned categories", func(t *testing.T) {
		rejected := []string{
			interfaces.ResourceCategoryTable,
			interfaces.ResourceCategoryFile,
			interfaces.ResourceCategoryFileset,
			interfaces.ResourceCategoryAPI,
			interfaces.ResourceCategoryMetric,
			interfaces.ResourceCategoryTopic,
			interfaces.ResourceCategoryIndex,
		}
		for _, category := range rejected {
			require.Error(t, validateCreateResourceCategory(ctx, category))
		}
	})

	t.Run("rejects empty category", func(t *testing.T) {
		require.Error(t, validateCreateResourceCategory(ctx, ""))
	})

	t.Run("rejects unknown category", func(t *testing.T) {
		require.Error(t, validateCreateResourceCategory(ctx, "foo"))
	})

	t.Run("category match is case sensitive", func(t *testing.T) {
		require.Error(t, validateCreateResourceCategory(ctx, "Dataset"))
	})
}

func TestValidateResourceRequestDatasetSchema(t *testing.T) {
	ctx := context.Background()
	baseReq := func(props []*interfaces.Property) *interfaces.ResourceRequest {
		return &interfaces.ResourceRequest{
			Name:             "ds",
			Category:         interfaces.ResourceCategoryDataset,
			SchemaDefinition: props,
		}
	}

	t.Run("ValidateResourceRequest rejects nil schema_definition", func(t *testing.T) {
		err := ValidateResourceRequest(ctx, baseReq(nil))
		require.Error(t, err)
	})

	t.Run("ValidateResourceRequest rejects empty schema_definition", func(t *testing.T) {
		err := ValidateResourceRequest(ctx, baseReq([]*interfaces.Property{}))
		require.Error(t, err)
	})

	t.Run("ValidateResourceRequest rejects duplicate field names", func(t *testing.T) {
		err := ValidateResourceRequest(ctx, baseReq([]*interfaces.Property{
			{Name: "dup", Type: interfaces.DataType_String},
			{Name: "dup", Type: interfaces.DataType_Integer},
		}))
		require.Error(t, err)
	})

	t.Run("ValidateResourceRequest rejects nil property", func(t *testing.T) {
		err := ValidateResourceRequest(ctx, baseReq([]*interfaces.Property{nil}))
		require.Error(t, err)
	})

	t.Run("ValidateResourceRequest accepts text keyword multi-field", func(t *testing.T) {
		err := ValidateResourceRequest(ctx, baseReq([]*interfaces.Property{
			{
				Name: "content",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{
					{FeatureName: "content.keyword", FeatureType: interfaces.PropertyFeatureType_Keyword},
				},
			},
		}))
		require.NoError(t, err)
	})

	t.Run("ValidateResourceRequest rejects dataset feature ref_property", func(t *testing.T) {
		err := ValidateResourceRequest(ctx, baseReq([]*interfaces.Property{
			{Name: "content_keyword", Type: interfaces.DataType_String},
			{
				Name: "content",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{
					{FeatureName: "content.keyword", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "content_keyword"},
				},
			},
		}))
		require.Error(t, err)
	})

	t.Run("ValidateResourceRequest rejects invalid table feature", func(t *testing.T) {
		err := ValidateResourceRequest(ctx, &interfaces.ResourceRequest{
			Name:     "table",
			Category: interfaces.ResourceCategoryTable,
			SchemaDefinition: []*interfaces.Property{
				{
					Name: "id",
					Type: interfaces.DataType_Integer,
					Features: []interfaces.PropertyFeature{
						{FeatureName: "id.keyword", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "id"},
					},
				},
			},
		})
		require.Error(t, err)
	})

	t.Run("validateSchemaProperties rejects invalid original resource fields", func(t *testing.T) {
		tests := []struct {
			name   string
			fields []*interfaces.Property
		}{
			{
				name: "empty field name",
				fields: []*interfaces.Property{
					{Name: "", Type: interfaces.DataType_String},
				},
			},
			{
				name: "field name length exceeded",
				fields: []*interfaces.Property{
					{Name: strings.Repeat("a", interfaces.MaxLength_PropertyName+1), Type: interfaces.DataType_String},
				},
			},
			{
				name: "display name length exceeded",
				fields: []*interfaces.Property{
					{Name: "f1", DisplayName: strings.Repeat("a", interfaces.MaxLength_PropertyDisplayName+1), Type: interfaces.DataType_String},
				},
			},
			{
				name: "description length exceeded",
				fields: []*interfaces.Property{
					{Name: "f1", Description: strings.Repeat("a", interfaces.MaxLength_PropertyDescription+1), Type: interfaces.DataType_String},
				},
			},
			{
				name: "duplicate field name",
				fields: []*interfaces.Property{
					{Name: "f1", Type: interfaces.DataType_String},
					{Name: "f1", Type: interfaces.DataType_Integer},
				},
			},
			{
				name: "duplicate display name",
				fields: []*interfaces.Property{
					{Name: "f1", DisplayName: "same", Type: interfaces.DataType_String},
					{Name: "f2", DisplayName: "same", Type: interfaces.DataType_String},
				},
			},
			{
				name: "invalid feature type",
				fields: []*interfaces.Property{
					{Name: "f1", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
						{FeatureName: "feat1", FeatureType: "bogus", RefProperty: "f1", IsNative: true},
					}},
				},
			},
			{
				name: "feature with unsupported property type",
				fields: []*interfaces.Property{
					{Name: "f1", Type: interfaces.DataType_String},
					{Name: "f2", Type: interfaces.DataType_Integer, Features: []interfaces.PropertyFeature{
						{FeatureName: "feat1", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "f1"},
					}},
				},
			},
			{
				name: "native feature with unsupported property type",
				fields: []*interfaces.Property{
					{Name: "f1", Type: interfaces.DataType_String},
					{Name: "f2", Type: interfaces.DataType_Integer, Features: []interfaces.PropertyFeature{
						{FeatureName: "feat1", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "f1", IsNative: true},
					}},
				},
			},
			{
				name: "feature with mismatched ref property type",
				fields: []*interfaces.Property{
					{Name: "f1", Type: interfaces.DataType_Vector},
					{Name: "f2", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
						{FeatureName: "feat1", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "f1"},
					}},
				},
			},
			{
				name: "duplicate default features without ref_property",
				fields: []*interfaces.Property{
					{Name: "body", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
						{FeatureName: "body.ft1", FeatureType: interfaces.PropertyFeatureType_Fulltext, IsDefault: true},
						{FeatureName: "body.ft2", FeatureType: interfaces.PropertyFeatureType_Fulltext, IsDefault: true},
					}},
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				require.Error(t, validateSchemaProperties(ctx, tt.fields, true))
			})
		}
	})

	t.Run("validateSchemaProperties rejects self-referencing ref_property", func(t *testing.T) {
		err := validateSchemaProperties(ctx, []*interfaces.Property{
			{Name: "body", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
				{FeatureName: "body.ft", FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "body", IsNative: true},
			}},
		}, true)

		require.Error(t, err)
		assert.ErrorContains(t, err, "cannot reference itself")
	})

	t.Run("validateSchemaProperties accepts valid original resource fields", func(t *testing.T) {
		tests := []struct {
			name   string
			fields []*interfaces.Property
		}{
			{
				name: "minimal valid dataset",
				fields: []*interfaces.Property{
					{Name: "id", Type: interfaces.DataType_Integer},
					{Name: "name", Type: interfaces.DataType_String},
				},
			},
			{
				name: "dataset with native fulltext feature without ref_property",
				fields: []*interfaces.Property{
					{Name: "body", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
						{FeatureName: "body.ft", FeatureType: interfaces.PropertyFeatureType_Fulltext, IsNative: true},
					}},
				},
			},
			{
				name: "original resource feature without ref_property",
				fields: []*interfaces.Property{
					{Name: "body", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
						{FeatureName: "body.ft", FeatureType: interfaces.PropertyFeatureType_Fulltext},
					}},
				},
			},
			{
				name: "feature can reuse a matching result from a differently typed property",
				fields: []*interfaces.Property{
					{Name: "title_keyword", Type: interfaces.DataType_String},
					{Name: "title", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
						{FeatureName: "title.keyword", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "title_keyword"},
					}},
				},
			},
			{
				name: "vector feature reuses vector property",
				fields: []*interfaces.Property{
					{Name: "embedding", Type: interfaces.DataType_Vector},
					{Name: "content", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
						{FeatureName: "content.vector", FeatureType: interfaces.PropertyFeatureType_Vector, RefProperty: "embedding"},
					}},
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				require.NoError(t, validateSchemaProperties(ctx, tt.fields, true))
			})
		}
	})
}

func TestValidateTags(t *testing.T) {
	ctx := context.Background()

	t.Run("valid tags", func(t *testing.T) {
		require.NoError(t, ValidateTags(ctx, []string{"tag1", "tag2"}))
	})

	t.Run("empty tags", func(t *testing.T) {
		require.NoError(t, ValidateTags(ctx, []string{}))
	})

	t.Run("exceeds max number", func(t *testing.T) {
		tags := make([]string, interfaces.TAGS_MAX_NUMBER+1)
		for i := range tags {
			tags[i] = "tag"
		}
		require.Error(t, ValidateTags(ctx, tags))
	})

	t.Run("invalid tag in list", func(t *testing.T) {
		require.Error(t, ValidateTags(ctx, []string{"good-tag", "bad/tag"}))
	})
}

func TestValidateTag(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		tag     string
		wantErr bool
	}{
		{name: "valid tag", tag: "my-tag"},
		{name: "empty tag", tag: "", wantErr: true},
		{name: "only spaces", tag: "   ", wantErr: true},
		{name: "exceeds max length", tag: strings.Repeat("a", interfaces.TAG_MAX_LENGTH+1), wantErr: true},
		{name: "trim spaces", tag: "  valid-tag  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTag(ctx, tt.tag)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}

	t.Run("special chars", func(t *testing.T) {
		invalidChars := []string{"/", ":", "?", "\\", "\"", "<", ">", "|", "#", "%", "&", "*", "$", "^", "!", "=", "."}
		for _, ch := range invalidChars {
			require.Error(t, validateTag(ctx, "tag"+ch+"name"))
		}
	})
}

func TestValidateDescription(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		description string
		wantErr     bool
	}{
		{name: "valid description", description: "A valid description"},
		{name: "empty description", description: ""},
		{name: "exceeds max length", description: strings.Repeat("a", interfaces.DESCRIPTION_MAX_LENGTH+1), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDescription(ctx, tt.description)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidatePaginationQueryParams(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		offset    string
		limit     string
		sort      string
		direction string
		assert    func(t *testing.T, got interfaces.PaginationQueryParams)
		wantErr   bool
	}{
		{
			name:      "valid pagination",
			offset:    "0",
			limit:     "10",
			sort:      "name",
			direction: "asc",
			assert: func(t *testing.T, got interfaces.PaginationQueryParams) {
				assert.Equal(t, 0, got.Offset)
				assert.Equal(t, 10, got.Limit)
				assert.Equal(t, "name", got.Sort)
				assert.Equal(t, "asc", got.Direction)
			},
		},
		{
			name:      "no limit",
			offset:    "0",
			limit:     "-1",
			sort:      "name",
			direction: "desc",
			assert: func(t *testing.T, got interfaces.PaginationQueryParams) {
				assert.Equal(t, -1, got.Limit)
			},
		},
		{name: "invalid offset", offset: "abc", limit: "10", sort: "name", direction: "asc", wantErr: true},
		{name: "negative offset", offset: "-1", limit: "10", sort: "name", direction: "asc", wantErr: true},
		{name: "invalid limit", offset: "0", limit: "abc", sort: "name", direction: "asc", wantErr: true},
		{name: "limit too small", offset: "0", limit: "0", sort: "name", direction: "asc", wantErr: true},
		{name: "limit too large", offset: "0", limit: "1001", sort: "name", direction: "asc", wantErr: true},
		{name: "invalid sort", offset: "0", limit: "10", sort: "unknown_sort", direction: "asc", wantErr: true},
		{name: "invalid direction", offset: "0", limit: "10", sort: "name", direction: "up", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validatePaginationQueryParams(ctx, tt.offset, tt.limit, tt.sort, tt.direction, testSortTypes)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.assert != nil {
				tt.assert(t, got)
			}
		})
	}
}
