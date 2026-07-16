// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestEmbeddingWorkerHandleTask(t *testing.T) {
	t.Run("injects creator into downstream context", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bts := vmock.NewMockBuildTaskService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		ew := &embeddingWorker{bts: bts, rs: rs}
		creator := interfaces.AccountInfo{ID: "u1", Type: "user"}

		bts.EXPECT().InternalGetByID(gomock.Any(), "t1").Return(&interfaces.BuildTask{
			ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusRunning, Creator: creator,
		}, nil)

		var gotAccount interfaces.AccountInfo
		var hasAccount bool
		rs.EXPECT().InternalGetByID(gomock.Any(), "r1").DoAndReturn(
			func(ctx context.Context, id string) (*interfaces.Resource, error) {
				gotAccount, hasAccount = workerAccountFromCtx(ctx)
				return nil, nil
			})
		bts.EXPECT().InternalUpdateStatus(gomock.Any(), nil, "t1",
			interfaces.NewBuildTaskUpdate().
				WithStatus(interfaces.BuildTaskStatusFailed).
				WithErrorMsg("resource not found")).
			Return(true, nil)

		task := asynq.NewTask("build:embedding", workerBuildTaskPayload(t, interfaces.EmbeddingBuildTaskMessage{TaskID: "t1"}))
		require.NoError(t, ew.HandleTask(context.Background(), task))
		require.True(t, hasAccount)
		assert.Equal(t, creator, gotAccount)
	})
}

func TestEmbeddingWorkerExecuteEmbedding(t *testing.T) {
	t.Run("ctx canceled returns error for requeue", func(t *testing.T) {
		ew, ts, ka := newEmbeddingLoopWorker(t)
		resource, task := embeddingLoopFixtures()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		expectEmbeddingKafkaSession(ka)
		ts.EXPECT().InternalGetStatus(gomock.Any(), "t1").Return(interfaces.BuildTaskStatusRunning, nil)
		expectEmbeddingCountFlush(ts, 7)

		err := ew.executeEmbedding(ctx, resource, task)

		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("persistent commit error gives up for retry", func(t *testing.T) {
		ew, ts, ka := newEmbeddingLoopWorker(t)
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		ew.lim = lim
		resource, task := embeddingLoopFixtures()
		deadCommit := errors.New("commit on dead generation")
		docMsg := func(id string) kafka.Message {
			return kafka.Message{Value: []byte(`{"document_id":"` + id + `"}`)}
		}

		expectEmbeddingKafkaSession(ka)
		ts.EXPECT().InternalGetStatus(gomock.Any(), "t1").Return(interfaces.BuildTaskStatusRunning, nil).AnyTimes()
		gomock.InOrder(
			ka.EXPECT().ReadMessage(gomock.Any(), gomock.Any()).Return(docMsg("d1"), nil),
			ka.EXPECT().ReadMessage(gomock.Any(), gomock.Any()).Return(docMsg("d2"), nil),
			ka.EXPECT().ReadMessage(gomock.Any(), gomock.Any()).Return(docMsg("d3"), nil),
		)
		lim.EXPECT().GetDocument(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(map[string]any{"team_name": ""}, nil).Times(3)
		ka.EXPECT().CommitMessages(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(deadCommit).Times(embeddingKafkaMaxConsecutiveErrors)
		ts.EXPECT().InternalUpdateStatus(gomock.Any(), nil, "t1", gomock.Any()).Return(true, nil).AnyTimes()

		err := ew.executeEmbedding(context.Background(), resource, task)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "commit")
	})

	t.Run("persistent read error gives up for retry", func(t *testing.T) {
		ew, ts, ka := newEmbeddingLoopWorker(t)
		resource, task := embeddingLoopFixtures()
		deadConn := errors.New("committing message: use of closed network connection")

		expectEmbeddingKafkaSession(ka)
		ts.EXPECT().InternalGetStatus(gomock.Any(), "t1").Return(interfaces.BuildTaskStatusRunning, nil).Times(embeddingKafkaMaxConsecutiveErrors)
		ka.EXPECT().ReadMessage(gomock.Any(), gomock.Any()).Return(kafka.Message{}, deadConn).Times(embeddingKafkaMaxConsecutiveErrors)
		expectEmbeddingCountFlush(ts, 7)

		err := ew.executeEmbedding(context.Background(), resource, task)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "use of closed network connection")
	})

	t.Run("end sentinel switches local index and completes task", func(t *testing.T) {
		ew, ts, ka := newEmbeddingLoopWorker(t)
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		ew.rs = rs
		resource, task := embeddingLoopFixtures()
		resource.LocalIndexName = interfaces.BuildIndexName("r1", "old-task")
		task.SyncedCount = 7
		wantIndexName := interfaces.BuildIndexName("r1", "t1")

		expectEmbeddingKafkaSession(ka)
		ts.EXPECT().InternalGetStatus(gomock.Any(), "t1").Return(interfaces.BuildTaskStatusRunning, nil)
		ka.EXPECT().ReadMessage(gomock.Any(), gomock.Any()).
			Return(kafka.Message{Value: []byte(`{"document_id":"` + interfaces.EmptyDocumentID + `"}`)}, nil)
		ka.EXPECT().CommitMessages(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		ka.EXPECT().ReadMessage(gomock.Any(), gomock.Any()).
			Return(kafka.Message{}, context.DeadlineExceeded).
			Times(embeddingDrainEmptyPolls)
		rs.EXPECT().InternalUpdate(gomock.Any(), nil, resource).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, got *interfaces.Resource) error {
				assert.Equal(t, wantIndexName, got.LocalIndexName)
				return nil
			})
		ts.EXPECT().InternalGetByID(gomock.Any(), "t1").
			Return(&interfaces.BuildTask{ID: "t1", SyncedCount: 7}, nil)
		ts.EXPECT().InternalUpdateStatus(gomock.Any(), nil, "t1", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, _ string, update interfaces.BuildTaskUpdate, _ ...string) (bool, error) {
				require.NotNil(t, update.Status)
				require.NotNil(t, update.VectorizedCount)
				require.NotNil(t, update.FailureDetail)
				assert.Equal(t, interfaces.BuildTaskStatusCompleted, *update.Status)
				assert.Equal(t, int64(7), *update.VectorizedCount)
				assert.Equal(t, "", *update.FailureDetail)
				return true, nil
			})

		require.NoError(t, ew.executeEmbedding(context.Background(), resource, task))
		assert.Equal(t, wantIndexName, resource.LocalIndexName)
	})
}

func TestEmbeddingWorkerVectorizeDoc(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ew, lim, mfs := newVectorizeWorker(t)
		ctx := t.Context()

		lim.EXPECT().GetDocument(ctx, "idx", "doc1").
			Return(map[string]any{"team_name": "Iran", "other": 1}, nil)
		mfs.EXPECT().GetVector(ctx, "m1", []string{"Iran"}).
			Return([]*interfaces.VectorResp{{Vector: []float32{0.1, 0.2}}}, nil)
		lim.EXPECT().UpsertDocuments(ctx, "idx", gomock.Any()).
			DoAndReturn(func(_ any, _ string, reqs []map[string]any) ([]string, error) {
				require.Len(t, reqs, 1)
				assert.Equal(t, "doc1", reqs[0]["id"])
				doc := reqs[0]["document"].(map[string]any)
				assert.Contains(t, doc, "team_name_vector")
				return []string{"doc1"}, nil
			})

		require.NoError(t, ew.vectorizeDoc(ctx, "idx", "doc1", testEmbeddingConfig("m1", "team_name")))
	})

	t.Run("empty text is success without embedding", func(t *testing.T) {
		ew, lim, _ := newVectorizeWorker(t)
		ctx := t.Context()

		lim.EXPECT().GetDocument(ctx, "idx", "doc1").
			Return(map[string]any{"team_name": ""}, nil)

		require.NoError(t, ew.vectorizeDoc(ctx, "idx", "doc1", testEmbeddingConfig("m1", "team_name")))
	})

	t.Run("groups fields by model", func(t *testing.T) {
		ew, lim, mfs := newVectorizeWorker(t)
		ctx := t.Context()

		lim.EXPECT().GetDocument(ctx, "idx", "doc1").
			Return(map[string]any{"title": "hello", "body": "world"}, nil)
		mfs.EXPECT().GetVector(ctx, "m1", []string{"hello"}).
			Return([]*interfaces.VectorResp{{Vector: []float32{0.1}}}, nil)
		mfs.EXPECT().GetVector(ctx, "m2", []string{"world"}).
			Return([]*interfaces.VectorResp{{Vector: []float32{0.2}}}, nil)
		lim.EXPECT().UpsertDocuments(ctx, "idx", gomock.Any()).
			DoAndReturn(func(_ any, _ string, reqs []map[string]any) ([]string, error) {
				doc := reqs[0]["document"].(map[string]any)
				assert.Equal(t, []float32{0.1}, doc["title_vector"])
				assert.Equal(t, []float32{0.2}, doc["body_vector"])
				return []string{"doc1"}, nil
			})

		require.NoError(t, ew.vectorizeDoc(ctx, "idx", "doc1", map[string]interfaces.BuildTaskEmbeddingConfig{
			"title": {ModelID: "m1", Dimensions: 1024},
			"body":  {ModelID: "m2", Dimensions: 2048},
		}))
	})

	t.Run("failures return error", func(t *testing.T) {
		boom := errors.New("boom")
		cases := []struct {
			name  string
			setup func(lim *vmock.MockLocalIndexManager, mfs *vmock.MockModelFactoryService, ctx any)
		}{
			{"get document fails", func(lim *vmock.MockLocalIndexManager, mfs *vmock.MockModelFactoryService, ctx any) {
				lim.EXPECT().GetDocument(ctx, "idx", "doc1").Return(nil, boom)
			}},
			{"get vector fails", func(lim *vmock.MockLocalIndexManager, mfs *vmock.MockModelFactoryService, ctx any) {
				lim.EXPECT().GetDocument(ctx, "idx", "doc1").Return(map[string]any{"f": "text"}, nil)
				mfs.EXPECT().GetVector(ctx, "m1", []string{"text"}).Return(nil, boom)
			}},
			{"vector count mismatch", func(lim *vmock.MockLocalIndexManager, mfs *vmock.MockModelFactoryService, ctx any) {
				lim.EXPECT().GetDocument(ctx, "idx", "doc1").Return(map[string]any{"f": "text"}, nil)
				mfs.EXPECT().GetVector(ctx, "m1", []string{"text"}).Return([]*interfaces.VectorResp{}, nil)
			}},
			{"upsert fails", func(lim *vmock.MockLocalIndexManager, mfs *vmock.MockModelFactoryService, ctx any) {
				lim.EXPECT().GetDocument(ctx, "idx", "doc1").Return(map[string]any{"f": "text"}, nil)
				mfs.EXPECT().GetVector(ctx, "m1", []string{"text"}).
					Return([]*interfaces.VectorResp{{Vector: []float32{0.1}}}, nil)
				lim.EXPECT().UpsertDocuments(ctx, "idx", gomock.Any()).Return(nil, boom)
			}},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ew, lim, mfs := newVectorizeWorker(t)
				ctx := t.Context()
				tc.setup(lim, mfs, ctx)

				require.Error(t, ew.vectorizeDoc(ctx, "idx", "doc1", testEmbeddingConfig("m1", "f")))
			})
		}
	})
}

func testEmbeddingConfig(model string, fields ...string) map[string]interfaces.BuildTaskEmbeddingConfig {
	config := map[string]interfaces.BuildTaskEmbeddingConfig{}
	for _, field := range fields {
		config[field] = interfaces.BuildTaskEmbeddingConfig{ModelID: model, Dimensions: 1024}
	}
	return config
}

func TestFormatVectorizeFailures(t *testing.T) {
	t.Run("truncates long failed document list", func(t *testing.T) {
		failed := make([]string, 25)
		for i := range failed {
			failed[i] = fmt.Sprintf("doc%02d", i)
		}

		msg := formatVectorizeFailures(failed, nil)

		assert.Contains(t, msg, "failed for 25 documents")
		assert.Contains(t, msg, "and 5 more")
		assert.NotContains(t, msg, "doc20")

		short := formatVectorizeFailures([]string{"a", "b"}, nil)
		assert.NotContains(t, short, "more")
		assert.Contains(t, short, "a,b")
	})

	t.Run("includes cause", func(t *testing.T) {
		cause := errors.New("get vector request failed with status code: 400, ModelFactory.ExternalSmallModel.Used.NameNotExist")

		msg := formatVectorizeFailures([]string{"1-", "2-"}, cause)

		assert.Contains(t, msg, "cause: ")
		assert.Contains(t, msg, "NameNotExist")
		assert.Contains(t, msg, "1-,2-")

		long := errors.New(strings.Repeat("x", 600))
		capped := formatVectorizeFailures([]string{"1-"}, long)
		assert.Contains(t, capped, "...")
		assert.LessOrEqual(t, len(capped), 500)
	})
}

func newEmbeddingLoopWorker(t *testing.T) (*embeddingWorker, *vmock.MockBuildTaskService, *vmock.MockKafkaAccess) {
	t.Helper()

	ctrl := gomock.NewController(t)
	bts := vmock.NewMockBuildTaskService(ctrl)
	ka := vmock.NewMockKafkaAccess(ctrl)
	return &embeddingWorker{
		bts:         bts,
		kafkaAccess: ka,
		sleep:       func(time.Duration) {},
	}, bts, ka
}

func embeddingLoopFixtures() (*interfaces.Resource, *interfaces.BuildTask) {
	resource := &interfaces.Resource{ID: "r1"}
	task := &interfaces.BuildTask{
		ID:         "t1",
		ResourceID: "r1",
		IndexConfig: &interfaces.BuildTaskIndexConfig{
			Features: map[string]interfaces.BuildTaskFieldIndexFeature{
				"team_name": {Vector: &interfaces.BuildTaskEmbeddingConfig{ModelID: "m1", Dimensions: 1024}},
			},
		},
		VectorizedCount: 7,
	}
	return resource, task
}

func expectEmbeddingKafkaSession(ka *vmock.MockKafkaAccess) {
	ka.EXPECT().CreateTopic(gomock.Any(), gomock.Any()).Return(nil)
	ka.EXPECT().NewReader(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
	ka.EXPECT().CloseReader(gomock.Any())
}

func expectEmbeddingCountFlush(ts *vmock.MockBuildTaskService, count int64) *gomock.Call {
	return ts.EXPECT().InternalUpdateStatus(gomock.Any(), nil, "t1", gomock.AssignableToTypeOf(interfaces.BuildTaskUpdate{})).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ string, update interfaces.BuildTaskUpdate, _ ...string) (bool, error) {
			if update.VectorizedCount == nil || *update.VectorizedCount != count {
				return false, errors.New("vectorizedCount not flushed")
			}
			return true, nil
		})
}

func newVectorizeWorker(t *testing.T) (*embeddingWorker, *vmock.MockLocalIndexManager, *vmock.MockModelFactoryService) {
	t.Helper()

	ctrl := gomock.NewController(t)
	lim := vmock.NewMockLocalIndexManager(ctrl)
	mfs := vmock.NewMockModelFactoryService(ctrl)
	return &embeddingWorker{lim: lim, mfs: mfs}, lim, mfs
}
