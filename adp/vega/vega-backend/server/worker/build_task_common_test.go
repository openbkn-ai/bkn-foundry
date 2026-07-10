// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestUpdateResourceIndexName(t *testing.T) {
	t.Run("updates empty old index", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ra := vmock.NewMockResourceAccess(ctrl)
		resource := &interfaces.Resource{ID: "r1"}

		ra.EXPECT().Update(gomock.Any(), resource).DoAndReturn(func(_ context.Context, got *interfaces.Resource) error {
			assert.Equal(t, "new-index", got.LocalIndexName)
			return nil
		})

		require.NoError(t, updateResourceIndexName(context.Background(), resource, ra, "new-index"))
	})

	t.Run("skips unchanged index", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ra := vmock.NewMockResourceAccess(ctrl)
		resource := &interfaces.Resource{ID: "r1", LocalIndexName: "same-index"}

		require.NoError(t, updateResourceIndexName(context.Background(), resource, ra, "same-index"))
	})

	t.Run("keeps old index after update failure", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ra := vmock.NewMockResourceAccess(ctrl)
		resource := &interfaces.Resource{ID: "r1", LocalIndexName: "old-index"}

		ra.EXPECT().Update(gomock.Any(), resource).DoAndReturn(func(_ context.Context, got *interfaces.Resource) error {
			assert.Equal(t, "new-index", got.LocalIndexName)
			return errors.New("update failed")
		})

		err := updateResourceIndexName(context.Background(), resource, ra, "new-index")

		require.Error(t, err)
		assert.Equal(t, "old-index", resource.LocalIndexName)
	})
}
