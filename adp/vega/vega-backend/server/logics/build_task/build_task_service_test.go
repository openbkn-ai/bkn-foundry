// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package build_task

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

func TestCreateBuildTaskRejectsDisabledCatalog(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRA := mock_interfaces.NewMockResourceAccess(ctrl)
	service := &buildTaskService{cs: mockCS, ra: mockRA}

	mockRA.EXPECT().GetByID(gomock.Any(), "resource-1").
		Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
		}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

	_, err := service.CreateBuildTask(context.Background(), &interfaces.CreateBuildTaskRequest{ResourceID: "resource-1"})
	assertCatalogDisabledError(t, err)
}

func TestStartBuildTaskRejectsDisabledCatalog(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	service := &buildTaskService{cs: mockCS, bta: mockBTA}

	mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
		Return(&interfaces.BuildTask{
			ID:        "task-1",
			CatalogID: "catalog-1",
			Status:    interfaces.BuildTaskStatusInit,
		}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

	err := service.StartBuildTask(context.Background(), "task-1", interfaces.BuildTaskExecuteTypeIncremental)
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
