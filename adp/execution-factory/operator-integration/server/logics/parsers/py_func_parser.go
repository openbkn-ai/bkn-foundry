package parsers

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-python/gpython/ast"
	"github.com/go-python/gpython/parser"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

const (
	sandboxSDKModule  = "sandbox_sdk"
	toolDecoratorName = "tool"
	handlerFuncName   = "handler"
)

// Fallback detection for syntax the bundled parser cannot read. It is deliberately
// permissive so code supported by the sandbox is not rejected here.
var (
	handlerEntryPattern = regexp.MustCompile(`def\s+handler\s*\(`)
	toolEntryPattern    = regexp.MustCompile(`(?m)^\s*@(?:\w+\.)?tool\b`)
)

// pythonFunctionParser parses Python function metadata.
type pythonFunctionParser struct {
	Logger    interfaces.Logger
	Validator interfaces.Validator
}

func (p *pythonFunctionParser) Type() interfaces.MetadataType {
	return interfaces.MetadataTypeFunc
}

func (p *pythonFunctionParser) validate(ctx context.Context, inputValue any) (input *interfaces.FunctionInput, err error) {
	input, ok := inputValue.(*interfaces.FunctionInput)
	if !ok {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, "input value is not *interfaces.FunctionInput")
		return
	}
	if input == nil {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, "input content is empty")
		return
	}
	// Validate the function source.
	if input.Code == "" {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, "python function code is empty")
		return
	}
	// Validate parameter definitions.
	err = p.Validator.ValidatorStruct(ctx, input)
	if err != nil {
		return
	}
	if err = validateParameterDescriptions(ctx, input); err != nil {
		return
	}
	if input.Inputs == nil {
		input.Inputs = make([]*interfaces.ParameterDef, 0)
	}
	for _, param := range input.Inputs {
		err = p.Validator.VisitorParameterDef(ctx, param)
		if err != nil {
			return
		}
	}
	return
}

// hasEntryPoint reports whether the source defines a supported entry point.
//
// The sandbox accepts either a regular function decorated with @tool (the sandbox SDK
// expands event into arguments) or an AWS Lambda-style handler(event). Validation must
// agree with runtime behavior in both directions.
//
// AST detection therefore recognizes tool only when imported from sandbox_sdk. Other
// libraries and user code may use the same name and must not be treated as entry points.
func hasEntryPoint(code string) bool {
	mod, err := parser.ParseString(code, "exec")
	if err != nil {
		// gpython supports syntax only through Python 3.4, so it cannot parse f-strings,
		// async functions, or PEP 526 annotations such as Pydantic model fields. These are
		// valid in the sandbox; fall back to permissive patterns instead of rejecting them.
		return entryPatternFallback(code)
	}
	return moduleHasEntryPoint(mod)
}

// entryPatternFallback checks syntax shape rather than import origin and may accept
// another library's @tool. A false positive can still be diagnosed by the runtime,
// whereas a false negative prevents the user from saving otherwise valid code.
func entryPatternFallback(code string) bool {
	return handlerEntryPattern.MatchString(code) || toolEntryPattern.MatchString(code)
}

func moduleHasEntryPoint(mod ast.Ast) bool {
	// Collect names bound to sandbox_sdk first.
	sdkToolNames := map[string]bool{}   // from sandbox_sdk import tool [as x]
	sdkModuleNames := map[string]bool{} // import sandbox_sdk [as x]
	ast.Walk(mod, func(node ast.Ast) bool {
		switch n := node.(type) {
		case *ast.ImportFrom:
			if string(n.Module) != sandboxSDKModule {
				return true
			}
			for _, alias := range n.Names {
				if string(alias.Name) != toolDecoratorName {
					continue
				}
				if alias.AsName != "" {
					sdkToolNames[string(alias.AsName)] = true
				} else {
					sdkToolNames[string(alias.Name)] = true
				}
			}
		case *ast.Import:
			for _, alias := range n.Names {
				if string(alias.Name) != sandboxSDKModule {
					continue
				}
				if alias.AsName != "" {
					sdkModuleNames[string(alias.AsName)] = true
				} else {
					sdkModuleNames[string(alias.Name)] = true
				}
			}
		}
		return true
	})

	var found bool
	ast.Walk(mod, func(node ast.Ast) bool {
		fn, ok := node.(*ast.FunctionDef)
		if !ok {
			return true
		}
		if string(fn.Name) == handlerFuncName {
			found = true
			return true
		}
		for _, deco := range fn.DecoratorList {
			if isSandboxSDKTool(deco, sdkToolNames, sdkModuleNames) {
				found = true
				return true
			}
		}
		return true
	})
	return found
}

// isSandboxSDKTool reports whether a decorator expression refers to sandbox_sdk.tool.
func isSandboxSDKTool(expr ast.Expr, sdkToolNames, sdkModuleNames map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.Call:
		// @tool(name="...")
		return isSandboxSDKTool(e.Func, sdkToolNames, sdkModuleNames)
	case *ast.Name:
		id := string(e.Id)
		// A bare @tool imported from sandbox_sdk.
		if sdkToolNames[id] {
			return true
		}
		// Unlike CPython, gpython represents @sandbox_sdk.tool as a Name whose ID contains a dot.
		if base, attr, ok := strings.Cut(id, "."); ok {
			return attr == toolDecoratorName && sdkModuleNames[base]
		}
		return false
	case *ast.Attribute:
		// @sandbox_sdk.tool
		if string(e.Attr) != toolDecoratorName {
			return false
		}
		base, ok := e.Value.(*ast.Name)
		return ok && sdkModuleNames[string(base.Id)]
	}
	return false
}

// checkRegexpHandler validates that the source has a supported entry point.
func checkRegexpHandler(ctx context.Context, code string) (err error) {
	if hasEntryPoint(code) {
		return nil
	}
	return errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtFunctionNoHandlerFound,
		"python function must define a @tool decorated function or a handler(event) function")
}

// func checAstkHandler(ctx context.Context, code string) (err error) {
// 	// Parse the Python source.
// 	mod, err := parser.ParseString(code, py.ExecMode)
// 	if err != nil {
// 		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf("parse python code failed: %v", err))
// 		return
// 	}
// 	// Check for a handler entry point.
// 	var hasHandler bool
// 	ast.Walk(mod, func(node ast.Ast) bool {
// 		n, ok := node.(*ast.FunctionDef)
// 		if ok && n.Name == "handler" {
// 			hasHandler = true
// 		}
// 		return true
// 	})
// 	if !hasHandler {
// 		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, "python function must have a handler function")
// 	}
// 	return
// }

// Parse converts Python function input into persisted metadata.
func (p *pythonFunctionParser) Parse(ctx context.Context, inputValue any) (metadatas []interfaces.IMetadataDB, err error) {
	// Record the operation span.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	input, err := p.validate(ctx, inputValue)
	if err != nil {
		return nil, err
	}
	err = checkRegexpHandler(ctx, input.Code)
	if err != nil {
		return nil, err
	}
	pathItem := convertToPathItemContent(input)
	desc := pathItem.Description
	if desc == "" {
		desc = pathItem.Summary
	}
	metadatas = make([]interfaces.IMetadataDB, 0)
	metadataDB := &model.FunctionMetadataDB{
		ScriptType:      string(input.ScriptType),
		Code:            input.Code,
		Dependencies:    utils.ObjectToJSON(input.Dependencies),
		DependenciesURL: input.DependenciesURL,
		Summary:         pathItem.Summary,
		Description:     desc,
		Path:            pathItem.Path,
		ServerURL:       pathItem.ServerURL,
		Method:          pathItem.Method,
		APISpec:         pathItem.APISpec.ToJSON(),
	}
	metadatas = append(metadatas, metadataDB)
	return
}

// GetAllContent returns the complete generated API contract.
func (p *pythonFunctionParser) GetAllContent(ctx context.Context, inputValue any) (content any, err error) {
	input, err := p.validate(ctx, inputValue)
	if err != nil {
		return nil, err
	}
	// Use the same entry-point detection as the persistence path, including its parser
	// fallback. Syntax supported by the sandbox but not gpython must not be rejected here;
	// the sandbox remains responsible for reporting actual syntax errors.
	if err = checkRegexpHandler(ctx, input.Code); err != nil {
		return
	}
	content = convertToPathItemContent(input)
	return
}

// convertToPathItemContent converts function inputs and outputs into an API contract.
func convertToPathItemContent(input *interfaces.FunctionInput) (result *interfaces.PathItemContent) {
	result = &interfaces.PathItemContent{
		Path:        interfaces.GetAOIFuncExecPath(),
		Method:      http.MethodPost,
		ServerURL:   interfaces.AOIServerURL,
		Summary:     input.Name,
		Description: input.Description,
		APISpec:     &interfaces.APISpec{},
	}
	// Add infrastructure parameters declared by the public contract.
	result.APISpec.Parameters = createParameter()
	// Build the request body from the declared inputs.
	result.APISpec.RequestBody = createRequestBody(input.Inputs)
	// Build responses from the declared outputs.
	result.APISpec.Responses = createResponseBody(input.Outputs)
	return
}

// createParameter returns infrastructure parameters exposed in the API contract.
//
// api_spec describes only the user-declared contract. Execution timeout is a sandbox
// infrastructure option. Exposing it as a query parameter caused agents and schema-driven
// UIs to treat it as an optional business input.
//
// The execution endpoint still accepts timeout in the query string (see
// FunctionExecuteProxyReq), but it is no longer advertised as part of the tool contract.
func createParameter() []*interfaces.Parameter {
	return make([]*interfaces.Parameter, 0)
}

// createRequestBody builds the request-body schema.
func createRequestBody(inputs []*interfaces.ParameterDef) *interfaces.RequestBody {
	// Build the object schema.
	requestBodySchema := openapi3.NewObjectSchema()
	if len(inputs) > 0 {
		for _, input := range inputs {
			propertySchema := createParameterSchema(input)
			requestBodySchema.Properties[input.Name] = openapi3.NewSchemaRef("", propertySchema)
			// Record required fields.
			if input.Required {
				requestBodySchema.Required = append(requestBodySchema.Required, input.Name)
			}
		}
	}
	// Build the request body.
	requestBody := &interfaces.RequestBody{
		Description: "Function input parameters",
		Content:     openapi3.NewContentWithJSONSchema(requestBodySchema),
		Required:    true,
	}
	return requestBody
}

// createResponseBody builds response schemas from the declared outputs.
func createResponseBody(outputs []*interfaces.ParameterDef) []*interfaces.Response {
	// Build the successful response schema.
	responseSchema := openapi3.NewObjectSchema()
	responseSchema.Properties["stdout"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Description: "Standard output stream content",
	})
	responseSchema.Properties["stderr"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Description: "Standard error stream content",
	})

	resultSchema := &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeObject},
		Description: "Business result returned by the handler: any value or null",
		Properties:  make(openapi3.Schemas),
	}
	for _, output := range outputs {
		propertySchema := createParameterSchema(output)
		resultSchema.Properties[output.Name] = openapi3.NewSchemaRef("", propertySchema)
		// Record required fields.
		if output.Required {
			resultSchema.Required = append(resultSchema.Required, output.Name)
		}
	}
	responseSchema.Properties["result"] = openapi3.NewSchemaRef("", resultSchema)
	// Add execution metrics.
	metricsSchema := &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeObject},
		Description: "Execution metrics",
		Properties:  make(openapi3.Schemas),
	}
	metricsSchema.Properties["duration_ms"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeNumber},
		Description: "Total execution duration (milliseconds)",
	})
	metricsSchema.Properties["memory_peak_mb"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeNumber},
		Description: "Peak memory usage (MB)",
	})
	metricsSchema.Properties["cpu_time_ms"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeNumber},
		Description: "CPU time (milliseconds)",
	})
	responseSchema.Properties["metrics"] = openapi3.NewSchemaRef("", metricsSchema)
	// Add the error response schema.
	errSchema := &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeObject},
		Description: "Failure details",
		Properties:  map[string]*openapi3.SchemaRef{},
	}
	errSchema.Properties["code"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Description: "Error code",
	})
	errSchema.Properties["description"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Description: "Error description",
	})
	errSchema.Properties["detail"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeObject},
		Description: "Error details",
	})
	errSchema.Properties["solution"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Description: "Error solution",
	})
	errSchema.Properties["link"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Description: "Error link",
	})
	// Build the response list.
	responseBody := []*interfaces.Response{
		{
			StatusCode:  "200",
			Description: "Success",
			Content:     openapi3.NewContentWithJSONSchema(responseSchema),
		},
		{
			StatusCode:  "400",
			Description: "Parameter validation failed",
			Content:     openapi3.NewContentWithJSONSchema(errSchema),
		},
		{
			StatusCode:  "404",
			Description: "Resource not found",
			Content:     openapi3.NewContentWithJSONSchema(errSchema),
		},
		{
			StatusCode:  "500",
			Description: "Function execution failed",
			Content:     openapi3.NewContentWithJSONSchema(errSchema),
		},
	}
	return responseBody
}

// mapTypeToOpenAPI maps a function parameter type to an OpenAPI type.
func mapTypeToOpenAPI(paramType string) *openapi3.Types {
	switch strings.ToLower(paramType) {
	case "string":
		return &openapi3.Types{openapi3.TypeString}
	case "int", "integer", "number":
		return &openapi3.Types{openapi3.TypeNumber}
	case "float", "double":
		return &openapi3.Types{openapi3.TypeNumber}
	case "bool", "boolean":
		return &openapi3.Types{openapi3.TypeBoolean}
	case "array":
		return &openapi3.Types{openapi3.TypeArray}
	case "object":
		return &openapi3.Types{openapi3.TypeObject}
	default:
		return &openapi3.Types{openapi3.TypeString}
	}
}

func createParameterSchema(param *interfaces.ParameterDef) *openapi3.Schema {
	if param.Description == "" {
		param.Description = param.Name
	}
	propertySchema := &openapi3.Schema{
		Type:        mapTypeToOpenAPI(string(param.Type)),
		Description: param.Description,
	}

	// Preserve the default value.
	if param.Default != nil {
		propertySchema.Default = param.Default
	}
	// Preserve enum values.
	if len(param.Enum) > 0 {
		propertySchema.Enum = param.Enum
	}
	// Preserve the example value.
	if param.Example != nil {
		propertySchema.Example = param.Example
	}
	// Convert nested parameters.
	if len(param.SubParameters) > 0 {
		switch param.Type {
		case interfaces.ParameterTypeObject:
			// Object sub-parameters define object properties.
			propertySchema.Properties = make(openapi3.Schemas)
			for _, subParam := range param.SubParameters {
				subPropertySchema := createParameterSchema(subParam)
				propertySchema.Properties[subParam.Name] = openapi3.NewSchemaRef("", subPropertySchema)
				// Required child fields belong to the parent object's required list.
				if subParam.Required {
					propertySchema.Required = append(propertySchema.Required, subParam.Name)
				}
			}

		case interfaces.ParameterTypeArray:
			// An array has one sub-parameter that defines its item schema.
			subParam := param.SubParameters[0]
			if subParam.Description == "" {
				subParam.Description = param.Description
			}
			itemsSchema := createParameterSchema(subParam)
			propertySchema.Items = openapi3.NewSchemaRef("", itemsSchema)

		case interfaces.ParameterTypeString, interfaces.ParameterTypeNumber, interfaces.ParameterTypeBoolean:
		}
	}
	return propertySchema
}

// validateParameterDescriptions refuses a function whose parameters say nothing
// about themselves.
//
// A parameter description is the only place a caller can learn what a value has
// to be before sending it. Not its type - the schema carries that - but its
// meaning: that demand_end has to equal the forecast order's enddate rather than
// its startdate, that a product code is the BOM root and an intermediate material
// will not do. Every one of those is discoverable today only by calling and
// reading the failure.
//
// The link from a docstring to the agent already works: infer-schema lifts it,
// the catalogue schema stores it, get_action_info delivers it. Nothing ever
// required anyone to write it, so descriptions filled in once decay as the next
// function ships without them.
//
// This checks that something was written, not that it is right - "demand_end: the
// demand cut-off date" passes and is exactly the kind of restatement that misled
// a caller into sending startdate. Keeping the description accurate stays a human
// job; this only stops the field going empty.
//
// Outputs are left alone deliberately. A missing output description costs a
// reader some guessing; a missing input description costs a failed call. The
// function's own description is left to ValidateOperatorDesc, which already
// refuses it with a localised code of its own.
func validateParameterDescriptions(ctx context.Context, input *interfaces.FunctionInput) error {
	for _, missing := range undescribedParameters(input.Inputs, "", "") {
		return errors.DefaultHTTPError(ctx, http.StatusBadRequest,
			fmt.Sprintf("input parameter %q has no description: state what the value has to be, not just its type", missing))
	}
	return nil
}

// undescribedParameters walks into sub_parameters, because that is where the
// meaning of a composite argument lives. The demands parameter of a real
// published function is an array of objects, and its product and qty fields are
// the ones a caller gets wrong - naming only "demands" would report nothing.
//
// Structural placeholders are skipped. List[X] and Dict[K,V] have no field for
// the author to describe: the SDK invents a child called items or values to
// carry the element type, and no docstring or signature can reach it, so
// demanding a description there would reject a plain List[str] with an error the
// author cannot act on. createParameterSchema already treats the array case this
// way, letting items inherit the parent's description. Their own children are
// still checked - the fields of a List[Demand] element are the author's to name.
func undescribedParameters(params []*interfaces.ParameterDef, parentType, prefix string) []string {
	var missing []string
	for _, param := range params {
		if param == nil {
			continue
		}
		path := param.Name
		if prefix != "" {
			path = prefix + "." + param.Name
		}
		if !isStructuralPlaceholder(param, parentType, len(params)) &&
			strings.TrimSpace(param.Description) == "" {
			missing = append(missing, path)
		}
		missing = append(missing, undescribedParameters(param.SubParameters, string(param.Type), path)...)
	}
	return missing
}

// isStructuralPlaceholder reports whether the SDK invented this node rather than
// the author declaring it: the sole child of an array is its element type, and
// the sole child named values under an object is a Dict's value type.
func isStructuralPlaceholder(param *interfaces.ParameterDef, parentType string, siblings int) bool {
	if siblings != 1 {
		return false
	}
	switch parentType {
	case string(interfaces.ParameterTypeArray):
		return param.Name == "items"
	case string(interfaces.ParameterTypeObject):
		return param.Name == "values"
	}
	return false
}
