// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestAuditNativeValidatorAndExplicitTime(t *testing.T) {
	payload, err := os.ReadFile("../../src/drivenadapter/kafkaaccess/auditvalidator/assets/execution-factory-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	good := validate(input{Kind: "audit", Payload: payload, BrokerTime: "2026-09-24T08:31:00Z"})
	if !good.Accepted || good.EventID == "" || good.ContentHash == "" || len(good.CanonicalPayload) == 0 {
		t.Fatalf("unexpected result: %+v", good)
	}
	again := validate(input{Kind: "audit", Payload: payload, BrokerTime: "2026-09-24T08:31:00Z"})
	if good.ContentHash != again.ContentHash {
		t.Fatal("unstable hash")
	}
	for _, clock := range []string{"", "invalid", "2036-01-01T00:00:00Z"} {
		if validate(input{Kind: "audit", Payload: payload, BrokerTime: clock}).Accepted {
			t.Fatalf("accepted invalid/expired clock %q", clock)
		}
	}
}

func TestEvidenceNativeValidation(t *testing.T) {
	payload := json.RawMessage(`{"event_id":"e","event_type":"retrieval.completed","bkn.trace.schema.version":"3.0.0","payload_hash":"44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","conversation_id":"c","interaction_id":"i","producer_id":"p","producer_stream_id":"s","producer_epoch":1,"producer_sequence":1,"started_at":"2026-09-01T00:00:00Z","observed_at":"2026-09-01T00:00:00Z","emitted_at":"2026-09-01T00:00:00Z","envelope":{}}`)
	result := validate(input{Kind: "evidence", Payload: payload})
	if !result.Accepted {
		t.Fatalf("rejected: %+v", result)
	}
	bad := bytes.ReplaceAll(payload, []byte("44136fa"), []byte("0000000"))
	if validate(input{Kind: "evidence", Payload: bad}).Accepted {
		t.Fatal("accepted corrupt hash")
	}
}

func TestSpanPreservesFullSource(t *testing.T) {
	payload := json.RawMessage(`{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","parentSpanId":"","startTime":"2026-09-01T00:00:00.123456789Z","endTime":"2026-09-01T00:00:01Z","attributes":{"big":9007199254740993},"links":[],"extra":"preserve"}`)
	result := validate(input{Kind: "span", Payload: payload})
	if !result.Accepted || !bytes.Equal(result.CanonicalPayload, payload) {
		t.Fatalf("span lost full source: %+v", result)
	}
	for _, bad := range []string{strings.Replace(string(payload), "0123456789abcdef0123456789abcdef", "00000000000000000000000000000000", 1), strings.Replace(string(payload), "2026-09-01T00:00:01Z", "2026-08-01T00:00:01Z", 1), `{"resourceSpans":[]}`} {
		if validate(input{Kind: "span", Payload: json.RawMessage(bad)}).Accepted {
			t.Fatal("accepted invalid or unsupported span")
		}
	}
}

func TestNDJSONContinuesAfterRejectedRecord(t *testing.T) {
	var output bytes.Buffer
	if err := run(strings.NewReader("bad\n{\"kind\":\"unknown\",\"payload\":{}}\n"), &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatal(output.String())
	}
	for _, line := range lines {
		var result result
		if err := json.Unmarshal([]byte(line), &result); err != nil || result.Accepted || len(result.CanonicalPayload) > 0 {
			t.Fatalf("unsafe rejection: %s", line)
		}
	}
}

func TestBackendHTTPStatusRequiredAndRejectionDoesNotLeak(t *testing.T) {
	payload, err := os.ReadFile("../../src/drivenadapter/kafkaaccess/auditvalidator/assets/audit-record-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	delete(value, "http_status")
	payload, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	out := validate(input{Kind: "audit", Payload: payload, BrokerTime: "2026-09-24T08:31:00Z"})
	if out.Accepted || out.Reason != "schema_invalid" || out.EventID != "" || len(out.CanonicalPayload) != 0 {
		t.Fatalf("unsafe result: %+v", out)
	}
}

func TestNDJSONStrictWrapperAndRecursiveKeys(t *testing.T) {
	span := `{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","startTime":"2026-09-01T00:00:00Z","endTime":"2026-09-01T00:00:01Z","extra":"preserve"}`
	for _, line := range []string{
		`{"kind":"unknown","kind":"span","payload":` + span + `}`,
		`{"kind":"unknown","\u006bind":"span","payload":` + span + `}`,
		`{"kind":"span","payload":{},"payload":` + span + `}`,
		`{"kind":"span","payload":` + span + `,"broker_time":"a","broker_time":"b"}`,
		`{"kind":"span","payload":` + strings.Replace(span, `"extra":"preserve"`, `"extra":{"same":1,"same":2}`, 1) + `}`,
		`{"kind":"span","payload":` + strings.Replace(span, `"extra":"preserve"`, `"extra":[{"same":1,"same":2}]`, 1) + `}`,
		`{"kind":"span","payload":` + span + `,"unrecognized_provenance":true}`,
		`{"kind":"span","KIND":"unknown","payload":` + span + `}`,
		`{"kind":"span","payload":` + span + `} {}`,
	} {
		var output bytes.Buffer
		if err := run(strings.NewReader(line+"\n"), &output); err != nil {
			t.Fatal(err)
		}
		var out result
		if json.Unmarshal(output.Bytes(), &out) != nil || out.Accepted || out.Reason != "input_json_invalid" || len(out.CanonicalPayload) != 0 {
			t.Fatalf("ambiguous wrapper accepted: %s", output.String())
		}
	}
	var output bytes.Buffer
	if err := run(strings.NewReader(`{"kind":"span","payload":`+span+"}\n"), &output); err != nil {
		t.Fatal(err)
	}
	var out result
	_ = json.Unmarshal(output.Bytes(), &out)
	if !out.Accepted || !bytes.Contains(out.CanonicalPayload, []byte(`"extra":"preserve"`)) {
		t.Fatal("valid extra Span source lost")
	}
}
