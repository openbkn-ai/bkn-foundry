package tracesvc

import (
	"context"
	"encoding/json"

	"github.com/kowell-ai/kowell-core/trace-ai/agent-observability/src/domain/valueobject/opensearchvo"
	"github.com/kowell-ai/kowell-core/trace-ai/agent-observability/src/port/driven/ihttpaccess/tracequeryport"
)

type TraceQueryService struct {
	traceQueryPort tracequeryport.TraceQueryPort
}

func New(traceQueryPort tracequeryport.TraceQueryPort) *TraceQueryService {
	return &TraceQueryService{traceQueryPort: traceQueryPort}
}

func (s *TraceQueryService) SearchTraces(ctx context.Context, query json.RawMessage) (opensearchvo.SearchResult, error) {
	return s.traceQueryPort.SearchTraces(ctx, query)
}
