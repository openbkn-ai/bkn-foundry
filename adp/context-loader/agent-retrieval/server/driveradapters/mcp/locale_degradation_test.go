// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

// A bad locale resource must cost the caller wording, never their request.
// /mcp/info rebuilds the tool schemas per request through tryLoadToolSchemas,
// so a panic in the overlay path would surface as a failed request rather than
// as the translation defect it is.

func TestOverlaySchemasDegradesOnUnusableInput(t *testing.T) {
	convey.Convey("an overlay over an unusable base schema should serve the baseline", t, func() {
		bundle := &mcpLocaleBundle{
			locale: "en-US",
			schemaDescriptions: map[string]map[string]string{
				"probe": {"input_schema.properties.q.description": "Query."},
			},
		}

		convey.Convey("base schema that is not valid JSON", func() {
			input := json.RawMessage(`{"properties":`)
			output := json.RawMessage(`{}`)
			gotInput, gotOutput := bundle.OverlaySchemas("probe", input, output)
			convey.So(string(gotInput), convey.ShouldEqual, string(input))
			convey.So(string(gotOutput), convey.ShouldEqual, string(output))
		})

		convey.Convey("a tool with no overlay entry is returned untouched", func() {
			input := json.RawMessage(`{"type":"object"}`)
			output := json.RawMessage(`{"type":"object"}`)
			gotInput, gotOutput := bundle.OverlaySchemas("absent", input, output)
			convey.So(string(gotInput), convey.ShouldEqual, string(input))
			convey.So(string(gotOutput), convey.ShouldEqual, string(output))
		})

		convey.Convey("a usable base schema is still localized", func() {
			input := json.RawMessage(`{"type":"object","properties":{"q":{"description":"原文"}}}`)
			output := json.RawMessage(`{"type":"object"}`)
			gotInput, _ := bundle.OverlaySchemas("probe", input, output)
			convey.So(string(gotInput), convey.ShouldContainSubstring, "Query.")
		})
	})
}

func TestPTCErrorDegradesWhenTheCatalogCannotAnswer(t *testing.T) {
	convey.Convey("a missing PTC message should not escalate the error it describes", t, func() {
		bundle := loadMCPLocaleBundle("en-US")

		convey.Convey("a known key renders its message", func() {
			convey.So(bundle.PTCError("code_required"), convey.ShouldNotBeBlank)
			convey.So(bundle.PTCError("code_required"), convey.ShouldNotEqual, "code_required")
		})

		convey.Convey("an unknown key falls back to the key instead of panicking", func() {
			convey.So(func() { bundle.PTCError("no_such_message") }, convey.ShouldNotPanic)
			convey.So(bundle.PTCError("no_such_message"), convey.ShouldEqual, "no_such_message")
		})
	})
}

func TestPTCHintsDegradeToNone(t *testing.T) {
	convey.Convey("hints are advisory and never fail a request", t, func() {
		bundle := loadMCPLocaleBundle("en-US")
		convey.So(func() { bundle.PTCHints("no_such_tool") }, convey.ShouldNotPanic)
		convey.So(bundle.PTCHints("no_such_tool"), convey.ShouldBeNil)
	})
}
