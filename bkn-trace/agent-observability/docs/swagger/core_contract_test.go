package swagger_test

import (
	"os"
	"strings"
	"testing"
)

func TestGeneratedSwaggerContainsManagedLifecycleContract(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile("swagger.yaml")
	if err != nil {
		t.Fatalf("read generated Swagger: %v", err)
	}
	swagger := string(content)
	requiredPaths := []string{
		"/conversations:",
		"/conversations:ensure-current:",
		"/conversations:create-new-generation:",
		"/conversations:resume-by-id:",
		"/conversations/{conversation_id}:",
		"/conversations/{conversation_id}/close:",
		"/conversations/{conversation_id}/interactions:",
		"/conversations/{conversation_id}/interactions/{interaction_id}/operations:ensure:",
		"/interactions/{interaction_id}:",
		"/interactions/{interaction_id}/complete:",
		"/interactions/{interaction_id}/fail:",
		"/interactions/{interaction_id}/cancel:",
		"/interactions/{interaction_id}/handoff:",
		"/operations/{operation_id}:",
		"/operations/{operation_id}/attempts:",
		"/operations/{operation_id}/attempts/{attempt}:complete:",
		"/operations/{operation_id}/attempts/{attempt}:fail:",
		"/receipts/{receipt_id}:",
		"/evidence/events:",
	}
	for _, path := range requiredPaths {
		if !strings.Contains(swagger, "  "+path) {
			t.Errorf("generated Swagger is missing %s", path)
		}
	}
}
