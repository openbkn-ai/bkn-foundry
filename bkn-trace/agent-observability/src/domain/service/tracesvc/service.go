package tracesvc

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/opensearchvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/oteltracevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ihttpaccess/tracequeryport"
)

type TraceQueryService struct {
	traceQueryPort tracequeryport.TraceQueryPort
}

const DefaultTraceGraphSpanLimit = 1000

func New(traceQueryPort tracequeryport.TraceQueryPort) *TraceQueryService {
	return &TraceQueryService{traceQueryPort: traceQueryPort}
}

func (s *TraceQueryService) SearchTraces(ctx context.Context, query json.RawMessage) (opensearchvo.SearchResult, error) {
	return s.traceQueryPort.SearchTraces(ctx, query)
}

func (s *TraceQueryService) GetTraceGraphByTraceID(ctx context.Context, traceID string) (oteltracevo.TraceGraphResponse, bool, error) {
	traceID = strings.TrimSpace(traceID)
	query, err := json.Marshal(map[string]any{
		"size": DefaultTraceGraphSpanLimit + 1,
		"query": map[string]any{
			"term": map[string]string{
				"traceId": traceID,
			},
		},
		"sort": []map[string]any{
			{
				"startTime": map[string]string{
					"order":         "asc",
					"unmapped_type": "date",
				},
			},
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
	spans = deduplicateSpanRecords(spans)
	truncated := false
	if len(spans) > DefaultTraceGraphSpanLimit {
		truncated = true
		spans = spans[:DefaultTraceGraphSpanLimit]
	}
	return buildTraceGraph(traceID, spans, truncated), true, nil
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

type ss4oFlatSpan struct {
	TraceID      string             `json:"traceId"`
	SpanID       string             `json:"spanId"`
	ParentSpanID string             `json:"parentSpanId"`
	Name         string             `json:"name"`
	Kind         string             `json:"kind"`
	StartTime    string             `json:"startTime"`
	EndTime      string             `json:"endTime"`
	Resource     map[string]string  `json:"resource"`
	Status       oteltracevo.Status `json:"status"`
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
		var flatSpan ss4oFlatSpan
		if err := json.Unmarshal(hit.Source, &flatSpan); err == nil && isSS4OFlatSpan(flatSpan, traceID) {
			records = append(records, spanRecord{
				Span: oteltracevo.Span{
					TraceID:           flatSpan.TraceID,
					SpanID:            flatSpan.SpanID,
					ParentSpanID:      flatSpan.ParentSpanID,
					Name:              flatSpan.Name,
					Kind:              flatSpan.Kind,
					StartTimeUnixNano: rfc3339ToNanoString(flatSpan.StartTime),
					EndTimeUnixNano:   rfc3339ToNanoString(flatSpan.EndTime),
					Status:            flatSpan.Status,
				},
				ServiceName: flatSpan.Resource["service.name"],
			})
			continue
		}
		var span oteltracevo.Span
		if err := json.Unmarshal(hit.Source, &span); err == nil && isOTLPSpan(span, traceID) {
			records = append(records, spanRecord{Span: span})
		}
	}
	return records, nil
}

func isSS4OFlatSpan(span ss4oFlatSpan, traceID string) bool {
	return span.TraceID == traceID && span.SpanID != "" && (span.StartTime != "" || span.EndTime != "")
}

func isOTLPSpan(span oteltracevo.Span, traceID string) bool {
	return span.TraceID == traceID && span.SpanID != "" && (span.StartTimeUnixNano != "" || span.EndTimeUnixNano != "")
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

func deduplicateSpanRecords(records []spanRecord) []spanRecord {
	seen := map[string]struct{}{}
	unique := make([]spanRecord, 0, len(records))
	for _, record := range records {
		if record.Span.SpanID == "" {
			unique = append(unique, record)
			continue
		}
		if _, ok := seen[record.Span.SpanID]; ok {
			continue
		}
		seen[record.Span.SpanID] = struct{}{}
		unique = append(unique, record)
	}
	return unique
}

func buildTraceGraph(traceID string, records []spanRecord, truncated bool) oteltracevo.TraceGraphResponse {
	sort.SliceStable(records, func(i, j int) bool {
		left, _ := parseNano(records[i].Span.StartTimeUnixNano)
		right, _ := parseNano(records[j].Span.StartTimeUnixNano)
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
		start, startOK := parseNano(span.StartTimeUnixNano)
		end, endOK := parseNano(span.EndTimeUnixNano)
		duration := int64(0)
		if !startOK || !endOK || end < start {
			partialReasons["invalid_span_timestamp"] = struct{}{}
			end = start
		} else {
			duration = end - start
		}
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
			DurationNano: duration,
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
	if response.DurationNano < 0 {
		response.DurationNano = 0
	}
	response.Page.NodeCount = len(response.Data.Nodes)
	response.Page.EdgeCount = len(response.Data.Edges)
	response.Page.Truncated = truncated
	if truncated {
		partialReasons["trace_query_truncated"] = struct{}{}
	}
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
	switch strings.ToUpper(status.Code) {
	case "STATUS_CODE_ERROR", "ERROR":
		return "error"
	default:
		return "ok"
	}
}

func parseNano(value string) (int64, bool) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	return parsed, err == nil
}

func rfc3339ToNanoString(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return ""
	}
	return strconv.FormatInt(parsed.UnixNano(), 10)
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
