// Copyright openbkn.ai

package vega_backend

import (
	"context"
	"testing"
)

func TestDatasetWriteTrackerTracksAttemptsIndependentlyByDataset(t *testing.T) {
	ctx, tracker := WithDatasetWriteTracker(context.Background())
	if len(tracker.AttemptedDocumentIDs("dataset-a")) != 0 {
		t.Fatal("new tracker unexpectedly contains writes")
	}

	TrackDatasetWriteAttempt(ctx, "dataset-a", "doc-1")
	TrackDatasetWriteAttempt(ctx, "dataset-a", "doc-1")

	ids := tracker.AttemptedDocumentIDs("dataset-a")
	if len(ids) != 1 || ids[0] != "doc-1" {
		t.Fatalf("dataset write attempts = %v, want doc-1 once", ids)
	}
	if len(tracker.AttemptedDocumentIDs("dataset-b")) != 0 {
		t.Fatal("write attempt leaked into another dataset")
	}
}

func TestDatasetWriteTrackerHelpersAreNoOpWithoutTracker(t *testing.T) {
	ctx := context.Background()
	TrackDatasetWriteAttempt(ctx, "dataset-a", "doc-1")
}
