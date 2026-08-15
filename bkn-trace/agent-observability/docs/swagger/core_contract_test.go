package swagger_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	generated "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/docs/swagger"
	"gopkg.in/yaml.v2"
)

func TestSwaggerGenerationUsesPinnedCLIAndDriftTarget(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(content)
	for _, required := range []string{
		"github.com/swaggo/swag/cmd/swag@v1.8.12",
		"check-swag-version:",
		"check-swag:",
		"set -e;",
		"--packageName swagger",
		"diff -u $(SWAG_OUT)/",
		"PUBLISHED_SWAG :=",
		"diff -u $(PUBLISHED_SWAG)",
	} {
		if !strings.Contains(makefile, required) {
			t.Fatalf("Makefile does not freeze Swagger generation with %q", required)
		}
	}
}

type schema struct {
	Ref        string            `yaml:"$ref"`
	Type       string            `yaml:"type"`
	Required   []string          `yaml:"required"`
	Enum       []string          `yaml:"enum"`
	Properties map[string]schema `yaml:"properties"`
	Items      *schema           `yaml:"items"`
}

type parameter struct {
	Name     string `yaml:"name"`
	In       string `yaml:"in"`
	Required bool   `yaml:"required"`
	Schema   schema `yaml:"schema"`
}

type response struct {
	Schema schema `yaml:"schema"`
}

type operation struct {
	Parameters []parameter         `yaml:"parameters"`
	Responses  map[string]response `yaml:"responses"`
}

type pathItem struct {
	Get  operation `yaml:"get"`
	Post operation `yaml:"post"`
}

type swaggerDocument struct {
	Paths       map[string]pathItem `yaml:"paths"`
	Definitions map[string]schema   `yaml:"definitions"`
}

func TestGeneratedSwaggerLifecycleArtifactsStayStructurallyEquivalent(t *testing.T) {
	t.Parallel()

	documents := map[string]swaggerDocument{
		"docs.go": parseSwagger(t, []byte(generated.SwaggerInfo.ReadDoc())),
	}
	for _, name := range []string{"swagger.json"} {
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		documents[name] = parseSwagger(t, content)
	}
	published, err := os.ReadFile("../../../../docs/api/agent-observability/agent-observability.yaml")
	if err != nil {
		t.Fatalf("read published OpenAPI YAML: %v", err)
	}
	documents["published YAML"] = parseSwagger(t, published)

	definitions := []string{
		"assemblysvc.BusinessRefView",
		"assemblysvc.ProjectedResult",
		"httphandler.evidenceEventRequest",
		"httphandler.ensureOperationRequest",
		"httphandler.finishAttemptRequest",
		"httphandler.operationResult",
		"sessionvo.BusinessRef",
		"sessionvo.BusinessRefType",
		"sessionvo.Claim",
		"sessionvo.ClaimSupport",
		"sessionvo.Conversation",
		"sessionvo.EvidenceRef",
		"sessionvo.Interaction",
		"sessionvo.Operation",
		"sessionvo.OperationCallFact",
		"sessionvo.OperationProtocol",
		"sessionvo.PayloadEnvelope",
		"sessionvo.OperationBusinessEdge",
		"sessionvo.OperationBusinessRole",
		"sessionvo.Receipt",
	}
	for _, definition := range definitions {
		assertAllEqual(t, documents, "definition "+definition, func(document swaggerDocument) any {
			return document.Definitions[definition]
		})
	}
	const completePath = "/operations/{operation_id}/attempts/{attempt}:complete"
	for path, method := range map[string]string{
		completePath: "post",
		"/operations/{operation_id}/attempts/{attempt}": "get",
	} {
		assertAllEqual(t, documents, "path "+path, func(document swaggerDocument) any {
			if method == "get" {
				return document.Paths[path].Get
			}
			return document.Paths[path].Post
		})
	}

	finish := documents["swagger.json"].Definitions["httphandler.finishAttemptRequest"]
	assertStringSet(t, finish.Required,
		"receipt_id", "evidence_durability", "request_id", "trace_id")
	for _, required := range []string{"output", "error"} {
		property, exists := finish.Properties[required]
		if !exists {
			t.Fatalf("finish request is missing %q", required)
		}
		if property.Ref != "#/definitions/sessionvo.PayloadEnvelope" {
			t.Fatalf("finish %s schema = %#v, want PayloadEnvelope", required, property)
		}
	}
	if _, exists := finish.Properties["payload_hash"]; exists {
		t.Fatal("finish request must not expose payload_hash")
	}
	if _, exists := finish.Properties["retryable"]; !exists {
		t.Fatal("finish request must expose optional retryable")
	}
	ensure := documents["swagger.json"].Definitions["httphandler.ensureOperationRequest"]
	assertStringSet(t, ensure.Required,
		"input", "lease_epoch", "lease_token", "operation_key", "protocol", "source_module", "tool_name")
	input, exists := ensure.Properties["input"]
	if !exists {
		t.Fatal("ensure request must expose real input")
	}
	if input.Ref != "#/definitions/sessionvo.PayloadEnvelope" {
		t.Fatalf("ensure input schema = %#v, want PayloadEnvelope", input)
	}
	if _, exists := ensure.Properties["normalized_input_hash"]; exists {
		t.Fatal("ensure request must not expose normalized_input_hash")
	}
	for _, definition := range []string{"sessionvo.Operation", "sessionvo.Receipt"} {
		for _, forbidden := range []string{"normalized_input_hash", "payload_hash"} {
			if _, exists := documents["swagger.json"].Definitions[definition].Properties[forbidden]; exists {
				t.Fatalf("%s must not expose %s", definition, forbidden)
			}
		}
	}
	result := documents["swagger.json"].Definitions["httphandler.operationResult"]
	assertStringSet(t, result.Required, "operation", "receipt", "created", "execute")
	businessRef := documents["swagger.json"].Definitions["sessionvo.BusinessRef"]
	assertStringSet(t, businessRef.Required, "ref_type", "ref_id", "business_domain_id", "version")

	complete := documents["swagger.json"].Paths[completePath].Post
	body := findParameter(t, complete.Parameters, "request", "body")
	if !body.Required || body.Schema.Ref != "#/definitions/httphandler.finishAttemptRequest" {
		t.Fatalf("finish body contract drifted: %#v", body)
	}
	if complete.Responses["200"].Schema.Ref != "#/definitions/httphandler.operationResult" {
		t.Fatalf("finish response contract drifted: %#v", complete.Responses["200"])
	}

	event := documents["swagger.json"].Definitions["httphandler.evidenceEventRequest"]
	assertStringSet(t, event.Required,
		"bkn.trace.schema.version", "event_id", "event_type", "payload_hash", "conversation_id", "interaction_id",
		"producer_id", "producer_stream_id", "producer_epoch", "producer_sequence",
		"started_at", "observed_at", "emitted_at", "envelope")
	for _, forbidden := range []string{"schema_version", "tenant_id", "business_domain_id"} {
		if _, exists := event.Properties[forbidden]; exists {
			t.Fatalf("3.0 evidence request exposes forbidden legacy or caller-controlled field %q", forbidden)
		}
	}
	for _, required := range []string{"artifact_refs", "business_refs", "evidence_refs", "claims", "operation_business_edges"} {
		if _, exists := event.Properties[required]; !exists {
			t.Fatalf("3.0 evidence request is missing semantic field %q", required)
		}
	}
}

func TestGeneratedSwaggerContainsEveryManagedLifecycleRoute(t *testing.T) {
	t.Parallel()
	document := parseSwagger(t, []byte(generated.SwaggerInfo.ReadDoc()))
	for _, path := range []string{
		"/conversations",
		"/conversations:ensure-current",
		"/conversations:create-new-generation",
		"/conversations:resume-by-id",
		"/conversations/{conversation_id}",
		"/conversations/{conversation_id}/close",
		"/conversations/{conversation_id}/interactions",
		"/conversations/{conversation_id}/interactions/{interaction_id}/operations:ensure",
		"/interactions/{interaction_id}",
		"/interactions/{interaction_id}/business-graph",
		"/interactions/{interaction_id}/operations",
		"/interactions/{interaction_id}/complete",
		"/interactions/{interaction_id}/fail",
		"/interactions/{interaction_id}/cancel",
		"/interactions/{interaction_id}/handoff",
		"/operations/{operation_id}",
		"/operations/{operation_id}/attempts",
		"/operations/{operation_id}/attempts/{attempt}:complete",
		"/operations/{operation_id}/attempts/{attempt}:fail",
		"/receipts/{receipt_id}",
		"/evidence/events",
	} {
		if _, exists := document.Paths[path]; !exists {
			t.Errorf("generated Swagger is missing %s", path)
		}
	}
}

func TestGeneratedSwaggerDoesNotPublishRemovedBusinessProvenanceRoutes(t *testing.T) {
	t.Parallel()
	document := parseSwagger(t, []byte(generated.SwaggerInfo.ReadDoc()))
	for _, path := range []string{
		"/traces",
		"/traces/{trace_id}",
	} {
		if _, exists := document.Paths[path]; !exists {
			t.Errorf("generated Swagger is missing relative trace route %s", path)
		}
	}
	for _, path := range []string{
		"/business-provenance/conversations", "/business-provenance/interactions",
		"/business-provenance/interactions/{interaction_id}",
		"/business-provenance/requests", "/business-provenance/requests/{request_id}",
		"/business-provenance/requests/{request_id}/traces",
		"/business-provenance/requests/{request_id}/evidence-chain",
		"/business-provenance/requests/{request_id}/business-graph",
		"/business-provenance/requests/{request_id}/snapshot-preview",
		"/business-provenance/traces/{trace_id}/evidence-chain",
		"/business-provenance/traces/{trace_id}/business-graph",
		"/business-provenance/traces/{trace_id}/snapshot-preview",
		"/business-provenance/evidence-nodes/{node_id}",
		"/traces/_search", "/traces/by-conversation", "/traces/by-request",
		"/traces/by-request/business-graph", "/traces/by-request/snapshot-preview",
		"/traces/{trace_id}/trace-graph", "/evidence/by-trace", "/trace-executions", "/evidence-nodes/{node_id}",
	} {
		if _, exists := document.Paths[path]; exists {
			t.Errorf("generated Swagger still exposes removed raw trace route %s", path)
		}
	}
	for path := range document.Paths {
		if strings.HasPrefix(path, "/api/agent-observability/v1/") {
			t.Errorf("generated Swagger route repeats base path: %s", path)
		}
	}
}

func TestLifecycleSourceRequiredTagsDriveSwagger(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(),
		"../../src/domain/valueobject/sessionvo/operation.go", nil, 0)
	if err != nil {
		t.Fatalf("parse operation value object: %v", err)
	}
	required := map[string][]string{
		"BusinessRef": {"RefType", "RefID", "BusinessDomainID", "Version"},
	}
	for typeName, fields := range required {
		structType := findStruct(t, file, typeName)
		for _, fieldName := range fields {
			field := findField(t, structType, fieldName)
			if field.Tag == nil {
				t.Fatalf("%s.%s has no binding tag", typeName, fieldName)
			}
			tag, err := strconv.Unquote(field.Tag.Value)
			if err != nil || reflect.StructTag(tag).Get("binding") != "required" {
				t.Fatalf("%s.%s must declare binding:\"required\": %s", typeName, fieldName, field.Tag.Value)
			}
		}
	}
}

func parseSwagger(t *testing.T, content []byte) swaggerDocument {
	t.Helper()
	var document swaggerDocument
	if err := yaml.Unmarshal(content, &document); err != nil {
		t.Fatalf("parse generated Swagger: %v", err)
	}
	return document
}

func assertAllEqual(
	t *testing.T,
	documents map[string]swaggerDocument,
	label string,
	selectValue func(swaggerDocument) any,
) {
	t.Helper()
	var baseline any
	var baselineName string
	for name, document := range documents {
		value := selectValue(document)
		if baselineName == "" {
			baseline, baselineName = value, name
			continue
		}
		if !reflect.DeepEqual(baseline, value) {
			t.Fatalf("%s differs between %s and %s\n%#v\n%#v",
				label, baselineName, name, baseline, value)
		}
	}
}

func assertStringSet(t *testing.T, actual []string, expected ...string) {
	t.Helper()
	sort.Strings(actual)
	sort.Strings(expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("required fields = %v, want %v", actual, expected)
	}
}

func findParameter(t *testing.T, parameters []parameter, name, location string) parameter {
	t.Helper()
	for _, value := range parameters {
		if value.Name == name && value.In == location {
			return value
		}
	}
	t.Fatalf("missing %s parameter %q", location, name)
	return parameter{}
}

func findStruct(t *testing.T, file *ast.File, name string) *ast.StructType {
	t.Helper()
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if ok && typeSpec.Name.Name == name {
				value, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					t.Fatalf("%s is not a struct", name)
				}
				return value
			}
		}
	}
	t.Fatalf("missing struct %s", name)
	return nil
}

func findField(t *testing.T, value *ast.StructType, name string) *ast.Field {
	t.Helper()
	for _, field := range value.Fields.List {
		if len(field.Names) == 1 && field.Names[0].Name == name {
			return field
		}
	}
	t.Fatalf("missing field %s", name)
	return nil
}
