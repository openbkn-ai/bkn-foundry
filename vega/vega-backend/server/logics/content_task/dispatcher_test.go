// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package content_task

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
)

// resetContentTaskDispatcher restores the package defaults after a test.
func resetContentTaskDispatcher(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		dispatcher = noopDispatcher{}
		frozen = false
	})
}

func TestSetContentTaskDispatcher(t *testing.T) {
	t.Run("installs a dispatcher before freeze", func(t *testing.T) {
		resetContentTaskDispatcher(t)
		installed := vmock.NewMockContentTaskDispatcher(gomock.NewController(t))

		require.NoError(t, SetContentTaskDispatcher(installed))

		assert.Same(t, installed, GetContentTaskDispatcher())
	})

	t.Run("rejects nil", func(t *testing.T) {
		resetContentTaskDispatcher(t)

		err := SetContentTaskDispatcher(nil)

		require.ErrorIs(t, err, ErrNilContentTaskDispatcher)
		assert.IsType(t, noopDispatcher{}, GetContentTaskDispatcher())
	})

	t.Run("rejects install after freeze and keeps the previous dispatcher", func(t *testing.T) {
		resetContentTaskDispatcher(t)
		ctrl := gomock.NewController(t)
		first := vmock.NewMockContentTaskDispatcher(ctrl)
		require.NoError(t, SetContentTaskDispatcher(first))
		FreezeContentTaskDispatcher()

		err := SetContentTaskDispatcher(vmock.NewMockContentTaskDispatcher(ctrl))

		require.ErrorIs(t, err, ErrContentTaskDispatcherFrozen)
		assert.Same(t, first, GetContentTaskDispatcher())
	})

	t.Run("concurrent reads during install are safe", func(t *testing.T) {
		resetContentTaskDispatcher(t)
		installed := vmock.NewMockContentTaskDispatcher(gomock.NewController(t))
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				assert.NotNil(t, GetContentTaskDispatcher())
			}()
		}

		require.NoError(t, SetContentTaskDispatcher(installed))
		wg.Wait()
		assert.Same(t, installed, GetContentTaskDispatcher())
	})
}

func TestGetContentTaskDispatcher(t *testing.T) {
	t.Run("defaults to the no-op dispatcher", func(t *testing.T) {
		resetContentTaskDispatcher(t)
		ctx := context.Background()
		d := GetContentTaskDispatcher()

		assert.False(t, d.Enabled(ctx))

		ref, err := d.Dispatch(ctx, &interfaces.ContentTaskRequest{CatalogID: "catalog-1", ContentVersion: "c-1", ResourceIDs: []string{"r1"}})
		var unavailable *interfaces.ContentTaskUnavailableError
		require.True(t, errors.As(err, &unavailable))
		assert.Nil(t, ref)

		status, err := d.GetStatus(ctx, "task-1")
		require.True(t, errors.As(err, &unavailable))
		assert.Nil(t, status)
	})
}
