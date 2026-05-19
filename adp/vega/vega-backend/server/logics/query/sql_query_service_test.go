// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"context"
	"net/http"
	"testing"

	"github.com/kweaver-ai/kweaver-go-lib/rest"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
)

func TestExecuteRejectsDisabledCatalogForOpenSearchQuery(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	service := NewRawQueryServiceWithDeps(mockCS, mockRS)

	mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
		Return(&interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1"}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false, ConnectorType: interfaces.ConnectorTypeOpenSearch}, nil)

	_, err := service.Execute(context.Background(), &interfaces.RawQueryRequest{
		ResourceType: interfaces.ConnectorTypeOpenSearch,
		Query:        map[string]any{"resource_id": "resource-1"},
	})
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

func TestExecuteRejectsDisabledCatalogForExistingStreamSession(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	service := NewRawQueryServiceWithDeps(mockCS, mockRS)

	session, err := GetStreamQueryManager().CreateSession(
		interfaces.ConnectorTypeMariaDB,
		"catalog",
		"catalog-1",
		&interfaces.Catalog{ID: "catalog-1", Enabled: true, ConnectorType: interfaces.ConnectorTypeMariaDB},
		100,
		"select * from {{resource-1}}",
		[]string{"resource-1"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer GetStreamQueryManager().RemoveSession(session.QueryID)

	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false, ConnectorType: interfaces.ConnectorTypeMariaDB}, nil)

	_, err = service.Execute(context.Background(), &interfaces.RawQueryRequest{
		QueryType: interfaces.QueryType_Stream,
		QueryID:   session.QueryID,
	})
	assertCatalogDisabledError(t, err)
}

func assertCatalogDisabledError(t *testing.T, err error) {
	t.Helper()
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
