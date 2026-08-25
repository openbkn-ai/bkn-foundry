// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package vega_backend

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	interfacesmock "bkn-backend/interfaces/mock"
)

func TestVegaBackendService_GetResourceByID(t *testing.T) {
	ctrl := gomock.NewController(t)
	vba := interfacesmock.NewMockVegaBackendAccess(ctrl)
	vbs := &vegaBackendService{vba: vba}
	resource := &interfaces.VegaResource{ID: "resource-1"}

	vba.EXPECT().GetResourceByID(gomock.Any(), resource.ID).Return(resource, nil)

	got, err := vbs.GetResourceByID(context.Background(), resource.ID)
	if err != nil {
		t.Fatalf("GetResourceByID() error = %v", err)
	}
	if got != resource {
		t.Fatalf("GetResourceByID() = %p, want %p", got, resource)
	}
}
