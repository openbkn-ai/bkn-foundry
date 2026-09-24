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
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const refusalUnknownArguments = "unknown_arguments"

// unknownArgumentsRefusal names the arguments a tool does not accept and the
// ones it does, so a misspelt name is fixed instead of silently ignored: a
// misspelt paging argument otherwise returns the first page again.
type unknownArgumentsRefusal struct {
	gatewayRefusal
	Unknown []string `json:"unknown"`
	Allowed []string `json:"allowed"`
}

func (r *unknownArgumentsRefusal) Error() string {
	raw, _ := json.Marshal(r)
	return string(raw)
}

// checkArgumentNames refuses argument names the schema does not declare.
func checkArgumentNames(name string, schema json.RawMessage, arguments []string) error {
	var parsed struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &parsed); err != nil || parsed.Properties == nil {
		// Nothing to check against; the handler validates its own input.
		return nil
	}
	var unknown []string
	for _, argument := range arguments {
		if _, declared := parsed.Properties[argument]; !declared {
			unknown = append(unknown, argument)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	allowed := make([]string, 0, len(parsed.Properties))
	for property := range parsed.Properties {
		allowed = append(allowed, property)
	}
	sort.Strings(unknown)
	sort.Strings(allowed)
	return &unknownArgumentsRefusal{
		gatewayRefusal: gatewayRefusal{
			Code: refusalUnknownArguments, Name: name,
			Message: fmt.Sprintf("%s does not accept %s; use only the allowed arguments.", name, strings.Join(unknown, ", ")),
		},
		Unknown: unknown,
		Allowed: allowed,
	}
}

// publishedToolResolver returns a tool as a profile publishes it at this
// moment: licence filter first, then the profile's own filter and view, the
// order in which the server applies them.
type publishedToolResolver func(ctx context.Context, name string) (mcp.Tool, bool)

// compactArgumentsMiddleware refuses a business call that names an argument
// the published definition does not declare, before the guard runs, so the
// refusal leaves no Operation behind. The lifecycle pair validates its own
// input.
func compactArgumentsMiddleware(published publishedToolResolver) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if _, lifecycle := lifecycleToolNames[req.Params.Name]; lifecycle {
				return next(ctx, req)
			}
			tool, ok := published(ctx, req.Params.Name)
			if !ok {
				return next(ctx, req)
			}
			names := make([]string, 0, len(req.GetArguments()))
			for argument := range req.GetArguments() {
				names = append(names, argument)
			}
			if err := checkArgumentNames(req.Params.Name, tool.RawInputSchema, names); err != nil {
				return refusalResult(err), nil
			}
			return next(ctx, req)
		}
	}
}
