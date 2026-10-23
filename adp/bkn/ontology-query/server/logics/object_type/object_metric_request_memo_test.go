// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
	"go.uber.org/mock/gomock"
)

func TestObjectMetricRequestMemoSeparatesQueries(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := omock.NewMockObjectMetricQueryServiceV1(ctrl)
	ctx := withObjectMetricRequestMemo(context.Background())
	first := &interfaces.ObjectMetricQueryRequestV1{PageSize: 10}
	second := &interfaces.ObjectMetricQueryRequestV1{PageSize: 20}
	calls := 0
	service.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "metric", 1, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _ string, _ int,
			_ *interfaces.ObjectMetricQueryRequestV1) (interfaces.ObjectMetricDataV1, error) {
			calls++
			return interfaces.ObjectMetricDataV1{TotalCount: calls}, nil
		}).Times(2)
	for index, query := range []*interfaces.ObjectMetricQueryRequestV1{first, first, second, second} {
		result, err := queryObjectMetricWithRequestMemo(ctx, service, "kn", "main", "metric", 1, query)
		if err != nil {
			t.Fatal(err)
		}
		if want := index/2 + 1; result.TotalCount != want {
			t.Fatalf("query %d: total=%d, want %d", index, result.TotalCount, want)
		}
	}
	if calls != 2 {
		t.Fatalf("got %d downstream calls, want one per distinct query", calls)
	}
}

func TestObjectMetricRequestMemoDoesNotRetainFailuresOrLargeResults(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := omock.NewMockObjectMetricQueryServiceV1(ctrl)
	ctx := withObjectMetricRequestMemo(context.Background())
	query := &interfaces.ObjectMetricQueryRequestV1{}
	wantErr := errors.New("temporary failure")
	service.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "metric", 1, gomock.Any()).
		Return(interfaces.ObjectMetricDataV1{}, wantErr).Times(2)
	for range 2 {
		_, err := queryObjectMetricWithRequestMemo(ctx, service, "kn", "main", "metric", 1, query)
		if !errors.Is(err, wantErr) {
			t.Fatalf("got %v, want temporary failure", err)
		}
	}
	large := &interfaces.ObjectMetricQueryRequestV1{PageSize: 1000}
	service.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "metric", 1, large).
		Return(interfaces.ObjectMetricDataV1{Entries: make([]interfaces.ObjectMetricDataEntryV1, maxObjectMetricMemoRows+1)}, nil).Times(2)
	for range 2 {
		if _, err := queryObjectMetricWithRequestMemo(ctx, service, "kn", "main", "metric", 1, large); err != nil {
			t.Fatal(err)
		}
	}
}

func TestObjectMetricRequestMemoKeepsInstanceQueriesIndependent(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := omock.NewMockObjectMetricQueryServiceV1(ctrl)
	ctx := withObjectMetricRequestMemo(context.Background())
	query := &interfaces.ObjectMetricQueryRequestV1{InstanceIdentities: []map[string]any{{"id": "a"}}}
	service.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "metric", 1, query).
		Return(interfaces.ObjectMetricDataV1{}, nil).Times(2)
	for range 2 {
		if _, err := queryObjectMetricWithRequestMemo(ctx, service, "kn", "main", "metric", 1, query); err != nil {
			t.Fatal(err)
		}
	}
}

func TestObjectMetricRequestMemoSkipsAmbiguousQueries(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := omock.NewMockObjectMetricQueryServiceV1(ctrl)
	ctx := withObjectMetricRequestMemo(context.Background())
	queries := []*interfaces.ObjectMetricQueryRequestV1{
		{Filter: &interfaces.ObjectMetricConditionV1{NodeType: "predicate",
			Right: []interfaces.ObjectMetricOperandV1{{Source: "literal", Value: string([]byte{0xff})}}}},
		{GroupBy: []interfaces.ObjectMetricPropertyRefV1{{ObjectTypeID: "shipment", PropertyName: string([]byte{0xff})}}},
	}
	service.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "metric", 1, gomock.Any()).
		Return(interfaces.ObjectMetricDataV1{}, nil).Times(4)
	for _, query := range queries {
		for range 2 {
			if _, err := queryObjectMetricWithRequestMemo(ctx, service, "kn", "main", "metric", 1, query); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestObjectMetricRequestMemoWaiterCanCancel(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := omock.NewMockObjectMetricQueryServiceV1(ctrl)
	ctx := withObjectMetricRequestMemo(context.Background())
	query := &interfaces.ObjectMetricQueryRequestV1{}
	started, release := make(chan struct{}), make(chan struct{})
	service.EXPECT().QueryObjectMetricDataV1(gomock.Any(), "kn", "main", "metric", 1, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _ string, _ int,
			_ *interfaces.ObjectMetricQueryRequestV1) (interfaces.ObjectMetricDataV1, error) {
			close(started)
			<-release
			return interfaces.ObjectMetricDataV1{TotalCount: 7}, nil
		}).Times(1)
	leaderResult := make(chan error, 1)
	go func() {
		_, err := queryObjectMetricWithRequestMemo(ctx, service, "kn", "main", "metric", 1, query)
		leaderResult <- err
	}()
	<-started
	waiterCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := queryObjectMetricWithRequestMemo(waiterCtx, service, "kn", "main", "metric", 1, query); !errors.Is(err, context.Canceled) {
		close(release)
		t.Fatalf("waiting call returned %v, want cancellation", err)
	}
	close(release)
	if err := <-leaderResult; err != nil {
		t.Fatal(err)
	}
	result, err := queryObjectMetricWithRequestMemo(ctx, service, "kn", "main", "metric", 1, query)
	if err != nil || result.TotalCount != 7 {
		t.Fatalf("completed cached call: result=%#v err=%v", result, err)
	}
}
