// Copyright openbkn.ai

package model_factory

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	mock_interfaces "bkn-backend/interfaces/mock"
)

func TestDefaultModelCacheSharesOneLookup(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := mock_interfaces.NewMockModelFactoryService(ctrl)
	model := &interfaces.SmallModel{ModelID: "embedding-model"}
	service.EXPECT().GetDefaultModel(gomock.Any()).Return(model, nil).Times(1)

	ctx := WithDefaultModelCache(context.Background())
	for range 3 {
		got, err := GetDefaultModel(ctx, service)
		if err != nil || got != model {
			t.Fatalf("GetDefaultModel() = (%v, %v), want (%v, nil)", got, err, model)
		}
	}
}

func TestDefaultModelCacheIsOptIn(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := mock_interfaces.NewMockModelFactoryService(ctrl)
	service.EXPECT().GetDefaultModel(gomock.Any()).Return(nil, nil).Times(2)

	if _, err := GetDefaultModel(context.Background(), service); err != nil {
		t.Fatal(err)
	}
	if _, err := GetDefaultModel(context.Background(), service); err != nil {
		t.Fatal(err)
	}
}
