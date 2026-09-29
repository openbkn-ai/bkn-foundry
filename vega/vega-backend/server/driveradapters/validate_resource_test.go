// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
)

type validatingViewService struct{ err error }

func (service *validatingViewService) ValidateRequest(context.Context, *interfaces.ResourceRequest) error {
	return service.err
}

func (*validatingViewService) Prepare(context.Context, *interfaces.ResourceRequest) (string, []*interfaces.Property, error) {
	return "", nil, nil
}

func (*validatingViewService) QueryWithPaging(context.Context, *interfaces.Resource, *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	return nil, errors.New("query is not expected")
}

func TestValidateResourceRequestLogicViewService(t *testing.T) {
	previous := resourcelogic.GetLogicViewService()
	t.Cleanup(func() { resourcelogic.SetLogicViewService(previous) })
	req := &interfaces.ResourceRequest{Name: "view", Category: interfaces.ResourceCategoryLogicView}
	want := errors.New("invalid view")
	resourcelogic.SetLogicViewService(&validatingViewService{err: want})
	require.ErrorIs(t, ValidateResourceRequest(context.Background(), req), want)

	resourcelogic.SetLogicViewService(nil)
	var httpErr *rest.HTTPError
	require.ErrorAs(t, ValidateResourceRequest(context.Background(), req), &httpErr)
	require.Equal(t, http.StatusNotImplemented, httpErr.HTTPCode)
}

func TestValidateResourceRequestIgnoresExpectedUpdateTime(t *testing.T) {
	err := ValidateResourceRequest(context.Background(), &interfaces.ResourceRequest{
		Name:               "resource",
		Category:           interfaces.ResourceCategoryTable,
		ExpectedUpdateTime: -1,
	})
	require.NoError(t, err)
}

func TestValidateResourceRequestDatasetDisplayName(t *testing.T) {
	newRequest := func(props ...*interfaces.Property) *interfaces.ResourceRequest {
		return &interfaces.ResourceRequest{
			Name: "dataset", Category: interfaces.ResourceCategoryDataset, SchemaDefinition: props,
		}
	}

	t.Run("trims display name before saving", func(t *testing.T) {
		req := newRequest(&interfaces.Property{Name: "field", Type: interfaces.DataType_String, DisplayName: "  Title \t"})
		require.NoError(t, ValidateResourceRequest(context.Background(), req))
		assert.Equal(t, "Title", req.SchemaDefinition[0].DisplayName)
	})

	t.Run("blank display name falls back to field name", func(t *testing.T) {
		req := newRequest(&interfaces.Property{Name: "field", Type: interfaces.DataType_String, DisplayName: " \t "})
		require.NoError(t, ValidateResourceRequest(context.Background(), req))
		assert.Equal(t, "field", req.SchemaDefinition[0].DisplayName)
	})

	t.Run("checks length after trimming", func(t *testing.T) {
		req := newRequest(&interfaces.Property{
			Name: "field", Type: interfaces.DataType_String,
			DisplayName: " " + strings.Repeat("a", interfaces.MaxLength_PropertyDisplayName) + " ",
		})
		require.NoError(t, ValidateResourceRequest(context.Background(), req))
		assert.Equal(t, strings.Repeat("a", interfaces.MaxLength_PropertyDisplayName), req.SchemaDefinition[0].DisplayName)
	})

	t.Run("rejects duplicates after trimming", func(t *testing.T) {
		req := newRequest(
			&interfaces.Property{Name: "first", Type: interfaces.DataType_String, DisplayName: "Title"},
			&interfaces.Property{Name: "second", Type: interfaces.DataType_String, DisplayName: " Title "},
		)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, ValidateResourceRequest(context.Background(), req), &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Dataset_Duplicated_FieldDisplayName, httpErr.BaseError.ErrorCode)
		assert.Equal(t, "Title", req.SchemaDefinition[1].DisplayName)
	})

	t.Run("rejects blank fallback duplicate", func(t *testing.T) {
		req := newRequest(
			&interfaces.Property{Name: "first", Type: interfaces.DataType_String, DisplayName: "second"},
			&interfaces.Property{Name: "second", Type: interfaces.DataType_String, DisplayName: "  "},
		)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, ValidateResourceRequest(context.Background(), req), &httpErr)
		assert.Equal(t, verrors.VegaBackend_Dataset_Duplicated_FieldDisplayName, httpErr.BaseError.ErrorCode)
	})
}

func TestValidateResourceRequestRawDisplayNameUnchanged(t *testing.T) {
	req := &interfaces.ResourceRequest{
		Name: "table", Category: interfaces.ResourceCategoryTable,
		SchemaDefinition: []*interfaces.Property{{Name: "field", Type: interfaces.DataType_String, DisplayName: " Title "}},
	}
	require.NoError(t, ValidateResourceRequest(context.Background(), req))
	assert.Equal(t, " Title ", req.SchemaDefinition[0].DisplayName)
}

func TestValidateResourceRequestRejectsDuplicateFeatureTypes(t *testing.T) {
	for _, category := range []string{interfaces.ResourceCategoryTable, interfaces.ResourceCategoryDataset} {
		t.Run(category, func(t *testing.T) {
			for _, tc := range []struct {
				name         string
				firstDefault bool
				lastDefault  bool
			}{
				{name: "neither default"},
				{name: "first default", firstDefault: true},
				{name: "both default", firstDefault: true, lastDefault: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					req := &interfaces.ResourceRequest{
						Name:     "resource",
						Category: category,
						SchemaDefinition: []*interfaces.Property{{
							Name: "title",
							Type: interfaces.DataType_Text,
							Features: []interfaces.PropertyFeature{
								{FeatureName: "standard", FeatureType: interfaces.PropertyFeatureType_Fulltext, IsDefault: tc.firstDefault},
								{FeatureName: "english", FeatureType: interfaces.PropertyFeatureType_Fulltext, IsDefault: tc.lastDefault},
							},
						}},
					}

					var httpErr *rest.HTTPError
					require.ErrorAs(t, ValidateResourceRequest(rest.WithLanguage(context.Background(), rest.AmericanEnglish), req), &httpErr)
					require.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
					require.Equal(t, verrors.VegaBackend_Resource_Duplicated_FieldFeatureType, httpErr.BaseError.ErrorCode)
					require.Equal(t, map[string]any{"FieldName": "title", "FieldFeatureType": interfaces.PropertyFeatureType_Fulltext}, httpErr.BaseError.DescriptionTemplateData)
					require.Equal(t, "Field title already has a fulltext feature", httpErr.BaseError.Description)
					require.Contains(t, httpErr.BaseError.ErrorDetails, `property "title" has more than one "fulltext" feature`)
				})
			}
		})
	}
}
