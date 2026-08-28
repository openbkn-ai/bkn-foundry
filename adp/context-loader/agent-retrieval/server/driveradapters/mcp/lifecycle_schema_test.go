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
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBusinessToolSchemasRequireManagedBKNContext(t *testing.T) {
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
			// Every business tool is protected by the managed interaction gate.
			// Clients must use the authority IDs returned by bkn_start_interaction.
			if _, ok := schema.Properties["bkn_context"]; !ok {
				t.Fatalf("%s must advertise bkn_context", toolKey)
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
			for _, field := range []string{"conversation_id", "interaction_id"} {
				if _, ok := contextSchema.Properties[field]; !ok || !containsString(contextSchema.Required, field) {
					t.Fatalf("%s bkn_context must require %s", toolKey, field)
				}
			}
			for _, field := range []string{"parent_operation_id", "causation_event_ids", "business_refs"} {
				if _, ok := contextSchema.Properties[field]; !ok {
					t.Fatalf("%s bkn_context must declare optional %s", toolKey, field)
				}
			}
			var businessRefs struct {
				Type     string `json:"type"`
				MaxItems int    `json:"maxItems"`
				Items    struct {
					AdditionalProperties any                        `json:"additionalProperties"`
					Properties           map[string]json.RawMessage `json:"properties"`
					Required             []string                   `json:"required"`
				} `json:"items"`
			}
			if err := json.Unmarshal(contextSchema.Properties["business_refs"], &businessRefs); err != nil {
				t.Fatalf("decode %s business_refs schema: %v", toolKey, err)
			}
			if businessRefs.Type != "array" || businessRefs.MaxItems != 64 ||
				businessRefs.Items.AdditionalProperties != false ||
				!sameStringSet(businessRefs.Items.Required, []string{"ref_type", "ref_id"}) {
				t.Fatalf("%s business_refs must be a bounded closed declaration: %#v", toolKey, businessRefs)
			}
			for _, field := range []string{"ref_type", "ref_id", "version"} {
				if _, ok := businessRefs.Items.Properties[field]; !ok {
					t.Fatalf("%s business_refs item must declare %s", toolKey, field)
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
				[]string{"conversation_id", "interaction_id"},
			) {
				t.Fatalf("BKNContext contract is missing or incomplete: %#v", contextSchema)
			}
			if _, ok := contextSchema.Properties["business_refs"]; !ok {
				t.Fatalf("BKNContext must document optional business_refs: %#v", contextSchema)
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
		"bkn_start_interaction":  {"interaction_id", "conversation_id", "execution_status"},
		"bkn_finish_interaction": {"interaction_id", "conversation_id", "execution_status", "evidence_status"},
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
}

func TestStartInteractionHidesCoreLeaseField(t *testing.T) {
	input, _ := loadToolSchemas("bkn_start_interaction")
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	_ = json.Unmarshal(input, &schema)
	if _, ok := schema.Properties["lease_seconds"]; ok {
		t.Fatal("ordinary agents must not manage lease_seconds")
	}
}

func TestStartInteractionRequiresBoundedStableAgentName(t *testing.T) {
	input, _ := loadToolSchemas("bkn_start_interaction")
	var schema struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			MaxLength   int    `json:"maxLength"`
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(input, &schema); err != nil {
		t.Fatalf("decode start schema: %v", err)
	}
	agentName, ok := schema.Properties["agent_name"]
	if !ok || agentName.Type != "string" || agentName.MaxLength != 128 || agentName.Description != "The current Agent's stable name. Provide the same value on every call in the same conversation_id." {
		t.Fatalf("agent_name must be a required bounded stable declaration: %s", input)
	}
	if !containsString(schema.Required, "agent_name") {
		t.Fatalf("agent_name must be required: %s", input)
	}
}

func TestLifecycleSchemasRequireAnExplicitConversationChoiceAndCompletedAnswer(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args map[string]any
		want bool
	}{
		{
			name: "new conversation", tool: "bkn_start_interaction",
			args: map[string]any{"question": "查询库存", "agent_name": "supply-chain-analyst", "conversation_mode": "new"},
			want: true,
		},
		{
			name: "continue conversation", tool: "bkn_start_interaction",
			args: map[string]any{"question": "继续查询", "agent_name": "supply-chain-analyst", "conversation_mode": "continue", "conversation_id": "conv-1"},
			want: true,
		},
		{
			name: "missing conversation choice", tool: "bkn_start_interaction",
			args: map[string]any{"question": "查询库存", "agent_name": "supply-chain-analyst"},
			want: false,
		},
		{
			name: "continue without conversation", tool: "bkn_start_interaction",
			args: map[string]any{"question": "继续查询", "agent_name": "supply-chain-analyst", "conversation_mode": "continue"},
			want: false,
		},
		{
			name: "completed without answer", tool: "bkn_finish_interaction",
			args: map[string]any{"interaction_id": "int-1", "outcome": "completed"},
			want: false,
		},
		{
			name: "completed with empty answer", tool: "bkn_finish_interaction",
			args: map[string]any{"interaction_id": "int-1", "outcome": "completed", "answer": ""},
			want: false,
		},
		{
			name: "failed without reason", tool: "bkn_finish_interaction",
			args: map[string]any{"interaction_id": "int-1", "outcome": "failed"},
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := validateLifecycleArguments(test.tool, test.args) == nil
			if got != test.want {
				t.Fatalf("%s validation = %t, want %t", test.name, got, test.want)
			}
		})
	}
}

func TestLifecycleWireSchemasAvoidCompositionKeywords(t *testing.T) {
	for _, tool := range []string{"bkn_start_interaction", "bkn_finish_interaction"} {
		input, _ := loadToolSchemas(tool)
		var schema map[string]any
		if err := json.Unmarshal(input, &schema); err != nil {
			t.Fatalf("decode %s input schema: %v", tool, err)
		}
		for _, keyword := range []string{"oneOf", "anyOf", "allOf", "if", "then", "else", "dependentRequired"} {
			if _, found := schema[keyword]; found {
				t.Fatalf("%s wire schema must not publish %s: %s", tool, keyword, input)
			}
		}
	}
}

func TestLifecycleInputDescriptionsAreConciseAndActionable(t *testing.T) {
	wantFields := map[string][]string{
		"bkn_start_interaction":  {"conversation_id", "conversation_mode", "question", "agent_name"},
		"bkn_finish_interaction": {"interaction_id", "outcome", "answer", "reason"},
	}
	for tool, fields := range wantFields {
		input, _ := loadToolSchemas(tool)
		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(input, &schema); err != nil {
			t.Fatalf("decode %s schema: %v", tool, err)
		}
		for _, field := range fields {
			description := schema.Properties[field].Description
			if description == "" || len(description) > 120 {
				t.Fatalf("%s.%s description must be present and concise: %q", tool, field, description)
			}
		}
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
		"permission_denied", "resource_not_disclosed", "trace_core_unavailable",
		"evidence_capture_denied", "evidence_capture_failed",
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

func TestLifecycleMCPInputRequiredFieldsFollowAgentFacadeContract(t *testing.T) {
	expected := map[string][]string{
		"bkn_start_interaction":  {"question", "agent_name", "conversation_mode"},
		"bkn_finish_interaction": {"interaction_id", "outcome"},
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
	if !strings.Contains(string(values), `core_url: "http://agent-observability-internal:8081"`) {
		t.Fatalf("Helm lifecycle default must target the internal agent-observability service: %s", values)
	}
	if !strings.Contains(string(values), `ingest_url: "http://agent-observability:8080/api/agent-observability/v1/evidence/events"`) {
		t.Fatal("Helm must preserve the token-protected public evidence producer contract")
	}
	if !strings.Contains(string(values), `ingest_token_secret_name: "bkn-trace-evidence-ingest"`) {
		t.Fatal("Helm must wire the standard evidence ingest Secret by default")
	}
	if !strings.Contains(string(values), `default_tenant_id: "openbkn-local"`) {
		t.Fatalf("Helm lifecycle values must align with the observability single-tenant scope: %s", values)
	}
	deploymentPath := filepath.Clean("../../../helm/agent-retrieval/templates/deployment.yaml")
	deployment, err := os.ReadFile(deploymentPath)
	if err != nil {
		t.Fatalf("read Helm deployment: %v", err)
	}
	rendering := string(deployment)
	if !strings.Contains(rendering, `app.kubernetes.io/name: agent-retrieval`) {
		t.Fatal("Helm must retain the stable pod label admitted by the Trace lifecycle NetworkPolicy")
	}
	if !strings.Contains(rendering, `required "observability.lifecycle.core_url is required"`) {
		t.Fatal("Helm must reject lifecycle enforcement with an empty Core URL")
	}
	if strings.Contains(rendering, `if .Values.observability.lifecycle.core_url`) {
		t.Fatal("lifecycle enforcement must not have a long-lived disable switch")
	}
	if strings.Contains(rendering, `BKN_TRACE_QUERY_GATEWAY_TOKEN`) {
		t.Fatal("Helm must not inject a shared lifecycle token into agent-retrieval")
	}
	if !strings.Contains(rendering, `BKN_TRACE_EVIDENCE_INGEST_TOKEN`) {
		t.Fatal("Helm must retain the evidence ingest token for the public producer contract")
	}
	if !strings.Contains(rendering, `optional: true`) {
		t.Fatal("evidence ingest Secret reference must stay optional so standalone retrieval still starts")
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
