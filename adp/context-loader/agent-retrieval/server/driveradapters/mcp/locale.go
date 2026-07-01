// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const defaultMCPLocale = "zh-CN"

type mcpLocaleBundle struct {
	locale             string
	instructions       string
	toolMeta           map[string]ToolMeta
	schemaDescriptions map[string]map[string]string
}

func loadMCPLocaleBundle(locale string) *mcpLocaleBundle {
	normalized := normalizeMCPLocale(locale)
	bundle := &mcpLocaleBundle{
		locale:       normalized,
		instructions: serverInstructions,
	}
	if normalized == defaultMCPLocale {
		return bundle
	}
	base := fmt.Sprintf("schemas/locales/%s", normalized)
	if data, err := schemasFS.ReadFile(base + "/instructions.txt"); err == nil {
		bundle.instructions = string(data)
	}
	if data, err := schemasFS.ReadFile(base + "/tools_meta.json"); err == nil {
		if err := json.Unmarshal(data, &bundle.toolMeta); err != nil {
			panic("invalid localized tools_meta.json: " + err.Error())
		}
	}
	if data, err := schemasFS.ReadFile(base + "/schema_descriptions.json"); err == nil {
		if err := json.Unmarshal(data, &bundle.schemaDescriptions); err != nil {
			panic("invalid schema_descriptions.json: " + err.Error())
		}
	}
	return bundle
}

func mcpLocaleFromEnv() string {
	for _, key := range []string{"MCP_LOCALE", "X_LOCALE", "LANGUAGE", "LC_ALL", "LANG"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return defaultMCPLocale
}

func normalizeMCPLocale(locale string) string {
	locale = strings.TrimSpace(locale)
	if locale == "" {
		return defaultMCPLocale
	}
	locale = strings.Split(locale, ".")[0]
	locale = strings.Split(locale, ":")[0]
	locale = strings.ReplaceAll(locale, "_", "-")
	switch strings.ToLower(locale) {
	case "en", "en-us":
		return "en-US"
	case "zh", "zh-cn", "zh-hans":
		return defaultMCPLocale
	default:
		return defaultMCPLocale
	}
}

func (b *mcpLocaleBundle) ServerInstructions() string {
	return b.instructions
}

func (b *mcpLocaleBundle) ToolMeta(toolKey string) (name, description string) {
	if b.toolMeta != nil {
		if meta, ok := b.toolMeta[toolKey]; ok {
			return meta.Name, meta.Description
		}
	}
	return loadToolMeta(toolKey)
}

func (b *mcpLocaleBundle) ToolSchemas(toolKey string) (input, output json.RawMessage) {
	input, output = loadToolSchemas(toolKey)
	if b.schemaDescriptions == nil {
		return input, output
	}
	replacements := b.schemaDescriptions[toolKey]
	if len(replacements) == 0 {
		return input, output
	}

	var schema any
	if err := json.Unmarshal(mustMarshalToolSchema(input, output), &schema); err != nil {
		panic("invalid base schema for localized overlay: " + err.Error())
	}
	root, ok := schema.(map[string]any)
	if !ok {
		panic("invalid base schema for localized overlay: root is not object")
	}
	for path, value := range replacements {
		setNestedString(root, strings.Split(path, "."), value)
	}
	wrapper := toolSchemaFile{}
	if rawInput, err := json.Marshal(root["input_schema"]); err == nil {
		wrapper.InputSchema = rawInput
	} else {
		panic("cannot marshal localized input schema: " + err.Error())
	}
	if rawOutput, err := json.Marshal(root["output_schema"]); err == nil {
		wrapper.OutputSchema = rawOutput
	} else {
		panic("cannot marshal localized output schema: " + err.Error())
	}
	return wrapper.InputSchema, wrapper.OutputSchema
}

func mustMarshalToolSchema(input, output json.RawMessage) []byte {
	data, err := json.Marshal(toolSchemaFile{
		InputSchema:  input,
		OutputSchema: output,
	})
	if err != nil {
		panic("cannot marshal tool schema wrapper: " + err.Error())
	}
	return data
}

func setNestedString(root map[string]any, path []string, value string) {
	var current any = root
	for i, segment := range path {
		obj, ok := current.(map[string]any)
		if !ok {
			return
		}
		if i == len(path)-1 {
			obj[segment] = value
			return
		}
		current = obj[segment]
	}
}
