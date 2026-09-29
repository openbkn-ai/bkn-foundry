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
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
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
