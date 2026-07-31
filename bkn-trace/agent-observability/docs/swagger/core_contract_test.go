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
	for _, name := range []string{"swagger.json", "swagger.yaml"} {
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		documents[name] = parseSwagger(t, content)
	}

	definitions := []string{
		"httphandler.finishAttemptRequest",
		"httphandler.operationResult",
		"sessionvo.BusinessRef",
		"sessionvo.Conversation",
		"sessionvo.Interaction",
		"sessionvo.Operation",
		"sessionvo.Receipt",
	}
	for _, definition := range definitions {
		assertAllEqual(t, documents, "definition "+definition, func(document swaggerDocument) any {
			return document.Definitions[definition]
		})
	}
	path := "/operations/{operation_id}/attempts/{attempt}:complete"
	assertAllEqual(t, documents, "path "+path, func(document swaggerDocument) any {
		return document.Paths[path].Post
	})

	finish := documents["swagger.json"].Definitions["httphandler.finishAttemptRequest"]
	assertStringSet(t, finish.Required,
		"receipt_id", "payload_hash", "evidence_durability", "request_id", "trace_id")
	if _, exists := finish.Properties["retryable"]; !exists {
		t.Fatal("finish request must expose optional retryable")
	}
	result := documents["swagger.json"].Definitions["httphandler.operationResult"]
	assertStringSet(t, result.Required, "operation", "receipt", "created")
	businessRef := documents["swagger.json"].Definitions["sessionvo.BusinessRef"]
	assertStringSet(t, businessRef.Required, "ref_type", "ref_id", "business_domain_id", "version")

	complete := documents["swagger.json"].Paths[path].Post
	body := findParameter(t, complete.Parameters, "request", "body")
	if !body.Required || body.Schema.Ref != "#/definitions/httphandler.finishAttemptRequest" {
		t.Fatalf("finish body contract drifted: %#v", body)
	}
	if complete.Responses["200"].Schema.Ref != "#/definitions/httphandler.operationResult" {
		t.Fatalf("finish response contract drifted: %#v", complete.Responses["200"])
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
