// Copyright openbkn.ai

package vega_backend

import (
	"context"
	"sync"
)

type datasetWriteTrackerKey struct{}

// DatasetWriteTracker records Vega writes performed as part of one business
// operation. Attempts are tracked before the remote call because a failed or
// cancelled response does not prove that Vega rejected the write.
type DatasetWriteTracker struct {
	mu        sync.Mutex
	attempted map[string]map[string]struct{}
}

// WithDatasetWriteTracker returns the tracker already attached to ctx, or
// installs a new operation-scoped tracker.
func WithDatasetWriteTracker(ctx context.Context) (context.Context, *DatasetWriteTracker) {
	if tracker, ok := ctx.Value(datasetWriteTrackerKey{}).(*DatasetWriteTracker); ok {
		return ctx, tracker
	}
	tracker := &DatasetWriteTracker{attempted: make(map[string]map[string]struct{})}
	return context.WithValue(ctx, datasetWriteTrackerKey{}, tracker), tracker
}

// TrackDatasetWriteAttempt records a document before sending it to Vega. A
// failed response is ambiguous: Vega may already have applied the write.
func TrackDatasetWriteAttempt(ctx context.Context, datasetID, documentID string) {
	tracker, ok := ctx.Value(datasetWriteTrackerKey{}).(*DatasetWriteTracker)
	if !ok || datasetID == "" || documentID == "" {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.attempted[datasetID] == nil {
		tracker.attempted[datasetID] = make(map[string]struct{})
	}
	tracker.attempted[datasetID][documentID] = struct{}{}
}

// AttemptedDocumentIDs returns only documents touched by this operation.
func (tracker *DatasetWriteTracker) AttemptedDocumentIDs(datasetID string) []string {
	if tracker == nil {
		return nil
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	ids := make([]string, 0, len(tracker.attempted[datasetID]))
	for id := range tracker.attempted[datasetID] {
		ids = append(ids, id)
	}
	return ids
}
