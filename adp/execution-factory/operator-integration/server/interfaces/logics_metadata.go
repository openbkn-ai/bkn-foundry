package interfaces

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/getkin/kin-openapi/openapi3"
	jsoniter "github.com/json-iterator/go"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
)

// MetadataInfo metadata information.
type MetadataInfo struct {
	Version         string           `json:"version" validate:"required"`     // version.
	Summary         string           `json:"summary" validate:"required"`     // Summary.
	Description     string           `json:"description"`                     // Description.
	ServerURL       string           `json:"server_url" validate:"required"`  // Service URL.
	Path            string           `json:"path" validate:"required"`        // path.
	Method          string           `json:"method" validate:"required"`      // method.
	CreateTime      int64            `json:"create_time" validate:"required"` // creation time.
	UpdateTime      int64            `json:"update_time" validate:"required"` // Update time.
	CreateUser      string           `json:"create_user" validate:"required"` // Creator.
	UpdateUser      string           `json:"update_user" validate:"required"` // Updater.
	APISpec         *APISpec         `json:"api_spec" validate:"required"`    // OpenAPI format.
	FunctionContent *FunctionContent `json:"function_content,omitempty"`      // Function content.
}

// OpenAPI format definition.

// OpenAPIContent OpenAPI content.
type OpenAPIContent struct {
	SererURL string `json:"server_url" validate:"required"` // Server URL.
	// Info information.
	// @description: information.
	Info *openapi3.Info `json:"info"`
	// PathItems path item content.
	// @description: path item content.
	PathItems []*PathItemContent `json:"path_items"`
}

// GetPathItemByMethodAndPath gets the path item content.
// @description: Get the path item content based on the method and path.
// @param method method.
// @param path path.
// @return []*PathItemContent path item content.
func (o *OpenAPIContent) GetPathItemByMethodAndPath(method, path string) *PathItemContent {
	// Get the specified path item.
	for _, item := range o.PathItems {
		if item.Path != path || item.Method != method {
			continue
		}

		return item
	}
	return nil
}

// PathItemContent path item content.
type PathItemContent struct {
	Summary     string   `json:"summary" validate:"required"`
	Path        string   `json:"path" validate:"required"`
	Method      string   `json:"method" validate:"required"`
	Description string   `json:"description"`
	APISpec     *APISpec `json:"api_spec"`
	ServerURL   string   `json:"server_url" validate:"required"` // Server URL.
	ErrMessage  string   `json:"err_message,omitempty"`
}

// APISpec OpenAPI format.
type APISpec struct {
	Parameters   []*Parameter `json:"parameters"`    // Structured parameters.
	RequestBody  *RequestBody `json:"request_body"`  // Request body structure.
	Responses    []*Response  `json:"responses"`     // response structure.
	Components   *Components  `json:"components"`    // Component definition.
	Callbacks    interface{}  `json:"callbacks"`     // callback function definition.
	Security     interface{}  `json:"security"`      // security requirements.
	Tags         []string     `json:"tags"`          // label.
	ExternalDocs interface{}  `json:"external_docs"` // external documentation.
}

// ToJSON Convert APISpec to JSON string.
func (a *APISpec) ToJSON() string {
	jsonBytes, _ := jsoniter.Marshal(a)
	return string(jsonBytes)
}

// Components component definition.
type Components struct {
	Schemas interface{} `json:"schemas"` // Referenced structure definition.
}

// Parameter parameter type.
type Parameter struct {
	Name        string              `json:"name"`
	In          string              `json:"in"` // path/query/header/cookie
	Description string              `json:"description"`
	Required    bool                `json:"required"`
	Schema      *openapi3.SchemaRef `json:"schema,omitempty"`
	Example     any                 `json:"example,omitempty"`
	Examples    openapi3.Examples   `json:"examples,omitempty"`
	Content     openapi3.Content    `json:"content,omitempty"`
}

// RequestBody request body structure.
type RequestBody struct {
	Description string           `json:"description"`
	Content     openapi3.Content `json:"content"` // Sort by media type.
	Required    bool             `json:"required"`
}

// Response response structure.
type Response struct {
	StatusCode  string           `json:"status_code"` // 200/400 etc.
	Description string           `json:"description"`
	Content     openapi3.Content `json:"content"`
}

// Definition of function related parameters.

// ScriptType script type.
type ScriptType string

const (
	ScriptTypePython ScriptType = "python" // Python script types.
)

// FunctionContent function content definition.
type FunctionContent struct {
	ScriptType      ScriptType       `json:"script_type" form:"script_type" default:"python" validate:"required,oneof=python"` // Script type.
	Code            string           `json:"code" form:"code" validate:"required"`                                             // Python code (required)
	Dependencies    []DependencyInfo `json:"dependencies" form:"dependencies"`                                                 // Dependency list.
	DependenciesURL string           `json:"dependencies_url" form:"dependencies_url"`                                         // Dependency library installation source address.
	// The parameter definition is expanded into the API specification when it is stored, and is decoded back when read, so that the caller can use it in its original form.
	Inputs  []*ParameterDef `json:"inputs,omitempty"`  // Input parameter definition.
	Outputs []*ParameterDef `json:"outputs,omitempty"` // Output parameter definition.
}

// ParameterType parameter type.
type ParameterType string

const (
	ParameterTypeString  ParameterType = "string"  // string type.
	ParameterTypeNumber  ParameterType = "number"  // Numeric type.
	ParameterTypeBoolean ParameterType = "boolean" // Boolean type.
	ParameterTypeArray   ParameterType = "array"   // array type.
	ParameterTypeObject  ParameterType = "object"  // Object type.
)

// ParameterDef parameter definition.
// Supports multi-level nesting through the SubParameters field, applicable to Object and Array types.
type ParameterDef struct {
	Name        string `json:"name"`                  // Parameter name.
	Description string `json:"description,omitempty"` // Parameter description.
	Required    bool   `json:"required"`              // Is it required?.

	// Parameter type: string, number, boolean, array, object.
	Type ParameterType `json:"type,omitempty" validate:"omitempty,oneof=string number boolean array object"` // Parameter type.

	// Simple type of constraint fields.
	Default any   `json:"default,omitempty"` // Default value.
	Enum    []any `json:"enum,omitempty"`    // enum value (optional)
	Example any   `json:"example,omitempty"` // Example value.

	// Nested definitions of complex types.
	// Usage scenarios:
	// - Object type: SubParameters defines the property list of the object.
	// - Array type: SubParameters contains only one element and defines the structure of the array elements.
	// (It is recommended to use "items" for array element names)
	SubParameters []*ParameterDef `json:"sub_parameters,omitempty"` // subparameter list (for object and array types)
}

// FunctionInput function input definition.
type FunctionInput struct {
	// Basic information.
	Name        string `json:"name" form:"name"`                         // function name.
	Description string `json:"description,omitempty" form:"description"` // Function description, used to describe the function and behavior of the function.
	// Parameter definition.
	Inputs  []*ParameterDef `json:"inputs,omitempty" form:"inputs"`   // Input parameter list.
	Outputs []*ParameterDef `json:"outputs,omitempty" form:"outputs"` // Output parameter list.
	// Code related.
	ScriptType      ScriptType        `json:"script_type" form:"script_type" default:"python" validate:"required,oneof=python"` // Script type.
	Code            string            `json:"code" form:"code"`                                                                 // Python code (required)
	Dependencies    []*DependencyInfo `json:"dependencies,omitempty" form:"dependencies"`                                       // Dependency list.
	DependenciesURL string            `json:"dependencies_url,omitempty" form:"dependencies_url"`                               // Dependency library installation source address.
}

// FunctionInputEdit function input edit definition.
type FunctionInputEdit struct {
	// Parameter definition.
	Inputs  []*ParameterDef `json:"inputs,omitempty" form:"inputs"`   // Input parameter list.
	Outputs []*ParameterDef `json:"outputs,omitempty" form:"outputs"` // Output parameter list.
	// Code related.
	ScriptType      ScriptType        `json:"script_type" form:"script_type" default:"python" validate:"required,oneof=python"` // Script type.
	Code            string            `json:"code" form:"code"`                                                                 // Python code (required)
	Dependencies    []*DependencyInfo `json:"dependencies,omitempty" form:"dependencies"`                                       // Dependency list.
	DependenciesURL string            `json:"dependencies_url,omitempty" form:"dependencies_url"`                               // Dependency library installation source address.
}

// OpenAPIInput OpenAPI input definition.
type OpenAPIInput struct {
	// Basic information.
	Data json.RawMessage `json:"data" form:"data"` // Original content (OpenAPI JSON/YAML)
}

// IMetadataService unified metadata management interface.
type IMetadataService interface {
	// Register metadata.
	RegisterMetadata(ctx context.Context, tx *sql.Tx, metadata IMetadataDB) (version string, err error)
	// Register metadata in batches.
	BatchRegisterMetadata(ctx context.Context, tx *sql.Tx, metadatas []IMetadataDB) (versions []string, err error)
	// Query metadata based on version.
	GetMetadataByVersion(ctx context.Context, metadataType MetadataType, version string) (IMetadataDB, error)
	// Query metadata in batches.
	BatchGetMetadata(ctx context.Context, apiVersions, funcVersions []string) ([]IMetadataDB, error)
	// Update metadata.
	UpdateMetadata(ctx context.Context, tx *sql.Tx, metadata IMetadataDB) error
	// Delete metadata.
	// DeleteMetadata(ctx context.Context, tx *sql.Tx, metadataType MetadataType, version string) error
	// Delete metadata in batches.
	BatchDeleteMetadata(ctx context.Context, tx *sql.Tx, metadataType MetadataType, versions []string) error
	// Verify metadata format.
	ValidateMetadata(ctx context.Context, metadata IMetadataDB) error
	// Metadata parsing.
	ParseMetadata(ctx context.Context, metadataType MetadataType, input any) ([]IMetadataDB, error)
	// Get the parsed original content.
	ParseRawContent(ctx context.Context, metadataType MetadataType, input any) (content any, err error)
	// Query metadata based on SourceID and SourceType.
	GetMetadataBySource(ctx context.Context, sourceID string, sourceType model.SourceType) (bool, IMetadataDB, error)
	// Query metadata in batches based on SourceID and SourceType.
	BatchGetMetadataBySourceIDs(ctx context.Context, sourceMap map[model.SourceType][]string) (sourceIDToMetadataMap map[string]IMetadataDB, err error)
	// Checks and returns whether metadata exists.
	CheckMetadataExists(ctx context.Context, metadataType MetadataType, version string) (bool, IMetadataDB, error)
}
