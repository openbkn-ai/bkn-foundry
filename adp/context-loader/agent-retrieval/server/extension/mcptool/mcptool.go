// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package mcptool is the typed socket for the MCP tool surface. The enterprise
// code line plugs two kinds of thing in here:
//
//   - ExtraTool: a tool that only the enterprise build has. A community binary
//     has an empty registry, so the tool is absent from tools/list entirely —
//     not present-but-erroring. tools/list is the capability catalogue an LLM
//     reads, and a tool it can see is a tool it will call.
//   - Decorator: extra input properties and extra output on a tool that already
//     exists. With no decorator registered the wrapping is the identity, which
//     is the path every community binary takes.
//
// The socket sits at the capability entrance — where tools are assembled — and
// not inside the business logic. Few sockets is what makes two code lines
// affordable to keep.
//
// Registration happens during assembly, between app.Boot and app.Run. Once
// app.Run freezes the extension registry, nothing can register: tools/list is
// fixed at handler construction, so a late registration would produce a
// capability set that some callers have already read a different version of.
//
// Design: bkn-docs shared/licensing/context-loader-ee-socket.md §4.
package mcptool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension"
)

// Handler matches mcp-go's tool handler signature.
type Handler = func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)

// ExtraTool is one tool contributed by the enterprise code line. Its schemas
// come with it rather than from core's embedded schemas directory: core should
// not carry the schema of a capability whose code it cannot see.
type ExtraTool struct {
	Feature       extension.Feature
	Key           string // tool key; also the ordering key in /mcp/info
	Name, Desc    string
	Input, Output json.RawMessage
	Handle        Handler
}

// Decorator adds to a tool core already serves.
//
// PatchInput runs once during assembly and adds properties to the input schema.
// It is optional — a decorator may add output without taking new input.
//
// After runs on every call and turns core's result into the enterprise result.
// It is required, because it is the only thing that can act on what PatchInput
// advertised (see below).
//
// # What a decorator can and cannot do
//
// Core's handler does not know about the added parameter; it parses the request
// against its own schema. So After has to read the parameter off req itself,
// and — this is the part worth reading twice — a new parameter can only shape
// what the enterprise appends or rewrites. It cannot change what core did:
// the filters, the recall set and the generated SQL were all computed from the
// original parameters before After ever ran.
//
// Making a paid parameter genuinely change core's behaviour is a different
// design, and the answer is not a Before hook that rewrites req — that drags
// the socket into the business logic this package deliberately stays out of.
// It would be core's handler taking an explicit options value, with core
// defining the extension point and deciding what it means.
type Decorator struct {
	Feature    extension.Feature
	PatchInput func(json.RawMessage) json.RawMessage
	After      func(context.Context, mcp.CallToolRequest, *mcp.CallToolResult) (*mcp.CallToolResult, error)
}

var (
	mu         sync.RWMutex
	extras     = map[string]ExtraTool{}
	decorators = map[string]Decorator{}
	claimed    = map[extension.Feature]bool{}
)

// claimOnceLocked takes the feature's socket the first time this package plugs
// something in for it, and checks the license on every subsequent entry.
//
// extension.Claim models "one implementation per feature" and panics on a
// second claim. That is right for a socket holding a single implementation, but
// this one holds many entries, and one feature legitimately spans several of
// them — context_probe is precisely an extra tool plus a decorator. The
// implementation extension sees is the socket as a whole, so it is claimed
// once. Everything after that still has to be licensed, with the same failure
// message, because an unlicensed registration means ee skipped its own check.
//
// Callers hold mu.
func claimOnceLocked(f extension.Feature, impl string) {
	if claimed[f] {
		if !extension.Enabled(f) {
			panic(fmt.Sprintf("mcptool: %s registered by %s without a license for it — check the license before registering", f, impl))
		}
		return
	}
	extension.Claim(f, "mcptool")
	claimed[f] = true
}

// Register plugs in an enterprise tool. Assembly-time only; registering after
// the registry is frozen panics, and so does registering a feature the license
// does not carry — ee is expected to check its own license first.
func Register(t ExtraTool) {
	if t.Key == "" || t.Name == "" || t.Handle == nil || len(t.Input) == 0 {
		panic("mcptool: Register with incomplete ExtraTool (need Key, Name, Input, Handle)")
	}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := extras[t.Key]; dup {
		panic(fmt.Sprintf("mcptool: extra tool %q registered twice", t.Key))
	}
	claimOnceLocked(t.Feature, "mcptool:"+t.Key)
	extras[t.Key] = t
}

// Decorate attaches a decorator to an existing tool. Decorating the same tool
// twice panics: the second decorator's schema patch would silently swallow the
// first one's.
//
// After is mandatory even though the struct field is a pointer-ish func: a
// decorator that only patches the input schema advertises a parameter with
// nothing behind it, so a client that supplies it gets silence. That is an
// assembly bug, and it is invisible at runtime, which is exactly the kind that
// has to surface at startup.
func Decorate(toolKey string, d Decorator) {
	if toolKey == "" {
		panic("mcptool: Decorate with an empty tool key")
	}
	if d.After == nil {
		panic(fmt.Sprintf("mcptool: decorator for %q has no After hook — a schema patch with nothing to consume it advertises a parameter that silently does nothing", toolKey))
	}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := decorators[toolKey]; dup {
		panic(fmt.Sprintf("mcptool: tool %q decorated twice", toolKey))
	}
	claimOnceLocked(d.Feature, "mcptool:decorate:"+toolKey)
	decorators[toolKey] = d
}

// Extras returns the registered enterprise tools, ordered by Key so that
// tools/list and /mcp/info are stable across restarts.
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

// Patch layers the decorator's schema change onto core's input schema. With no
// PatchInput the schema is returned untouched — the community path is byte for
// byte what it was.
func (d Decorator) Patch(input json.RawMessage) json.RawMessage {
	if d.PatchInput == nil {
		return input
	}
	return d.PatchInput(input)
}

// Wrap puts the After hook around core's handler and re-checks the license on
// every call. Re-checking is not belt and braces: a license can expire after
// assembly, and from that moment the enterprise processing has to stop and the
// call fall back to core's own result rather than keep emitting paid content.
func (d Decorator) Wrap(h Handler) Handler {
	if d.After == nil {
		return h
	}
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := h(ctx, req)
		if err != nil || res == nil || !extension.Enabled(d.Feature) {
			return res, err
		}
		return d.After(ctx, req, res)
	}
}

// Gated is how an enterprise tool is called: when the license lapses it errors
// instead of serving. tools/list is fixed at freeze time, so a license that
// dies mid-flight shows up as "still listed, calling it fails".
//
// The two directions differ on purpose. A listed tool will be called by the
// LLM, and returning an empty success is harder to diagnose than an error.
// A decorator is additive, though — core's own result is complete and useful,
// so failing the whole tool because the paid layer went away would be wrong.
//
// The message names no feature key and no package: on a customer's machine the
// log is somebody else's log.
func Gated(t ExtraTool) Handler {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !extension.Enabled(t.Feature) {
			return mcp.NewToolResultError("tool " + t.Name + " requires an enterprise license"), nil
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
	claimed = map[extension.Feature]bool{}
}
