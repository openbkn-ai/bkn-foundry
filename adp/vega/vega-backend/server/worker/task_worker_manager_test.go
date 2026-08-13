// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestTaskWorkerManagerStopsRecoveryBeforeStartingLoops(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	semanticService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
	discoverService := vmock.NewMockDiscoverTaskService(ctrl)

	semanticService.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(
		[]*interfaces.SemanticUnderstandingTaskSummary{}, int64(0), nil)
	discoverService.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(
		nil, int64(0), errors.New("database unavailable"))
	manager := &TaskWorkerManger{
		sutw: &SemanticUnderstandingTaskWorker{suts: semanticService, queueSize: 1},
		dtw:  &DiscoverTaskWorker{dts: discoverService, queueSize: 1},
		btw:  &BuildTaskWorker{},
	}

	err := manager.Start(context.Background())

	require.ErrorContains(t, err, "recover discover tasks")
	require.ErrorContains(t, err, "database unavailable")
}
