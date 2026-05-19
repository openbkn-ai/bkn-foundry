// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource_data

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/kweaver-ai/kweaver-go-lib/rest"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
)

func TestPrepareOutputFieldsParams_FiltersUndefinedFields(t *testing.T) {
	rds := &resourceDataService{}
	resource := &interfaces.Resource{
		Category: interfaces.ResourceCategoryTable,
		SchemaDefinition: []*interfaces.Property{
			{Name: "name"},
			{Name: "age"},
		},
	}
	params := &interfaces.ResourceDataQueryParams{
		OutputFields: []string{"name", "missing", "age"},
	}

	rds.prepareOutputFieldsParams(resource, params)

	expected := []string{"name", "age"}
	if !reflect.DeepEqual(params.OutputFields, expected) {
		t.Fatalf("expected output fields %v, got %v", expected, params.OutputFields)
	}
}

func TestPrepareOutputFieldsParams_IndexKeepsScore(t *testing.T) {
	rds := &resourceDataService{}
	resource := &interfaces.Resource{
		Category: interfaces.ResourceCategoryIndex,
		SchemaDefinition: []*interfaces.Property{
			{Name: "name"},
		},
	}
	params := &interfaces.ResourceDataQueryParams{
		OutputFields: []string{"name", "_score", "missing"},
	}

	rds.prepareOutputFieldsParams(resource, params)

	expected := []string{"name", "_score"}
	if !reflect.DeepEqual(params.OutputFields, expected) {
		t.Fatalf("expected output fields %v, got %v", expected, params.OutputFields)
	}
}

func TestQueryRejectsDisabledCatalog(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	rds := &resourceDataService{cs: mockCS}
	resource := &interfaces.Resource{
		ID:        "resource-1",
		CatalogID: "catalog-1",
		Category:  interfaces.ResourceCategoryTable,
	}
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

	_, _, err := rds.Query(context.Background(), resource, &interfaces.ResourceDataQueryParams{})
	if err == nil {
		t.Fatal("expected error")
	}
	httpErr, ok := err.(*rest.HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T", err)
	}
	if httpErr.HTTPCode != http.StatusConflict {
		t.Fatalf("expected HTTP 409, got %d", httpErr.HTTPCode)
	}
	if httpErr.BaseError.ErrorCode != verrors.VegaBackend_Catalog_IsDisabled {
		t.Fatalf("expected %s, got %s", verrors.VegaBackend_Catalog_IsDisabled, httpErr.BaseError.ErrorCode)
	}
}
