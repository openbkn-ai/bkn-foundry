// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBusinessToolSchemasRequireExplicitBKNContext(t *testing.T) {
	rawMeta, err := schemasFS.ReadFile("schemas/tools_meta.json")
	if err != nil {
		t.Fatalf("read registered tool metadata: %v", err)
	}
	var registeredTools map[string]ToolMeta
	if err := json.Unmarshal(rawMeta, &registeredTools); err != nil {
		t.Fatalf("decode registered tool metadata: %v", err)
	}
	for toolKey := range registeredTools {
		if _, lifecycle := lifecycleToolNames[toolKey]; lifecycle {
			continue
		}
		t.Run(toolKey, func(t *testing.T) {
			input, _ := loadToolSchemas(toolKey)
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			}
			if err := json.Unmarshal(input, &schema); err != nil {
				t.Fatalf("decode %s input schema: %v", toolKey, err)
			}
			if !containsString(schema.Required, "bkn_context") {
				t.Fatalf("%s must require bkn_context", toolKey)
			}

			var contextSchema struct {
				Type       string                     `json:"type"`
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			}
			if err := json.Unmarshal(schema.Properties["bkn_context"], &contextSchema); err != nil {
				t.Fatalf("decode %s bkn_context schema: %v", toolKey, err)
			}
			for _, field := range []string{"conversation_id", "interaction_id", "operation_key"} {
				if _, ok := contextSchema.Properties[field]; !ok || !containsString(contextSchema.Required, field) {
					t.Fatalf("%s bkn_context must require %s", toolKey, field)
				}
			}
			for _, field := range []string{"parent_operation_id", "causation_event_ids"} {
				if _, ok := contextSchema.Properties[field]; !ok {
					t.Fatalf("%s bkn_context must declare optional %s", toolKey, field)
				}
			}
		})
	}
}

func TestModuleOpenAPIRequiresManagedBKNContext(t *testing.T) {
	documents := map[string]string{
		"api_public/kn.yaml":                           "SemanticSearchRequest",
		"api_private/kn_schema_search.yaml":            "SemanticSearchRequest",
		"api_private/kn_search.yaml":                   "KnSearchCompatRequest",
		"api_private/search_schema.yaml":               "SearchSchemaRequest",
		"api_private/find_skills.yaml":                 "FindSkillsRequest",
		"api_private/get_action_info.yaml":             "ActionRecallRequest",
		"api_private/get_logic_properties_values.yaml": "ResolveLogicPropertiesRequest",
		"api_private/query_object_instance.yaml":       "FirstQueryWithSearchAfter",
		"api_private/query_instance_subgraph.yaml":     "SubGraphQueryBaseOnTypePath",
	}
	for relativePath, requestSchema := range documents {
		t.Run(relativePath, func(t *testing.T) {
			path := filepath.Join("../../../docs/apis", relativePath)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read module OpenAPI: %v", err)
			}
			var document struct {
				Components struct {
					Schemas map[string]struct {
						Required   []string                  `yaml:"required"`
						Properties map[string]map[string]any `yaml:"properties"`
					} `yaml:"schemas"`
				} `yaml:"components"`
			}
			if err := yaml.Unmarshal(raw, &document); err != nil {
				t.Fatalf("decode module OpenAPI: %v", err)
			}
			contextSchema, ok := document.Components.Schemas["BKNContext"]
			if !ok || !sameStringSet(
				contextSchema.Required,
				[]string{"conversation_id", "interaction_id", "operation_key"},
			) {
				t.Fatalf("BKNContext contract is missing or incomplete: %#v", contextSchema)
			}
			request, ok := document.Components.Schemas[requestSchema]
			if !ok || !containsString(request.Required, "bkn_context") {
				t.Fatalf("%s must require bkn_context: %#v", requestSchema, request)
			}
			property, ok := request.Properties["bkn_context"]
			if !ok || property["$ref"] != "#/components/schemas/BKNContext" {
				t.Fatalf("%s must reference BKNContext: %#v", requestSchema, property)
			}
		})
	}
}

func TestLifecycleCapabilityDiscoveryDoesNotRequireBusinessContext(t *testing.T) {
	info, err := BuildMCPInfo("http://context-loader.test/mcp")
	if err != nil {
		t.Fatalf("build MCP info: %v", err)
	}
	found := map[string]bool{}
	for _, tool := range info.Tools {
		if _, lifecycle := lifecycleToolNames[tool.Name]; !lifecycle {
			continue
		}
		found[tool.Name] = true
		var schema struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("decode %s schema: %v", tool.Name, err)
		}
		if containsString(schema.Required, "bkn_context") {
			t.Fatalf("%s must not require business context", tool.Name)
		}
	}
	if len(found) != len(lifecycleToolNames) {
		t.Fatalf("lifecycle discovery registered %d tools, want %d: %#v", len(found), len(lifecycleToolNames), found)
	}
}

func TestLifecycleToolsExposeExactCoreOutputSchemas(t *testing.T) {
	tests := map[string][]string{
		"bkn_create_conversation":  {"conversation_id", "owner", "external_conversation_key", "generation", "status", "one_shot", "row_version", "created_at", "updated_at"},
		"bkn_resume_conversation":  {"conversation_id", "owner", "external_conversation_key", "generation", "status", "one_shot", "row_version", "created_at", "updated_at"},
		"bkn_close_conversation":   {"conversation_id", "owner", "external_conversation_key", "generation", "status", "one_shot", "row_version", "created_at", "updated_at"},
		"bkn_start_interaction":    {"interaction_id", "conversation_id", "ordinal", "execution_status", "evidence_status", "lease_token", "lease_epoch", "lease_version", "lease_expires_at", "row_version", "created_at", "updated_at"},
		"bkn_complete_interaction": {"interaction_id", "conversation_id", "ordinal", "execution_status", "evidence_status", "lease_token", "lease_epoch", "lease_version", "lease_expires_at", "row_version", "created_at", "updated_at"},
		"bkn_fail_interaction":     {"interaction_id", "conversation_id", "ordinal", "execution_status", "evidence_status", "lease_token", "lease_epoch", "lease_version", "lease_expires_at", "row_version", "created_at", "updated_at"},
		"bkn_cancel_interaction":   {"interaction_id", "conversation_id", "ordinal", "execution_status", "evidence_status", "lease_token", "lease_epoch", "lease_version", "lease_expires_at", "row_version", "created_at", "updated_at"},
		"bkn_handoff_interaction":  {"interaction_id", "conversation_id", "ordinal", "execution_status", "evidence_status", "lease_token", "lease_epoch", "lease_version", "lease_expires_at", "row_version", "created_at", "updated_at"},
		"bkn_get_operation":        {"operation_id", "conversation_id", "interaction_id", "operation_key", "tool_name", "normalized_input_hash", "attempt", "attempt_status", "retryable", "row_version", "created_at", "updated_at"},
		"bkn_get_receipt":          {"receipt_id", "schema_version", "owner", "conversation_id", "interaction_id", "operation_id", "attempt", "operation_key", "tool_name", "normalized_input_hash", "receipt_status", "evidence_durability", "required", "request_id", "trace_id", "causation_event_ids", "observed_evidence_refs", "business_refs", "artifact_refs", "partial_reasons", "row_version", "issued_at", "payload_hash"},
	}
	for tool, required := range tests {
		_, output := loadToolSchemas(tool)
		var schema struct {
			Type                 string                     `json:"type"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Required             []string                   `json:"required"`
			AdditionalProperties any                        `json:"additionalProperties"`
		}
		if err := json.Unmarshal(output, &schema); err != nil {
			t.Fatalf("decode %s output: %v", tool, err)
		}
		if schema.Type != "object" || schema.AdditionalProperties != false {
			t.Fatalf("%s output must be a closed object: %s", tool, output)
		}
		for _, field := range required {
			if _, ok := schema.Properties[field]; !ok || !containsString(schema.Required, field) {
				t.Fatalf("%s output must require Core field %s: %s", tool, field, output)
			}
		}
	}
	for _, tool := range []string{"bkn_retry_operation"} {
		_, output := loadToolSchemas(tool)
		var result struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		_ = json.Unmarshal(output, &result)
		for _, field := range []string{"operation", "receipt", "created", "execute"} {
			if _, ok := result.Properties[field]; !ok || !containsString(result.Required, field) {
				t.Fatalf("%s output must require %s: %s", tool, field, output)
			}
		}
	}
}

func TestStartInteractionLeaseSecondsMatchesOptionalCoreField(t *testing.T) {
	input, _ := loadToolSchemas("bkn_start_interaction")
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	_ = json.Unmarshal(input, &schema)
	if _, ok := schema.Properties["lease_seconds"]; !ok {
		t.Fatal("start interaction must expose optional lease_seconds")
	}
	if containsString(schema.Required, "lease_seconds") {
		t.Fatalf("lease_seconds is optional in Core request: %s", input)
	}
}

func TestLifecycleSchemaUsesRegisteredIssue541ErrorsAndCoreTypes(t *testing.T) {
	swaggerPath := filepath.Clean("../../../../../../bkn-trace/agent-observability/docs/swagger/swagger.json")
	raw, err := os.ReadFile(swaggerPath)
	if err != nil {
		t.Fatalf("read Core OpenAPI %s: %v", swaggerPath, err)
	}
	var core struct {
		Definitions map[string]map[string]any `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &core); err != nil {
		t.Fatalf("parse Core OpenAPI: %v", err)
	}
	issueErrors := []string{
		"conversation_required", "conversation_closed", "conversation_expired",
		"interaction_required", "interaction_in_progress", "interaction_terminal",
		"operation_required", "idempotency_conflict", "receipt_pending",
		"terminal_conflict", "closure_manifest_invalid", "feature_not_installed",
		"permission_denied", "resource_not_disclosed",
	}
	errorSchema := core.Definitions["httphandler.lifecycleError"]
	codeSchema := errorSchema["properties"].(map[string]any)["code"].(map[string]any)
	registeredErrors := make(map[string]bool)
	for _, code := range anyStringSlice(codeSchema["enum"]) {
		registeredErrors[code] = true
	}
	for _, code := range issueErrors {
		if !registeredErrors[code] {
			t.Fatalf("Issue #541 error %q is absent from the Core 3.0 registry", code)
		}
	}

	outputs := map[string]string{
		"bkn_create_conversation": "sessionvo.Conversation",
		"bkn_start_interaction":   "sessionvo.Interaction",
		"bkn_get_operation":       "sessionvo.Operation",
		"bkn_get_receipt":         "sessionvo.Receipt",
		"bkn_retry_operation":     "httphandler.operationResult",
	}
	for tool, definition := range outputs {
		_, rawOutput := loadToolSchemas(tool)
		var mcpSchema map[string]any
		if err := json.Unmarshal(rawOutput, &mcpSchema); err != nil {
			t.Fatalf("decode %s output: %v", tool, err)
		}
		want := freezeSchema(core.Definitions[definition], core.Definitions, map[string]bool{})
		got := freezeSchema(mcpSchema, core.Definitions, map[string]bool{})
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s recursively drifted from %s:\ngot=%#v\nwant=%#v", tool, definition, got, want)
		}
	}
}

func TestLifecycleSwaggerPathsRequestsAndResponsesAreStructurallyFrozen(t *testing.T) {
	swaggerPath := filepath.Clean("../../../../../../bkn-trace/agent-observability/docs/swagger/swagger.json")
	raw, err := os.ReadFile(swaggerPath)
	if err != nil {
		t.Fatalf("read Core OpenAPI: %v", err)
	}
	var document struct {
		Paths       map[string]map[string]json.RawMessage `json:"paths"`
		Definitions map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse Core OpenAPI JSON: %v", err)
	}
	type pathContract struct {
		path, method, requestRef, responseCode, responseRef string
	}
	contracts := []pathContract{
		{"/conversations:ensure-current", "post", "#/definitions/httphandler.ensureConversationRequest", "201", "#/definitions/sessionvo.Conversation"},
		{"/conversations:resume-by-id", "post", "#/definitions/httphandler.resumeConversationRequest", "200", "#/definitions/sessionvo.Conversation"},
		{"/conversations/{conversation_id}/interactions", "post", "#/definitions/httphandler.startInteractionRequest", "201", "#/definitions/sessionvo.Interaction"},
		{"/interactions/{interaction_id}/complete", "post", "#/definitions/httphandler.terminalInteractionRequest", "200", "#/definitions/sessionvo.Interaction"},
		{"/interactions/{interaction_id}/fail", "post", "#/definitions/httphandler.terminalInteractionRequest", "200", "#/definitions/sessionvo.Interaction"},
		{"/interactions/{interaction_id}/cancel", "post", "#/definitions/httphandler.terminalInteractionRequest", "200", "#/definitions/sessionvo.Interaction"},
		{"/interactions/{interaction_id}/handoff", "post", "#/definitions/httphandler.terminalInteractionRequest", "200", "#/definitions/sessionvo.Interaction"},
		{"/conversations/{conversation_id}/close", "post", "#/definitions/httphandler.closeConversationRequest", "200", "#/definitions/sessionvo.Conversation"},
		{"/operations/{operation_id}", "get", "", "200", "#/definitions/sessionvo.Operation"},
		{"/operations/{operation_id}/attempts", "post", "#/definitions/httphandler.interactionLeaseRequest", "201", "#/definitions/httphandler.operationResult"},
	}
	for _, contract := range contracts {
		operationRaw, ok := document.Paths[contract.path][contract.method]
		if !ok {
			t.Fatalf("Core OpenAPI missing %s %s", contract.method, contract.path)
		}
		var operation struct {
			Parameters []struct {
				In     string `json:"in"`
				Schema struct {
					Ref string `json:"$ref"`
				} `json:"schema"`
			} `json:"parameters"`
			Responses map[string]struct {
				Schema struct {
					Ref string `json:"$ref"`
				} `json:"schema"`
			} `json:"responses"`
		}
		_ = json.Unmarshal(operationRaw, &operation)
		if contract.requestRef != "" {
			found := false
			for _, parameter := range operation.Parameters {
				found = found || parameter.In == "body" && parameter.Schema.Ref == contract.requestRef
			}
			if !found {
				t.Fatalf("%s %s request ref drifted: %s", contract.method, contract.path, operationRaw)
			}
		}
		if operation.Responses[contract.responseCode].Schema.Ref != contract.responseRef {
			t.Fatalf("%s %s response ref drifted: %s", contract.method, contract.path, operationRaw)
		}
	}
	requiredDefinitions := map[string][]string{
		"httphandler.ensureConversationRequest": {"external_conversation_key"},
		"httphandler.resumeConversationRequest": {"conversation_id"},
		"httphandler.startInteractionRequest":   {"idempotency_key"},
		"httphandler.interactionLeaseRequest":   {"lease_token", "lease_epoch"},
		"httphandler.terminalInteractionRequest": {
			"terminal_idempotency_key", "lease_token", "lease_epoch",
			"completion_manifest_version", "completion_reason",
		},
		"httphandler.operationResult": {"operation", "receipt", "created", "execute"},
	}
	for name, fields := range requiredDefinitions {
		definition, ok := document.Definitions[name]
		if !ok {
			t.Fatalf("Core OpenAPI missing definition %s", name)
		}
		for _, field := range fields {
			if _, ok := definition.Properties[field]; !ok || !containsString(definition.Required, field) {
				t.Fatalf("Core definition %s must require %s", name, field)
			}
		}
	}
}

func TestLifecycleMCPInputRequiredFieldsMatchCorePathAndBody(t *testing.T) {
	expected := map[string][]string{
		"bkn_create_conversation":  {"external_conversation_key"},
		"bkn_resume_conversation":  {"conversation_id"},
		"bkn_start_interaction":    {"conversation_id", "idempotency_key", "question"},
		"bkn_complete_interaction": {"interaction_id", "terminal_idempotency_key", "lease_token", "lease_epoch", "completion_manifest_version", "completion_reason", "answer"},
		"bkn_fail_interaction":     {"interaction_id", "terminal_idempotency_key", "lease_token", "lease_epoch", "completion_manifest_version", "completion_reason"},
		"bkn_cancel_interaction":   {"interaction_id", "terminal_idempotency_key", "lease_token", "lease_epoch", "completion_manifest_version", "completion_reason"},
		"bkn_handoff_interaction":  {"interaction_id", "terminal_idempotency_key", "lease_token", "lease_epoch", "completion_manifest_version", "completion_reason"},
		"bkn_close_conversation":   {"conversation_id"},
		"bkn_get_operation":        {"operation_id"},
		"bkn_retry_operation":      {"operation_id"},
		"bkn_get_receipt":          {"receipt_id"},
	}
	for tool, want := range expected {
		input, _ := loadToolSchemas(tool)
		var schema struct {
			Required []string `json:"required"`
		}
		_ = json.Unmarshal(input, &schema)
		if !sameStringSet(schema.Required, want) {
			t.Fatalf("%s required fields drifted: got=%v want=%v schema=%s", tool, schema.Required, want, input)
		}
	}
}

func TestHelmEnforcesInstalledLifecycleCoreByDefault(t *testing.T) {
	valuesPath := filepath.Clean("../../../helm/agent-retrieval/values.yaml")
	values, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("read Helm values: %v", err)
	}
	if !strings.Contains(string(values), `core_url: "http://agent-observability:8080"`) {
		t.Fatalf("Helm lifecycle default must target the agent-observability service: %s", values)
	}
	if !strings.Contains(string(values), `gateway_token_secret_key: "token"`) {
		t.Fatalf("Helm lifecycle values must define the trusted gateway token key: %s", values)
	}
	if !strings.Contains(string(values), `default_tenant_id: "openbkn-local"`) {
		t.Fatalf("Helm lifecycle values must align with the observability single-tenant scope: %s", values)
	}
	if !strings.Contains(string(values), `default_business_domain: "bd_public"`) {
		t.Fatalf("Helm lifecycle values must carry the platform default business domain: %s", values)
	}
	deploymentPath := filepath.Clean("../../../helm/agent-retrieval/templates/deployment.yaml")
	deployment, err := os.ReadFile(deploymentPath)
	if err != nil {
		t.Fatalf("read Helm deployment: %v", err)
	}
	rendering := string(deployment)
	if !strings.Contains(rendering, `required "observability.lifecycle.core_url is required"`) {
		t.Fatal("Helm must reject lifecycle enforcement with an empty Core URL")
	}
	if strings.Contains(rendering, `if .Values.observability.lifecycle.core_url`) {
		t.Fatal("lifecycle enforcement must not have a long-lived disable switch")
	}
	if !strings.Contains(rendering, `name: BKN_TRACE_QUERY_GATEWAY_TOKEN`) ||
		!strings.Contains(rendering, `.Values.observability.lifecycle.gateway_token_secret_name`) {
		t.Fatal("Helm must inject the lifecycle gateway token from a Secret")
	}
	if !strings.Contains(rendering, `name: BKN_TRACE_DEFAULT_TENANT_ID`) {
		t.Fatal("Helm must support an explicit single-tenant trust scope")
	}
}

type frozenSchema struct {
	Type       string
	Required   []string
	Enum       []string
	Properties map[string]frozenSchema
	Items      *frozenSchema
}

func freezeSchema(
	value map[string]any,
	definitions map[string]map[string]any,
	visiting map[string]bool,
) frozenSchema {
	if reference, _ := value["$ref"].(string); reference != "" {
		name := strings.TrimPrefix(reference, "#/definitions/")
		if visiting[name] {
			return frozenSchema{Type: "recursive:" + name}
		}
		next := make(map[string]bool, len(visiting)+1)
		for key, state := range visiting {
			next[key] = state
		}
		next[name] = true
		return freezeSchema(definitions[name], definitions, next)
	}
	result := frozenSchema{
		Type:       stringValue(value["type"]),
		Required:   anyStringSlice(value["required"]),
		Enum:       anyStringSlice(value["enum"]),
		Properties: map[string]frozenSchema{},
	}
	sort.Strings(result.Required)
	sort.Strings(result.Enum)
	if properties, ok := value["properties"].(map[string]any); ok {
		for name, raw := range properties {
			if property, ok := raw.(map[string]any); ok {
				result.Properties[name] = freezeSchema(property, definitions, visiting)
			}
		}
	}
	if rawItems, ok := value["items"].(map[string]any); ok {
		items := freezeSchema(rawItems, definitions, visiting)
		result.Items = &items
	}
	return result
}

func anyStringSlice(value any) []string {
	raw, _ := value.([]any)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for _, value := range left {
		if !containsString(right, value) {
			return false
		}
	}
	return true
}
