package metadata

import (
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/logics/parsers"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/utils"
)

const (
	legacyTimeoutParamName = "timeout"
	legacyTimeoutParamIn   = "query"
)

// stripLegacyTimeoutParameter 去掉函数元数据里遗留的 timeout 查询参数。
//
// 执行超时曾被当成契约参数写进 api_spec，导致它和业务入参并列出现在
// 工具 schema 里：Agent 会把它当成可传的参数去猜，按 schema 渲染的界面
// 也会要求使用者为它选固定值或动态输入。生成侧已经不再写入，但升级前
// 建的函数工具库里仍有这个字段，这里在读取时剥掉，免去一次数据迁移。
//
// 执行侧仍从请求的 query 读取 timeout，存量调用方不受影响。
func stripLegacyTimeoutParameter(spec *interfaces.APISpec) {
	if spec == nil || len(spec.Parameters) == 0 {
		return
	}
	kept := make([]*interfaces.Parameter, 0, len(spec.Parameters))
	for _, param := range spec.Parameters {
		if param != nil && param.Name == legacyTimeoutParamName && param.In == legacyTimeoutParamIn {
			continue
		}
		kept = append(kept, param)
	}
	spec.Parameters = kept
}

// MetadataDBToStruct 将数据库模型转换为元数据接口
func MetadataDBToStruct(metadataDB interfaces.IMetadataDB) *interfaces.MetadataInfo {
	switch v := metadataDB.(type) {
	case *model.FunctionMetadataDB:
		apiMetadataDB := &model.APIMetadataDB{
			Version:     v.Version,
			Summary:     v.Summary,
			Description: v.Description,
			ServerURL:   v.ServerURL,
			Path:        v.Path,
			Method:      v.Method,
			CreateTime:  v.CreateTime,
			UpdateTime:  v.UpdateTime,
			CreateUser:  v.CreateUser,
			UpdateUser:  v.UpdateUser,
			APISpec:     v.APISpec,
		}
		metadata := apimetadataDBToAPIMetadata(apiMetadataDB)
		stripLegacyTimeoutParameter(metadata.APISpec)
		dependencies := []interfaces.DependencyInfo{}
		if v.GetDependencies() != "" {
			dependencies = utils.JSONToObject[[]interfaces.DependencyInfo](v.GetDependencies())
		}
		// 参数定义落库时被展开进 API 规格,这里反解回来,调用方无需自行解析 OpenAPI
		inputs, outputs := parsers.FunctionParamsFromAPISpec(v.GetAPISpec())
		metadata.FunctionContent = &interfaces.FunctionContent{
			ScriptType:      interfaces.ScriptType(v.GetScriptType()),
			Code:            v.GetCode(),
			Dependencies:    dependencies,
			DependenciesURL: v.GetDependenciesURL(),
			Inputs:          inputs,
			Outputs:         outputs,
		}
		return metadata
	case *model.APIMetadataDB:
		return apimetadataDBToAPIMetadata(v)
	default:
		return nil
	}
}

// DefaultMetadataInfo 获取默认元数据信息
func DefaultMetadataInfo(metadataType interfaces.MetadataType) *interfaces.MetadataInfo {
	metadataInfo := &interfaces.MetadataInfo{}
	switch metadataType {
	case interfaces.MetadataTypeAPI:
		metadataInfo.APISpec = &interfaces.APISpec{}
		return metadataInfo
	case interfaces.MetadataTypeFunc:
		metadataInfo.FunctionContent = &interfaces.FunctionContent{
			Dependencies: []interfaces.DependencyInfo{},
		}
		return metadataInfo
	default:
		return nil
	}
}

// apimetadataDBToAPIMetadata 将数据库模型转换为 API 元数据接口
func apimetadataDBToAPIMetadata(metadataDB *model.APIMetadataDB) *interfaces.MetadataInfo {
	apiSpec := &interfaces.APISpec{}
	_ = utils.StringToObject(metadataDB.APISpec, apiSpec)
	return &interfaces.MetadataInfo{
		Version:     metadataDB.Version,
		Summary:     metadataDB.Summary,
		Description: metadataDB.Description,
		ServerURL:   metadataDB.ServerURL,
		Path:        metadataDB.Path,
		Method:      metadataDB.Method,
		CreateTime:  metadataDB.CreateTime,
		UpdateTime:  metadataDB.UpdateTime,
		CreateUser:  metadataDB.CreateUser,
		UpdateUser:  metadataDB.UpdateUser,
		APISpec:     apiSpec,
	}
}
