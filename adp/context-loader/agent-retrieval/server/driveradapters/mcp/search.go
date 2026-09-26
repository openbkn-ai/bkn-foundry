// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
)

const (
	searchDefaultLimit = 3
	searchMaxLimit     = 5
	// maxUncardedSummaryRunes bounds the summary of a target without a gateway
	// card, which falls back to the start of its description.
	maxUncardedSummaryRunes = 120
	// maxExecutableSchemaChars bounds what describe_native_tool hands back. A
	// target over it is a catalogue defect, reported rather than truncated.
	maxExecutableSchemaChars = 8000
	// maxReadySchemaBytes bounds the schema search carries back with an
	// unambiguous hit (see gatewaySearchResult.Ready).
	//
	// It is a safety rail, not a tuning knob. Carrying a schema costs that
	// schema once; the describe_native_tool call it replaces costs the same
	// schema plus a model turn, so carrying wins as long as the model would
	// have asked often enough — and in the #1704 evaluation two thirds of the
	// searches were followed by a describe. The rail is here for the outlier
	// whose schema dwarfs the search result it rides on, which today is
	// query_instance_subgraph and nothing else.
	//
	// Bytes rather than runes, unlike maxExecutableSchemaChars: a schema in
	// Chinese has about half the runes of the same schema in English and about
	// as many bytes, so a rune budget would hand the shortcut to one locale
	// and withhold it from the other for the same tool.
	maxReadySchemaBytes = 4000
)

// Search weights. The catalogue is a handful of tools with short copy, so
// matching is lexical and every hit is explainable: naming a tool outright
// beats everything, a keyword is the main signal (more for a longer one, see
// keywordWeight), and the title and the words of the name add a little.
const (
	weightName     = 10
	weightKeyword  = 3
	weightTitle    = 2
	weightNameWord = 1
)

type gatewayCandidate struct {
	Name     string `json:"name"`
	Summary  string `json:"summary"`
	UseWhen  string `json:"use_when,omitempty"`
	NotFor   string `json:"not_for,omitempty"`
	NextStep string `json:"next_step,omitempty"`
	// Arguments lists the top-level arguments, required ones marked with *.
	Arguments string `json:"arguments,omitempty"`
}

// gatewayReadyCall is what describe_native_tool would answer for the one
// candidate a search settled on, minus what the candidate already carries.
type gatewayReadyCall struct {
	Name            string          `json:"name"`
	ArgumentsSchema json.RawMessage `json:"arguments_schema"`
	CallTemplate    gatewayCall     `json:"call_template"`
}

type gatewaySearchResult struct {
	Candidates []gatewayCandidate `json:"candidates"`
	// Ready is the top candidate ready to run: its arguments schema and a
	// call template. It is filled only when one candidate outscored every
	// other and its schema is small enough to carry, and it saves the
	// describe_native_tool hop those searches would otherwise cost.
	Ready *gatewayReadyCall `json:"ready,omitempty"`
	// Matched counts the targets that matched, before the limit.
	Matched   int  `json:"matched"`
	Truncated bool `json:"truncated,omitempty"`
	// NoMatch marks a result that lists every target, name and summary only,
	// because nothing matched: with a catalogue this small, letting the model
	// choose beats an empty answer.
	NoMatch bool `json:"no_match,omitempty"`
}

type gatewayCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type gatewayDescription struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	ArgumentsSchema json.RawMessage `json:"arguments_schema"`
	// CallTemplate is a complete execute_native_tool call that passes
	// ArgumentsSchema.
	CallTemplate gatewayCall `json:"call_template"`
	// OutputFields names the top-level output fields, required ones marked.
	OutputFields string          `json:"output_fields,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

// gatewayRefusal is why the gateway will not describe or run a name. Unknown,
// enterprise and unauthorized targets share one code, so a caller cannot tell
// them apart.
type gatewayRefusal struct {
	Code    string `json:"error"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

const (
	refusalPublishedDirectly = "published_directly"
	refusalUnknownTool       = "unknown_tool"
	refusalSchemaTooLarge    = "schema_too_large"
)

func (r *gatewayRefusal) Error() string {
	raw, _ := json.Marshal(r)
	return string(raw)
}

// scoredTarget is one catalogue entry against one query.
type scoredTarget struct {
	candidate gatewayCandidate
	score     int
	order     int
}

// search ranks the targets the caller can use now against what they want to
// do.
func (c *nativeCatalog) search(ctx context.Context, query string, limit int) gatewaySearchResult {
	if limit < 1 {
		limit = searchDefaultLimit
	}
	limit = min(limit, searchMaxLimit)
	query = normalizeSearchText(query)

	var matched, all []scoredTarget
	for order, name := range c.order {
		tool, _, ok := c.lookup(ctx, name)
		if !ok {
			continue
		}
		schema, err := executableSchema(tool.RawInputSchema)
		if err != nil {
			continue
		}
		meta := c.targetMeta(name, tool)
		candidate := gatewayCandidate{Name: name, Arguments: fieldSignature(schema)}
		if meta.Gateway != nil {
			candidate.Summary = meta.Gateway.Summary
			candidate.UseWhen = meta.Gateway.UseWhen
			candidate.NotFor = meta.Gateway.NotFor
			candidate.NextStep = meta.Gateway.NextStep
		} else {
			candidate.Summary = firstRunes(meta.Description, maxUncardedSummaryRunes)
		}
		entry := scoredTarget{candidate: candidate, score: matchScore(query, meta), order: order}
		all = append(all, entry)
		if entry.score > 0 {
			matched = append(matched, entry)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].score != matched[j].score {
			return matched[i].score > matched[j].score
		}
		return matched[i].order < matched[j].order
	})

	result := gatewaySearchResult{Matched: len(matched)}
	if len(matched) == 0 {
		result.NoMatch = true
		for _, entry := range all {
			result.Candidates = append(result.Candidates, gatewayCandidate{Name: entry.candidate.Name, Summary: entry.candidate.Summary})
		}
		if result.Candidates == nil {
			result.Candidates = []gatewayCandidate{}
		}
		return result
	}
	result.Truncated = len(matched) > limit
	for _, entry := range matched[:min(limit, len(matched))] {
		result.Candidates = append(result.Candidates, entry.candidate)
	}
	result.Ready = c.readyCall(ctx, matched)
	return result
}

// readyCall returns the top candidate ready to run, or nil.
//
// Two conditions, and both are about not paying for a guess. The top score has
// to be strictly higher than the second: a tie means the model still has a
// choice to make, and handing it one schema would weigh that choice by which
// tool happened to sort first. And the schema has to fit maxReadySchemaBytes,
// which keeps the one outsized schema in the catalogue off every search.
//
// What it returns has to be what describe_native_tool returns for the same
// name, or a caller that trusts it will build a call the executor then refuses
// against a different schema — hence describe rather than a second rendering.
func (c *nativeCatalog) readyCall(ctx context.Context, matched []scoredTarget) *gatewayReadyCall {
	if len(matched) == 0 {
		return nil
	}
	if len(matched) > 1 && matched[0].score == matched[1].score {
		return nil
	}
	name := matched[0].candidate.Name
	described, err := c.describe(ctx, name, false)
	if err != nil {
		return nil
	}
	if len(described.ArgumentsSchema) > maxReadySchemaBytes {
		return nil
	}
	return &gatewayReadyCall{
		Name:            described.Name,
		ArgumentsSchema: described.ArgumentsSchema,
		CallTemplate:    described.CallTemplate,
	}
}

// targetMeta is a target's metadata. A core tool has it in tools_meta.json;
// an enterprise tool carries its own title and description instead.
func (c *nativeCatalog) targetMeta(name string, tool mcp.Tool) ToolMeta {
	if _, core := allToolMeta()[name]; core {
		return c.builder.locale.ToolMeta(name)
	}
	return ToolMeta{Name: name, Title: tool.Title, Description: tool.Description}
}

// targetDescription is what describe_native_tool says a target does.
//
// A tool whose description is rendered at assembly time - run_code carries the
// whole tool digest - has none in tools_meta.json, and its rendered one runs to
// thousands of characters, which is the cost this entry exists to avoid. The
// card's summary is the one-line form written for exactly this place; the
// rendered text, cut short, is the last resort.
func targetDescription(meta ToolMeta, tool mcp.Tool) string {
	if meta.Description != "" {
		return meta.Description
	}
	if meta.Gateway != nil && meta.Gateway.Summary != "" {
		return meta.Gateway.Summary
	}
	return firstRunes(tool.Description, maxUncardedSummaryRunes)
}

// firstRunes cuts text to at most n runes at a sentence end when it can.
func firstRunes(text string, n int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= n {
		return string(runes)
	}
	cut := string(runes[:n])
	// U+3002 is the ideographic full stop; source strings stay ASCII.
	for _, stop := range []string{"\u3002", ". "} {
		if i := strings.LastIndex(cut, stop); i > 0 {
			return cut[:i+len(stop)]
		}
	}
	return cut + "…"
}

// matchScore scores a normalized query against one tool's metadata.
func matchScore(query string, meta ToolMeta) int {
	score := 0
	if containsTerm(query, meta.Name) {
		score += weightName
	}
	if meta.Title != "" && containsTerm(query, normalizeSearchText(meta.Title)) {
		score += weightTitle
	}
	if meta.Gateway != nil {
		for _, keyword := range meta.Gateway.Keywords {
			if term := normalizeSearchText(keyword); containsTerm(query, term) {
				score += keywordWeight(term)
			}
		}
	}
	// Tool names are descriptive, so the words of the name break ties between
	// tools that already matched: "list recent action executions" leans to
	// list_action_executions. They never make a match on their own, or "list"
	// alone would find it.
	if score > 0 {
		for _, word := range strings.Split(meta.Name, "_") {
			if containsTerm(query, word) {
				score += weightNameWord
			}
		}
	}
	return score
}

// normalizeSearchText lower-cases text and drops the particle 的, so 执行的状态
// matches 执行状态 and 关系的定义 matches 关系定义.
func normalizeSearchText(text string) string {
	return strings.ReplaceAll(strings.ToLower(text), particleDe, "")
}

// particleDe is 的, escaped because source strings stay ASCII.
const particleDe = "\u7684"

// keywordWeight gives a longer, more specific keyword more weight than a short
// one it contains, so 字段关联 outranks 关联. A Chinese keyword counts one word
// per two characters, an English one by its words.
func keywordWeight(keyword string) int {
	words := len(strings.Fields(keyword))
	if strings.IndexFunc(keyword, func(r rune) bool { return r > unicode.MaxASCII }) >= 0 {
		words = max(1, utf8.RuneCountInString(keyword)/2)
	}
	return weightKeyword + words - 1
}

// containsTerm reports whether term occurs in text. A term with any non-ASCII
// letter is matched as a substring, since Chinese has no word boundaries. An
// ASCII term must stand as a whole word, optionally plural, so "count" does
// not match "account".
func containsTerm(text, term string) bool {
	if term == "" {
		return false
	}
	if strings.IndexFunc(term, func(r rune) bool { return r > unicode.MaxASCII }) >= 0 {
		return strings.Contains(text, term)
	}
	for start := 0; ; {
		i := strings.Index(text[start:], term)
		if i < 0 {
			return false
		}
		i += start
		end := i + len(term)
		for _, suffix := range []string{"es", "s"} {
			if strings.HasPrefix(text[end:], suffix) && !isWordByte(text, end+len(suffix)) {
				end += len(suffix)
				break
			}
		}
		if !isWordByte(text, i-1) && !isWordByte(text, end) {
			return true
		}
		start = i + 1
	}
}

func isWordByte(text string, i int) bool {
	if i < 0 || i >= len(text) {
		return false
	}
	b := text[i]
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// fieldSignature lists a schema's top-level properties: required ones first,
// in their declared order and marked with *, then the rest by name.
func fieldSignature(schema json.RawMessage) string {
	var parsed struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(schema, &parsed); err != nil || len(parsed.Properties) == 0 {
		return ""
	}
	fields := make([]string, 0, len(parsed.Properties))
	for _, name := range parsed.Required {
		if _, ok := parsed.Properties[name]; ok {
			fields = append(fields, name+"*")
		}
	}
	var optional []string
	for name := range parsed.Properties {
		if !slices.Contains(parsed.Required, name) {
			optional = append(optional, name)
		}
	}
	sort.Strings(optional)
	return strings.Join(append(fields, optional...), ", ")
}

// targetDefinition is a target's effective definition at the moment of the
// call: the schemas the licence currently allows, and the handler.
type targetDefinition struct {
	input   json.RawMessage
	output  json.RawMessage
	handler mcptool.Handler
}

// resolve decides whether the gateway may describe or run name, and returns
// the target's effective definition when it may.
func (c *nativeCatalog) resolve(ctx context.Context, name string) (targetDefinition, error) {
	if tool, handler, ok := c.lookup(ctx, name); ok {
		return targetDefinition{input: tool.RawInputSchema, output: tool.RawOutputSchema, handler: handler}, nil
	}
	if _, published := compactProfile.published[name]; published {
		return targetDefinition{}, &gatewayRefusal{
			Code: refusalPublishedDirectly, Name: name,
			Message: fmt.Sprintf("%s is published on this entry; call it directly.", name),
		}
	}
	// The commonest miss is a mounted function called by the name or id
	// search_capabilities returned. Those are not on-demand tools, and pointing
	// the caller back at search_native_tools sends it in a circle, so the refusal
	// spells out the one call that runs them. The text is the same for every
	// name: it must not tell a real function from a made-up one.
	return targetDefinition{}, &gatewayRefusal{
		Code: refusalUnknownTool, Name: name,
		Message: fmt.Sprintf("No on-demand tool is named %q. %s", name, unknownToolHint),
	}
}

// unknownToolHint is the recovery path shown with every unknown_tool refusal.
const unknownToolHint = `A function or MCP tool found by search_capabilities is not an on-demand tool: ` +
	`run it with name "execute_tool" and arguments {"kn_id": "<kn_id>", "toolbox_id": "<owner_id>", ` +
	`"tool_id": "<capability_id>", "arguments": {<its input>}}. A skill is read with name "get_skill_content". ` +
	`For anything else, find the tool with search_native_tools.`

// describe returns what a caller needs to run a target through the gateway.
func (c *nativeCatalog) describe(ctx context.Context, name string, includeOutputSchema bool) (gatewayDescription, error) {
	target, err := c.resolve(ctx, name)
	if err != nil {
		return gatewayDescription{}, err
	}
	schema, err := executableSchema(target.input)
	if err != nil {
		return gatewayDescription{}, err
	}
	if n := len([]rune(string(schema))); n > maxExecutableSchemaChars {
		return gatewayDescription{}, &gatewayRefusal{
			Code: refusalSchemaTooLarge, Name: name,
			Message: fmt.Sprintf("The arguments schema of %s is %d characters, over the %d this entry returns.", name, n, maxExecutableSchemaChars),
		}
	}
	tool, _, _ := c.lookup(ctx, name)
	meta := c.targetMeta(name, tool)
	description := gatewayDescription{
		Name:            name,
		Description:     targetDescription(meta, tool),
		ArgumentsSchema: schema,
		CallTemplate:    gatewayCall{Name: name, Arguments: json.RawMessage(`{}`)},
		OutputFields:    fieldSignature(target.output),
	}
	if meta.Gateway != nil && len(meta.Gateway.ExampleArguments) > 0 {
		description.CallTemplate.Arguments = meta.Gateway.ExampleArguments
	}
	if includeOutputSchema {
		description.OutputSchema = target.output
	}
	return description, nil
}
