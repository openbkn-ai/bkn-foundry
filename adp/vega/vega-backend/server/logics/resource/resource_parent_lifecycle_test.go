// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package resource

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	vmock "vega-backend/interfaces/mock"
)

func TestResourceParentTrackerCleanup(t *testing.T) {
	ctrl := gomock.NewController(t)
	permissionService := vmock.NewMockPermissionService(ctrl)
	tracker := &ResourceParentTracker{
		ps: permissionService,
		entries: map[string]trackedResourceParent{
			"resource\x00r1": {resourceType: "resource", resourceID: "r1"},
			"resource\x00r2": {resourceType: "resource", resourceID: "r2"},
			"other\x00r3":    {resourceType: "other", resourceID: "r3"},
		},
	}

	permissionService.EXPECT().DeleteResourceParents(gomock.Any(), "resource", gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ string, resourceIDs []string) error {
			assert.NoError(t, ctx.Err())
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			assert.Greater(t, time.Until(deadline), 25*time.Second)
			assert.ElementsMatch(t, []string{"r1", "r2"}, resourceIDs)
			return nil
		})
	permissionService.EXPECT().DeleteResourceParents(gomock.Any(), "other", []string{"r3"}).Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, tracker.Cleanup(ctx))
	assert.Empty(t, tracker.entries)
}
