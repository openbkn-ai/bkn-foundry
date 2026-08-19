package local

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	jsoniter "github.com/json-iterator/go"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// APIMetadata API metadata.
type APIMetadata struct {
	ID          string       `json:"id" validate:"required"`          // Primary key ID.
	Hash        string       `json:"hash" validate:"required"`        // hash value.
	Version     string       `json:"version" validate:"required"`     // version.
	Title       string       `json:"title" validate:"required"`       // Title.
	Summary     string       `json:"summary" validate:"required"`     // Summary.
	Description string       `json:"description" validate:"required"` // Description.
	Path        string       `json:"path" validate:"required"`        // path.
	Method      string       `json:"method" validate:"required"`      // method.
	Parameters  []*Parameter `json:"parameters" validate:"required"`  // Structured parameters.
	RequestBody *RequestBody `json:"request_body"`                    // Request body structure.
	Responses   []*Response  `json:"responses"`                       // response structure.
	CreateTime  int64        `json:"create_time" validate:"required"` // creation time.
	UpdateTime  int64        `json:"update_time" validate:"required"` // Update time.
	CreateUser  string       `json:"create_user" validate:"required"` // Creator.
	UpdateUser  string       `json:"update_user" validate:"required"` // Updater.
	IsDeleted   bool         `json:"is_deleted" validate:"required"`  // Whether to delete.
	// APISpec string `json:"api_spec" validate:"required"` // OpenAPI format.
}

// type APISpec

// Parameter parameter type.
type Parameter struct {
	Name        string                 `json:"name"`
	In          string                 `json:"in"` // path/query/header/cookie
	Description string                 `json:"description"`
	Required    bool                   `json:"required"`
	Ref         string                 `json:"$ref,omitempty"` // Add a new reference field.
	Schema      map[string]interface{} `json:"schema"`         // parameterschema.
}

// RequestBody request body structure.
type RequestBody struct {
	Description string             `json:"description"`
	Content     map[string]Content `json:"content"` // Sort by media type.
}

// Response response structure.
type Response struct {
	StatusCode  string             `json:"status_code"` // 200/400 etc.
	Description string             `json:"description"`
	Content     map[string]Content `json:"content"`
}

// Content content structure.
type Content struct {
	Ref     string                 `json:"$ref,omitempty"` // Add a new reference field.
	Schema  map[string]interface{} `json:"schema"`         // Complete schema.
	Example interface{}            `json:"example,omitempty"`
}

// Added schema conversion method.
func schemaRefToMap(ref *openapi3.SchemaRef, components *openapi3.Components,
	visited map[string]bool) (refPath string, schema map[string]interface{}) {
	if ref == nil {
		return "", nil
	}
	// Handle references.
	if ref.Ref != "" {
		refPath = ref.Ref
		refKey := strings.TrimPrefix(ref.Ref, "#/components/schemas/")
		if visited[refKey] {
			return refPath, map[string]interface{}{"$ref": refPath}
		}
		visited[refKey] = true
		defer delete(visited, refKey)

		if schemaDef, exists := components.Schemas[refKey]; exists {
			// Keep original reference.
			schema = map[string]interface{}{"$ref": refPath}
			// Also retain the parsed schema.
			_, resolved := schemaRefToMap(schemaDef, components, visited)
			for k, v := range resolved {
				if k != "$ref" { // Avoid overwriting the original reference.
					schema[k] = v
				}
			}
			return refPath, schema
		}
	}

	// Parse the current schema.
	schema = make(map[string]interface{})
	if ref.Value == nil {
		return refPath, schema
	}
	// Keep original citation information.
	if refPath != "" {
		schema["$ref"] = refPath
	}

	// Basic type handling.
	if ref.Value.Type != nil {
		schema["type"] = ref.Value.Type
	}

	if ref.Value.Format != "" {
		schema["format"] = ref.Value.Format
	}

	// Handle nested structures.
	if ref.Value.Properties != nil {
		props := make(map[string]interface{})
		for name, prop := range ref.Value.Properties {
			refPath, resolved := schemaRefToMap(prop, components, visited)
			if refPath != "" {
				props[name] = map[string]interface{}{"$ref": refPath}
				components.Schemas[strings.TrimPrefix(refPath, "#/components/schemas/")] = prop
			} else {
				props[name] = resolved
			}
		}
		schema["properties"] = props
	}

	// Handle array types.
	if ref.Value.Items != nil {
		refPath, resolved := schemaRefToMap(ref.Value.Items, components, visited)
		if refPath != "" {
			schema["items"] = map[string]interface{}{"$ref": refPath}
			components.Schemas[strings.TrimPrefix(refPath, "#/components/schemas/")] = ref.Value.Items
		} else {
			schema["items"] = resolved
		}
	}

	// Handling combination types.
	handleComposition := func(schemas openapi3.SchemaRefs) []interface{} {
		var result []interface{}
		for _, s := range schemas {
			// Generate reference paths and preserve original structure.
			refPath, resolvedSchema := schemaRefToMap(s, components, visited)
			if refPath != "" {
				result = append(result, map[string]interface{}{"$ref": refPath})
				components.Schemas[strings.TrimPrefix(refPath, "#/components/schemas/")] = s
			} else {
				result = append(result, resolvedSchema)
			}
		}
		return result
	}
	if len(ref.Value.AllOf) > 0 {
		schema["allOf"] = handleComposition(ref.Value.AllOf)
	}
	if len(ref.Value.AnyOf) > 0 {
		schema["anyOf"] = handleComposition(ref.Value.AnyOf)
	}
	if len(ref.Value.OneOf) > 0 {
		schema["oneOf"] = handleComposition(ref.Value.OneOf)
	}

	return refPath, schema
}

// type ParameterIn string

// const (
// ParameterInPath ParameterIn = "path" //Path parameter.
// ParameterInQuery ParameterIn = "query" // Query parameters.
// ParameterInHeader ParameterIn = "header" // Header parameter.
// ParameterInCookie ParameterIn = "cookie" // Cookie parameter.
// ParameterInBody ParameterIn = "body" //Request body parameters.
// )

type OpenAPIDataType string

const (
	ContentDataType OpenAPIDataType = "content"
	FileDataType    OpenAPIDataType = "file"
)

// GetHash Gets the hash.
// func GetHash(path, method string) (hash string, err error) {
// 	type hashGenerator struct {
// 		Path    string `json:"path"`
// 		Method  string `json:"method"`

// 	}
// 	hash, err = utils.ObjectMD5Hash(&hashGenerator{
// 		Path:   path,
// 		Method: method,
// 	})
// 	return
// }

// Summary string `json:"summary"` na

// GetVersion Get version.
// func GetVersion(path, method, title, summary string) (version string, err error) {
// 	type versionGenerator struct {
// 		Path    string `json:"path"`
// 		Method  string `json:"method"`
// 		Summary string `json:"summary"`
// 	}
// 	version, err = utils.ObjectMD5Hash(&versionGenerator{
// 		Path:    path,
// 		Method:  method,
// 		Title:   title,
// 		Summary: summary,
// 	})
// 	return
// }

type openAPIParser struct {
	Loader    *openapi3.Loader  // loader.
	Doc       *openapi3.T       // OpenAPI documentation.
	DataType  string            // data type.
	DataValue interface{}       // data value.
	SubParser []*openapi3.T     // subparser.
	Logger    interfaces.Logger // Logger.
}

// LoadOpenAPIMetadata loads OpenAPI metadata.
func LoadOpenAPIMetadata(ctx context.Context, dataType string, dataValue interface{}, logger interfaces.Logger) (metadatas []*APIMetadata, err error) {
	p := &openAPIParser{
		Loader:    openapi3.NewLoader(),
		DataType:  dataType,
		DataValue: dataValue,
		Logger:    logger,
	}
	// ParseAndValidateOpenAPI parses and verifies injected OpenAPI data.
	err = p.parseAndValidateOpenAPI(ctx)
	if err != nil {
		return
	}
	// Split OpenAPI documentation.
	err = p.splitOpenAPIDocument(ctx)
	if err != nil {
		return
	}
	fmt.Println(len(p.SubParser))
	metadatas = make([]*APIMetadata, 0, len(p.SubParser))
	for _, doc := range p.SubParser {
		// Parse OpenAPI documentation.
		metadata, err := p.getAPIMetadata(doc)
		if err != nil {
			return nil, err
		}
		metadatas = append(metadatas, metadata)
	}
	data, _ := jsoniter.Marshal(metadatas)
	fmt.Println(string(data))
	return
}

// getAPIMetadatas Get API metadata.
func (p *openAPIParser) getAPIMetadata(doc *openapi3.T) (metadata *APIMetadata, err error) {
	for path, pathItem := range doc.Paths.Map() {
		for method, operation := range pathItem.Operations() {
			// Convert structured parameters.
			parameters := make([]*Parameter, 0)
			for _, param := range operation.Parameters {
				ref, paramSchema := schemaRefToMap(param.Value.Schema, doc.Components, make(map[string]bool))
				parameters = append(parameters, &Parameter{
					Name:        param.Value.Name,
					In:          param.Value.In,
					Description: param.Value.Description,
					Required:    param.Value.Required,
					Ref:         ref,
					Schema:      paramSchema,
				})
			}
			// Process request body.
			var requestBody *RequestBody
			if operation.RequestBody != nil {
				reqContent := make(map[string]Content)
				for contentType, content := range operation.RequestBody.Value.Content {
					ref, contentSchema := schemaRefToMap(content.Schema, doc.Components, make(map[string]bool))
					reqContent[contentType] = Content{
						Ref:     ref,
						Schema:  contentSchema,
						Example: content.Example,
					}
				}
				requestBody = &RequestBody{
					Description: operation.RequestBody.Value.Description,
					Content:     reqContent,
				}
			}

			// Handle response.
			var responses []*Response
			for statusCode, resp := range operation.Responses.Map() {
				respContent := make(map[string]Content)
				for contentType, content := range resp.Value.Content {
					ref, contentSchema := schemaRefToMap(content.Schema, doc.Components, make(map[string]bool))
					respContent[contentType] = Content{
						Ref:     ref,
						Schema:  contentSchema,
						Example: content.Example,
					}
				}
				responses = append(responses, &Response{
					StatusCode:  statusCode,
					Description: *resp.Value.Description,
					Content:     respContent,
				})
			}
			metadata = &APIMetadata{
				Title:       doc.Info.Title,
				Summary:     operation.Summary,
				Description: operation.Description,
				Path:        path,
				Method:      method,
				CreateTime:  time.Now().UnixNano(),
				UpdateTime:  time.Now().UnixNano(),
				IsDeleted:   false,
				Parameters:  parameters,
				RequestBody: requestBody,
				Responses:   responses,
			}
			// metadata.Hash, err = GetHash(metadata.Path, metadata.Method)
			// if err != nil {
			// 	return
			// }
			// metadata.Version, err = GetVersion(metadata.Path, metadata.Method, metadata.Title, metadata.Summary)
			// if err != nil {
			// 	return
			// }
		}
	}
	return
}

// ParseOpenAPIFromData parses OpenAPI data.
func (p *openAPIParser) parseAndValidateOpenAPI(ctx context.Context) (err error) {
	switch p.DataType {
	case string(ContentDataType):
		p.Doc, err = p.Loader.LoadFromData(p.DataValue.([]byte))
	case string(FileDataType):
		p.Doc, err = p.Loader.LoadFromFile(p.DataValue.(string))
	default:
		err = fmt.Errorf("unsupported data type: %s", p.DataType)
	}
	if err != nil {
		p.Logger.WithContext(ctx).Warnf("Failed to load OpenAPI document: %v", err)
		return
	}
	err = p.Doc.Validate(p.Loader.Context)
	if err != nil {
		p.Logger.WithContext(ctx).Warnf("Failed to validate OpenAPI document: %v", err)
	}
	return
}

// SplitOpenAPIDocument splits the OpenAPI document.
func (p *openAPIParser) splitOpenAPIDocument(ctx context.Context) (err error) {
	if p.Doc == nil {
		err = fmt.Errorf("OpenAPI document is nil")
		return
	}
	// Split batch imported OpenAPI into multiple.
	for path, pathItem := range p.Doc.Paths.Map() {
		// Create new lite version of OpenAPI documentation.
		newDoc := &openapi3.T{
			OpenAPI: p.Doc.OpenAPI,
			Info:    p.Doc.Info,
			Servers: p.Doc.Servers,
			Components: &openapi3.Components{
				SecuritySchemes: p.Doc.Components.SecuritySchemes,
				Schemas:         make(map[string]*openapi3.SchemaRef),
			},
			Paths:    openapi3.NewPaths(openapi3.WithPath(path, pathItem)),
			Security: p.Doc.Security,
		}
		// Automatically collect dependent schema.
		for _, op := range pathItem.Operations() {
			if op.RequestBody != nil {
				collectSchemas(p.Doc.Components, op.RequestBody.Value.Content, newDoc.Components.Schemas, make(map[string]bool))
			}
			for _, resp := range op.Responses.Map() {
				collectSchemas(p.Doc.Components, resp.Value.Content, newDoc.Components.Schemas, make(map[string]bool))
			}
		}
		err = newDoc.Validate(p.Loader.Context)
		if err != nil {
			p.Logger.WithContext(ctx).Warnf("Failed to validate OpenAPI document: %v", err)
			return
		}
		p.SubParser = append(p.SubParser, newDoc)
	}
	return
}

// Collect all nested schema references (add visited parameter)
func collectSchemas(docComponents *openapi3.Components, content openapi3.Content, schemas map[string]*openapi3.SchemaRef, visited map[string]bool) {
	for _, mediaType := range content {
		if mediaType.Schema != nil {
			// Keep original schema reference.
			if mediaType.Schema.Ref != "" {
				refKey := strings.TrimPrefix(mediaType.Schema.Ref, "#/components/schemas/")
				if _, exists := schemas[refKey]; !exists {
					schemas[refKey] = mediaType.Schema
				}
			}
			traverseSchema(docComponents, mediaType.Schema, schemas, visited)
		}
	}
}

// Recursively traverse the schema (add visited parameters to track reference paths)
func traverseSchema(docComponents *openapi3.Components, schemaRef *openapi3.SchemaRef, schemas map[string]*openapi3.SchemaRef, visited map[string]bool) {
	if schemaRef == nil || schemaRef.Value == nil {
		return
	}

	// Handling direct references.
	if schemaRef.Ref != "" {
		refKey := strings.TrimPrefix(schemaRef.Ref, "#/components/schemas/")
		if visited[refKey] {
			return
		}

		if _, exists := schemas[refKey]; !exists {
			if origSchema, exists := docComponents.Schemas[refKey]; exists {
				visited[refKey] = true
				schemas[refKey] = origSchema
				// Make sure to keep all nested references.
				if origSchema.Ref != "" {
					nestedRefKey := strings.TrimPrefix(origSchema.Ref, "#/components/schemas/")
					if _, exists := schemas[nestedRefKey]; !exists {
						schemas[nestedRefKey] = origSchema
					}
				}
				traverseSchema(docComponents, origSchema, schemas, visited)
				delete(visited, refKey)
			}
		}
		return
	}

	// Passing visited parameters when working with composite structures, object properties, and array items.
	schema := schemaRef.Value
	for _, s := range schema.AllOf {
		traverseSchema(docComponents, s, schemas, visited)
	}
	for _, s := range schema.AnyOf {
		traverseSchema(docComponents, s, schemas, visited)
	}
	for _, s := range schema.OneOf {
		traverseSchema(docComponents, s, schemas, visited)
	}
	for _, prop := range schema.Properties {
		traverseSchema(docComponents, prop, schemas, visited)
	}
	if schema.Items != nil {
		traverseSchema(docComponents, schema.Items, schemas, visited)
	}
}
