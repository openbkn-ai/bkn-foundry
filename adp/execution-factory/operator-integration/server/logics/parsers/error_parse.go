package parsers

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/localize"
)

const (
	errorTypeLoadFailed       = "OpenAPILoadFailed"       // The OpenAPI document could not be loaded.
	errorTypeValidationFailed = "OpenAPIValidationFailed" // The OpenAPI document failed validation.
	elementTypeLen            = 2
)

var (
	parameterRegex = regexp.MustCompile(`parameter\s*["']([^"']+)["']`)
	responseRegex  = regexp.MustCompile(`response\s*["']([^"']+)["']`)
	schemaRegex    = regexp.MustCompile(`schema\s*["']([^"']+)["']`)
	operationRegex = regexp.MustCompile(`operation\s*["']([^"']+)["']`)
	fieldRegex     = regexp.MustCompile(`field\s*["']([^"']+)["']`)
)

// parseOpenAPILoadError maps an OpenAPI loading failure to a public error.
func parseOpenAPILoadError(ctx context.Context, originalErr error) *errors.HTTPError {
	if originalErr == nil {
		return nil
	}
	errorCode, errorParams, errorDetails := extractErrorInfo(ctx, originalErr, errorTypeLoadFailed)
	return errors.NewHTTPError(ctx, http.StatusBadRequest, errorCode, errorDetails, errorParams...)
}

// parseOpenAPIValidationError maps an OpenAPI validation failure to a public error.
func parseOpenAPIValidationError(ctx context.Context, originalErr error) *errors.HTTPError {
	if originalErr == nil {
		return nil
	}
	errorCode, errorParams, errorDetails := extractErrorInfo(ctx, originalErr, errorTypeValidationFailed)
	return errors.NewHTTPError(ctx, http.StatusBadRequest, errorCode, errorDetails, errorParams...)
}

// extractErrorInfo returns the stable code, description parameters, and diagnostic details.
func extractErrorInfo(ctx context.Context, err error, errorType string) (errors.ErrorCode, []interface{}, interface{}) {
	// Preserve all entries from a MultiError.
	if multiErr, ok := err.(*openapi3.MultiError); ok {
		return handleMultiError(ctx, multiErr, errorType)
	}

	// Handle a single error directly.
	return handleSingleError(err, errorType)
}

// handleMultiError uses the first entry as the primary code and includes every entry in details.
func handleMultiError(ctx context.Context, multiErr *openapi3.MultiError, errorType string) (errors.ErrorCode, []interface{}, interface{}) {
	var (
		mainErrorCode errors.ErrorCode
		mainParams    []interface{}
		errorDetails  []string
	)

	tr := localize.NewI18nTranslator(common.GetLanguageFromCtx(ctx))

	// Iterate over every nested error.
	for i, subErr := range *multiErr {
		if subErr != nil {
			subErrorCode, subParams, subErrorDetails := handleSingleError(subErr, errorType)

			// Use the first error as the primary error code.
			if i == 0 {
				mainErrorCode = subErrorCode
				mainParams = subParams
			}

			// Include every nested error in the client-visible details.
			errorDetails = append(errorDetails, fmt.Sprintf(tr.Trans("detail.openapi_error_item"), i+1, subErrorDetails))
		}
	}

	// Use the default code when the collection has no usable entry.
	if mainErrorCode == "" {
		mainErrorCode = getDefaultErrorCode(errorType)
	}

	return mainErrorCode, mainParams, errorDetails
}

// getDefaultErrorCode returns the fallback code for a parser stage.
func getDefaultErrorCode(errorType string) errors.ErrorCode {
	switch errorType {
	case errorTypeLoadFailed:
		return errors.ErrExtOpenAPISyntaxInvalid
	case errorTypeValidationFailed:
		return errors.ErrExtOpenAPIInvalidSpecification
	default:
		return errors.ErrExtOpenAPISyntaxInvalid
	}
}

// handleSingleError maps one parser error to its public representation.
func handleSingleError(err error, errorType string) (errors.ErrorCode, []interface{}, interface{}) {
	errStr := err.Error()

	// Select a code and parameters while preserving the original diagnostic.
	var errorCode errors.ErrorCode
	var errorParams []interface{}
	errorDetails := errStr

	switch errorType {
	case errorTypeLoadFailed:
		errorCode = errors.ErrExtOpenAPISyntaxInvalid
	case errorTypeValidationFailed:
		errorCode, errorParams = getValidationErrorCodeAndParams(errStr)
	default:
		errorCode, errorParams = getGenericErrorCodeAndParams(errStr)
	}
	return errorCode, errorParams, errorDetails
}

// extractElement extracts an element name from a parser error.
func extractElement(errStr string, regex *regexp.Regexp) string {
	matches := regex.FindStringSubmatch(errStr)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// getGenericErrorCodeAndParams maps generic parser errors.
func getGenericErrorCodeAndParams(errStr string) (errors.ErrorCode, []interface{}) {
	field := extractElement(errStr, fieldRegex)

	if strings.Contains(errStr, "required") && field != "" {
		return errors.ErrExtOpenAPIInvalidSpecificationRequired, []interface{}{field}
	}
	if strings.Contains(errStr, "invalid") && field != "" {
		return errors.ErrExtOpenAPIInvalidSpecificationInvalid, []interface{}{field}
	}
	if strings.Contains(errStr, "missing") && field != "" {
		return errors.ErrExtOpenAPIInvalidSpecificationMissing, []interface{}{field}
	}
	if strings.Contains(errStr, "duplicate") && field != "" {
		return errors.ErrExtOpenAPIInvalidSpecificationDuplicate, []interface{}{field}
	}

	return errors.ErrExtOpenAPIInvalidSpecificationOperation, nil
}

// getValidationErrorCodeAndParams maps OpenAPI validation errors.
func getValidationErrorCodeAndParams(errStr string) (errors.ErrorCode, []interface{}) {
	if strings.Contains(errStr, "invalid components") {
		return errors.ErrExtOpenAPIInvalidComponent, nil
	}

	// Handle an invalid info object.
	if strings.Contains(errStr, "invalid info") {
		return errors.ErrExtOpenAPIInvalidSpecificationInvalid, []interface{}{"info"}
	}

	// Handle values that must be objects.
	if strings.Contains(errStr, "must be an object") {
		// Identify the affected element.
		if strings.Contains(errStr, "info") {
			return errors.ErrExtOpenAPIInvalidSpecificationInvalid, []interface{}{"info"}
		}
		if strings.Contains(errStr, "components") {
			return errors.ErrExtOpenAPIInvalidComponent, nil
		}
		if strings.Contains(errStr, "path") {
			return errors.ErrExtOpenAPIInvalidPath, nil
		}

		// Fall back to the generic required-field error.
		return errors.ErrExtOpenAPIInvalidSpecificationRequired, nil
	}

	// Handle an empty OpenAPI version.
	if strings.Contains(errStr, "value of openapi must be a non-empty string") {
		return errors.ErrExtOpenAPIInvalidSpecificationRequired, []interface{}{"openapi"}
	}
	// 1. Schema errors have the highest priority.
	if strings.Contains(errStr, "schema") {
		schema := extractElement(errStr, schemaRegex)
		if schema != "" {
			if strings.Contains(errStr, "ref") {
				return errors.ErrExtOpenAPIInvalidSchemaRef, []interface{}{schema}
			}
			if strings.Contains(errStr, "type") {
				return errors.ErrExtOpenAPIInvalidSchemaType, []interface{}{schema}
			}
			return errors.ErrExtOpenAPIInvalidSchemaType, []interface{}{schema}
		}
		return errors.ErrExtOpenAPIInvalidSchemaValue, nil
	}

	// 2. Parameter errors.
	if strings.Contains(errStr, "parameter") {
		parameter := extractElement(errStr, parameterRegex)
		if parameter != "" {
			if strings.Contains(errStr, "required") {
				return errors.ErrExtOpenAPIInvalidParameterRequired, []interface{}{parameter}
			}
			if strings.Contains(errStr, "schema") {
				return errors.ErrExtOpenAPIInvalidParameterSchema, []interface{}{parameter}
			}
			return errors.ErrExtOpenAPIInvalidParameterDefinition, []interface{}{parameter}
		}
		return errors.ErrExtOpenAPIInvalidParameterValue, nil
	}

	// 3. Response errors.
	if strings.Contains(errStr, "response") {
		response := extractElement(errStr, responseRegex)
		if response != "" {
			if strings.Contains(errStr, "required") {
				return errors.ErrExtOpenAPIInvalidResponseRequired, []interface{}{response}
			}
			return errors.ErrExtOpenAPIInvalidResponseDefinition, []interface{}{response}
		}
		return errors.ErrExtOpenAPIInvalidResponseSchema, nil
	}

	// 4. Path errors.
	if strings.Contains(errStr, "path") {
		return errors.ErrExtOpenAPIInvalidPath, nil
	}

	// 5. Operation errors.
	if strings.Contains(errStr, "operation") {
		operation := extractElement(errStr, operationRegex)
		if operation != "" {
			return errors.ErrExtOpenAPIInvalidSpecificationOperation, []interface{}{operation}
		}
		return errors.ErrExtOpenAPIInvalidSpecificationOperation, nil
	}

	// 6. Generic validation errors.
	field := extractElement(errStr, fieldRegex)
	if field != "" {
		if strings.Contains(errStr, "required") {
			return errors.ErrExtOpenAPIInvalidSpecificationRequired, []interface{}{field}
		}
		if strings.Contains(errStr, "missing") {
			return errors.ErrExtOpenAPIInvalidSpecificationMissing, []interface{}{field}
		}
		if strings.Contains(errStr, "invalid") {
			return errors.ErrExtOpenAPIInvalidSpecificationInvalid, []interface{}{field}
		}
		if strings.Contains(errStr, "duplicate") {
			return errors.ErrExtOpenAPIInvalidSpecificationDuplicate, []interface{}{field}
		}
	}

	// 7. Default validation error.
	return errors.ErrExtOpenAPIInvalidSpecification, nil
}
