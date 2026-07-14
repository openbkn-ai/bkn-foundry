package tracequeryport

import (
	"context"
	"encoding/json"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/opensearchvo"
)

type TraceQueryPort interface {
	SearchTraces(ctx context.Context, query json.RawMessage) (opensearchvo.SearchResult, error)
}
