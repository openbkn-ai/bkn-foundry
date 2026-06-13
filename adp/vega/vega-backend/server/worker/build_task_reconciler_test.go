// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"testing"
	"time"

	"vega-backend/interfaces"
)

// 复现 bug：任务创建即入队，但入队消息会因 pod 更替/入队失败而丢失；DB 状态停在
// init（界面"排队中"），没有任何机制重新入队，任务永久假排队，只能人工 stop→start。
// findStuckBuildTasks 是对账判定核心：init 超过时限且队列中无对应消息的任务判为卡死。

func TestFindStuckBuildTasks(t *testing.T) {
	now := time.Date(2026, 6, 11, 22, 0, 0, 0, time.UTC)
	staleAfter := 3 * time.Minute
	ms := func(t time.Time) int64 { return t.UnixMilli() }

	mkTask := func(id, status string, updatedAgo time.Duration) *interfaces.BuildTask {
		return &interfaces.BuildTask{ID: id, Status: status, UpdateTime: ms(now.Add(-updatedAgo))}
	}

	tasks := []*interfaces.BuildTask{
		mkTask("lost", interfaces.BuildTaskStatusInit, 10*time.Minute),     // 卡死：超时且不在队列
		mkTask("queued", interfaces.BuildTaskStatusInit, 10*time.Minute),   // 正常排队：队列中有消息
		mkTask("fresh", interfaces.BuildTaskStatusInit, 10*time.Second),    // 刚创建：创建→入队存在间隙，不能误判
		mkTask("running", interfaces.BuildTaskStatusRunning, 10*time.Minute), // 防御：非 init 不碰
	}
	queued := map[string]struct{}{"queued": {}}

	stuck := findStuckBuildTasks(tasks, queued, now, staleAfter)

	if len(stuck) != 1 || stuck[0].ID != "lost" {
		ids := []string{}
		for _, s := range stuck {
			ids = append(ids, s.ID)
		}
		t.Fatalf("expected exactly [lost], got %v", ids)
	}
}

func TestFindStuckBuildTasks_EmptyInputs(t *testing.T) {
	now := time.Now()
	if got := findStuckBuildTasks(nil, nil, now, time.Minute); len(got) != 0 {
		t.Fatalf("expected empty result for nil input, got %d", len(got))
	}
}
