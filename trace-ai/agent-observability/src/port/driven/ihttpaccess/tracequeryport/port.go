package tracequeryport

import (
	"context"
	"encoding/json"

	"github.com/kowell-ai/kowell-core/trace-ai/agent-observability/src/domain/valueobject/opensearchvo"
)

type TraceQueryPort interface {
	SearchTraces(ctx context.Context, query json.RawMessage) (opensearchvo.SearchResult, error)
}
