// Copyright openbkn.ai

package vega_backend

import (
	"context"
	"sync"

	"github.com/bytedance/sonic"

	"bkn-backend/interfaces"
)

const ImportDatasetWriteConcurrency = 4

type datasetWriteExecutorKey struct{}

// DatasetDocument is the explicit input to the shared Vega writer.
type DatasetDocument struct {
	ID       string
	Document map[string]any
}

// NewDatasetDocument retains the established map payload sent to Vega.
func NewDatasetDocument(id string, value any) (DatasetDocument, error) {
	body, err := sonic.Marshal(value)
	if err != nil {
		return DatasetDocument{}, err
	}
	var document map[string]any
	if err := sonic.Unmarshal(body, &document); err != nil {
		return DatasetDocument{}, err
	}
	document["_id"] = id
	return DatasetDocument{ID: id, Document: document}, nil
}

// WithDatasetWriteConcurrency enables bounded writes for a batch. Import
// stages run sequentially, so no cross-stage semaphore is necessary.
func WithDatasetWriteConcurrency(ctx context.Context, concurrency int) context.Context {
	if concurrency < 1 {
		concurrency = 1
	}
	return context.WithValue(ctx, datasetWriteExecutorKey{}, concurrency)
}

// WriteDatasetDocuments writes documents sequentially by default and uses the
// configured per-batch worker limit for whole-network imports.
func WriteDatasetDocuments(ctx context.Context, service interfaces.VegaBackendService,
	datasetID string, documents []DatasetDocument) error {
	if len(documents) == 0 {
		return nil
	}
	concurrency, concurrent := ctx.Value(datasetWriteExecutorKey{}).(int)
	if !concurrent || len(documents) == 1 {
		for _, document := range documents {
			TrackDatasetWriteAttempt(ctx, datasetID, document.ID)
			if err := service.WriteDatasetDocument(ctx, datasetID, document.ID, document.Document); err != nil {
				return err
			}
		}
		return nil
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan DatasetDocument)
	workerCount := min(concurrency, len(documents))
	var workers sync.WaitGroup
	var firstError error
	var errorOnce sync.Once
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for document := range jobs {
				TrackDatasetWriteAttempt(ctx, datasetID, document.ID)
				err := service.WriteDatasetDocument(workerCtx, datasetID, document.ID, document.Document)
				if err != nil {
					errorOnce.Do(func() {
						firstError = err
						cancel()
					})
					return
				}
			}
		}()
	}

dispatch:
	for _, document := range documents {
		select {
		case jobs <- document:
		case <-workerCtx.Done():
			break dispatch
		}
	}
	close(jobs)
	workers.Wait()
	if firstError != nil {
		return firstError
	}
	return ctx.Err()
}
