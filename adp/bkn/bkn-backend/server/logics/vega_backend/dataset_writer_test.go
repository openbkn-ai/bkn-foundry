// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.

package vega_backend

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	mock_interfaces "bkn-backend/interfaces/mock"
)

func TestWriteDatasetDocumentsUsesPerBatchBoundedConcurrency(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := mock_interfaces.NewMockVegaBackendService(ctrl)
	var mu sync.Mutex
	inFlight := 0
	maximum := 0
	callCount := 0
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	service.EXPECT().WriteDatasetDocument(gomock.Any(), "dataset", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, _ map[string]any) error {
			mu.Lock()
			callCount++
			inFlight++
			maximum = max(maximum, inFlight)
			currentCall := callCount
			mu.Unlock()
			if currentCall <= 3 {
				started <- struct{}{}
			}
			<-release
			mu.Lock()
			inFlight--
			mu.Unlock()
			return nil
		}).Times(8)

	documents := make([]DatasetDocument, 8)
	for index := range documents {
		documents[index] = DatasetDocument{ID: string(rune('a' + index)), Document: map[string]any{}}
	}
	ctx := WithDatasetWriteConcurrency(context.Background(), 3)
	result := make(chan error, 1)
	go func() {
		result <- WriteDatasetDocuments(ctx, service, "dataset", documents)
	}()
	for range 3 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for concurrent Vega writes")
		}
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if maximum != 3 {
		t.Fatalf("maximum concurrency = %d, want 3", maximum)
	}
}

func TestWriteDatasetDocumentsStopsDispatchAfterFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := mock_interfaces.NewMockVegaBackendService(ctrl)
	want := errors.New("write failed")
	service.EXPECT().WriteDatasetDocument(gomock.Any(), "dataset", gomock.Any(), gomock.Any()).
		Return(want).MaxTimes(2)
	documents := []DatasetDocument{
		{ID: "a", Document: map[string]any{}},
		{ID: "b", Document: map[string]any{}},
		{ID: "c", Document: map[string]any{}},
	}
	ctx := WithDatasetWriteConcurrency(context.Background(), 1)
	if err := WriteDatasetDocuments(ctx, service, "dataset", documents); !errors.Is(err, want) {
		t.Fatalf("WriteDatasetDocuments() error = %v, want %v", err, want)
	}
}

func TestWriteDatasetDocumentsRemainsSequentialWithoutExecutor(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := mock_interfaces.NewMockVegaBackendService(ctrl)
	first := service.EXPECT().WriteDatasetDocument(gomock.Any(), "dataset", "a", gomock.Any()).Return(nil)
	service.EXPECT().WriteDatasetDocument(gomock.Any(), "dataset", "b", gomock.Any()).Return(nil).After(first)
	if err := WriteDatasetDocuments(context.Background(), service, "dataset", []DatasetDocument{
		{ID: "a", Document: map[string]any{}},
		{ID: "b", Document: map[string]any{}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteDatasetDocumentsTracksFailedRequestByDocumentID(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := mock_interfaces.NewMockVegaBackendService(ctrl)
	want := errors.New("ambiguous Vega response")
	service.EXPECT().WriteDatasetDocument(gomock.Any(), "dataset", "doc-1", gomock.Any()).Return(want)
	ctx, tracker := WithDatasetWriteTracker(context.Background())
	err := WriteDatasetDocuments(ctx, service, "dataset", []DatasetDocument{
		{ID: "doc-1", Document: map[string]any{"_id": "doc-1"}},
	})
	if !errors.Is(err, want) {
		t.Fatalf("WriteDatasetDocuments() error = %v, want %v", err, want)
	}
	ids := tracker.AttemptedDocumentIDs("dataset")
	if len(ids) != 1 || ids[0] != "doc-1" {
		t.Fatalf("attempted IDs = %v, want doc-1", ids)
	}
}

func TestNewDatasetDocumentKeepsMapPayloadAndID(t *testing.T) {
	document, err := NewDatasetDocument("doc-1", struct {
		Name   string `json:"name"`
		Hidden string `json:"-"`
	}{Name: "resource", Hidden: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if document.ID != "doc-1" || document.Document["_id"] != "doc-1" ||
		document.Document["name"] != "resource" {
		t.Fatalf("unexpected indexed document: %#v", document)
	}
	if _, exists := document.Document["Hidden"]; exists {
		t.Fatalf("non-persisted field leaked into index: %#v", document.Document)
	}
}
