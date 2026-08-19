package metadata

import (
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/parsers"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

const (
	legacyTimeoutParamName = "timeout"
	legacyTimeoutParamIn   = "query"
)

// stripLegacyTimeoutParameter removes the remaining timeout query parameters in function metadata.
//
// The execution timeout was once written into the api_spec as a contract parameter, causing it to be listed alongside the business parameters.
// In the tool schema: Agent will guess it as a passable parameter, and the interface will be rendered according to the schema.
// The user will also be asked to select a fixed value or dynamic input for it. The build side is no longer writing, but before the upgrade.
// There is still this field in the built function tool record, but it is stripped off when reading to avoid a data migration.
//
// The execution side still reads the timeout from the requested query, and the existing caller is not affected.
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

// MetadataDBToStruct converts the database model into a metadata interface.
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
		// The parameter definition is expanded into the API specification when it is stored, and is decoded back here. The caller does not need to parse OpenAPI by itself.
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

// DefaultMetadataInfo gets default metadata information.
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

// apimetadataDBToAPIMetadata converts the database model into an API metadata interface.
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
