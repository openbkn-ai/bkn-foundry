// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

// 复现 bug：嵌入循环里 GetVector/GetDocument/Upsert 瞬时失败的文档被 sleep+continue
// 跳过后，后续消息提交 Kafka 位点会把它们悄悄盖掉，向量永久缺失（线上 wc_teams 缺 Iran、
// wc_tournaments 缺 1954/1958 两届）。vectorizeDoc 把单文档处理收敛成可重试单元，
// 失败必须如实返回错误，由调用方记入失败清单。

func newVectorizeHandler(t *testing.T) (*embeddingHandler, *vmock.MockDatasetService, *vmock.MockModelFactoryAccess) {
	t.Helper()
	ctrl := gomock.NewController(t)
	ds := vmock.NewMockDatasetService(ctrl)
	mfa := vmock.NewMockModelFactoryAccess(ctrl)
	return &embeddingHandler{ds: ds, mfa: mfa}, ds, mfa
}

func TestVectorizeDoc_Success(t *testing.T) {
	eh, ds, mfa := newVectorizeHandler(t)
	ctx := t.Context()

	ds.EXPECT().GetDocument(ctx, "idx", "doc1").
		Return(map[string]any{"team_name": "Iran", "other": 1}, nil)
	mfa.EXPECT().GetVector(ctx, "m1", []string{"Iran"}).
		Return([]*interfaces.VectorResp{{Vector: []float32{0.1, 0.2}}}, nil)
	ds.EXPECT().UpsertDocuments(ctx, "idx", gomock.Any()).
		DoAndReturn(func(_ any, _ string, reqs []map[string]any) ([]string, error) {
			if len(reqs) != 1 || reqs[0]["id"] != "doc1" {
				t.Fatalf("unexpected upsert request: %v", reqs)
			}
			doc := reqs[0]["document"].(map[string]any)
			if _, ok := doc["team_name_vector"]; !ok {
				t.Fatalf("vector field missing in upsert: %v", doc)
			}
			return []string{"doc1"}, nil
		})

	if err := eh.vectorizeDoc(ctx, "idx", "doc1", "m1", []string{"team_name"}); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestVectorizeDoc_EmptyTextIsSuccessWithoutEmbedding(t *testing.T) {
	eh, ds, _ := newVectorizeHandler(t)
	ctx := t.Context()

	// 源字段为空/缺失：不调嵌入模型、不写回，但视为成功（分母里有它）
	ds.EXPECT().GetDocument(ctx, "idx", "doc1").
		Return(map[string]any{"team_name": ""}, nil)

	if err := eh.vectorizeDoc(ctx, "idx", "doc1", "m1", []string{"team_name"}); err != nil {
		t.Fatalf("expected success for empty-text doc, got %v", err)
	}
}

func TestVectorizeDoc_FailuresReturnError(t *testing.T) {
	boom := errors.New("boom")

	cases := []struct {
		name  string
		setup func(ds *vmock.MockDatasetService, mfa *vmock.MockModelFactoryAccess, ctx any)
	}{
		{"get document fails", func(ds *vmock.MockDatasetService, mfa *vmock.MockModelFactoryAccess, ctx any) {
			ds.EXPECT().GetDocument(ctx, "idx", "doc1").Return(nil, boom)
		}},
		{"get vector fails", func(ds *vmock.MockDatasetService, mfa *vmock.MockModelFactoryAccess, ctx any) {
			ds.EXPECT().GetDocument(ctx, "idx", "doc1").Return(map[string]any{"f": "text"}, nil)
			mfa.EXPECT().GetVector(ctx, "m1", []string{"text"}).Return(nil, boom)
		}},
		{"vector count mismatch", func(ds *vmock.MockDatasetService, mfa *vmock.MockModelFactoryAccess, ctx any) {
			ds.EXPECT().GetDocument(ctx, "idx", "doc1").Return(map[string]any{"f": "text"}, nil)
			mfa.EXPECT().GetVector(ctx, "m1", []string{"text"}).Return([]*interfaces.VectorResp{}, nil)
		}},
		{"upsert fails", func(ds *vmock.MockDatasetService, mfa *vmock.MockModelFactoryAccess, ctx any) {
			ds.EXPECT().GetDocument(ctx, "idx", "doc1").Return(map[string]any{"f": "text"}, nil)
			mfa.EXPECT().GetVector(ctx, "m1", []string{"text"}).
				Return([]*interfaces.VectorResp{{Vector: []float32{0.1}}}, nil)
			ds.EXPECT().UpsertDocuments(ctx, "idx", gomock.Any()).Return(nil, boom)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eh, ds, mfa := newVectorizeHandler(t)
			ctx := t.Context()
			tc.setup(ds, mfa, ctx)
			if err := eh.vectorizeDoc(ctx, "idx", "doc1", "m1", []string{"f"}); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestFormatVectorizeFailures_Truncates(t *testing.T) {
	failed := make([]string, 25)
	for i := range failed {
		failed[i] = fmt.Sprintf("doc%02d", i)
	}

	msg := formatVectorizeFailures(failed)
	if !strings.Contains(msg, "failed for 25 documents") {
		t.Fatalf("missing total count: %s", msg)
	}
	if !strings.Contains(msg, "and 5 more") {
		t.Fatalf("missing truncation suffix: %s", msg)
	}
	if strings.Contains(msg, "doc20") {
		t.Fatalf("should list only first 20 ids: %s", msg)
	}

	short := formatVectorizeFailures([]string{"a", "b"})
	if strings.Contains(short, "more") || !strings.Contains(short, "a,b") {
		t.Fatalf("short list should be complete: %s", short)
	}
}
