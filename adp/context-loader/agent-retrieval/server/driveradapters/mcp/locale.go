// Copyright 2026 openbkn.ai
// Copyright The openbkn.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"strings"
	"sync"
)

const defaultMCPLocale = "zh-CN"

type mcpLocaleBundle struct {
	locale             string
	instructions       string
	ptcInstructions    string
	toolMeta           map[string]ToolMeta
	schemaDescriptions map[string]map[string]string
}

// localeBundles memoises the parsed bundles.
//
// /mcp/info builds one per request, and each build re-reads and re-decodes
// three embedded files. A bundle is immutable once constructed and the set of
// locales is closed, so caching them is free of the usual staleness risk.
var localeBundles sync.Map // normalised locale -> *mcpLocaleBundle

func loadMCPLocaleBundle(locale string) *mcpLocaleBundle {
	normalized := normalizeMCPLocale(locale)
	if cached, ok := localeBundles.Load(normalized); ok {
		return cached.(*mcpLocaleBundle)
	}
	bundle := buildMCPLocaleBundle(normalized)
	localeBundles.Store(normalized, bundle)
	return bundle
}

func buildMCPLocaleBundle(normalized string) *mcpLocaleBundle {
	return buildMCPLocaleBundleFromFS(schemasFS, normalized)
}

func buildMCPLocaleBundleFromFS(resources fs.FS, normalized string) *mcpLocaleBundle {
	bundle := &mcpLocaleBundle{
		locale:          normalized,
		instructions:    mustReadMCPInstructions(resources, defaultMCPLocale),
		ptcInstructions: mustReadMCPResource(resources, defaultMCPLocale, "ptc_instructions.txt"),
	}
	if normalized == defaultMCPLocale {
		return bundle
	}
	if content, err := readMCPResource(resources, normalized, "instructions.txt"); err != nil {
		log.Printf("WARN: cannot load MCP locale instructions for %s: %v; using baseline", normalized, err)
	} else if strings.TrimSpace(content) == "" {
		log.Printf("WARN: MCP locale instructions for %s are empty; using baseline", normalized)
	} else {
		bundle.instructions = content
	}
	if content, err := readMCPResource(resources, normalized, "ptc_instructions.txt"); err != nil {
		log.Printf("WARN: cannot load PTC MCP locale instructions for %s: %v; using baseline", normalized, err)
	} else if strings.TrimSpace(content) == "" {
		log.Printf("WARN: PTC MCP locale instructions for %s are empty; using baseline", normalized)
	} else {
		bundle.ptcInstructions = content
	}
	if content, err := readMCPResource(resources, normalized, "tools_meta.json"); err != nil {
		log.Printf("WARN: cannot load MCP locale tool metadata for %s: %v; using baseline", normalized, err)
	} else {
		if err := json.Unmarshal([]byte(content), &bundle.toolMeta); err != nil {
			log.Printf("WARN: cannot parse MCP locale tool metadata for %s: %v; using baseline", normalized, err)
			bundle.toolMeta = nil
		} else if bundle.toolMeta == nil {
			log.Printf("WARN: MCP locale tool metadata for %s is empty; using baseline", normalized)
		}
	}
	if content, err := readMCPResource(resources, normalized, "schema_descriptions.json"); err != nil {
		log.Printf("WARN: cannot load MCP locale schema descriptions for %s: %v; using baseline", normalized, err)
	} else {
		if err := json.Unmarshal([]byte(content), &bundle.schemaDescriptions); err != nil {
			log.Printf("WARN: cannot parse MCP locale schema descriptions for %s: %v; using baseline", normalized, err)
			bundle.schemaDescriptions = nil
		} else if bundle.schemaDescriptions == nil {
			log.Printf("WARN: MCP locale schema descriptions for %s are empty; using baseline", normalized)
		}
	}
	return bundle
}

func mustReadMCPInstructions(resources fs.FS, locale string) string {
	return mustReadMCPResource(resources, locale, "instructions.txt")
}

func mustReadMCPStaticResource(resource string) string {
	path := fmt.Sprintf("schemas/%s", resource)
	data, err := fs.ReadFile(schemasFS, path)
	if err != nil {
		panic("cannot load MCP static resource: " + err.Error())
	}
	content := string(data)
	if strings.TrimSpace(content) == "" {
		panic("MCP static resource must not be empty: " + path)
	}
	return content
}

func mustReadMCPResource(resources fs.FS, locale, resource string) string {
	content, err := readMCPResource(resources, locale, resource)
	if err != nil {
		panic("cannot load MCP baseline resource: " + err.Error())
	}
	instructions := strings.TrimSpace(content)
	if instructions == "" {
		panic("MCP baseline resource must not be empty")
	}
	return instructions
}

func readMCPResource(resources fs.FS, locale, resource string) (string, error) {
	path := fmt.Sprintf("schemas/locales/%s/%s", locale, resource)
	data, err := fs.ReadFile(resources, path)
	if err != nil {
		return "", err
	}
	return stripMCPResourceLicenseHeader(string(data)), nil
}

func stripMCPResourceLicenseHeader(content string) string {
	if !strings.HasPrefix(content, "/*") {
		return content
	}

	end := strings.Index(content, "*/")
	if end == -1 {
		return content
	}
	header := content[:end+2]
	if !strings.Contains(header, "Copyright") || !strings.Contains(header, "openbkn.ai") {
		return content
	}
	return strings.TrimLeft(content[end+2:], "\r\n")
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

func (b *mcpLocaleBundle) PTCServerInstructions() string {
	return b.ptcInstructions
}

func (b *mcpLocaleBundle) PTCResource(name string) string {
	base := mustReadMCPResource(schemasFS, defaultMCPLocale, name)
	if b.locale == defaultMCPLocale {
		return base
	}
	content, err := readMCPResource(schemasFS, b.locale, name)
	if err != nil || strings.TrimSpace(content) == "" {
		return base
	}
	return content
}

func (b *mcpLocaleBundle) PTCHints(toolName string) []string {
	var hints map[string][]string
	if err := json.Unmarshal([]byte(b.PTCResource("ptc_hints.json")), &hints); err != nil {
		panic("cannot parse PTC hints: " + err.Error())
	}
	return hints[toolName]
}

func (b *mcpLocaleBundle) PTCError(key string, params ...any) string {
	var messages map[string]string
	if err := json.Unmarshal([]byte(b.PTCResource("ptc_errors.json")), &messages); err != nil {
		panic("cannot parse PTC errors: " + err.Error())
	}
	message, ok := messages[key]
	if !ok {
		panic("PTC error message not found: " + key)
	}
	return fmt.Sprintf(message, params...)
}

// ToolMeta returns the tool's metadata for the bundle's locale.
//
// A locale file translates; it does not re-model. So the localized entry
// overlays only the fields it actually carries and everything else falls back
// to the base file — a translator who adds a title but no group must not drop
// the tool out of its group, and Order is a layout decision that has no reason
// to differ between languages at all.
//
// Name is deliberately not overlayable. It is the identifier a tools/call
// carries, and a locale file that renamed it would give the same tool two
// different names depending on the deployment's language — every client
// integration written against one would break on the other. Before Title
// existed a locale file could set it, which was the only way to localise how a
// tool reads; now that Title is where display names live, that door closes.
func (b *mcpLocaleBundle) ToolMeta(toolKey string) ToolMeta {
	meta := loadToolMeta(toolKey)
	if b.toolMeta == nil {
		return meta
	}
	localized, ok := b.toolMeta[toolKey]
	if !ok {
		return meta
	}
	if localized.Title != "" {
		meta.Title = localized.Title
	}
	if localized.Group != "" {
		meta.Group = localized.Group
	}
	if localized.GroupTitle != "" {
		meta.GroupTitle = localized.GroupTitle
	}
	if localized.Order != 0 {
		meta.Order = localized.Order
	}
	if localized.Description != "" {
		meta.Description = localized.Description
	}
	return meta
}

func (b *mcpLocaleBundle) ToolSchemas(toolKey string) (input, output json.RawMessage) {
	input, output = loadToolSchemas(toolKey)
	return b.OverlaySchemas(toolKey, input, output)
}

// OverlaySchemas applies the locale's description overlay to schemas the caller
// already has. Split out of ToolSchemas for /mcp/info, which loads its schemas
// its own way (nil instead of a panic when a file is missing) but has to end up
// with the same text tools/list serves.
func (b *mcpLocaleBundle) OverlaySchemas(
	toolKey string,
	input, output json.RawMessage,
) (json.RawMessage, json.RawMessage) {
	if b == nil || b.schemaDescriptions == nil {
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
