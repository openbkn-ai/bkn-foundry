// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package model_factory

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
)

func TestModelFactoryServiceGetVector(t *testing.T) {
	t.Run("reorders each batch by its local index", func(t *testing.T) {
		access := vmock.NewMockModelFactoryAccess(gomock.NewController(t))
		service := &modelFactoryService{mfa: access}
		model := &interfaces.SmallModel{ModelID: "model", BatchSize: 2}
		gomock.InOrder(
			access.EXPECT().GetVector(gomock.Any(), "model", []string{"a", "b"}).Return([]*interfaces.VectorResp{
				{Index: 1, Vector: []float32{2}}, {Index: 0, Vector: []float32{1}},
			}, nil),
			access.EXPECT().GetVector(gomock.Any(), "model", []string{"c", "d"}).Return([]*interfaces.VectorResp{
				{Index: 1, Vector: []float32{4}}, {Index: 0, Vector: []float32{3}},
			}, nil),
		)
		vectors, err := service.GetVector(context.Background(), model, []string{"a", "b", "c", "d"})
		require.NoError(t, err)
		require.Len(t, vectors, 4)
		for i, vector := range vectors {
			assert.Equal(t, []float32{float32(i + 1)}, vector.Vector)
		}
	})

	for _, tc := range []struct {
		name    string
		vectors []*interfaces.VectorResp
		err     error
	}{
		{name: "duplicate index", vectors: []*interfaces.VectorResp{{Index: 0}, {Index: 0}}},
		{name: "negative index", vectors: []*interfaces.VectorResp{{Index: -1}, {Index: 0}}},
		{name: "out of range index", vectors: []*interfaces.VectorResp{{Index: 0}, {Index: 2}}},
		{name: "missing vector", vectors: []*interfaces.VectorResp{{Index: 0}}},
		{name: "extra vector", vectors: []*interfaces.VectorResp{{Index: 0}, {Index: 1}, {Index: 2}}},
		{name: "nil vector", vectors: []*interfaces.VectorResp{nil, {Index: 1}}},
		{name: "downstream error", err: errors.New("model unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access := vmock.NewMockModelFactoryAccess(gomock.NewController(t))
			service := &modelFactoryService{mfa: access}
			model := &interfaces.SmallModel{ModelID: "model", BatchSize: 2}
			gomock.InOrder(
				access.EXPECT().GetVector(gomock.Any(), "model", []string{"a", "b"}).Return([]*interfaces.VectorResp{{Index: 0}, {Index: 1}}, nil),
				access.EXPECT().GetVector(gomock.Any(), "model", []string{"c", "d"}).Return(tc.vectors, tc.err),
			)
			vectors, err := service.GetVector(context.Background(), model, []string{"a", "b", "c", "d", "e"})
			require.Error(t, err)
			assert.Nil(t, vectors)
			if tc.err != nil {
				assert.ErrorIs(t, err, tc.err)
			}
		})
	}
}
