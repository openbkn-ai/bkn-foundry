package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
)

// helper to build a CallToolRequest with simple arguments
func newCallToolRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: args,
		},
	}
}

func TestGetResponseFormatFromRequest_DefaultAndValid(t *testing.T) {
	convey.Convey("GetResponseFormatFromRequest default and valid values", t, func() {
		convey.Convey("missing response_format defaults to toon", func() {
			req := newCallToolRequest(map[string]any{})
			format, err := GetResponseFormatFromRequest(req)
			convey.So(err, convey.ShouldBeNil)
			convey.So(format, convey.ShouldEqual, rest.FormatTOON)
		})

		convey.Convey("explicit json and toon", func() {
			reqJSON := newCallToolRequest(map[string]any{
				"response_format": "json",
			})
			fJSON, err := GetResponseFormatFromRequest(reqJSON)
			convey.So(err, convey.ShouldBeNil)
			convey.So(fJSON, convey.ShouldEqual, rest.FormatJSON)

			reqTOON := newCallToolRequest(map[string]any{
				"response_format": "toon",
			})
			fTOON, err := GetResponseFormatFromRequest(reqTOON)
			convey.So(err, convey.ShouldBeNil)
			convey.So(fTOON, convey.ShouldEqual, rest.FormatTOON)
		})
	})
}

func TestGetResponseFormatFromRequest_Invalid(t *testing.T) {
	convey.Convey("GetResponseFormatFromRequest invalid value returns error", t, func() {
		req := newCallToolRequest(map[string]any{
			"response_format": "xml",
		})
		_, err := GetResponseFormatFromRequest(req)
		convey.So(err, convey.ShouldNotBeNil)
	})
}

func TestBuildMCPToolResult_JSON(t *testing.T) {
	convey.Convey("BuildMCPToolResult with JSON format", t, func() {
		resp := map[string]any{
			"foo": "bar",
			"num": 42,
		}

		result, err := BuildMCPToolResult(resp, rest.FormatJSON)
		convey.So(err, convey.ShouldBeNil)
		convey.So(result, convey.ShouldNotBeNil)

		// StructuredContent should keep the original object
		convey.So(result.StructuredContent, convey.ShouldResemble, resp)

		// Fallback text should be valid JSON equivalent to the structured object.
		convey.So(result.Content, convey.ShouldHaveLength, 1)
		textContent, ok := mcp.AsTextContent(result.Content[0])
		convey.So(ok, convey.ShouldBeTrue)
		var fallback map[string]any
		err = json.Unmarshal([]byte(textContent.Text), &fallback)
		convey.So(err, convey.ShouldBeNil)
		convey.So(fallback["foo"], convey.ShouldEqual, "bar")
		convey.So(fallback["num"], convey.ShouldEqual, float64(42))

		raw, err := json.Marshal(result)
		convey.So(err, convey.ShouldBeNil)
		convey.So(string(raw), convey.ShouldContainSubstring, "structuredContent")
		convey.So(result.IsError, convey.ShouldBeFalse)
	})
}

func TestBuildMCPToolResult_TOON(t *testing.T) {
	convey.Convey("BuildMCPToolResult with TOON format", t, func() {
		resp := map[string]any{
			"hits_total": float64(1),
			"concepts": []map[string]any{
				{"concept_id": "ot_1"},
			},
		}

		result, err := BuildMCPToolResult(resp, rest.FormatTOON)
		convey.So(err, convey.ShouldBeNil)
		convey.So(result, convey.ShouldNotBeNil)
		convey.So(result.StructuredContent, convey.ShouldResemble, resp)
		convey.So(result.IsError, convey.ShouldBeFalse)

		// Marshal to JSON to ensure the result is serializable
		_, err = json.Marshal(result)
		convey.So(err, convey.ShouldBeNil)
	})
}

func TestToolResultRunesMatchesMCPResponse(t *testing.T) {
	resp := map[string]any{"description": strings.Repeat("图<&>", 100)}
	for _, format := range []rest.ResponseFormat{rest.FormatJSON, rest.FormatTOON} {
		t.Run(string(format), func(t *testing.T) {
			result, err := BuildMCPToolResult(resp, format)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Content           []mcp.TextContent `json:"content"`
				StructuredContent json.RawMessage   `json:"structuredContent"`
			}
			if err := json.Unmarshal(wire, &payload); err != nil {
				t.Fatal(err)
			}
			want := max(utf8.RuneCountInString(payload.Content[0].Text), utf8.RuneCount(payload.StructuredContent))
			got, err := toolResultRunes(resp, format)
			if err != nil || got != want {
				t.Fatalf("toolResultRunes = %d, %v; want %d", got, err, want)
			}
		})
	}
}
