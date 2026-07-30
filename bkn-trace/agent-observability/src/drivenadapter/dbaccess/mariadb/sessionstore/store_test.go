package sessionstore

import (
	"testing"
	"time"
)

func TestTransactionRetryDelayUsesBoundedExponentialJitter(t *testing.T) {
	t.Parallel()

	for attempt := 0; attempt < transactionRetries; attempt++ {
		maximum := 5 * time.Millisecond * time.Duration(1<<attempt)
		for sample := 0; sample < 100; sample++ {
			delay := transactionRetryDelay(attempt)
			if delay < 0 || delay > maximum {
				t.Fatalf("attempt %d delay %s exceeds [0,%s]", attempt, delay, maximum)
			}
		}
	}
}
