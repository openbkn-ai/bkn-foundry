// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
)

// toolBuilder assembles the MCP tool surface: core's tools, whatever the
// enterprise code line plugged in, and the per-request decisions that keep an
// under-licensed enterprise binary indistinguishable from a community one.
//
// It exists so the licence-aware parts live in exactly one place. A hook copied
// into sixteen call sites is a hook that will be missing from the seventeenth.
type toolBuilder struct {
	locale *mcpLocaleBundle

	// pending holds what to register, in registration order.
	pending []pendingTool
	// names maps an advertised tool name to whatever claimed it, so a
	// collision fails at assembly instead of silently dropping a tool.
	names map[string]string
	// keys is the same guard for the ordering key. /mcp/info sorts by key, so
	// two entries sharing one — an ExtraTool keyed after a core tool but
	// advertised under another name — would order unpredictably between runs.
	keys map[string]string
	// patched holds the decorated variant of a core tool, advertised only
	// while the licence covers the decorator.
	patched map[string]mcp.Tool
	// gated maps an enterprise tool's advertised name to its declaration, for
	// the visibility filter.
	gated map[string]mcptool.ExtraTool
}

type pendingTool struct {
	tool    mcp.Tool
	handler mcptool.Handler
}

func newToolBuilder(locale *mcpLocaleBundle) *toolBuilder {
	return &toolBuilder{
		locale:  locale,
		names:   map[string]string{},
		keys:    map[string]string{},
		patched: map[string]mcp.Tool{},
		gated:   map[string]mcptool.ExtraTool{},
	}
}

// add registers a core tool, taking metadata and schemas from the locale
// bundle.
func (b *toolBuilder) add(key string, h mcptool.Handler) {
	name, desc := b.locale.ToolMeta(key)
	in, out := b.locale.ToolSchemas(key)
	b.addWith(key, name, desc, in, out, h)
}

// addEmbedded registers a core tool from the embedded schemas directory,
// bypassing the locale bundle.
//
// Only get_object_types and get_relation_types come through here, and that is a
// standing bug rather than a design — they miss out on localisation. Fixing it
// changes what a community binary advertises, so it is a separate change with
// its own parity baseline.
func (b *toolBuilder) addEmbedded(key string, h mcptool.Handler) {
	name, desc := loadToolMeta(key)
	in, out := loadToolSchemas(key)
	b.addWith(key, name, desc, in, out, h)
}

// addWith is where a core tool meets its decorator, if it has one.
//
// The tool is registered with core's own schema and the patched variant is kept
// aside. Which one gets advertised is decided when tools/list is answered, not
// here: registration is unconditional, so an unlicensed binary would otherwise
// publish a paid parameter in its schema and stop being byte-identical to
// community. The handler is wrapped either way — Wrap re-checks the licence on
// every call and falls back to core's own result when it does not hold.
func (b *toolBuilder) addWith(key, name, desc string, in, out json.RawMessage, h mcptool.Handler) {
	b.claimName(name, key)
	if d, ok := mcptool.DecoratorFor(key); ok {
		b.patched[name] = newToolWithSchemas(name, desc, d.Patch(in), out)
		h = d.Wrap(h)
	}
	b.pending = append(b.pending, pendingTool{newToolWithSchemas(name, desc, in, out), h})
}

// addExtras appends the enterprise-only tools. In a community binary the
// registry is empty and this is a no-op loop: the tools are absent from the
// catalogue rather than present-and-refusing.
func (b *toolBuilder) addExtras() {
	for _, t := range mcptool.Extras() {
		b.claimName(t.Name, t.Key)
		b.gated[t.Name] = t
		// An enterprise tool is a business tool by this service's own
		// definition (isBusinessTool is "not a lifecycle tool"), so the
		// lifecycle middleware will demand bkn_context from it exactly as it
		// does from core's tools. Its advertised schema has to say so, or the
		// tool is unusable by anyone following it — the schema comes from ee,
		// which has no reason to know this service has a lifecycle guard.
		b.pending = append(b.pending, pendingTool{
			newToolWithSchemas(t.Name, t.Desc, requireBKNContext(t.Input), t.Output),
			mcptool.Gated(t),
		})
	}
}

// verifyDecoratorsLanded fails assembly if some decorator was registered
// against a tool this builder never assembled.
//
// Such a decorator is inert in a way that looks like it works: /mcp/info walks
// tools_meta.json and stamps the paid parameter onto the entry, while
// tools/list only swaps schemas for tools the builder registered — the two
// descriptions of the same tool disagree, and After never runs. Before the
// lifecycle tools arrived, every advertised key came through the builder and
// this could not happen; eleven of them now bypass it.
//
// The check is a count rather than a name because the socket registry does not
// enumerate keys, and adding that API for a startup assertion would mean
// changing the shared library and bumping every consumer. The count is enough
// to stop the process; the message says where to look.
func (b *toolBuilder) verifyDecoratorsLanded() {
	if registered, landed := mcptool.DecoratorCount(), len(b.patched); registered != landed {
		panic(fmt.Sprintf(
			"mcp: %d decorator(s) registered but only %d landed on an assembled tool — a decorator on a tool this builder does not assemble (a lifecycle tool, say) would show up in /mcp/info and nowhere else",
			registered, landed))
	}
}

// claimName records who advertises a tool name and panics on a collision.
//
// This is the one way an extension could break the promise that community
// behaviour is unchanged: an ExtraTool named after a core tool would replace it
// in mcp-go's registry — later registration wins, silently — and the community
// tool would simply vanish from tools/list with nothing logged. The socket
// cannot catch it at Register time, because core's names are only resolved
// here, once the locale is known.
func (b *toolBuilder) claimName(name, key string) {
	if prev, dup := b.names[name]; dup {
		panic(fmt.Sprintf("mcp: tool name %q is claimed by both %q and %q — registering both would silently drop one", name, prev, key))
	}
	if prev, dup := b.keys[key]; dup {
		panic(fmt.Sprintf("mcp: tool key %q is claimed by both %q and %q — /mcp/info orders by key, so the two would swap places between runs", key, prev, name))
	}
	b.names[name] = key
	b.keys[key] = name
}

// claimLifecycleNames records the tracing lifecycle tools' advertised names.
//
// They are added to the server directly by registerLifecycleTools, not through
// this builder, so without this they would be invisible to the collision check
// and an ExtraTool could take one of their names — silently, because mcp-go's
// AddTool lets the later registration win.
func (b *toolBuilder) claimLifecycleNames() {
	for key := range lifecycleToolNames {
		name, _ := loadToolMeta(key)
		b.claimName(name, key)
	}
}

// filter is mcp-go's ToolFilterFunc: it decides what tools/list shows, per
// request, against the licence in force at that moment.
//
// Two jobs. Enterprise tools the licence does not cover are dropped, so the
// catalogue matches what a community binary would show. Decorated core tools
// are swapped for their patched variant only while the decorator is covered,
// so the paid parameter appears and disappears with the licence instead of
// being baked in at boot.
//
// This is also what makes a licence installed later take effect without a
// restart: the registry is frozen, the answer is not.
func (b *toolBuilder) filter(_ context.Context, tools []mcp.Tool) []mcp.Tool {
	out := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if extra, isExtra := b.gated[t.Name]; isExtra {
			if extra.Allowed() {
				out = append(out, t)
			}
			continue
		}
		if patched, hasPatch := b.patched[t.Name]; hasPatch {
			if d, ok := mcptool.DecoratorFor(b.names[t.Name]); ok && d.Allowed() {
				out = append(out, patched)
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

// attach installs the assembled tools on the server.
func (b *toolBuilder) attach(srv *server.MCPServer) {
	for _, p := range b.pending {
		srv.AddTool(p.tool, p.handler)
	}
}
