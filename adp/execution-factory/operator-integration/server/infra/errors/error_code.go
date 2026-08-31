// Package errors define error codes.
// @file errors_code.go
// @description: Define error code.
package errors

// ErrorCode Error code.
type ErrorCode string

func (e ErrorCode) String() string {
	return string(e)
}

// Operator expansion error code definition.
const (
	ErrExtOperatorExists           ErrorCode = "OperatorExists"           // operator already exists.
	ErrExtOperatorRegisterFailed   ErrorCode = "OperatorRegisterFailed"   // Operator registration failed.
	ErrExtOperatorDirectPublishErr ErrorCode = "OperatorDirectPublishErr" // Operator direct release failed.
	ErrExtCategoryTypeInvalid      ErrorCode = "CategoryTypeInvalid"      // Invalid operator type.
	ErrExtOperatorUnparsed         ErrorCode = "OperatorUnparsed"         // No valid operator was parsed.
	ErrExtOperatorNotFound         ErrorCode = "OperatorNotFound"         // operator does not exist.
	ErrExtOperatorMetadataNotFound ErrorCode = "OperatorMetadataNotFound" // Operator metadata does not exist.
	ErrExtOperatorUnSupportUpgrade ErrorCode = "OperatorUnSupportUpgrade" // The current operator does not support upgrades.
	ErrExtOperatorDeleteForbidden  ErrorCode = "OperatorDeleteForbidden"  // The current operator does not allow deletion.
	ErrExtOperatorUnSupportEdit    ErrorCode = "OperatorUnSupportEdit"    // The current operator does not support editing.
	ErrExtOperatorEditFailed       ErrorCode = "OperatorEditFailed"       // Operator editing failed.
	ErrExtOperatorImportLimit      ErrorCode = "OperatorImportLimit"      // Limit on the number of operators imported at a time.
	ErrExtOperatorNameEmpty        ErrorCode = "OperatorNameEmpty"        // Operator name cannot be empty.
	ErrExtOperatorNameTooLong      ErrorCode = "OperatorNameTooLong"      // The length of the operator name cannot exceed %d characters.
	ErrExtOperatorDescEmpty        ErrorCode = "OperatorDescEmpty"        // Operator description cannot be empty.
	ErrExtOperatorDescTooLong      ErrorCode = "OperatorDescTooLong"      // The operator description length cannot exceed %d characters.
	ErrExtOperatorImportDataLimit  ErrorCode = "OperatorImportDataLimit"  // Import operator data exceeds limit.
	ErrExtOperatorExistsSameName   ErrorCode = "OperatorExistsSameName"   // Operator "%s" already exists.
	ErrExtOperatorEditLimit        ErrorCode = "OperatorEditLimit"        // Only allow single operator editing.
	ErrExtOperatorNotAvailable     ErrorCode = "OperatorNotAvailable"     // Operator is not available.
	ErrExtOnlySyncModeDebug        ErrorCode = "OnlySyncModeDebug"        // Only supports synchronous mode debugging.
	ErrExtOperatorStatusInvalid    ErrorCode = "OperatorStatusInvalid"    // Operator status is invalid.
	ErrExtOperatorAsyncDataSource  ErrorCode = "OperatorAsyncDataSource"  // Asynchronous operators do not support adding as data source operators.
	ErrExtOperatorNotExistInFile   ErrorCode = "OperatorNotExistInFile"   // The file you uploaded does not contain an existing operator.
)

// Toolbox extension error code definition.
const (
	ErrExtToolBoxNotFound                 ErrorCode = "ToolBoxNotFound"                 // Toolbox does not exist.
	ErrExtToolBoxNameExists               ErrorCode = "ToolBoxNameExists"               // Toolbox name already exists.
	ErrExtToolBoxCategoryTypeInvalid      ErrorCode = "ToolBoxCategoryTypeInvalid"      // Invalid toolbox type.
	ErrExtToolExists                      ErrorCode = "ToolExists"                      // Tool already exists.
	ErrExtMetadataNotFound                ErrorCode = "MetadataNotFound"                // Metadata does not exist.
	ErrExtToolNotFound                    ErrorCode = "ToolNotFound"                    // Tool does not exist.
	ErrExtToolNotAvailable                ErrorCode = "ToolNotAvailable"                // Tool is not available.
	ErrExtToolConvertOnlySupportSync      ErrorCode = "ToolConvertOnlySupportSync"      // Only supports synchronized operators converted to tools.
	ErrExtToolConvertOnlySupportAPI       ErrorCode = "ToolConvertOnlySupportAPI"       // Only supports API operators to convert to tools.
	ErrExtToolBoxStatusInvalid            ErrorCode = "ToolBoxStatusInvalid"            // Toolbox status is invalid.
	ErrExtToolBoxNameEmpty                ErrorCode = "ToolBoxNameEmpty"                // Tool name cannot be empty.
	ErrExtToolBoxNameLimit                ErrorCode = "ToolBoxNameLimit"                // Tool name cannot exceed %d characters in length.
	ErrExtToolBoxDescLimit                ErrorCode = "ToolBoxDescLimit"                // Tool description length cannot exceed %d characters.
	ErrExtToolNameEmpty                   ErrorCode = "ToolNameEmpty"                   // Tool name cannot be empty.
	ErrExtToolNameLimit                   ErrorCode = "ToolNameLimit"                   // Tool name cannot exceed %d characters in length.
	ErrExtToolDescLimit                   ErrorCode = "ToolDescLimit"                   // Tool description length cannot exceed %d characters.
	ErrExtInternalToolBoxVersion          ErrorCode = "InternalToolBoxVersion"          // Internal toolbox version number format is wrong.
	ErrExtToolNameDuplicate               ErrorCode = "ToolNameDuplicate"               // Duplicate tool name.
	ErrExtToolOperatorNotAllowEdit        ErrorCode = "ToolOperatorNotAllowEdit"        // The operator tool does not allow editing of metadata.
	ErrExtToolDescEmpty                   ErrorCode = "ToolDescEmpty"                   // Tool description cannot be empty.
	ErrExtToolBoxDescEmpty                ErrorCode = "ToolBoxDescEmpty"                // Toolbox description cannot be empty.
	ErrExtToolNotExistInFile              ErrorCode = "ToolNotExistInFile"              // The file you uploaded does not contain an existing tool.
	ErrExtToolConvertMetadataTypeNotMatch ErrorCode = "ToolConvertMetadataTypeNotMatch" // Operator metadata type does not match tool.
	ErrExtToolTypeMismatch                ErrorCode = "ToolTypeMismatch"                // Tool type does not match toolbox type.
	ErrExtToolRefOperatorNotFound         ErrorCode = "ToolRefOperatorNotFound"         // The tool "%s" cannot be enabled. The dependent operator has been deleted. Please reconfigure it.
)

// MCP extended error code definition.
const (
	ErrExtMCPModeNotSupported      ErrorCode = "MCPModeNotSupported"      // MCP mode is not supported.
	ErrExtMCPExists                ErrorCode = "MCPExists"                // MCP already exists.
	ErrExtMCPNotFound              ErrorCode = "MCPNotFound"              // MCP does not exist.
	ErrExtMCPStatusInvalid         ErrorCode = "MCPStatusInvalid"         // MCP status is invalid.
	ErrExtMCPNameEmpty             ErrorCode = "MCPNameEmpty"             // MCP name cannot be empty.
	ErrExtMCPNameLimit             ErrorCode = "MCPNameLimit"             // The MCP name cannot exceed %d characters in length.
	ErrExtMCPUnSupportEdit         ErrorCode = "MCPUnSupportEdit"         // MCP does not support editing.
	ErrExtMCPUnSupportDelete       ErrorCode = "MCPUnSupportDelete"       // The current MCP does not allow deletion.
	ErrExtMCPParseFailed           ErrorCode = "MCPParseFailed"           // MCP parsing failed.
	ErrExtMCPServerNotAccessible   ErrorCode = "MCPServerNotAccessible"   // MCP Server cannot be accessed.
	ErrExtMCPServerAuthFailed      ErrorCode = "MCPServerAuthFailed"      // MCP Server authentication failed, the upstream returned %d.
	ErrExtMCPListToolsFailed       ErrorCode = "MCPListToolsFailed"       // Unable to obtain the tool list under the current MCP service.
	ErrExtMCPCallToolFailed        ErrorCode = "MCPCallToolFailed"        // Failed to call MCP tool.
	ErrExtMCPDescLimit             ErrorCode = "MCPDescLimit"             // MCP description length cannot exceed %d characters.
	ErrExtMCPToolMaxCount          ErrorCode = "MCPToolMaxCount"          // The number of MCP tools cannot exceed %d.
	ErrExtMCPToolNameDuplicate     ErrorCode = "MCPToolNameDuplicate"     // Duplicate MCP tool name.
	ErrExtMCPInstanceAlreadyExists ErrorCode = "MCPInstanceAlreadyExists" // MCP instance already exists.
	ErrExtMCPInstanceNotFound      ErrorCode = "MCPInstanceNotFound"      // MCP instance does not exist.
	// ErrExtMCPServerEndpointUnsupported The custom MCP only acts as an agent for external services and does not provide an access address on the platform side.
	ErrExtMCPServerEndpointUnsupported ErrorCode = "MCPServerEndpointUnsupported"
)

// Operator classification expanded error code definition.
const (
	ErrExtCategoryNameEmpty ErrorCode = "CategoryNameEmpty" // Operator classification name cannot be empty.
	ErrExtCategoryNameLimit ErrorCode = "CategoryNameLimit" // The length of operator classification name cannot exceed %d characters.
	ErrExtCategoryNotFound  ErrorCode = "CategoryNotFound"  // Operator classification does not exist.
	ErrExtCategoryNameExist ErrorCode = "CategoryNameExist" // Operator classification name already exists.
)

// Skill extended error code definition.
const (
	// The current Agent Skill is not allowed to be deleted.
	ErrExtSkillUnSupportDelete ErrorCode = "SkillUnSupportDelete" // The current Agent Skill is not allowed to be deleted.
	// Skill status is invalid.
	ErrExtSkillStatusInvalid ErrorCode = "SkillStatusInvalid" // Skill status is invalid.
	// Duplicate skill name.
	ErrExtSkillNameDuplicate ErrorCode = "SkillNameDuplicate" // Duplicate skill name.
	// Skill classification does not exist.
	ErrExtSkillCategoryNotFound ErrorCode = "SkillCategoryNotFound" // Skill classification does not exist.
)

// Agent module error code definition.
const (
	// Request forwarding failed, please check if it is available, or try again later.
	ErrExtProxyForwardFailed ErrorCode = "ProxyForwardFailed"
	// Path parameters are missing and there are still unreplaced placeholders in the URL template.
	ErrExtProxyPathParamMissing ErrorCode = "ProxyPathParamMissing"
)

// common extended error code definition.
const (
	ErrExtCommonOperationForbidden                ErrorCode = "CommonOperationForbidden"                // No operation permission.
	ErrExtCommonAddForbidden                      ErrorCode = "CommonAddForbidden"                      // No new permission.
	ErrExtCommonEditForbidden                     ErrorCode = "CommonEditForbidden"                     // No editing rights.
	ErrExtCommonDeleteForbidden                   ErrorCode = "CommonDeleteForbidden"                   // No delete permission.
	ErrExtCommonPublishForbidden                  ErrorCode = "CommonPublishForbidden"                  // No publishing permission.
	ErrExtCommonUnpublishForbidden                ErrorCode = "CommonUnpublishForbidden"                // No removal permission.
	ErrExtCommonPermissionForbidden               ErrorCode = "CommonPermissionForbidden"               // No permission management permissions.
	ErrExtCommonPublicAccessForbidden             ErrorCode = "CommonPublicAccessForbidden"             // No public access.
	ErrExtCommonUseForbidden                      ErrorCode = "CommonUseForbidden"                      // No permission to use.
	ErrExtCommonViewForbidden                     ErrorCode = "CommonViewForbidden"                     // No viewing permission.
	ErrExtCommonUserNotFound                      ErrorCode = "CommonUserNotFound"                      // User does not exist.
	ErrExtCommonAnonymousUserNotAllowed           ErrorCode = "CommonAnonymousUserNotAllowed"           // Anonymous users are not allowed access.
	ErrExtCommonDepartmentOrGroupOrRoleNotAllowed ErrorCode = "CommonDepartmentOrGroupOrRoleNotAllowed" // Department/user group/role account does not allow access.
	ErrExtCommonInvalidAccessorType               ErrorCode = "CommonInvalidAccessorType"               // Invalid account type.
)

// Common error code definitions.
const (
	ErrExtCommonNameInvalid                 ErrorCode = "CommonNameInvalid"                 // Only supports input of Chinese characters, letters, numbers, underlines or spaces.
	ErrExtCommonResourceIDConflict          ErrorCode = "CommonResourceIDConflict"          // Resource ID conflict.
	ErrExtCommonInternalComponentNotAllowed ErrorCode = "CommonInternalComponentNotAllowed" // Built-in components do not allow import and export.
	ErrExtCommonImportDataEmpty             ErrorCode = "CommonImportDataEmpty"             // Import data is empty.
	ErrExtCommonNameExists                  ErrorCode = "CommonNameExists"                  // This name is already taken, please rename it.
	ErrExtCommonNoMatchedMethodPath         ErrorCode = "CommonNoMatchedMethodPath"         // The corresponding API method path was not matched.
	ErrExtCommonCodeNotFound                ErrorCode = "CommonCodeNotFound"                // In debug mode, the code cannot be empty.
	ErrExtCommonMetadataTypeConflict        ErrorCode = "CommonMetadataTypeConflict"        // Metadata type conflict.
)

// Validator error code definition.
const (
	ErrExtCodeValidationRequired ErrorCode = "ValidationRequired" // Required fields.
	ErrExtCodeValidationFormat   ErrorCode = "ValidationFormat"   // Format error.
	ErrExtCodeValidationRange    ErrorCode = "ValidationRange"    // Range error.
	ErrExtCodeValidationEnum     ErrorCode = "ValidationEnum"     // Enumeration error.
)

// openapi error code.
const (
	// Loading phase error.
	ErrExtOpenAPISyntaxInvalid ErrorCode = "OpenAPISyntaxInvalid" // The file format is incorrect, please check whether it complies with the OpenAPI 3.0 specification.

	// Validation phase errors - error messages that support parameters.
	ErrExtOpenAPIInvalidPath                ErrorCode = "OpenAPIInvalidPath"                // The API path definition is missing or has an incorrect format. Please check whether the path definition is correct.
	ErrExtOpenAPIInvalidParameterRequired   ErrorCode = "OpenAPIInvalidParameterRequired"   // Parameter '%s' is missing a required field, please check if there are any missing parameters.
	ErrExtOpenAPIInvalidParameterSchema     ErrorCode = "OpenAPIInvalidParameterSchema"     // The parameter "%s" Schema is incorrectly defined, please check whether the parameter definition is correct.
	ErrExtOpenAPIInvalidParameterDefinition ErrorCode = "OpenAPIInvalidParameterDefinition" // The parameter "%s" is incorrectly defined, please check whether the parameter definition is correct.
	ErrExtOpenAPIInvalidParameterValue      ErrorCode = "OpenAPIInvalidParameterValue"      // Parameter verification error, please check the error details.
	ErrExtOpenAPIInvalidResponseRequired    ErrorCode = "OpenAPIInvalidResponseRequired"    // Response '%s' is missing a required field, please check if there are any missing response fields.
	ErrExtOpenAPIInvalidResponseDefinition  ErrorCode = "OpenAPIInvalidResponseDefinition"  // Response "%s" definition error, please check whether the response definition is correct.
	ErrExtOpenAPIInvalidResponseSchema      ErrorCode = "OpenAPIInvalidResponseSchema"      // Response Schema definition error, please view error details.
	ErrExtOpenAPIInvalidSchemaRef           ErrorCode = "OpenAPIInvalidSchemaRef"           // Schema "%s" reference error, please check whether $ref definition is correct.
	ErrExtOpenAPIInvalidSchemaType          ErrorCode = "OpenAPIInvalidSchemaType"          // Schema type "%s" is defined incorrectly, please check whether the type definition is correct.
	ErrExtOpenAPIInvalidSchemaValue         ErrorCode = "OpenAPIInvalidSchemaValue"         // Schema definition error, please check whether the value definition is correct.
	ErrExtOpenAPIInvalidSpecification       ErrorCode = "OpenAPIInvalidSpecification"       // OpenAPI specification verification failed, please check integrity.
	ErrExtOpenAPIInvalidURLFormat           ErrorCode = "OpenAPIInvalidURLFormat"           // URL format error, please check whether the URL complies with the specification.
	ErrExtOpenAPIInvalidComponent           ErrorCode = "OpenAPIInvalidComponent"           // Component definition error, please check whether the component definition is correct.

	// Generic validation error.
	ErrExtOpenAPIInvalidSpecificationRequired  ErrorCode = "OpenAPIInvalidSpecificationRequired"  // Required field '%s' is missing, please check if there are any missing fields.
	ErrExtOpenAPIInvalidSpecificationMissing   ErrorCode = "OpenAPIInvalidSpecificationMissing"   // Field "%s" is missing, please check if there are any missing fields.
	ErrExtOpenAPIInvalidSpecificationInvalid   ErrorCode = "OpenAPIInvalidSpecificationInvalid"   // Field "%s" value is invalid, please check if there is any invalid value.
	ErrExtOpenAPIInvalidSpecificationDuplicate ErrorCode = "OpenAPIInvalidSpecificationDuplicate" // Field "%s" is duplicated, please check whether there are duplicate fields.
	ErrExtOpenAPIInvalidSpecificationOperation ErrorCode = "OpenAPIInvalidSpecificationOperation" // Operation "%s" failed, please check for other errors.

	// Custom validation.
	// Summary is not allowed to be empty.
	ErrExtOpenAPIInvalidSpecificationSummaryEmpty ErrorCode = "OpenAPIInvalidSpecificationSummaryEmpty" // The "%s" Summary is empty, please complete it.

	// function check.
	ErrExtFunctionNoHandlerFound                     ErrorCode = "FunctionNoHandlerFound"                     // Entry function not detected, please decorate the function with @tool or define handler(event)
	ErrExtFunctionInvalidParameterType               ErrorCode = "FunctionInvalidParameterType"               // Invalid type of parameter "%s": %s, must be one of string, number, boolean, array, object.
	ErrExtFunctionInvalidParameterSubParameters      ErrorCode = "FunctionInvalidParameterSubParameters"      // The parameter "%s" type is %s, the sub_parameters field is not supported, only array and object types can have sub-parameters.
	ErrExtFunctionInvalidParameterSubParametersCount ErrorCode = "FunctionInvalidParameterSubParametersCount" // Parameter "%s" is of array type, sub_parameters must contain only one element to define the structure of the array item, currently there are %d elements.
)

// Dependent service error code definition.
const (
	// Error when running sandbox function.
	ErrExtSandboxRuntimeExecuteCodeFailed ErrorCode = "SandboxRuntimeExecuteCodeFailed" // An error occurs when running the sandbox function. Please check whether the code is correct.
	ErrExtDebugParamsInvalid              ErrorCode = "DebugParamsInvalid"              // Debugging parameter passing errors, must be in JSON format.
	ErrExtFunctionAIGenerateFailed        ErrorCode = "FunctionAIGenerateFailed"        // AI generation failed, please check whether the default model is normal.
	ErrExtFunctionAIGenerateModelFailed   ErrorCode = "FunctionAIGenerateModelFailed"   // The model generated content is abnormal. Please check whether the default model is available, or go to Settings to configure a valid model.
	// Dependency sandbox service exception.
	ErrExtSandboxControlPlaneFailed ErrorCode = "SandboxControlPlaneFailed" // Dependency sandbox service exception, please check the error details.
	// PyPI source is not available.
	ErrExtPypiRepoUnavailable ErrorCode = "PypiRepoUnavailable" // The PyPI repository is unavailable, please check the network connection or try again later.
	// PyPI source parser error.
	ErrExtPypiParserFailed ErrorCode = "PypiParserFailed" // Version suggestion requires the mirror source to support the JSON API (such as official PyPI or the Tsinghua mirror); check whether the mirror source is configured correctly.
	// Failed to request OSS gateway.
	ErrExtOSSGatewayFailed ErrorCode = "OSSGatewayFailed" // Requesting the OSS gateway failed. Please check whether the gateway configuration is correct.
	// The default OSS gateway storage does not exist.
	ErrExtOSSGatewayDefaultStorageNotFound ErrorCode = "OSSGatewayDefaultStorageNotFound" // The default OSS gateway storage does not exist. Please check whether the gateway configuration is correct.
)

const (
	NoneErrorLink = "None"
)

// Operation audit query error codes.
const (
	ErrExtOperationAuditAuthenticationRequired ErrorCode = "OperationAuditAuthenticationRequired"
	ErrExtOperationAuditAccessDenied           ErrorCode = "OperationAuditAccessDenied"
	ErrExtOperationAuditInvalidRange           ErrorCode = "OperationAuditInvalidRange"
	ErrExtOperationAuditInvalidBeforeTime      ErrorCode = "OperationAuditInvalidBeforeTime"
	ErrExtOperationAuditQueryFailed            ErrorCode = "OperationAuditQueryFailed"
	ErrExtOperationAuditNotFound               ErrorCode = "OperationAuditNotFound"
)
