// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"math"
	"time"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"github.com/kweaver-ai/kweaver-go-lib/logger"

	"vega-backend/interfaces"
	"vega-backend/logics"
)

const (
	// buildTaskReconcileInterval 对账周期
	buildTaskReconcileInterval = 5 * time.Minute
	// buildTaskInitStaleAfter init 停留超过该时长且队列无对应消息即判为消息丢失；
	// 需大于创建事务提交→入队完成的间隙，避免把正常创建流程误判为卡死
	buildTaskInitStaleAfter = 3 * time.Minute
	// reconcileListPageSize Inspector 翻页大小
	reconcileListPageSize = 100
)

// buildTaskReconciler 周期对账自愈：任务创建即入队，但入队消息会因 pod 更替或
// 入队失败而丢失，DB 状态停在 init（界面"排队中"）后没有任何机制重新入队。
// 对账把"init 超时且 asynq 队列中无对应消息"的任务重新入队，消除永久假排队。
type buildTaskReconciler struct {
	taskAccess interfaces.BuildTaskAccess
	aqa        interfaces.AsynqAccess
}

func newBuildTaskReconciler() *buildTaskReconciler {
	return &buildTaskReconciler{
		taskAccess: logics.BTA,
		aqa:        logics.AQA,
	}
}

// run 周期执行对账；启动即跑首轮，覆盖"重启前丢消息"的存量卡死任务
func (r *buildTaskReconciler) run() {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("Build task reconciler exited with panic: %v", err)
		}
	}()
	for {
		if err := r.reconcileOnce(context.Background()); err != nil {
			logger.Errorf("Build task reconcile failed: %v", err)
		}
		time.Sleep(buildTaskReconcileInterval)
	}
}

// reconcileOnce 执行一轮对账
func (r *buildTaskReconciler) reconcileOnce(ctx context.Context) error {
	tasks, _, err := r.taskAccess.List(ctx, interfaces.BuildTasksQueryParams{Statuses: []string{interfaces.BuildTaskStatusInit}})
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		return nil
	}

	queued, err := r.queuedBuildTaskIDs()
	if err != nil {
		return err
	}

	stuck := findStuckBuildTasks(tasks, queued, time.Now(), buildTaskInitStaleAfter)
	if len(stuck) == 0 {
		return nil
	}

	client := r.aqa.CreateClient()
	defer func() { _ = client.Close() }()
	for _, task := range stuck {
		if err := enqueueBuildTaskMessage(client, task); err != nil {
			logger.Errorf("Reconciler re-enqueue build task %s failed: %v", task.ID, err)
			continue
		}
		logger.Infof("Reconciler re-enqueued stuck build task %s (init since %s)",
			task.ID, time.UnixMilli(task.UpdateTime).Format(time.RFC3339))
	}
	return nil
}

// findStuckBuildTasks 返回卡死任务：init 停留超过 staleAfter 且队列中无对应消息。
// 纯判定函数，便于单测。
func findStuckBuildTasks(tasks []*interfaces.BuildTask, queuedIDs map[string]struct{}, now time.Time, staleAfter time.Duration) []*interfaces.BuildTask {
	stuck := []*interfaces.BuildTask{}
	for _, task := range tasks {
		if task.Status != interfaces.BuildTaskStatusInit {
			continue
		}
		if now.Sub(time.UnixMilli(task.UpdateTime)) < staleAfter {
			continue
		}
		if _, ok := queuedIDs[task.ID]; ok {
			continue
		}
		stuck = append(stuck, task)
	}
	return stuck
}

// queuedBuildTaskIDs 收集 asynq 队列中所有构建消息对应的任务 ID。
// 扫描顺序约束：消息只会沿 scheduled/retry → pending → active 方向流动，
// 按上游到下游的顺序扫描保证迁移中的消息至少出现在一个列表里，不会被误判为丢失。
func (r *buildTaskReconciler) queuedBuildTaskIDs() (map[string]struct{}, error) {
	inspector := r.aqa.CreateInspector()
	defer func() { _ = inspector.Close() }()

	ids := map[string]struct{}{}
	listFuncs := []func(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error){
		inspector.ListScheduledTasks,
		inspector.ListRetryTasks,
		inspector.ListPendingTasks,
		inspector.ListActiveTasks,
	}
	for _, list := range listFuncs {
		for page := 1; ; page++ {
			infos, err := list(interfaces.DefaultQueue, asynq.PageSize(reconcileListPageSize), asynq.Page(page))
			if err != nil {
				return nil, err
			}
			for _, info := range infos {
				if info.Type != interfaces.BuildTaskTypeBatch && info.Type != interfaces.BuildTaskTypeStreaming {
					continue
				}
				var msg interfaces.BatchBuildTaskMessage
				if err := sonic.Unmarshal(info.Payload, &msg); err == nil && msg.TaskID != "" {
					ids[msg.TaskID] = struct{}{}
				}
			}
			if len(infos) < reconcileListPageSize {
				break
			}
		}
	}
	return ids, nil
}

// enqueueBuildTaskMessage 重新投递构建消息，与 build_task_service.enqueueBuildTask 对齐。
// 执行类型用增量：从未跑过的任务游标为空，增量等效全量；跑过一半的任务沿游标续跑。
func enqueueBuildTaskMessage(client *asynq.Client, task *interfaces.BuildTask) error {
	payload, err := sonic.Marshal(&interfaces.BatchBuildTaskMessage{
		TaskID:      task.ID,
		ExecuteType: interfaces.BuildTaskExecuteTypeIncremental,
	})
	if err != nil {
		return err
	}
	typename := interfaces.BuildTaskTypeBatch
	if task.Mode == interfaces.BuildTaskModeStreaming {
		typename = interfaces.BuildTaskTypeStreaming
	}
	_, err = client.Enqueue(asynq.NewTask(typename, payload),
		asynq.Queue(interfaces.DefaultQueue),
		asynq.MaxRetry(interfaces.BUILD_TASK_MAX_RETRY_COUNT),
		asynq.Timeout(math.MaxInt64),
		asynq.Deadline(time.Unix(math.MaxInt64/1000000000, math.MaxInt64%1000000000)),
	)
	return err
}
