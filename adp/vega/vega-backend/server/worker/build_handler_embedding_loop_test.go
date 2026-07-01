// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

// 复现两个把任务永久冻成"构建中"的 bug：
//
// 1. 进程收到 SIGTERM（pod 更替）时 ctx 取消，executeEmbedding 返回 nil——asynq 把
//    任务标记为成功，重启后不再投递，任务状态停在 running，向量化进度永久冻结，
//    只能人工 stop→start 救活。取消必须返回 ctx.Err()，让 asynq 重启后重试续跑。
//
// 2. 消费组协调连接死亡（broker 重启/rebalance）后，reader 上的读和位点提交永远
//    返回 "use of closed network connection"，原实现 sleep+continue 在死 reader 上
//    无限重试，任务同样永久冻结。连续非超时读错误必须放弃本轮、返回错误，由 asynq
//    重试重建 reader 与消费组会话，从已提交位点续读。
//
// 两条路径都必须先回写 vectorizedCount，否则丢最后一个批量窗口的进度。

func newLoopHandler(t *testing.T) (*embeddingHandler, *vmock.MockBuildTaskAccess, *vmock.MockKafkaAccess) {
	t.Helper()
	ctrl := gomock.NewController(t)
	ta := vmock.NewMockBuildTaskAccess(ctrl)
	ka := vmock.NewMockKafkaAccess(ctrl)
	return &embeddingHandler{
		taskAccess:  ta,
		kafkaAccess: ka,
		sleep:       func(time.Duration) {},
	}, ta, ka
}

func loopFixtures() (*interfaces.Resource, *interfaces.BuildTask) {
	resource := &interfaces.Resource{ID: "r1"}
	task := &interfaces.BuildTask{
		ID:              "t1",
		ResourceID:      "r1",
		EmbeddingFields: "team_name",
		EmbeddingModel:  "m1",
		VectorizedCount: 7,
	}
	return resource, task
}

func expectKafkaSession(ka *vmock.MockKafkaAccess) {
	ka.EXPECT().CreateTopic(gomock.Any(), gomock.Any()).Return(nil)
	ka.EXPECT().NewReader(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
	ka.EXPECT().CloseReader(gomock.Any())
}

func expectCountFlush(ta *vmock.MockBuildTaskAccess, count int64) *gomock.Call {
	return ta.EXPECT().UpdateStatus(gomock.Any(), "t1", gomock.AssignableToTypeOf(map[string]interface{}{})).
		DoAndReturn(func(_ context.Context, _ string, updates map[string]interface{}) error {
			if got, ok := updates["vectorizedCount"].(int64); !ok || got != count {
				return errors.New("vectorizedCount not flushed")
			}
			return nil
		})
}

func TestExecuteEmbedding_CtxCanceledReturnsErrorForRequeue(t *testing.T) {
	eh, ta, ka := newLoopHandler(t)
	resource, task := loopFixtures()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 模拟 SIGTERM：asynq 在关停时取消任务 ctx

	expectKafkaSession(ka)
	ta.EXPECT().GetStatus(gomock.Any(), "t1").Return(interfaces.BuildTaskStatusRunning, nil)
	flush := expectCountFlush(ta, 7)

	err := eh.executeEmbedding(ctx, resource, task)
	if err == nil {
		t.Fatal("ctx 取消必须返回错误让 asynq 重投递；返回 nil 任务被标成功，重启后永久冻结")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	_ = flush
}

// 复现 bug：消费组会话死亡后 kafka-go CommitMessages 在无界 ctx 上永久阻塞或持续
// 失败，消费循环既不推进也不响应 stop（线上复现：任务冻在 10381/10401 六小时，
// stop 后 60 秒状态仍是 stopping）。提交必须有界，连续提交失败必须放弃本轮交给
// asynq 重建消费组会话。
func TestExecuteEmbedding_PersistentCommitErrorGivesUpForRetry(t *testing.T) {
	eh, ta, ka := newLoopHandler(t)
	ctrl := gomock.NewController(t)
	ds := vmock.NewMockDatasetService(ctrl)
	eh.ds = ds
	resource, task := loopFixtures()

	deadCommit := errors.New("commit on dead generation")
	docMsg := func(id string) kafka.Message {
		return kafka.Message{Value: []byte(`{"document_id":"` + id + `"}`)}
	}

	expectKafkaSession(ka)
	ta.EXPECT().GetStatus(gomock.Any(), "t1").Return(interfaces.BuildTaskStatusRunning, nil).AnyTimes()
	gomock.InOrder(
		ka.EXPECT().ReadMessage(gomock.Any(), gomock.Any()).Return(docMsg("d1"), nil),
		ka.EXPECT().ReadMessage(gomock.Any(), gomock.Any()).Return(docMsg("d2"), nil),
		ka.EXPECT().ReadMessage(gomock.Any(), gomock.Any()).Return(docMsg("d3"), nil),
	)
	// 空文本文档：vectorizeDoc 直接成功，不触发嵌入调用
	ds.EXPECT().GetDocument(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(map[string]any{"team_name": ""}, nil).Times(3)
	ka.EXPECT().CommitMessages(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(deadCommit).Times(embeddingKafkaMaxConsecutiveErrors)
	ta.EXPECT().UpdateStatus(gomock.Any(), "t1", gomock.Any()).Return(nil).AnyTimes()

	err := eh.executeEmbedding(context.Background(), resource, task)
	if err == nil {
		t.Fatal("连续提交失败必须返回错误交给 asynq 重建会话；否则循环静默冻结且不响应 stop")
	}
	if !strings.Contains(err.Error(), "commit") {
		t.Fatalf("expected wrapped commit error, got %v", err)
	}
}

func TestExecuteEmbedding_PersistentReadErrorGivesUpForRetry(t *testing.T) {
	eh, ta, ka := newLoopHandler(t)
	resource, task := loopFixtures()

	deadConn := errors.New("committing message: use of closed network connection")

	expectKafkaSession(ka)
	ta.EXPECT().GetStatus(gomock.Any(), "t1").Return(interfaces.BuildTaskStatusRunning, nil).Times(embeddingKafkaMaxConsecutiveErrors)
	ka.EXPECT().ReadMessage(gomock.Any(), gomock.Any()).Return(kafka.Message{}, deadConn).Times(embeddingKafkaMaxConsecutiveErrors)
	expectCountFlush(ta, 7)

	err := eh.executeEmbedding(context.Background(), resource, task)
	if err == nil {
		t.Fatal("持久性读错误必须返回错误交给 asynq 重建 reader；sleep+continue 会在死连接上永久冻结")
	}
	if !strings.Contains(err.Error(), "use of closed network connection") {
		t.Fatalf("expected wrapped read error, got %v", err)
	}
}
