package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/opensearchexporter"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

func reject(reason string) Response { return Response{Reason: reason, Spans: []Span{}} }

func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func convert(req Request) Response {
	if req.SourceDeployment == "" {
		return reject("source_deployment_required")
	}
	parsed, err := strictJSON(req.Document)
	if err != nil {
		return reject("invalid_source_json")
	}
	residue := map[string]any{}
	if err := walkFields("root", parsed, "", residue); err != nil {
		return reject(err.Error())
	}
	traces, err := (&ptrace.JSONUnmarshaler{}).UnmarshalTraces(req.Document)
	if err != nil {
		return reject("invalid_otlp")
	}
	originals := map[string]ptrace.Span{}
	positions := map[string]string{}
	for i := 0; i < traces.ResourceSpans().Len(); i++ {
		rs := traces.ResourceSpans().At(i)
		for j := 0; j < rs.ScopeSpans().Len(); j++ {
			ss := rs.ScopeSpans().At(j)
			for k := 0; k < ss.Spans().Len(); k++ {
				s := ss.Spans().At(k)
				if s.TraceID().IsEmpty() || s.SpanID().IsEmpty() || s.EndTimestamp() < s.StartTimestamp() {
					return reject("invalid_span_identity_or_time")
				}
				key := s.TraceID().String() + "/" + s.SpanID().String()
				if _, exists := originals[key]; exists {
					return reject("duplicate_span_identity")
				}
				for n := 0; n < s.Links().Len(); n++ {
					link := s.Links().At(n)
					if link.TraceID().IsEmpty() || link.SpanID().IsEmpty() {
						return reject("invalid_link_identity")
					}
				}
				originals[key] = s
				positions[key] = fmt.Sprintf("/resourceSpans/%d/scopeSpans/%d/spans/%d", i, j, k)
			}
		}
	}
	if len(originals) == 0 {
		return reject("empty_traces")
	}
	documents, err := captureExporter(traces)
	if err != nil || len(documents) != len(originals) {
		return reject("official_exporter_failed")
	}
	result := Response{Accepted: true, Reason: "official_ss4o_encoded", Spans: []Span{}}
	seen := map[string]bool{}
	for _, payload := range documents {
		key := fmt.Sprint(payload["traceId"]) + "/" + fmt.Sprint(payload["spanId"])
		s, ok := originals[key]
		if !ok || seen[key] {
			return reject("exporter_identity_mismatch")
		}
		seen[key] = true
		// The pinned encoder invents observedTimestamp for events whose Unix
		// second is zero. Recover only the original pdata time, never that clock.
		if events, exists := payload["events"]; exists {
			list, ok := events.([]any)
			if !ok || len(list) != s.Events().Len() {
				return reject("exporter_event_mismatch")
			}
			for n, item := range list {
				event, ok := item.(map[string]any)
				if !ok {
					return reject("exporter_event_mismatch")
				}
				delete(event, "observedTimestamp")
				ts := s.Events().At(n).Timestamp()
				if ts == 0 {
					delete(event, "@timestamp")
				} else {
					event["@timestamp"] = ts.AsTime().UTC().Format(time.RFC3339Nano)
				}
			}
		}
		native, err := json.Marshal(payload)
		if err != nil {
			return reject("invalid_exporter_json")
		}
		result.Spans = append(result.Spans, Span{Payload: payload, Sidecar: Sidecar{
			SourcePosition: positions[key],
			SourceDocument: req.Document, SourceDeployment: req.SourceDeployment,
			SourceHash: digest(req.Document), NativeHash: digest(native), UnknownFields: residue,
			Codec: "opensearchexporter-v0.148.0-ss4o-event-time-v1",
		}})
	}
	sort.Slice(result.Spans, func(i, j int) bool {
		a, b := result.Spans[i].Payload, result.Spans[j].Payload
		return fmt.Sprint(a["traceId"])+"/"+fmt.Sprint(a["spanId"]) < fmt.Sprint(b["traceId"])+"/"+fmt.Sprint(b["spanId"])
	})
	return result
}

type offlineHost struct{}

func (offlineHost) GetExtensions() map[component.ID]component.Component {
	return map[component.ID]component.Component{}
}

func captureExporter(traces ptrace.Traces) ([]map[string]any, error) {
	var mu sync.Mutex
	var documents []map[string]any
	var captureError error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/_bulk") {
			captureError = errors.New("unexpected exporter endpoint")
			http.Error(w, "offline bulk only", http.StatusBadRequest)
			return
		}
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				captureError = err
				http.Error(w, "invalid bulk compression", 400)
				return
			}
			defer gz.Close()
			reader = gz
		}
		scanner := bufio.NewScanner(io.LimitReader(reader, 32*1024*1024))
		scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
		items := []any{}
		for scanner.Scan() {
			var meta map[string]json.RawMessage
			if err := json.Unmarshal(scanner.Bytes(), &meta); err != nil || len(meta) != 1 || !scanner.Scan() {
				captureError = errors.New("invalid bulk framing")
				break
			}
			value, err := strictJSON(scanner.Bytes())
			payload, ok := value.(map[string]any)
			if err != nil || !ok {
				captureError = errors.New("invalid bulk payload")
				break
			}
			documents = append(documents, payload)
			for action := range meta {
				items = append(items, map[string]any{action: map[string]any{"status": 201}})
			}
		}
		if err := scanner.Err(); err != nil {
			captureError = err
		}
		if captureError != nil {
			http.Error(w, "invalid bulk", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": false, "items": items})
	}))
	defer server.Close()
	factory := opensearchexporter.NewFactory()
	cfg := factory.CreateDefaultConfig().(*opensearchexporter.Config)
	cfg.Endpoint = server.URL
	cfg.TracesIndex = "offline-span-capture"
	cfg.TracesIndexTimeFormat = ""
	cfg.BackOffConfig.Enabled = false
	cfg.QueueConfig = configoptional.None[exporterhelper.QueueBatchConfig]()
	settings := exporter.Settings{ID: component.NewID(factory.Type()), BuildInfo: component.NewDefaultBuildInfo(), TelemetrySettings: component.TelemetrySettings{
		Logger: zap.NewNop(), MeterProvider: metricnoop.NewMeterProvider(), TracerProvider: tracenoop.NewTracerProvider(), Resource: pcommon.NewResource(),
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	instance, err := factory.CreateTraces(ctx, settings, cfg)
	if err != nil {
		return nil, err
	}
	if err := instance.Start(ctx, offlineHost{}); err != nil {
		return nil, err
	}
	defer instance.Shutdown(context.Background())
	if err := instance.ConsumeTraces(ctx, traces); err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	return documents, captureError
}

func strictJSON(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, err := jsonValue(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return value, nil
}

func jsonValue(d *json.Decoder) (any, error) {
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := map[string]any{}
		for d.More() {
			keyToken, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("invalid key")
			}
			if _, exists := object[key]; exists {
				return nil, errors.New("duplicate key")
			}
			value, err := jsonValue(d)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		_, err := d.Token()
		return object, err
	case json.Delim('['):
		array := []any{}
		for d.More() {
			value, err := jsonValue(d)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err := d.Token()
		return array, err
	default:
		if _, delimiter := token.(json.Delim); delimiter {
			return nil, errors.New("invalid delimiter")
		}
		return token, nil
	}
}

var fields = map[string]map[string]string{
	"root":         {"resourceSpans": "resourceSpan[]"},
	"resourceSpan": {"resource": "resource", "scopeSpans": "scopeSpan[]", "schemaUrl": ""},
	"resource":     {"attributes": "attribute[]", "droppedAttributesCount": ""},
	"scopeSpan":    {"scope": "scope", "spans": "span[]", "schemaUrl": ""},
	"scope":        {"name": "", "version": "", "attributes": "attribute[]", "droppedAttributesCount": ""},
	"span":         {"traceId": "", "spanId": "", "parentSpanId": "", "traceState": "", "name": "", "kind": "", "startTimeUnixNano": "", "endTimeUnixNano": "", "attributes": "attribute[]", "droppedAttributesCount": "", "events": "event[]", "droppedEventsCount": "", "links": "link[]", "droppedLinksCount": "", "status": "status", "flags": ""},
	"event":        {"timeUnixNano": "", "name": "", "attributes": "attribute[]", "droppedAttributesCount": ""},
	"link":         {"traceId": "", "spanId": "", "traceState": "", "attributes": "attribute[]", "droppedAttributesCount": "", "flags": ""},
	"status":       {"code": "", "message": ""},
	"attribute":    {"key": "", "value": "value"},
	"value":        {"stringValue": "", "boolValue": "", "intValue": "", "doubleValue": "", "bytesValue": "", "arrayValue": "array", "kvlistValue": "kvlist"},
	"array":        {"values": "value[]"}, "kvlist": {"values": "attribute[]"},
}

func walkFields(kind string, value any, path string, residue map[string]any) error {
	object, ok := value.(map[string]any)
	if !ok {
		return errors.New("invalid_otlp_shape")
	}
	if kind == "value" {
		choices := 0
		for key := range object {
			if _, known := fields[kind][key]; known {
				choices++
			}
		}
		if choices > 1 {
			return errors.New("ambiguous_any_value")
		}
	}
	if kind == "span" {
		for _, field := range []string{"startTimeUnixNano", "endTimeUnixNano"} {
			text, ok := object[field].(string)
			if !ok || text == "" {
				return errors.New("span_endpoint_required")
			}
			for _, digit := range text {
				if digit < '0' || digit > '9' {
					return errors.New("span_endpoint_invalid")
				}
			}
			if _, err := strconv.ParseUint(text, 10, 64); err != nil {
				return errors.New("span_endpoint_invalid")
			}
		}
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		item := object[key]
		child, known := fields[kind][key]
		location := path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
		if !known {
			if criticalKey(key) || criticalResidue(item) {
				return errors.New("unknown_critical_field")
			}
			residue[location] = item
			continue
		}
		if child == "" {
			continue
		}
		if strings.HasSuffix(child, "[]") {
			list, ok := item.([]any)
			if !ok {
				return errors.New("invalid_otlp_shape")
			}
			seenKeys := map[string]bool{}
			for i, entry := range list {
				if child == "attribute[]" {
					if attribute, ok := entry.(map[string]any); ok {
						if name, ok := attribute["key"].(string); ok {
							if seenKeys[name] {
								return errors.New("duplicate_attribute_key")
							}
							seenKeys[name] = true
						}
					}
				}
				if err := walkFields(strings.TrimSuffix(child, "[]"), entry, fmt.Sprintf("%s/%d", location, i), residue); err != nil {
					return err
				}
			}
		} else if err := walkFields(child, item, location, residue); err != nil {
			return err
		}
	}
	return nil
}

func criticalKey(key string) bool {
	lower := strings.ToLower(key)
	return strings.HasSuffix(lower, "id") || strings.HasSuffix(lower, "ids") || strings.Contains(lower, "time") || strings.Contains(lower, "owner") || strings.Contains(lower, "subject") || strings.Contains(lower, "scope") || strings.Contains(lower, "permission")
}

func criticalResidue(value any) bool {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if criticalKey(key) || criticalResidue(child) {
				return true
			}
		}
	case []any:
		for _, child := range item {
			if criticalResidue(child) {
				return true
			}
		}
	}
	return false
}
