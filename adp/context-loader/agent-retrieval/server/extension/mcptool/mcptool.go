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
	"sort"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
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

	Key           string // tool key; also the ordering key in /mcp/info
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

var (
	mu         sync.RWMutex
	extras     = map[string]ExtraTool{}
	decorators = map[string]Decorator{}
)

// Register plugs in an enterprise tool. Assembly-time only.
func Register(t ExtraTool) {
	if t.Key == "" || t.Name == "" || t.Handle == nil || len(t.Input) == 0 {
		panic("mcptool: Register with an incomplete ExtraTool (needs Key, Name, Input, Handle)")
	}
	if t.Capability == "" {
		panic(fmt.Sprintf("mcptool: tool %q registered without a Capability — several entries share one capability and the registry records it by name", t.Key))
	}
	entitlement.MustBeAssembling("mcptool:" + t.Key)

	mu.Lock()
	defer mu.Unlock()
	if _, dup := extras[t.Key]; dup {
		panic(fmt.Sprintf("mcptool: extra tool %q registered twice", t.Key))
	}
	// MarkAssembled validates MinEdition — a missing one panics there, with a
	// message that names the capability.
	entitlement.MarkAssembled(t.Capability, t.MinEdition)
	extras[t.Key] = t
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
	entitlement.MustBeAssembling("mcptool:decorate:" + toolKey)

	mu.Lock()
	defer mu.Unlock()
	if _, dup := decorators[toolKey]; dup {
		panic(fmt.Sprintf("mcptool: tool %q decorated twice", toolKey))
	}
	entitlement.MarkAssembled(d.Capability, d.MinEdition)
	decorators[toolKey] = d
}

// Extras returns the registered enterprise tools ordered by Key, so tools/list
// and /mcp/info are stable across restarts. It reports what this binary has,
// not what it may serve — the licence decides that per call.
func Extras() []ExtraTool {
	mu.RLock()
	defer mu.RUnlock()
	keys := make([]string, 0, len(extras))
	for k := range extras {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]ExtraTool, 0, len(keys))
	for _, k := range keys {
		out = append(out, extras[k])
	}
	return out
}

// DecoratorFor returns the decorator attached to a tool, if any.
func DecoratorFor(toolKey string) (Decorator, bool) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := decorators[toolKey]
	return d, ok
}

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
	return d.PatchInput(input)
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
			return nil, fmt.Errorf("tool %q not found", t.Name)
		}
		return t.Handle(ctx, req)
	}
}

// reset empties the socket. Tests only; see testsupport.go.
func reset() {
	mu.Lock()
	defer mu.Unlock()
	extras = map[string]ExtraTool{}
	decorators = map[string]Decorator{}
}
