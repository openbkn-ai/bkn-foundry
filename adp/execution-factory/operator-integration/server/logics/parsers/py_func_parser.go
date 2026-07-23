package parsers

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-python/gpython/ast"
	"github.com/go-python/gpython/parser"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
)

const (
	sandboxSDKModule  = "sandbox_sdk"
	toolDecoratorName = "tool"
	handlerFuncName   = "handler"
)

// 解析不了时的兜底判定。放宽而不是收紧：沙箱跑得动的代码不能被这里挡死。
var (
	handlerEntryPattern = regexp.MustCompile(`def\s+handler\s*\(`)
	toolEntryPattern    = regexp.MustCompile(`(?m)^\s*@(?:\w+\.)?tool\b`)
)

// pythonFunctionParser Python 函数解析器
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
	// Code 校验
	if input.Code == "" {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, "python function code is empty")
		return
	}
	// 校验参数定义
	err = p.Validator.ValidatorStruct(ctx, input)
	if err != nil {
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

// hasEntryPoint 判断代码是否有入口函数。
//
// 两种写法：@tool 装饰的普通函数（沙箱 SDK 把 event 解包成形参），或
// handler(event)（AWS Lambda 风格）。判定必须与沙箱执行期一致 —— 保存时放行、
// 执行时找不到入口,或者反过来保存时拒掉沙箱支持的写法,两种都是错的。
//
// 因此和沙箱一样按 AST 判断,并且只认来自 sandbox_sdk 的 tool：tool 也是
// LangChain 的装饰器名,还可能是用户自己的函数名,只比对名字会误判。
func hasEntryPoint(code string) bool {
	mod, err := parser.ParseString(code, "exec")
	if err != nil {
		// gpython 只到 Python 3.4 级语法,f-string、async def、PEP 526 注解
		// （pydantic 模型的类体写法）都解析不了。这些代码在沙箱里跑得好好的,
		// 判成「没有入口」会把它们挡在保存之外,而且报错原因还是错的。
		// 解析不了时退回宽松正则。
		return entryPatternFallback(code)
	}
	return moduleHasEntryPoint(mod)
}

// entryPatternFallback 只看形状不看来源,会把别的库的 @tool 也算成入口。
// 相比之下漏放行的代价更大 —— 这里放行了,执行期至多报「找不到入口」;
// 这里拒了,用户根本存不进去。
func entryPatternFallback(code string) bool {
	return handlerEntryPattern.MatchString(code) || toolEntryPattern.MatchString(code)
}

func moduleHasEntryPoint(mod ast.Ast) bool {
	// 先收集 sandbox_sdk 绑定的名字
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

// isSandboxSDKTool 判断一个装饰器表达式是否是 sandbox_sdk 的 tool。
func isSandboxSDKTool(expr ast.Expr, sdkToolNames, sdkModuleNames map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.Call:
		// @tool(name="...")
		return isSandboxSDKTool(e.Func, sdkToolNames, sdkModuleNames)
	case *ast.Name:
		id := string(e.Id)
		// @tool，且 tool 来自 sandbox_sdk
		if sdkToolNames[id] {
			return true
		}
		// gpython 把 @sandbox_sdk.tool 归约成一个 Name（Id 含点），
		// 不像 CPython 那样给出 Attribute
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

// 检查是否包含入口函数
func checkRegexpHandler(ctx context.Context, code string) (err error) {
	if hasEntryPoint(code) {
		return nil
	}
	return errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtFunctionNoHandlerFound,
		"python function must define a @tool decorated function or a handler(event) function")
}

// func checAstkHandler(ctx context.Context, code string) (err error) {
// 	// 解析Python代码
// 	mod, err := parser.ParseString(code, py.ExecMode)
// 	if err != nil {
// 		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf("parse python code failed: %v", err))
// 		return
// 	}
// 	// 检查是否包含入口函数handler
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

// Parse 解析 Python 函数
func (p *pythonFunctionParser) Parse(ctx context.Context, inputValue any) (metadatas []interfaces.IMetadataDB, err error) {
	// 记录可观测性
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

// GetAllContent 获取所有内容
func (p *pythonFunctionParser) GetAllContent(ctx context.Context, inputValue any) (content any, err error) {
	input, err := p.validate(ctx, inputValue)
	if err != nil {
		return nil, err
	}
	// 与保存路径用同一套入口判定,含解析失败时的兜底。
	// 不在这里因为 gpython 解析不了就报语法错 —— 它只到 Python 3.4,
	// f-string 之类沙箱跑得动的代码会被误判。真正的语法错由沙箱执行时报出。
	if err = checkRegexpHandler(ctx, input.Code); err != nil {
		return
	}
	content = convertToPathItemContent(input)
	return
}

// 将input\output转换成 PathItemContent
func convertToPathItemContent(input *interfaces.FunctionInput) (result *interfaces.PathItemContent) {
	result = &interfaces.PathItemContent{
		Path:        interfaces.GetAOIFuncExecPath(),
		Method:      http.MethodPost,
		ServerURL:   interfaces.AOIServerURL,
		Summary:     input.Name,
		Description: input.Description,
		APISpec:     &interfaces.APISpec{},
	}
	// 添加超时时间参数
	result.APISpec.Parameters = createParameter()
	// 根据处理输入参数创建请求体
	result.APISpec.RequestBody = createRequestBody(input.Inputs)
	// 处理输出参数
	result.APISpec.Responses = createResponseBody(input.Outputs)
	return
}

// 构造Parameter参数
//
// api_spec 只描述用户声明的契约。执行超时是沙箱的基础设施开关,曾经作为
// query 参数写进这里,结果 Agent 把它当成一个可选业务入参去推断,按 schema
// 渲染参数表的界面也会把它和真实入参并列展示,还要用户为它选固定值或动态输入。
//
// 执行侧仍然接受 query 里的 timeout（见 FunctionExecuteProxyReq），
// 只是不再宣告成工具契约的一部分。
func createParameter() []*interfaces.Parameter {
	return make([]*interfaces.Parameter, 0)
}

// 构造请求体结构
func createRequestBody(inputs []*interfaces.ParameterDef) *interfaces.RequestBody {
	// 创建schema定义
	requestBodySchema := openapi3.NewObjectSchema()
	if len(inputs) > 0 {
		for _, input := range inputs {
			propertySchema := createParameterSchema(input)
			requestBodySchema.Properties[input.Name] = openapi3.NewSchemaRef("", propertySchema)
			// 设置必填字段
			if input.Required {
				requestBodySchema.Required = append(requestBodySchema.Required, input.Name)
			}
		}
	}
	// 创建请求体
	requestBody := &interfaces.RequestBody{
		Description: "函数输入参数",
		Content:     openapi3.NewContentWithJSONSchema(requestBodySchema),
		Required:    true,
	}
	return requestBody
}

// 处理输出参数
// 根据处理输出参数创建响应体
func createResponseBody(outputs []*interfaces.ParameterDef) []*interfaces.Response {
	// 创建schema定义
	responseSchema := openapi3.NewObjectSchema()
	responseSchema.Properties["stdout"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Description: "标准输出流内容",
	})
	responseSchema.Properties["stderr"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Description: "标准错误流内容",
	})

	resultSchema := &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeObject},
		Description: "Handler 函数返回的业务结果: any or null",
		Properties:  make(openapi3.Schemas),
	}
	for _, output := range outputs {
		propertySchema := createParameterSchema(output)
		resultSchema.Properties[output.Name] = openapi3.NewSchemaRef("", propertySchema)
		// 设置必填字段
		if output.Required {
			resultSchema.Required = append(resultSchema.Required, output.Name)
		}
	}
	responseSchema.Properties["result"] = openapi3.NewSchemaRef("", resultSchema)
	// 添加指标
	metricsSchema := &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeObject},
		Description: "指标",
		Properties:  make(openapi3.Schemas),
	}
	metricsSchema.Properties["duration_ms"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeNumber},
		Description: "执行总耗时（毫秒）",
	})
	metricsSchema.Properties["memory_peak_mb"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeNumber},
		Description: "峰值内存占用（MB）",
	})
	metricsSchema.Properties["cpu_time_ms"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeNumber},
		Description: "CPU 时间（毫秒）",
	})
	responseSchema.Properties["metrics"] = openapi3.NewSchemaRef("", metricsSchema)
	// 添加错误响应体
	errSchema := &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeObject},
		Description: "失败详情",
		Properties:  map[string]*openapi3.SchemaRef{},
	}
	errSchema.Properties["code"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Description: "错误码",
	})
	errSchema.Properties["description"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Description: "错误描述",
	})
	errSchema.Properties["detail"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeObject},
		Description: "错误详情",
	})
	errSchema.Properties["solution"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Description: "错误解决办法",
	})
	errSchema.Properties["link"] = openapi3.NewSchemaRef("", &openapi3.Schema{
		Type:        &openapi3.Types{openapi3.TypeString},
		Description: "错误链接",
	})
	// 创建响应体
	responseBody := []*interfaces.Response{
		{
			StatusCode:  "200",
			Description: "成功",
			Content:     openapi3.NewContentWithJSONSchema(responseSchema),
		},
		{
			StatusCode:  "400",
			Description: "参数校验失败",
			Content:     openapi3.NewContentWithJSONSchema(errSchema),
		},
		{
			StatusCode:  "404",
			Description: "资源不存在",
			Content:     openapi3.NewContentWithJSONSchema(errSchema),
		},
		{
			StatusCode:  "500",
			Description: "函数执行失败",
			Content:     openapi3.NewContentWithJSONSchema(errSchema),
		},
	}
	return responseBody
}

// mapTypeToOpenAPI 将参数类型映射到OpenAPI类型
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

	// 设置默认值
	if param.Default != nil {
		propertySchema.Default = param.Default
	}
	// 设置枚举值
	if len(param.Enum) > 0 {
		propertySchema.Enum = param.Enum
	}
	// 设置示例值
	if param.Example != nil {
		propertySchema.Example = param.Example
	}
	// 处理嵌套参数
	if len(param.SubParameters) > 0 {
		switch param.Type {
		case interfaces.ParameterTypeObject:
			// Object类型：SubParameters定义对象的属性
			propertySchema.Properties = make(openapi3.Schemas)
			for _, subParam := range param.SubParameters {
				subPropertySchema := createParameterSchema(subParam)
				propertySchema.Properties[subParam.Name] = openapi3.NewSchemaRef("", subPropertySchema)
				// 子对象的必填字段需要添加到父对象的Required数组中
				if subParam.Required {
					propertySchema.Required = append(propertySchema.Required, subParam.Name)
				}
			}

		case interfaces.ParameterTypeArray:
			// Array类型：SubParameters只包含一个元素，定义数组元素的结构
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
