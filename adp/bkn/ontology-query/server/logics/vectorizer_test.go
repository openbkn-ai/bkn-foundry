// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"context"
	"errors"
	"testing"

	cond "ontology-query/common/condition"
)

func vectorProperty(model string) *cond.DataProperty {
	return &cond.DataProperty{
		Name:        "stadium_name",
		IndexConfig: &cond.IndexConfig{VectorConfig: cond.VectorConfig{ModelID: model}},
	}
}

func TestMemoizeVectorizer_SameModelAndWordVectorizesOnce(t *testing.T) {
	calls := 0
	vectorize := MemoizeVectorizer(func(ctx context.Context, property *cond.DataProperty, word string) ([]cond.VectorResp, error) {
		calls++
		return []cond.VectorResp{{Vector: []float32{0.1}}}, nil
	})

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := vectorize(ctx, vectorProperty("m1"), "Mexico"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if calls != 1 {
		t.Fatalf("identical (model, word) must vectorize once, got %d calls", calls)
	}
}

func TestMemoizeVectorizer_DifferentModelOrWordAreSeparate(t *testing.T) {
	calls := 0
	vectorize := MemoizeVectorizer(func(ctx context.Context, property *cond.DataProperty, word string) ([]cond.VectorResp, error) {
		calls++
		return nil, nil
	})

	ctx := context.Background()
	_, _ = vectorize(ctx, vectorProperty("m1"), "Mexico")
	_, _ = vectorize(ctx, vectorProperty("m2"), "Mexico")
	_, _ = vectorize(ctx, vectorProperty("m1"), "Brazil")

	if calls != 3 {
		t.Fatalf("different model or word must not share a vector, got %d calls", calls)
	}
}

func TestMemoizeVectorizer_UnknownModelIsNotCached(t *testing.T) {
	calls := 0
	vectorize := MemoizeVectorizer(func(ctx context.Context, property *cond.DataProperty, word string) ([]cond.VectorResp, error) {
		calls++
		return nil, nil
	})

	ctx := context.Background()
	_, _ = vectorize(ctx, &cond.DataProperty{Name: "x"}, "Mexico")
	_, _ = vectorize(ctx, &cond.DataProperty{Name: "x"}, "Mexico")

	if calls != 2 {
		t.Fatalf("without a model id there is no safe cache key, got %d calls", calls)
	}
}

func TestMemoizeVectorizer_ErrorsAreCachedToo(t *testing.T) {
	calls := 0
	want := errors.New("vectorize failed")
	vectorize := MemoizeVectorizer(func(ctx context.Context, property *cond.DataProperty, word string) ([]cond.VectorResp, error) {
		calls++
		return nil, want
	})

	ctx := context.Background()
	_, err1 := vectorize(ctx, vectorProperty("m1"), "Mexico")
	_, err2 := vectorize(ctx, vectorProperty("m1"), "Mexico")

	if calls != 1 || !errors.Is(err1, want) || !errors.Is(err2, want) {
		t.Fatalf("a failing vectorization must not be retried per sub-condition: calls=%d", calls)
	}
}
