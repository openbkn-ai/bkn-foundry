// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcptool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension"
)

const probe = extension.FeatureContextProbe

func allowAll() extension.Gate {
	return extension.GateFunc(func(extension.Feature) bool { return true })
}

func denyAll() extension.Gate {
	return extension.GateFunc(func(extension.Feature) bool { return false })
}

// licensed is a gate whose answer a test can flip mid-run, standing in for a
// license that expires while the process keeps running.
type licensed struct{ on bool }

func (l *licensed) Enabled(extension.Feature) bool { return l.on }

func mustPanic(t *testing.T, what string, fn func()) string {
	t.Helper()
	var got any
	func() {
		defer func() { got = recover() }()
		fn()
	}()
	if got == nil {
		t.Fatalf("%s: expected panic, got none", what)
	}
	msg, _ := got.(string)
	return msg
}

func sampleTool(key string) ExtraTool {
	return ExtraTool{
		Feature: probe,
		Key:     key,
		Name:    key,
		Desc:    "sample",
		Input:   json.RawMessage(`{"type":"object"}`),
		Handle: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("enterprise"), nil
		},
	}
}

func sampleDecorator() Decorator {
	return Decorator{
		Feature:    probe,
		PatchInput: func(in json.RawMessage) json.RawMessage { return json.RawMessage(`{"patched":true}`) },
		After: func(_ context.Context, _ mcp.CallToolRequest, res *mcp.CallToolResult) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(textOf(res) + "+enterprise"), nil
		},
	}
}

func textOf(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		return ""
	}
	return tc.Text
}

func coreHandler(text string) Handler {
	return func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(text), nil
	}
}

func TestRegisterRejectsIncompleteTool(t *testing.T) {
	ResetForTest(allowAll())

	incomplete := sampleTool("probe")
	incomplete.Handle = nil
	mustPanic(t, "Register without a handler", func() { Register(incomplete) })
}

func TestRegisterTwicePanics(t *testing.T) {
	ResetForTest(allowAll())
	Register(sampleTool("probe"))

	msg := mustPanic(t, "second Register", func() { Register(sampleTool("probe")) })
	if !strings.Contains(msg, "registered twice") {
		t.Fatalf("panic should name the collision, got %q", msg)
	}
}

func TestDecorateTwicePanics(t *testing.T) {
	ResetForTest(allowAll())
	Decorate("search_schema", sampleDecorator())

	msg := mustPanic(t, "second Decorate", func() { Decorate("search_schema", sampleDecorator()) })
	if !strings.Contains(msg, "decorated twice") {
		t.Fatalf("panic should name the collision, got %q", msg)
	}
}

func TestDecorateWithoutAfterPanics(t *testing.T) {
	ResetForTest(allowAll())

	// A schema patch with no After advertises an input property that nothing
	// reads: a client sending it gets silence. Invisible at runtime, so it has
	// to fail at assembly.
	patchOnly := Decorator{
		Feature:    probe,
		PatchInput: func(in json.RawMessage) json.RawMessage { return in },
	}
	msg := mustPanic(t, "Decorate without After", func() { Decorate("search_schema", patchOnly) })
	if !strings.Contains(msg, "no After hook") {
		t.Fatalf("panic should explain the missing hook, got %q", msg)
	}
}

func TestOneFeatureSpansToolAndDecorator(t *testing.T) {
	ResetForTest(allowAll())

	// context_probe is exactly this shape: an extra tool plus a decorator on an
	// existing one. extension.Claim allows one implementation per feature, so
	// the socket has to claim once for itself rather than once per entry.
	Register(sampleTool("probe"))
	Decorate("search_schema", sampleDecorator())

	if got := extension.Registered(); len(got) != 1 || got[0] != string(probe) {
		t.Fatalf("Registered() = %v, want exactly [%s]", got, probe)
	}
}

func TestRegisterWithoutLicensePanics(t *testing.T) {
	ResetForTest(denyAll())

	msg := mustPanic(t, "unlicensed Register", func() { Register(sampleTool("probe")) })
	if !strings.Contains(msg, "without a license") {
		t.Fatalf("panic should say the license is missing, got %q", msg)
	}
}

func TestSecondEntryStillNeedsLicense(t *testing.T) {
	gate := &licensed{on: true}
	ResetForTest(gate)
	Register(sampleTool("probe"))

	// The feature is claimed, but a later entry must not ride in on the first
	// one's claim: ee is expected to check the license before every registration.
	gate.on = false
	msg := mustPanic(t, "unlicensed second entry", func() { Decorate("search_schema", sampleDecorator()) })
	if !strings.Contains(msg, "without a license") {
		t.Fatalf("panic should say the license is missing, got %q", msg)
	}
}

func TestExtrasAreOrderedByKey(t *testing.T) {
	ResetForTest(allowAll())
	Register(sampleTool("zeta"))
	Register(sampleTool("alpha"))

	got := Extras()
	if len(got) != 2 || got[0].Key != "alpha" || got[1].Key != "zeta" {
		t.Fatalf("Extras() should be key-ordered, got %v", []string{got[0].Key, got[1].Key})
	}
}

func TestWrapSkipsAfterWhenLicenseLapses(t *testing.T) {
	gate := &licensed{on: true}
	ResetForTest(gate)
	Decorate("search_schema", sampleDecorator())

	d, ok := DecoratorFor("search_schema")
	if !ok {
		t.Fatal("decorator should be registered")
	}
	h := d.Wrap(coreHandler("core"))

	res, err := h(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if got := textOf(res); got != "core+enterprise" {
		t.Fatalf("licensed call = %q, want the enterprise-processed result", got)
	}

	// The license dies under a running process: enterprise processing stops and
	// the call falls back to core's own, complete result. No restart involved.
	gate.on = false
	res, err = h(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if got := textOf(res); got != "core" {
		t.Fatalf("unlicensed call = %q, want core's untouched result", got)
	}
}

func TestPatchIsIdentityWithoutPatchInput(t *testing.T) {
	in := json.RawMessage(`{"type":"object","properties":{}}`)
	var d Decorator
	if got := string(d.Patch(in)); got != string(in) {
		t.Fatalf("Patch without PatchInput = %q, want the schema untouched", got)
	}
}

func TestGatedToolErrorsWhenLicenseLapses(t *testing.T) {
	gate := &licensed{on: true}
	ResetForTest(gate)
	tool := sampleTool("probe")
	Register(tool)
	h := Gated(tool)

	res, err := h(context.Background(), mcp.CallToolRequest{})
	if err != nil || textOf(res) != "enterprise" {
		t.Fatalf("licensed call = %q, err %v; want the enterprise result", textOf(res), err)
	}

	// tools/list was fixed at freeze time, so the tool is still advertised.
	// Calling it has to fail loudly rather than return an empty success.
	gate.on = false
	res, err = h(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("Gated should report the refusal in the result, not as a transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("an unlicensed enterprise tool call must return an error result")
	}
	if msg := textOf(res); strings.Contains(msg, string(probe)) {
		t.Fatalf("error message must not leak the feature key: %q", msg)
	}
}
