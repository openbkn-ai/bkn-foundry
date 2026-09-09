// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
)

// DefaultToolResultMaxChars bounds the text one tool result hands the model.
//
// Nothing below the envelope bounds a result: get_kn_detail on a large network, a
// SELECT * with no LIMIT, or a three-hop explore_subgraph each came back as
// hundreds of thousands of characters, and every one of them landed in the
// model's context whole. The bound is a last resort behind the per-tool knobs
// (limit / properties / cursor / concept_groups): a result that trips it is a
// call that should have been narrower, and the note appended to the text says
// which knob to turn. Measured in characters rather than bytes so a Chinese and
// an English result of the same length are cut at the same point.
//
// 40000 characters keeps a TOON result under the ~25K-token cap the common hosts
// apply to one MCP result, above which they drop the result entirely.
const DefaultToolResultMaxChars = 40000

// ToolResultMaxCharsEnv overrides DefaultToolResultMaxChars; 0 disables the bound.
const ToolResultMaxCharsEnv = "MCP_TOOL_RESULT_MAX_CHARS"

var toolResultMaxChars = loadToolResultMaxChars()

func loadToolResultMaxChars() int {
	raw := strings.TrimSpace(os.Getenv(ToolResultMaxCharsEnv))
	if raw == "" {
		return DefaultToolResultMaxChars
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		log.Printf("WARN: %s=%q is not a non-negative integer, using default %d", ToolResultMaxCharsEnv, raw, DefaultToolResultMaxChars)
		return DefaultToolResultMaxChars
	}
	return n
}

// truncationNoteFormat is appended to a cut result. It names the cut so the model
// does not read a partial list as the whole answer, and names the knobs so its
// next call is narrower rather than a retry of the same one.
const truncationNoteFormat = "\n\n[truncated] Showing the first %d of %d characters; the rest was cut to keep this result within the context budget. Do not repeat the same call. Narrow it: lower limit, request fewer properties/columns, add filters, or page with cursor / offset / search_after. For get_kn_detail pass concept_groups and/or detail_level=outline."

// GetResponseFormatFromRequest parses response_format from the arguments of MCP CallToolRequest. If not passed, it defaults to toon.
func GetResponseFormatFromRequest(req mcp.CallToolRequest) (rest.ResponseFormat, error) {
	s := req.GetString("response_format", "toon")
	return rest.ParseResponseFormat(s)
}

// BuildMCPToolResult uniformly constructs the MCP Tool return result based on response_format (the text is JSON or TOON, and structuredContent is still the original object)
func BuildMCPToolResult(resp interface{}, format rest.ResponseFormat) (*mcp.CallToolResult, error) {
	var textContent string
	if format == rest.FormatTOON {
		_, bodyBytes, err := rest.MarshalResponse(rest.FormatTOON, resp)
		if err != nil {
			return nil, err
		}
		textContent = string(bodyBytes)
	} else {
		textContent = utils.ObjectToJSON(resp)
	}
	return mcp.NewToolResultStructured(resp, boundToolResultText(textContent, toolResultMaxChars)), nil
}

// boundToolResultText cuts text to at most maxChars characters and appends the
// truncation note. maxChars <= 0 disables the bound.
//
// The cut backs up to the last line break so a TOON row or a JSON line is never
// split in the middle — a half row reads as a value, not as a cut. The
// structured content is left whole: hosts that validate it against the tool's
// output schema would reject a trimmed object, and they are not the ones that
// put the text into the model's context.
func boundToolResultText(text string, maxChars int) string {
	if maxChars <= 0 {
		return text
	}
	total := utf8.RuneCountInString(text)
	if total <= maxChars {
		return text
	}
	cut := len(text)
	seen := 0
	for i := range text {
		if seen == maxChars {
			cut = i
			break
		}
		seen++
	}
	kept := text[:cut]
	// Only back up to a line break when that keeps a useful share of the budget;
	// a single enormous line (minified JSON) is cut where it stands.
	if nl := strings.LastIndexByte(kept, '\n'); nl > 0 && utf8.RuneCountInString(kept[:nl]) >= maxChars/2 {
		kept = kept[:nl]
	}
	return kept + fmt.Sprintf(truncationNoteFormat, utf8.RuneCountInString(kept), total)
}
