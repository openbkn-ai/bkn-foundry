package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const fixture = `{"resourceSpans":[{"schemaUrl":"resource-schema","resource":{"attributes":[{"key":"service.name","value":{"stringValue":"synthetic"}},{"key":"numeric","value":{"intValue":"9007199254740993"}}],"droppedAttributesCount":2},"scopeSpans":[{"schemaUrl":"scope-schema","scope":{"name":"scope-a","version":"1","attributes":[{"key":"scope-key","value":{"boolValue":true}}],"droppedAttributesCount":3},"spans":[{"traceId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","spanId":"bbbbbbbbbbbbbbbb","name":"test","kind":3,"startTimeUnixNano":"1788220800123456789","endTimeUnixNano":"1788220800123456790","traceState":"vendor=value","flags":257,"status":{"code":1,"message":"ok"},"attributes":[{"key":"large","value":{"intValue":"9007199254740993"}},{"key":"bytes","value":{"bytesValue":"AQID"}},{"key":"nested","value":{"kvlistValue":{"values":[{"key":"items","value":{"arrayValue":{"values":[{"boolValue":true},{"doubleValue":1.5},{"stringValue":"x"}]}}}]}}}],"droppedAttributesCount":4,"droppedEventsCount":5,"droppedLinksCount":6,"events":[{"name":"absent","timeUnixNano":"0","droppedAttributesCount":7},{"name":"epoch-subsecond","timeUnixNano":"123456789"}],"links":[{"traceId":"cccccccccccccccccccccccccccccccc","spanId":"dddddddddddddddd","traceState":"link=value","flags":1,"attributes":[{"key":"relation","value":{"stringValue":"linked"}}],"droppedAttributesCount":8}]}]},{"scope":{"name":"scope-b"},"spans":[{"traceId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","spanId":"eeeeeeeeeeeeeeee","name":"second","startTimeUnixNano":"1788220800123456789","endTimeUnixNano":"1788220800123456790"}]}]},{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"another"}}]},"scopeSpans":[{"spans":[{"traceId":"ffffffffffffffffffffffffffffffff","spanId":"1111111111111111","name":"third","startTimeUnixNano":"1788220800123456789","endTimeUnixNano":"1788220800123456790"}]}]}]}`

func TestActualExporterPreservesCompleteSpanAndTypedSource(t *testing.T) {
	r := convert(Request{Document: json.RawMessage(fixture), SourceDeployment: "synthetic"})
	if !r.Accepted || len(r.Spans) != 3 {
		t.Fatalf("conversion: accepted=%v reason=%s spans=%d", r.Accepted, r.Reason, len(r.Spans))
	}
	p := r.Spans[0].Payload
	if p["spanId"] != "bbbbbbbbbbbbbbbb" {
		t.Fatal("unstable ordering")
	}
	if p["startTime"] != "2026-09-01T00:00:00.123456789Z" {
		t.Fatalf("lost nanos: %v", p["startTime"])
	}
	attrs := p["attributes"].(map[string]any)
	if attrs["large"] != json.Number("9007199254740993") || attrs["bytes"] != "AQID" {
		t.Fatalf("lost typed attributes: %v", attrs)
	}
	resource := p["resource"].(map[string]any)
	if resource["numeric"] != "9007199254740993" {
		t.Fatal("resource exporter semantics changed")
	}
	if p["droppedEventsCount"] != json.Number("5") || len(p["links"].([]any)) != 1 {
		t.Fatal("lost full fields")
	}
	scope := p["instrumentationScope"].(map[string]any)
	if scope["schemaUrl"] != "scope-schema" || scope["droppedAttributesCount"] != json.Number("3") {
		t.Fatal("lost scope")
	}
	events := p["events"].([]any)
	if _, ok := events[0].(map[string]any)["observedTimestamp"]; ok {
		t.Fatal("fabricated event time")
	}
	if events[1].(map[string]any)["@timestamp"] != "1970-01-01T00:00:00.123456789Z" {
		t.Fatal("lost subsecond epoch time")
	}
	if !strings.Contains(string(r.Spans[0].Sidecar.SourceDocument), `"flags":257`) {
		t.Fatal("source flags discarded")
	}
	if r.Spans[0].Sidecar.SourcePosition != "/resourceSpans/0/scopeSpans/0/spans/0" {
		t.Fatal("source position missing")
	}
}

func TestRepeatedEncodingRemovesExporterWallClock(t *testing.T) {
	req := Request{Document: json.RawMessage(fixture), SourceDeployment: "synthetic"}
	a, _ := json.Marshal(convert(req))
	time.Sleep(10 * time.Millisecond)
	b, _ := json.Marshal(convert(req))
	if string(a) != string(b) {
		t.Fatal("conversion depends on exporter clock")
	}
}

func TestUnknownResiduePreservedButUnknownIdentityRejected(t *testing.T) {
	ordinary := strings.Replace(fixture, `"name":"test"`, `"name":"test","vendorNote":{"anything":1}`, 1)
	r := convert(Request{Document: json.RawMessage(ordinary), SourceDeployment: "synthetic"})
	if !r.Accepted || len(r.Spans[0].Sidecar.UnknownFields) == 0 {
		t.Fatalf("residue missing: %s", r.Reason)
	}
	critical := strings.Replace(fixture, `"name":"test"`, `"name":"test","alternateTraceId":"x"`, 1)
	r = convert(Request{Document: json.RawMessage(critical), SourceDeployment: "synthetic"})
	if r.Accepted || r.Reason != "unknown_critical_field" {
		t.Fatalf("unsafe unknown field: %s", r.Reason)
	}
}

func TestInvalidAndDuplicateIdentityRejected(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(fixture, "bbbbbbbbbbbbbbbb", "0000000000000000", 1),
		strings.Replace(fixture, "1788220800123456790", "1788220800123456788", 1),
		`{"resourceSpans":[],"resourceSpans":[]}`,
	} {
		r := convert(Request{Document: json.RawMessage(raw), SourceDeployment: "synthetic"})
		if r.Accepted {
			t.Fatal("invalid source accepted")
		}
	}
}

func TestUnknownNestedCriticalFieldsAndAmbiguousAttributesRejected(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(fixture, `"name":"test"`, `"name":"test","extension":{"captureTimeUnixNano":"1"}`, 1),
		strings.Replace(fixture, `"intValue":"9007199254740993"`, `"intValue":"9007199254740993","stringValue":"conflicting"`, 1),
		strings.Replace(fixture, `{"key":"large","value":{"intValue":"9007199254740993"}}`, `{"key":"large","value":{"intValue":"9007199254740993"}},{"key":"large","value":{"stringValue":"conflict"}}`, 1),
	} {
		r := convert(Request{Document: json.RawMessage(raw), SourceDeployment: "synthetic"})
		if r.Accepted {
			t.Error("ambiguous source accepted")
		}
	}
}

func TestNDJSONProtocolDoesNotEmitDiagnosticsOrLoseRequestBoundaries(t *testing.T) {
	valid, _ := json.Marshal(Request{Document: json.RawMessage(fixture), SourceDeployment: "synthetic"})
	var output bytes.Buffer
	if err := run(strings.NewReader("{\n"+string(valid)+"\n"), &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatal("expected one response per request")
	}
	var bad, good Response
	if json.Unmarshal([]byte(lines[0]), &bad) != nil || bad.Accepted || bad.Reason != "invalid_request_json" {
		t.Fatal("malformed request not rejected")
	}
	if json.Unmarshal([]byte(lines[1]), &good) != nil || !good.Accepted || len(good.Spans) != 3 {
		t.Fatal("following request lost")
	}
}

func TestNDJSONRejectsAmbiguousRequestMetadata(t *testing.T) {
	for _, raw := range []string{
		`{"source_deployment":"one","source_deployment":"two","document":{}}`,
		`{"source_deployment":"one","document":{},"alternate_source_id":"two"}`,
	} {
		var output bytes.Buffer
		if err := run(strings.NewReader(raw+"\n"), &output); err != nil {
			t.Fatal(err)
		}
		var r Response
		if json.Unmarshal(output.Bytes(), &r) != nil || r.Reason != "invalid_request_json" {
			t.Fatal("ambiguous metadata accepted")
		}
	}
}

func TestSpanEndpointsMustBeExplicitIntegerStrings(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(fixture, `"startTimeUnixNano":"1788220800123456789",`, "", 1),
		strings.Replace(fixture, `"endTimeUnixNano":"1788220800123456790",`, "", 1),
		strings.Replace(fixture, `"startTimeUnixNano":"1788220800123456789"`, `"startTimeUnixNano":1788220800123456789`, 1),
		strings.Replace(fixture, `"startTimeUnixNano":"1788220800123456789"`, `"startTimeUnixNano":"1.0"`, 1),
	} {
		response := convert(Request{Document: json.RawMessage(raw), SourceDeployment: "synthetic"})
		if response.Accepted {
			t.Error("missing or noncanonical historical endpoint accepted")
		}
	}
	zero := strings.ReplaceAll(strings.ReplaceAll(fixture, `"1788220800123456789"`, `"0"`), `"1788220800123456790"`, `"0"`)
	response := convert(Request{Document: json.RawMessage(zero), SourceDeployment: "synthetic"})
	if !response.Accepted || response.Spans[0].Payload["startTime"] != "1970-01-01T00:00:00Z" {
		t.Fatal("explicit epoch zero confused with missing timestamp")
	}
}
