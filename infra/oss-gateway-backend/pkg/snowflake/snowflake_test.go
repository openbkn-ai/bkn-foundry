package snowflake

import (
	"fmt"
	"testing"
	"time"
)

func TestIDWorker_GetID(t *testing.T) {
	worker, err := NewIDWorker(1, 1)
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}

	// Test ID generation.
	id1, err := worker.GetID()
	if err != nil {
		t.Fatalf("Failed to generate ID: %v", err)
	}

	id2, err := worker.GetID()
	if err != nil {
		t.Fatalf("Failed to generate ID: %v", err)
	}

	// IDs should be increasing.
	if id2 <= id1 {
		t.Errorf("ID should be incrementing, got id1=%d, id2=%d", id1, id2)
	}

	// IDs should be 19-digit numbers.
	id1Str := fmt.Sprintf("%d", id1)
	if len(id1Str) != 19 {
		t.Errorf("ID should be 19 digits, got %d (length=%d)", id1, len(id1Str))
	}

	t.Logf("Generated IDs: %d, %d (both are 19 digits)", id1, id2)
}

func TestIDWorker_Concurrent(t *testing.T) {
	worker, err := NewIDWorker(1, 1)
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}

	const numGoroutines = 100
	const numIDsPerGoroutine = 1000

	ids := make(chan int64, numGoroutines*numIDsPerGoroutine)
	done := make(chan bool, numGoroutines)

	// Generate IDs concurrently.
	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < numIDsPerGoroutine; j++ {
				id, err := worker.GetID()
				if err != nil {
					t.Errorf("Failed to generate ID: %v", err)
					continue
				}
				ids <- id
			}
			done <- true
		}()
	}

	// Wait for all goroutines to finish.
	for i := 0; i < numGoroutines; i++ {
		<-done
	}
	close(ids)

	// Check ID uniqueness.
	idMap := make(map[int64]bool)
	for id := range ids {
		if idMap[id] {
			t.Errorf("Duplicate ID found: %d", id)
		}
		idMap[id] = true
	}

	t.Logf("Generated %d unique IDs", len(idMap))
}

func TestIDWorker_InvalidParams(t *testing.T) {
	// Test an invalid workerID.
	_, err := NewIDWorker(1, 32)
	if err == nil {
		t.Error("Expected error for invalid workerID, got nil")
	}

	// Test an invalid datacenterID.
	_, err = NewIDWorker(32, 1)
	if err == nil {
		t.Error("Expected error for invalid datacenterID, got nil")
	}

	// Test negative values.
	_, err = NewIDWorker(-1, 1)
	if err == nil {
		t.Error("Expected error for negative datacenterID, got nil")
	}
}

func TestGenerateID(t *testing.T) {
	// Test the default global function.
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("Failed to generate ID: %v", err)
	}

	idStr := fmt.Sprintf("%d", id)
	if len(idStr) != 19 {
		t.Errorf("ID should be 19 digits, got %d (length=%d)", id, len(idStr))
	}

	t.Logf("Generated ID: %d", id)
}

func BenchmarkIDWorker_GetID(b *testing.B) {
	worker, _ := NewIDWorker(1, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = worker.GetID()
	}
}

func TestIDLength(t *testing.T) {
	worker, _ := NewIDWorker(1, 1)

	// Generate multiple IDs and verify their length.
	for i := 0; i < 1000; i++ {
		id, err := worker.GetID()
		if err != nil {
			t.Fatalf("Failed to generate ID: %v", err)
		}

		// Convert to a string to check the length.
		idStr := fmt.Sprintf("%d", id)
		if len(idStr) != 19 {
			t.Errorf("ID length should be 19 digits, got %d (length=%d)", id, len(idStr))
		}
	}

	t.Log("All 1000 IDs have 19 digits")
}

func TestIDWorker_HighFrequency(t *testing.T) {
	worker, _ := NewIDWorker(1, 1)

	// High-frequency generation test.
	start := time.Now()
	count := 100000

	for i := 0; i < count; i++ {
		_, err := worker.GetID()
		if err != nil {
			t.Fatalf("Failed to generate ID at iteration %d: %v", i, err)
		}
	}

	elapsed := time.Since(start)
	t.Logf("Generated %d IDs in %v (%.2f IDs/ms)", count, elapsed, float64(count)/float64(elapsed.Milliseconds()))
}
