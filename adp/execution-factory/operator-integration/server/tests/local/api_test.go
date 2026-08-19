//go:build !skiptest
// +build !skiptest

package local

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	jsoniter "github.com/json-iterator/go"
)

// OperatorAPI operator API metadata.
type OperatorAPI struct {
}

// Test API format parsing.
func TestAPIAnalysis(t *testing.T) {
	localPath := "../file/test.json"

	// Load OpenAPI documentation.
	loader := openapi3.NewLoader()

	doc, err := loader.LoadFromFile(localPath)
	if err != nil {
		t.Fatalf("Failed to load OpenAPI document: %v", err)
	}
	// Verify OpenAPI documentation.
	err = doc.Validate(loader.Context)
	if err != nil {
		t.Fatalf("Failed to validate OpenAPI document: %v", err)
	}

	dstPath := "./data"
	err = os.MkdirAll(dstPath, os.ModePerm)
	if err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(dstPath)
	}()
	// Split batch imported OpenAPI into multiple.
	for path, pathItem := range doc.Paths.Map() {
		// Create new lite version of OpenAPI documentation.
		newDoc := &openapi3.T{
			OpenAPI: doc.OpenAPI,
			Info:    doc.Info,
			Servers: doc.Servers,
			Components: &openapi3.Components{
				SecuritySchemes: doc.Components.SecuritySchemes,
				Schemas:         make(map[string]*openapi3.SchemaRef),
			},
			Paths:    openapi3.NewPaths(openapi3.WithPath(path, pathItem)),
			Security: doc.Security,
		}
		// Automatically collect dependent schema.
		for _, op := range pathItem.Operations() {
			if op.RequestBody != nil {
				collectSchemas(doc.Components, op.RequestBody.Value.Content, newDoc.Components.Schemas, make(map[string]bool))
			}
			for _, resp := range op.Responses.Map() {
				collectSchemas(doc.Components, resp.Value.Content, newDoc.Components.Schemas, make(map[string]bool))
			}
		}
		// Generate safe file names.
		fileName := strings.ReplaceAll(path, "/", "_")
		if fileName == "" {
			fileName = "root"
		}
		fileName = strings.TrimLeft(fileName, "_") + ".json"
		filePath := filepath.Join(dstPath, fileName)
		// Serialize and write to file.
		data, err := json.MarshalIndent(newDoc, "", "  ")
		if err != nil {
			t.Fatalf("Failed to marshal API for %s: %v", path, err)
		}

		if err := os.WriteFile(filePath, data, 0644); err != nil {
			t.Fatalf("Failed to write file %s: %v", filePath, err)
		}

		fmt.Printf("Generated API fragment: %s\n", filePath)
	}
}

func TestValidateAPI(t *testing.T) {
	localPath := "./data/api_open-doc_v1_file-decrypt.json"
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(localPath)
	if err != nil {
		t.Fatalf("Failed to load OpenAPI document: %v", err)
	}
	err = doc.Validate(loader.Context)
	if err != nil {
		t.Fatalf("Failed to validate OpenAPI document: %v", err)
	}
	for path, pathItem := range doc.Paths.Map() {
		fmt.Println(path)
		i, _ := jsoniter.Marshal(pathItem)
		fmt.Println(string(i))
		// fmt.Println(len(pathItem.Parameters))
		// pathItem.Get.RequestBody.Value.GetMediaType()
		// if pathItem != nil {
		// 	b, _ := jsoniter.Marshal(pathItem.Post.Parameters)
		// 	fmt.Println("Parameters：", string(b))
		// }
		// if pathItem.Extensions != nil {
		// 	b, _ := jsoniter.Marshal(pathItem.Extensions)
		// 	fmt.Println("Extensions", string(b))
		// }
	}
}

// Test API metadata parsing.
func TestAPIMetadataAnalysis(t *testing.T) {
	_, err := LoadOpenAPIMetadata(context.Background(), string(FileDataType), "./data/api_open-doc_v1_file-decrypt.json", nil)
	if err != nil {
		t.Errorf("LoadOpenAPIMetadata failed: %v", err)
	}
}

func TestXxx(t *testing.T) {

}
