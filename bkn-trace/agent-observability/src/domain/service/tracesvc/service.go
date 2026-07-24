package tracesvc

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/opensearchvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/oteltracevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ihttpaccess/tracequeryport"
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

func (s *TraceQueryService) GetTraceGraphByTraceID(ctx context.Context, traceID string) (oteltracevo.TraceGraphResponse, bool, error) {
	traceID = strings.TrimSpace(traceID)
	query, err := json.Marshal(map[string]any{
		"size": 1000,
		"query": map[string]any{
			"term": map[string]string{
				"traceId.keyword": traceID,
			},
		},
		"sort": []map[string]any{
			{"startTimeUnixNano": map[string]string{"order": "asc"}},
		},
	})
	if err != nil {
		return oteltracevo.TraceGraphResponse{}, false, err
	}
	result, err := s.traceQueryPort.SearchTraces(ctx, query)
	if err != nil {
		return oteltracevo.TraceGraphResponse{}, false, err
	}
	spans, err := spansFromSearchResult(result, traceID)
	if err != nil {
		return oteltracevo.TraceGraphResponse{}, false, err
	}
	if len(spans) == 0 {
		return oteltracevo.TraceGraphResponse{}, false, nil
	}
	return buildTraceGraph(traceID, spans), true, nil
}

type searchResponse struct {
	Hits struct {
		Hits []struct {
			Source json.RawMessage `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

type spanRecord struct {
	Span        oteltracevo.Span
	ServiceName string
}

func spansFromSearchResult(result opensearchvo.SearchResult, traceID string) ([]spanRecord, error) {
	var response searchResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, err
	}
	var records []spanRecord
	for _, hit := range response.Hits.Hits {
		var traceData oteltracevo.TraceData
		if err := json.Unmarshal(hit.Source, &traceData); err == nil && len(traceData.ResourceSpans) > 0 {
			records = append(records, spansFromTraceData(traceData, traceID)...)
			continue
		}
		var span oteltracevo.Span
		if err := json.Unmarshal(hit.Source, &span); err == nil && span.TraceID == traceID {
			records = append(records, spanRecord{Span: span})
		}
	}
	return records, nil
}

func spansFromTraceData(traceData oteltracevo.TraceData, traceID string) []spanRecord {
	var records []spanRecord
	for _, resourceSpan := range traceData.ResourceSpans {
		serviceName := resourceAttribute(resourceSpan.Resource, "service.name")
		for _, scopeSpan := range resourceSpan.ScopeSpans {
			for _, span := range scopeSpan.Spans {
				if span.TraceID == traceID {
					records = append(records, spanRecord{Span: span, ServiceName: serviceName})
				}
			}
		}
	}
	return records
}

func buildTraceGraph(traceID string, records []spanRecord) oteltracevo.TraceGraphResponse {
	sort.SliceStable(records, func(i, j int) bool {
		left := parseNano(records[i].Span.StartTimeUnixNano)
		right := parseNano(records[j].Span.StartTimeUnixNano)
		if left == right {
			return records[i].Span.SpanID < records[j].Span.SpanID
		}
		return left < right
	})

	response := oteltracevo.TraceGraphResponse{
		TraceID: traceID,
		Status:  "ok",
	}
	seen := map[string]struct{}{}
	partialReasons := map[string]struct{}{}
	var minStart, maxEnd int64
	for i, record := range records {
		span := record.Span
		start := parseNano(span.StartTimeUnixNano)
		end := parseNano(span.EndTimeUnixNano)
		if i == 0 || start < minStart {
			minStart = start
		}
		if end > maxEnd {
			maxEnd = end
		}
		status := spanStatus(span.Status)
		if status == "error" {
			response.Status = "error"
		}
		seen[span.SpanID] = struct{}{}
		response.Data.Nodes = append(response.Data.Nodes, oteltracevo.TraceGraphNode{
			SpanID:       span.SpanID,
			ParentSpanID: span.ParentSpanID,
			Name:         span.Name,
			Kind:         span.Kind,
			ServiceName:  record.ServiceName,
			Status:       status,
			ErrorMessage: span.Status.Message,
			StartNano:    start,
			EndNano:      end,
			DurationNano: end - start,
		})
	}
	for _, node := range response.Data.Nodes {
		if node.ParentSpanID == "" {
			continue
		}
		if _, ok := seen[node.ParentSpanID]; !ok {
			partialReasons["orphan_span"] = struct{}{}
			continue
		}
		response.Data.Edges = append(response.Data.Edges, oteltracevo.TraceGraphEdge{
			ID:       "edge:" + strconv.Itoa(len(response.Data.Edges)+1),
			ParentID: node.ParentSpanID,
			ChildID:  node.SpanID,
			EdgeType: "parent_child",
		})
	}
	response.DurationNano = maxEnd - minStart
	response.Page.NodeCount = len(response.Data.Nodes)
	response.Page.EdgeCount = len(response.Data.Edges)
	response.PartialReasons = sortedKeys(partialReasons)
	response.Partial = len(response.PartialReasons) > 0
	return response
}

func resourceAttribute(resource oteltracevo.Resource, key string) string {
	for _, attribute := range resource.Attributes {
		if attribute.Key == key {
			return attribute.Value.StringValue
		}
	}
	return ""
}

func spanStatus(status oteltracevo.Status) string {
	switch status.Code {
	case "STATUS_CODE_ERROR", "ERROR":
		return "error"
	default:
		return "ok"
	}
}

func parseNano(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func sortedKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
