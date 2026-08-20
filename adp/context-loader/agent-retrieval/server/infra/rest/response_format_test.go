// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/toon-format/toon-go"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestParseResponseFormat(t *testing.T) {
	convey.Convey("ParseResponseFormat", t, func() {
		convey.Convey("empty or json defaults to JSON", func() {
			f, err := ParseResponseFormat("")
			convey.So(err, convey.ShouldBeNil)
			convey.So(f, convey.ShouldEqual, FormatJSON)

			f, err = ParseResponseFormat("json")
			convey.So(err, convey.ShouldBeNil)
			convey.So(f, convey.ShouldEqual, FormatJSON)
		})
		convey.Convey("toon returns TOON", func() {
			f, err := ParseResponseFormat("toon")
			convey.So(err, convey.ShouldBeNil)
			convey.So(f, convey.ShouldEqual, FormatTOON)
		})
		convey.Convey("invalid value returns error", func() {
			_, err := ParseResponseFormat("xml")
			convey.So(err, convey.ShouldNotBeNil)
			convey.So(err.Error(), convey.ShouldContainSubstring, "invalid response_format")
		})
	})
}

func TestMarshalResponse_JSON(t *testing.T) {
	convey.Convey("MarshalResponse FormatJSON", t, func() {
		body := map[string]any{"a": 1, "b": "x"}
		ct, b, err := MarshalResponse(FormatJSON, body)
		convey.So(err, convey.ShouldBeNil)
		convey.So(ct, convey.ShouldEqual, ContentTypeJSON)
		convey.So(string(b), convey.ShouldContainSubstring, `"a":1`)
		convey.So(string(b), convey.ShouldContainSubstring, `"b":"x"`)
	})
}

func TestMarshalResponse_TOON_RoundTrip(t *testing.T) {
	convey.Convey("MarshalResponse FormatTOON round-trip", t, func() {
		// Consistent with PRD example: array of homogeneous objects.
		body := map[string]any{
			"concepts": []map[string]any{
				{"concept_type": "object_type", "concept_id": "ot_1", "concept_name": "公司", "intent_score": 0.95},
				{"concept_type": "object_type", "concept_id": "ot_2", "concept_name": "产品", "intent_score": 0.88},
			},
			"hits_total": float64(2),
		}
		ct, toonBytes, err := MarshalResponse(FormatTOON, body)
		convey.So(err, convey.ShouldBeNil)
		convey.So(ct, convey.ShouldEqual, ContentTypeTOON)
		convey.So(len(toonBytes), convey.ShouldBeGreaterThan, 0)

		// TOON -> Unmarshal -> map
		var decoded map[string]any
		err = toon.Unmarshal(toonBytes, &decoded)
		convey.So(err, convey.ShouldBeNil)

		// Then Marshal to JSON with the same semantics as the original structure (compare key fields)
		jsonBytes, _ := json.Marshal(decoded)
		var decodedFromJSON map[string]any
		err = json.Unmarshal(jsonBytes, &decodedFromJSON)
		convey.So(err, convey.ShouldBeNil)
		convey.So(decodedFromJSON["hits_total"], convey.ShouldEqual, float64(2))
		concepts, ok := decodedFromJSON["concepts"].([]any)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(len(concepts), convey.ShouldEqual, 2)
		c0, _ := concepts[0].(map[string]any)
		convey.So(c0["concept_name"], convey.ShouldEqual, "公司")
		convey.So(c0["concept_id"], convey.ShouldEqual, "ot_1")
	})
}

func TestMarshalResponse_TOON_Struct(t *testing.T) {
	type Concept struct {
		ConceptType string  `json:"concept_type"`
		ConceptID   string  `json:"concept_id"`
		Score       float64 `json:"intent_score"`
	}
	type Resp struct {
		Concepts  []Concept `json:"concepts"`
		HitsTotal int       `json:"hits_total"`
	}
	convey.Convey("MarshalResponse FormatTOON with struct (json tag only, no toon tag)", t, func() {
		body := &Resp{
			Concepts: []Concept{
				{ConceptType: "object_type", ConceptID: "ot_1", Score: 0.95},
				{ConceptType: "object_type", ConceptID: "ot_2", Score: 0.88},
			},
			HitsTotal: 2,
		}
		ct, toonBytes, err := MarshalResponse(FormatTOON, body)
		convey.So(err, convey.ShouldBeNil)
		convey.So(ct, convey.ShouldEqual, ContentTypeTOON)

		// TOON round-trip: field names should use json tag names, not Go field names
		var decoded map[string]any
		err = toon.Unmarshal(toonBytes, &decoded)
		convey.So(err, convey.ShouldBeNil)
		convey.So(decoded["hits_total"], convey.ShouldEqual, float64(2))
		concepts, ok := decoded["concepts"].([]any)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(len(concepts), convey.ShouldEqual, 2)
		c0, _ := concepts[0].(map[string]any)
		convey.So(c0["concept_type"], convey.ShouldEqual, "object_type")
		convey.So(c0["concept_id"], convey.ShouldEqual, "ot_1")
		// should NOT have Go field names
		convey.So(c0["ConceptType"], convey.ShouldBeNil)
	})
}

func TestMarshalResponse_TOONStructPreservesLargeJSONNumber(t *testing.T) {
	convey.Convey("MarshalResponse FormatTOON preserves a large json.Number in a struct", t, func() {
		body := &interfaces.VegaRawQueryResp{
			Columns: []interfaces.VegaColumn{{Name: "id_card"}},
			Entries: []map[string]any{{
				"id_card": json.Number("110101199001152345"),
			}},
		}

		ct, toonBytes, err := MarshalResponse(FormatTOON, body)
		convey.So(err, convey.ShouldBeNil)
		convey.So(ct, convey.ShouldEqual, ContentTypeTOON)
		convey.So(string(toonBytes), convey.ShouldContainSubstring, `"110101199001152345"`)
		convey.So(body.Entries[0]["id_card"], convey.ShouldEqual, json.Number("110101199001152345"))

		var decoded map[string]any
		err = toon.Unmarshal(toonBytes, &decoded)
		convey.So(err, convey.ShouldBeNil)
		convey.So(decoded["columns"], convey.ShouldNotBeNil)
		convey.So(decoded["Columns"], convey.ShouldBeNil)
		convey.So(string(toonBytes), convey.ShouldNotContainSubstring, "total_count")
		convey.So(string(toonBytes), convey.ShouldNotContainSubstring, "warnings")
		convey.So(string(toonBytes), convey.ShouldNotContainSubstring, "paging")
	})
}

func TestMarshalResponse_TOON_MapSkipsJSONRoundTrip(t *testing.T) {
	convey.Convey("MarshalResponse FormatTOON with map skips JSON roundtrip", t, func() {
		body := map[string]any{"key": "value", "num": float64(42)}
		ct, toonBytes, err := MarshalResponse(FormatTOON, body)
		convey.So(err, convey.ShouldBeNil)
		convey.So(ct, convey.ShouldEqual, ContentTypeTOON)

		var decoded map[string]any
		err = toon.Unmarshal(toonBytes, &decoded)
		convey.So(err, convey.ShouldBeNil)
		convey.So(decoded["key"], convey.ShouldEqual, "value")
		convey.So(decoded["num"], convey.ShouldEqual, float64(42))
	})
}

func TestMarshalResponse_NilBody(t *testing.T) {
	convey.Convey("MarshalResponse nil body", t, func() {
		ct, b, err := MarshalResponse(FormatJSON, nil)
		convey.So(err, convey.ShouldBeNil)
		convey.So(ct, convey.ShouldEqual, ContentTypeJSON)
		convey.So(b, convey.ShouldBeNil)

		ct, b, err = MarshalResponse(FormatTOON, nil)
		convey.So(err, convey.ShouldBeNil)
		convey.So(ct, convey.ShouldEqual, ContentTypeJSON) // When nil, response_format does not change the contentType logic.
		convey.So(b, convey.ShouldBeNil)
	})
}

// toon-go normalizes json.Number through float64
// (internal/codec.normalizeNumberString), so the formatter converts wide
// integers to decimal strings first — the same contract VegaRawQueryResp's
// TOONValue already followed. Without it, MCP responses (which default to TOON)
// rendered 9223372036854775808 as 9223372036854776000. See
// openbkn-ai/bkn-studio#464.
func TestMarshalTOONKeepsWideIntegersFromStructResponses(t *testing.T) {
	type response struct {
		Datas []any `json:"datas"`
	}
	body := &response{Datas: []any{map[string]any{
		"id":    json.Number("9223372036854775808"),
		"safe":  json.Number("42"),
		"ratio": json.Number("1.5"),
		"name":  "S003",
	}}}

	_, out, err := MarshalResponse(FormatTOON, body)
	if err != nil {
		t.Fatalf("MarshalResponse() error = %v", err)
	}
	rendered := string(out)
	if !strings.Contains(rendered, "9223372036854775808") {
		t.Errorf("TOON output %q lost the wide integer", rendered)
	}
	if strings.Contains(rendered, "9223372036854776000") {
		t.Errorf("TOON output %q kept the rounded value", rendered)
	}
	// Values inside the safe range must stay bare numbers, not become strings.
	if !strings.Contains(rendered, "42") || strings.Contains(rendered, `"42"`) {
		t.Errorf("TOON output %q quoted a safe integer", rendered)
	}
	if !strings.Contains(rendered, "1.5") {
		t.Errorf("TOON output %q lost the float", rendered)
	}
}

func TestMarshalTOONKeepsWideIntegersFromPlainMaps(t *testing.T) {
	_, out, err := MarshalResponse(FormatTOON, map[string]any{
		"id":   json.Number("18446744073709551615"),
		"safe": json.Number("7"),
	})
	if err != nil {
		t.Fatalf("MarshalResponse() error = %v", err)
	}
	if !strings.Contains(string(out), "18446744073709551615") {
		t.Errorf("TOON output %q lost the wide integer", out)
	}
}

func TestMarshalJSONKeepsWideIntegers(t *testing.T) {
	_, out, err := MarshalResponse(FormatJSON, map[string]any{"id": json.Number("9223372036854775808")})
	if err != nil {
		t.Fatalf("MarshalResponse() error = %v", err)
	}
	if string(out) != `{"id":9223372036854775808}` {
		t.Errorf("JSON output = %s", out)
	}
}
