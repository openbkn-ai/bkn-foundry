// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
)

const (
	refusalInvalidArguments     = "invalid_arguments"
	refusalBKNContextMismatch   = "bkn_context_mismatch"
	refusalResponseFormatInArgs = "response_format_not_accepted"

	// normalizedArgumentsMetaKey carries the arguments as the executor ran
	// them, when it had to rewrite what the caller sent.
	normalizedArgumentsMetaKey = "openbkn.ai/normalized_arguments"
	// maxReportedViolations bounds the field errors one refusal lists.
	maxReportedViolations = 10
)

// executorSkipsServerGuard names the tools the server-level lifecycle guard
// passes through: the executor applies the guard to its target itself.
var executorSkipsServerGuard = map[string]struct{}{toolKeyExecuteNativeTool: {}}

// nativeExecutor runs execute_native_tool.
//
// The server applies its per-call middlewares (licence gate, lifecycle guard)
// by the name in tools/call, which here is the executor's. The executor is
// exempt from the guard at that level and applies the same middlewares itself
// under the target's name once the target is resolved and its arguments
// validated, so the target is governed exactly once and Trace records the tool
// that actually ran.
type nativeExecutor struct {
	catalog *nativeCatalog
	wrap    server.ToolHandlerMiddleware
}

func newNativeExecutor(catalog *nativeCatalog, lifecycleClient *bkntrace.LifecycleClient) *nativeExecutor {
	gate, guard := mcptool.GateMiddleware(), lifecycleToolMiddleware(lifecycleClient)
	return &nativeExecutor{
		catalog: catalog,
		// Same order as the server: the gate is the outermost.
		wrap: func(next server.ToolHandlerFunc) server.ToolHandlerFunc { return gate(guard(next)) },
	}
}

type argumentViolation struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// invalidArguments is the refusal for arguments that fail the executable
// schema. It carries the schema, so a caller who knew the target's name can
// fix the call without describing the target first.
type invalidArguments struct {
	gatewayRefusal
	Violations      []argumentViolation `json:"violations"`
	ArgumentsSchema json.RawMessage     `json:"arguments_schema"`
}

func (r *invalidArguments) Error() string {
	raw, _ := json.Marshal(r)
	return string(raw)
}

func (e *nativeExecutor) handle(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	outer, err := rawObject(req.GetRawArguments())
	if err != nil {
		return refusalResult(&gatewayRefusal{Code: refusalInvalidArguments, Message: err.Error()}), nil
	}
	var name string
	if err := json.Unmarshal(outer["name"], &name); err != nil || name == "" {
		return refusalResult(&gatewayRefusal{Code: refusalInvalidArguments, Message: "name is required and must be a string."}), nil
	}
	target, err := e.catalog.resolve(ctx, name)
	if err != nil {
		return refusalResult(err), nil
	}
	arguments, rewritten, refusal := normalizeTargetArguments(name, outer["arguments"], outer["bkn_context"])
	if refusal != nil {
		return refusalResult(refusal), nil
	}
	schema, err := executableSchema(target.input)
	if err != nil {
		return nil, fmt.Errorf("executable schema of %s: %w", name, err)
	}
	argumentNames := make([]string, 0, len(arguments))
	for argument := range arguments {
		argumentNames = append(argumentNames, argument)
	}
	if err := checkArgumentNames(name, schema, argumentNames); err != nil {
		return refusalResult(err), nil
	}
	if err := validateTargetArguments(name, schema, arguments); err != nil {
		var invalid *invalidArguments
		if errors.As(err, &invalid) {
			return refusalResult(invalid), nil
		}
		return nil, err
	}

	inner, err := innerRequest(req, name, arguments, outer["bkn_context"])
	if err != nil {
		return nil, err
	}
	result, err := e.wrap(server.ToolHandlerFunc(target.handler))(ctx, inner)
	if err == nil && result != nil && rewritten {
		if result.Meta == nil {
			result.Meta = &mcp.Meta{}
		}
		if result.Meta.AdditionalFields == nil {
			result.Meta.AdditionalFields = map[string]any{}
		}
		result.Meta.AdditionalFields[normalizedArgumentsMetaKey] = arguments
	}
	return result, err
}

// normalizeTargetArguments applies the fixed rewrites a caller may rely on:
// arguments given as a JSON string are parsed, and a bkn_context equal to the
// outer one is dropped. A different bkn_context and any response_format are
// refused, never silently ignored. rewritten reports whether the arguments
// differ from what the caller sent.
func normalizeTargetArguments(name string, raw, outerContext json.RawMessage) (map[string]json.RawMessage, bool, error) {
	rewritten := false
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, false, &gatewayRefusal{Code: refusalInvalidArguments, Name: name, Message: "arguments is not valid JSON."}
		}
		raw, rewritten = json.RawMessage(text), true
	}
	arguments, err := rawObject(raw)
	if err != nil {
		return nil, false, &gatewayRefusal{Code: refusalInvalidArguments, Name: name, Message: "arguments must be a JSON object: " + err.Error()}
	}
	if _, present := arguments["response_format"]; present {
		return nil, false, &gatewayRefusal{
			Code: refusalResponseFormatInArgs, Name: name,
			Message: "This entry does not accept response_format; remove it from arguments.",
		}
	}
	if inner, present := arguments["bkn_context"]; present {
		if !sameJSON(inner, outerContext) {
			return nil, false, &gatewayRefusal{
				Code: refusalBKNContextMismatch, Name: name,
				Message: "bkn_context belongs in the outer call, not in arguments, and the two differ.",
			}
		}
		delete(arguments, "bkn_context")
		rewritten = true
	}
	return arguments, rewritten, nil
}

// validateTargetArguments checks arguments against the executable schema, the
// same one describe_native_tool returns. Numbers are compared as written.
func validateTargetArguments(name string, schema json.RawMessage, arguments map[string]json.RawMessage) error {
	compiled, err := compileExecutableSchema(schema)
	if err != nil {
		return fmt.Errorf("compile executable schema of %s: %w", name, err)
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		return err
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	err = compiled.Validate(document)
	if err == nil {
		return nil
	}
	var validation *jsonschema.ValidationError
	if !errors.As(err, &validation) {
		return err
	}
	var violations []argumentViolation
	for _, unit := range validation.BasicOutput().Errors {
		if unit.Error == nil || len(violations) == maxReportedViolations {
			continue
		}
		violations = append(violations, argumentViolation{Path: unit.InstanceLocation, Message: unit.Error.String()})
	}
	return &invalidArguments{
		gatewayRefusal: gatewayRefusal{
			Code: refusalInvalidArguments, Name: name,
			Message: "The arguments do not match the target's arguments schema; fix the listed fields and call again.",
		},
		Violations:      violations,
		ArgumentsSchema: schema,
	}
}

// innerRequest is the call the target sees: the outer request with the
// target's name, the validated arguments byte for byte, and the outer
// bkn_context. Raw bytes matter: handlers bind numbers from them, so a wide
// integer reaches the target unrounded.
func innerRequest(outer mcp.CallToolRequest, name string, arguments map[string]json.RawMessage, bknContext json.RawMessage) (mcp.CallToolRequest, error) {
	merged := make(map[string]json.RawMessage, len(arguments)+1)
	for key, value := range arguments {
		merged[key] = value
	}
	if len(bknContext) > 0 {
		merged["bkn_context"] = bknContext
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return mcp.CallToolRequest{}, err
	}
	// Decode the way mcp-go decodes a tools/call, so helpers that read the
	// generic arguments see what they would see on a direct call.
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return mcp.CallToolRequest{}, err
	}
	inner := outer
	inner.Params.Name = name
	inner.Params.Arguments = decoded
	inner.Params.RawArguments = raw
	return inner, nil
}

func rawObject(value any) (map[string]json.RawMessage, error) {
	var raw []byte
	switch v := value.(type) {
	case nil:
		return nil, errors.New("arguments are missing")
	case json.RawMessage:
		raw = v
	case []byte:
		raw = v
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		raw = encoded
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("expected a JSON object")
	}
	return object, nil
}

func sameJSON(a, b json.RawMessage) bool {
	decode := func(raw json.RawMessage) (any, bool) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		return value, decoder.Decode(&value) == nil
	}
	left, okLeft := decode(a)
	right, okRight := decode(b)
	return okLeft && okRight && reflect.DeepEqual(left, right)
}

func refusalResult(err error) *mcp.CallToolResult {
	return mcp.NewToolResultError(err.Error())
}
