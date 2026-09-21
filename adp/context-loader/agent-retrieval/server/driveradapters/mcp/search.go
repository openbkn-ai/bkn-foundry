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

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
)

const (
	searchDefaultLimit = 3
	searchMaxLimit     = 5
	// maxNotOffered bounds how many "not on this entry" answers one search
	// returns, so they cannot crowd out the candidates.
	maxNotOffered = 2
	// maxExecutableSchemaChars bounds what describe_native_tool hands back. A
	// target over it is a catalogue defect, reported rather than truncated.
	maxExecutableSchemaChars = 8000
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

type gatewayNotOffered struct {
	Tools   []string `json:"tools"`
	Message string   `json:"message"`
}

type gatewaySearchResult struct {
	Candidates []gatewayCandidate `json:"candidates"`
	// Matched counts the targets that matched, before the limit.
	Matched   int  `json:"matched"`
	Truncated bool `json:"truncated,omitempty"`
	// NoMatch marks a result that lists every target, name and summary only,
	// because nothing matched: with a catalogue this small, letting the model
	// choose beats an empty answer.
	NoMatch    bool                `json:"no_match,omitempty"`
	NotOffered []gatewayNotOffered `json:"not_offered,omitempty"`
}

type gatewayCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type gatewayDescription struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	ArgumentsSchema json.RawMessage `json:"arguments_schema"`
	// CallTemplate is a complete execute_native_read_tool call that passes
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
	refusalNotInProfile      = "not_in_profile"
	refusalPublishedDirectly = "published_directly"
	refusalUnknownTool       = "unknown_tool"
	refusalSchemaTooLarge    = "schema_too_large"
)

func (r *gatewayRefusal) Error() string {
	raw, _ := json.Marshal(r)
	return string(raw)
}

// search ranks the targets the caller can use now against what they want to
// do. Tools the profile leaves out are matched too, and answered with their
// boundary rather than offered.
func (c *nativeCatalog) search(ctx context.Context, query string, limit int) gatewaySearchResult {
	if limit < 1 {
		limit = searchDefaultLimit
	}
	limit = min(limit, searchMaxLimit)
	query = normalizeSearchText(query)
	locale := c.builder.locale

	type scored struct {
		candidate gatewayCandidate
		score     int
		order     int
	}
	var matched, all []scored
	for order, name := range longTailTargets {
		tool, _, ok := c.lookup(ctx, name)
		if !ok {
			continue
		}
		meta := locale.ToolMeta(name)
		if meta.Gateway == nil {
			continue
		}
		schema, err := executableSchema(tool.RawInputSchema)
		if err != nil {
			continue
		}
		entry := scored{
			candidate: gatewayCandidate{
				Name:      name,
				Summary:   meta.Gateway.Summary,
				UseWhen:   meta.Gateway.UseWhen,
				NotFor:    meta.Gateway.NotFor,
				NextStep:  meta.Gateway.NextStep,
				Arguments: fieldSignature(schema),
			},
			score: matchScore(query, meta),
			order: order,
		}
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

	result := gatewaySearchResult{Matched: len(matched), NotOffered: c.notOffered(query)}
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
	return result
}

// notOffered answers for the tools the profile leaves out that the query
// matched. Tools sharing a message are answered once.
func (c *nativeCatalog) notOffered(query string) []gatewayNotOffered {
	type group struct {
		answer gatewayNotOffered
		score  int
		order  int
	}
	var groups []*group
	byMessage := map[string]*group{}
	for order, name := range notInProfileTools {
		meta := c.builder.locale.ToolMeta(name)
		if meta.Gateway == nil || meta.Gateway.Boundary == "" {
			continue
		}
		score := matchScore(query, meta)
		if score == 0 {
			continue
		}
		g, ok := byMessage[meta.Gateway.Boundary]
		if !ok {
			g = &group{answer: gatewayNotOffered{Message: meta.Gateway.Boundary}, order: order}
			byMessage[meta.Gateway.Boundary] = g
			groups = append(groups, g)
		}
		g.answer.Tools = append(g.answer.Tools, name)
		g.score = max(g.score, score)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].score != groups[j].score {
			return groups[i].score > groups[j].score
		}
		return groups[i].order < groups[j].order
	})
	var out []gatewayNotOffered
	for _, g := range groups[:min(maxNotOffered, len(groups))] {
		out = append(out, g.answer)
	}
	return out
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
	return strings.ReplaceAll(strings.ToLower(text), "的", "")
}

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
	if slices.Contains(notInProfileTools, name) {
		message := ""
		if card := c.builder.locale.ToolMeta(name).Gateway; card != nil {
			message = card.Boundary
		}
		return targetDefinition{}, &gatewayRefusal{Code: refusalNotInProfile, Name: name, Message: message}
	}
	if slices.Contains(compactProfileTools, name) {
		return targetDefinition{}, &gatewayRefusal{
			Code: refusalPublishedDirectly, Name: name,
			Message: fmt.Sprintf("%s is published on this entry; call it directly.", name),
		}
	}
	return targetDefinition{}, &gatewayRefusal{
		Code: refusalUnknownTool, Name: name,
		Message: fmt.Sprintf("No on-demand tool is named %q. Find one with search_native_tools.", name),
	}
}

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
	meta := c.builder.locale.ToolMeta(name)
	description := gatewayDescription{
		Name:            name,
		Description:     meta.Description,
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
