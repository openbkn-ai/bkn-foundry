// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

// 复现 bug：任务创建即入队，但入队消息会因 pod 更替/入队失败而丢失；DB 状态停在
// init（界面"排队中"），没有任何机制重新入队，任务永久假排队，只能人工 stop→start。
// findStuckBuildTasks 是对账判定核心：init 超过时限且队列中无对应消息的任务判为卡死。

func TestFindStuckBuildTasks(t *testing.T) {
	t.Run("returns init tasks that are stale and absent from queue", func(t *testing.T) {
		now := time.Date(2026, 6, 11, 22, 0, 0, 0, time.UTC)
		staleAfter := 3 * time.Minute
		ms := func(t time.Time) int64 { return t.UnixMilli() }
		mkTask := func(id, status string, updatedAgo time.Duration) *interfaces.BuildTask {
			return &interfaces.BuildTask{ID: id, Status: status, UpdateTime: ms(now.Add(-updatedAgo))}
		}
		tasks := []*interfaces.BuildTask{
			mkTask("lost", interfaces.BuildTaskStatusInit, 10*time.Minute),
			mkTask("queued", interfaces.BuildTaskStatusInit, 10*time.Minute),
			mkTask("fresh", interfaces.BuildTaskStatusInit, 10*time.Second),
			mkTask("running", interfaces.BuildTaskStatusRunning, 10*time.Minute),
		}
		queued := map[string]struct{}{"queued": {}}

		stuck := findStuckBuildTasks(tasks, queued, now, staleAfter)

		require.Len(t, stuck, 1)
		assert.Equal(t, "lost", stuck[0].ID)
	})

	t.Run("returns empty for nil input", func(t *testing.T) {
		got := findStuckBuildTasks(nil, nil, time.Now(), time.Minute)

		assert.Empty(t, got)
	})
}
