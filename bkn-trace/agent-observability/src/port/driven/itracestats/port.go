package itracestats

import "context"

// Source provides bounded, batch trace statistics for summary projections.
type Source interface {
	CountSpansByTraceIDs(ctx context.Context, traceIDs []string) (map[string]int, error)
}
