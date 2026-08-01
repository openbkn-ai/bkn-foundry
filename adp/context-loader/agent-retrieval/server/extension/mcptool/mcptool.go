// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package mcptool is the socket the enterprise code line plugs MCP tools into.
// Two shapes:
//
//   - ExtraTool — a tool only the enterprise build has. A community binary has
//     an empty registry, so the tool is absent from tools/list entirely rather
//     than present-and-refusing.
//   - Decorator — extra input properties and extra output on a tool that
//     already exists. With nothing registered the wrapping is the identity,
//     which is the path every community binary takes.
//
// The socket sits at the capability entrance — where tools are assembled — and
// never inside the business logic. Few sockets is what makes two code lines
// affordable to maintain.
//
// # Registration is unconditional
//
// Nothing here consults the licence at registration time. The enterprise entry
// point registers everything it has and declares what each item costs via
// MinEdition; whether it may run is decided per call, against the licence in
// force at that moment.
//
// The alternative — ee checks its own licence and registers only what it is
// entitled to — looks tidier and is wrong: a process that boots without a
// certificate would register nothing, and the socket freezes when the server
// starts. Installing the certificate afterwards could then do nothing until
// somebody restarted the process, which on a customer's site means an outage
// window to fix a licensing mistake.
//
// # What "not entitled" looks like
//
// It looks like the tool does not exist: filtered out of tools/list, and a
// direct call answered the way an unknown tool is answered. The community
// binary genuinely does not have the tool, and an under-licensed enterprise
// binary has to be indistinguishable from it — an explicit "requires
// enterprise" would advertise the paid surface to anyone probing.
//
// Design: bkn-docs docs/shared/licensing/ee-design.md §5.
package mcptool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement/socket"
	"github.com/openbkn-ai/licverify"
)

// Handler matches mcp-go's tool handler signature.
type Handler = func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)

// ExtraTool is one tool contributed by the enterprise code line. Its schemas
// come with it rather than from core's embedded schemas directory: core should
// not carry the schema of a capability whose code it cannot see.
type ExtraTool struct {
	// Capability names the paid capability this tool belongs to. Several
	// entries commonly share one — an extra tool plus a decorator is the usual
	// shape — and the assembly registry records it once.
	Capability string
	// MinEdition is the lowest tier that may use it. Required: the zero value
	// would silently make a paid tool free.
	MinEdition licverify.Edition

	// Key identifies the tool in the socket and orders it in /mcp/info. Keep it
	// equal to Name: the published API describes that catalogue as ordered by
	// tool name (docs/api/context-loader/mcp.yaml), which only holds while the
	// two agree. Assembly rejects a Key that collides with a core tool's key
	// even when the names differ, so the failure is loud rather than a pair of
	// entries swapping places between restarts.
	Key           string
	Name, Desc    string
	Input, Output json.RawMessage
	Handle        Handler
}

// Decorator adds to a tool core already serves.
//
// PatchInput runs during assembly and adds properties to the input schema; it
// is optional. After runs on every call and turns core's result into the
// enterprise result; it is required, because it is the only thing that can act
// on what PatchInput advertised.
//
// # What a decorator can and cannot do
//
// Core's handler does not know about the added parameter — it parses the
// request against its own schema — so After has to read the parameter off the
// request itself. Which means a new parameter can only shape what the
// enterprise appends or rewrites. It cannot change what core did: the filters,
// the recall set and the generated SQL were all computed from the original
// parameters before After ran.
//
// Making a paid parameter genuinely change core's behaviour is a different
// design, and the answer is not a Before hook that rewrites the request — that
// drags the socket into the business logic this package stays out of. It would
// be core's handler taking an explicit options value, with core defining the
// extension point and what it means.
type Decorator struct {
	Capability string
	MinEdition licverify.Edition

	PatchInput func(json.RawMessage) json.RawMessage
	After      func(context.Context, mcp.CallToolRequest, *mcp.CallToolResult) (*mcp.CallToolResult, error)
}

// Both registries come from comm-go: they carry the invariants every socket
// shares — registration only during assembly, a declared capability and minimum
// tier, no duplicate keys, stable ordering. What stays here is what is specific
// to the MCP tool surface: the shapes above, and what refusing looks like below.
var (
	extras     = socket.New[ExtraTool]("mcptool")
	decorators = socket.New[Decorator]("mcptool decorator")
)

// Register plugs in an enterprise tool. Assembly-time only.
func Register(t ExtraTool) {
	if t.Key == "" || t.Name == "" || t.Handle == nil || len(t.Input) == 0 {
		panic("mcptool: Register with an incomplete ExtraTool (needs Key, Name, Input, Handle)")
	}
	if t.Capability == "" {
		panic(fmt.Sprintf("mcptool: tool %q registered without a Capability — several entries share one capability and the registry records it by name", t.Key))
	}
	extras.Add(t.Key, t.Capability, t.MinEdition, t)
}

// Decorate attaches a decorator to an existing tool. Decorating the same tool
// twice panics: the second decorator's schema patch would silently swallow the
// first one's.
//
// After is mandatory. A decorator that only patches the input schema advertises
// a parameter with nothing behind it, so a client that supplies it gets
// silence — an assembly bug that is invisible at runtime, which is exactly the
// kind that has to surface at startup.
func Decorate(toolKey string, d Decorator) {
	if toolKey == "" {
		panic("mcptool: Decorate with an empty tool key")
	}
	if d.Capability == "" {
		panic(fmt.Sprintf("mcptool: decorator for %q registered without a Capability", toolKey))
	}
	if d.After == nil {
		panic(fmt.Sprintf("mcptool: decorator for %q has no After hook — a schema patch with nothing to consume it advertises a parameter that silently does nothing", toolKey))
	}
	decorators.Add(toolKey, d.Capability, d.MinEdition, d)
}

// Extras returns the registered enterprise tools ordered by Key, so tools/list
// and /mcp/info are stable across restarts. It reports what this binary has,
// not what it may serve — the licence decides that per call.
func Extras() []ExtraTool { return extras.All() }

// DecoratorCount reports how many decorators are registered. The assembly layer
// compares it against how many actually landed on a tool it assembled — a
// decorator on a key nobody assembles is inert in a way that looks like it
// works.
func DecoratorCount() int { return decorators.Len() }

// DecoratorFor returns the decorator attached to a tool, if any.
func DecoratorFor(toolKey string) (Decorator, bool) { return decorators.Get(toolKey) }

// Allowed reports whether the licence in force covers this tool right now.
func (t ExtraTool) Allowed() bool { return entitlement.AtLeast(t.MinEdition) }

// Allowed reports whether the licence in force covers this decorator right now.
func (d Decorator) Allowed() bool { return entitlement.AtLeast(d.MinEdition) }

// Patch layers the decorator's schema change onto core's input schema. With no
// PatchInput the schema comes back untouched.
//
// Callers must keep both versions and choose per request (see Assemble in the
// MCP adapter): the patch is computed once during assembly, but which one is
// advertised depends on the licence at the moment tools/list is answered.
func (d Decorator) Patch(input json.RawMessage) json.RawMessage {
	if d.PatchInput == nil {
		return input
	}
	// Hand the hook a copy. PatchInput comes from the enterprise code line and
	// is handed core's own schema bytes; one in-place edit there would rewrite
	// what the community tool advertises, and the community parity tests run
	// with no decorator registered so nothing would catch it.
	return d.PatchInput(append(json.RawMessage(nil), input...))
}

// Wrap puts the After hook around core's handler, re-checking the licence on
// every call.
//
// Re-checking is not belt and braces: a licence can lapse after assembly, and
// from that moment the enterprise processing has to stop and the call fall back
// to core's own result rather than keep emitting paid content. The degradation
// is silent on purpose — a decorator is additive, core's result is complete and
// useful, and failing the whole tool because the paid layer went away would
// take a working community capability down with it.
func (d Decorator) Wrap(h Handler) Handler {
	if d.After == nil {
		return h
	}
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := h(ctx, req)
		if err != nil || res == nil || !d.Allowed() {
			return res, err
		}
		// mcp-go reports tool-level failures in the result's IsError flag, not
		// in err. Appending enterprise content to a failed result produces a
		// response that is both isError and full of enterprise output, and a
		// client reading the last block treats the failure as success. This
		// was found by calling the real thing, not by a unit test.
		if res.IsError {
			return res, nil
		}
		return d.After(ctx, req, res)
	}
}

// Gated is how an enterprise tool is called. Under-licensed, it answers the way
// mcp-go answers a tool it has never heard of, because that is exactly what a
// community binary does with this name.
//
// The message deliberately carries no feature key and no package: on a
// customer's machine the log is somebody else's log.
func Gated(t ExtraTool) Handler {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !t.Allowed() {
			return nil, notFound(t.Name)
		}
		return t.Handle(ctx, req)
	}
}

// notFound produces the error mcp-go itself returns for a tool it has never
// heard of — same format string, wrapping the same sentinel, so the message is
// byte-identical (server.go: `tool '%s' not found: %w`, server.ErrToolNotFound).
//
// An under-licensed enterprise tool has to be indistinguishable from one that
// does not exist, and a client matching on the message text is the likeliest
// way for the difference to leak. Wrapping the sentinel rather than hard-coding
// its text means mcp-go changing it takes us along.
//
// One difference remains and cannot be closed here: mcp-go answers an unknown
// tool with INVALID_PARAMS (-32602) from inside handleToolCall, while an error
// returned by a handler or middleware becomes INTERNAL_ERROR (-32603). Closing
// that would mean not registering under-licensed tools at all — which is the
// startup-time decision this design deliberately rejects (a certificate
// installed later has to take effect without a restart). Recorded in
// bkn-docs docs/shared/licensing/extension-points.md §6.
func notFound(name string) error {
	return fmt.Errorf("tool '%s' not found: %w", name, server.ErrToolNotFound)
}

// GateMiddleware refuses calls to enterprise tools the licence does not cover,
// before anything else in the chain runs.
//
// Gated already refuses at the handler, but the handler is the innermost layer:
// any middleware the service installs — context-loader's lifecycle guard, for
// one — runs first and answers with its own error. The caller then sees
// "conversation_required" where a community binary would have said "no such
// tool", and the paid surface announces itself.
//
// Register this before the service's own middlewares. mcp-go applies them in
// reverse (server.go: `for i := len(mw) - 1; i >= 0; i--`), so the first one
// registered ends up outermost and runs first.
func GateMiddleware() server.ToolHandlerMiddleware {
	// Index by advertised name, which is what a call carries; the registry is
	// keyed by tool key and the two need not be equal. Snapshotting here rather
	// than scanning per call is safe because assembly is frozen by the time
	// handlers are built — that is the whole point of freezing.
	byName := map[string]ExtraTool{}
	for _, t := range extras.All() {
		byName[t.Name] = t
	}

	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if t, ok := byName[req.Params.Name]; ok && !t.Allowed() {
				return nil, notFound(req.Params.Name)
			}
			return next(ctx, req)
		}
	}
}

// reset empties the socket. Tests only; see testsupport.go.
func reset() {
	extras.ResetForTest()
	decorators.ResetForTest()
}
