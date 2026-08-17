// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

const workerPoolTestTimeout = time.Second

func TestDiscoverTaskWorkerLimitsConcurrency(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	taskService := vmock.NewMockDiscoverTaskService(ctrl)
	entered, release, completed := workerPoolTestChannels()
	queue, inFlight := workerPoolTestQueue("task")
	dispatchSignal := make(chan struct{})
	worker := &DiscoverTaskWorker{
		dts:         taskService,
		workerCount: 2,
		queue:       queue,
		inFlight:    inFlight,
	}
	taskService.EXPECT().InternalGetByID(gomock.Any(), gomock.Any()).Times(3).
		DoAndReturn(func(_ context.Context, taskID string) (*interfaces.DiscoverTask, error) {
			entered <- taskID
			<-release
			return &interfaces.DiscoverTask{ID: taskID, Status: interfaces.DiscoverTaskStatusFailed}, nil
		})
	taskService.EXPECT().InternalList(gomock.Any(), gomock.Any()).AnyTimes().Return(
		[]*interfaces.DiscoverTaskSummary{}, nil,
	)
	taskService.EXPECT().RequestDispatch().Times(3).Do(func() { completed <- struct{}{} })
	taskService.EXPECT().DispatchSignal().AnyTimes().Return((<-chan struct{})(dispatchSignal))

	assertWorkerPoolLimit(t, queue, worker.workerCount, worker.startLoops, entered, release, completed)
}

func TestSemanticUnderstandingTaskWorkerLimitsConcurrency(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
	entered, release, completed := workerPoolTestChannels()
	queue, inFlight := workerPoolTestQueue("semantic-task")
	dispatchSignal := make(chan struct{})
	worker := &SemanticUnderstandingTaskWorker{
		suts:        taskService,
		workerCount: 2,
		queue:       queue,
		inFlight:    inFlight,
	}
	taskService.EXPECT().InternalGetByID(gomock.Any(), gomock.Any()).Times(3).
		DoAndReturn(func(_ context.Context, taskID string) (*interfaces.SemanticUnderstandingTask, error) {
			entered <- taskID
			<-release
			return &interfaces.SemanticUnderstandingTask{
				ID: taskID, Status: interfaces.SemanticUnderstandingTaskStatusFailed,
			}, nil
		})
	taskService.EXPECT().InternalList(gomock.Any(), gomock.Any()).AnyTimes().Return(
		[]*interfaces.SemanticUnderstandingTaskSummary{}, nil,
	)
	taskService.EXPECT().RequestDispatch().Times(3).Do(func() { completed <- struct{}{} })
	taskService.EXPECT().DispatchSignal().AnyTimes().Return((<-chan struct{})(dispatchSignal))

	assertWorkerPoolLimit(t, queue, worker.workerCount, worker.startLoops, entered, release, completed)
}

func TestBuildTaskWorkerLimitsBatchConcurrency(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	taskService := vmock.NewMockBuildTaskService(ctrl)
	entered, release, completed := workerPoolTestChannels()
	queue, inFlight := workerPoolTestQueue("batch-task")
	dispatchSignal := make(chan struct{})
	worker := &BuildTaskWorker{
		bts:                  taskService,
		batchWorkerCount:     2,
		streamingWorkerCount: 0,
		batchQueue:           queue,
		streamingQueue:       make(chan string, 1),
		inFlight:             inFlight,
	}
	taskService.EXPECT().InternalGetByID(gomock.Any(), gomock.Any()).Times(3).
		DoAndReturn(func(_ context.Context, taskID string) (*interfaces.BuildTask, error) {
			entered <- taskID
			<-release
			return &interfaces.BuildTask{ID: taskID, Status: interfaces.BuildTaskStatusFailed}, nil
		})
	expectIdleBuildPoll(taskService, dispatchSignal)
	taskService.EXPECT().RequestDispatch().Times(3).Do(func() { completed <- struct{}{} })

	assertWorkerPoolLimit(t, queue, worker.batchWorkerCount, worker.startLoops, entered, release, completed)
}

func TestBuildTaskWorkerLimitsStreamingConcurrency(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	taskService := vmock.NewMockBuildTaskService(ctrl)
	entered, release, completed := workerPoolTestChannels()
	queue, inFlight := workerPoolTestQueue("streaming-task")
	dispatchSignal := make(chan struct{})
	worker := &BuildTaskWorker{
		bts:                  taskService,
		batchWorkerCount:     0,
		streamingWorkerCount: 2,
		batchQueue:           make(chan string, 1),
		streamingQueue:       queue,
		inFlight:             inFlight,
	}
	taskService.EXPECT().InternalGetByID(gomock.Any(), gomock.Any()).Times(3).
		DoAndReturn(func(_ context.Context, taskID string) (*interfaces.BuildTask, error) {
			entered <- taskID
			<-release
			return &interfaces.BuildTask{ID: taskID, Status: interfaces.BuildTaskStatusFailed}, nil
		})
	expectIdleBuildPoll(taskService, dispatchSignal)
	taskService.EXPECT().RequestDispatch().Times(3).Do(func() { completed <- struct{}{} })

	assertWorkerPoolLimit(t, queue, worker.streamingWorkerCount, worker.startLoops, entered, release, completed)
}

func workerPoolTestChannels() (chan string, chan struct{}, chan struct{}) {
	return make(chan string, 3), make(chan struct{}, 3), make(chan struct{}, 3)
}

func workerPoolTestQueue(prefix string) (chan string, map[string]struct{}) {
	queue := make(chan string, 3)
	inFlight := make(map[string]struct{}, 3)
	for _, suffix := range []string{"-1", "-2", "-3"} {
		taskID := prefix + suffix
		queue <- taskID
		inFlight[taskID] = struct{}{}
	}
	return queue, inFlight
}

func expectIdleBuildPoll(taskService *vmock.MockBuildTaskService, dispatchSignal chan struct{}) {
	taskService.EXPECT().InternalList(gomock.Any(), gomock.Any()).AnyTimes().Return(
		[]*interfaces.BuildTaskSummary{}, nil,
	)
	taskService.EXPECT().DispatchSignal().AnyTimes().Return((<-chan struct{})(dispatchSignal))
}

func assertWorkerPoolLimit(t *testing.T, queue chan string, workerCount int,
	startLoops func(context.Context), entered <-chan string, release chan struct{}, completed <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startLoops(ctx)
	for i := 0; i < workerCount; i++ {
		waitForWorkerPoolSignal(t, entered, "task did not start")
	}
	assert.Len(t, queue, 1)
	select {
	case taskID := <-entered:
		t.Fatalf("task %s exceeded the configured worker concurrency", taskID)
	default:
	}

	// Once one worker is released, the queued task can start immediately.
	release <- struct{}{}
	waitForWorkerPoolSignal(t, entered, "queued task did not start after a worker was released")
	for i := 0; i < workerCount; i++ {
		release <- struct{}{}
	}
	for i := 0; i < 3; i++ {
		waitForWorkerPoolSignal(t, completed, "task did not complete")
	}
}

func waitForWorkerPoolSignal[T any](t *testing.T, signal <-chan T, failure string) T {
	t.Helper()
	select {
	case value := <-signal:
		return value
	case <-time.After(workerPoolTestTimeout):
		t.Fatal(failure)
		var zero T
		return zero
	}
}
