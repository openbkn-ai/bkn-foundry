package validator

import (
	myErr "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
)

// TagToErrorType Validate tag maps to error category.
var TagToErrorType = map[string]myErr.ErrorCode{
	// Required category.
	"required":        myErr.ErrExtCodeValidationRequired,
	"required_if":     myErr.ErrExtCodeValidationRequired,
	"required_unless": myErr.ErrExtCodeValidationRequired,
	"required_with":   myErr.ErrExtCodeValidationRequired,

	// Format class.
	"email":    myErr.ErrExtCodeValidationFormat,
	"url":      myErr.ErrExtCodeValidationFormat,
	"uuid":     myErr.ErrExtCodeValidationFormat,
	"datetime": myErr.ErrExtCodeValidationFormat,
	"numeric":  myErr.ErrExtCodeValidationFormat,
	"alpha":    myErr.ErrExtCodeValidationFormat,
	"alphanum": myErr.ErrExtCodeValidationFormat,
	"ip":       myErr.ErrExtCodeValidationFormat,
	"mac":      myErr.ErrExtCodeValidationFormat,

	// scope class.
	"min": myErr.ErrExtCodeValidationRange,
	"max": myErr.ErrExtCodeValidationRange,
	"len": myErr.ErrExtCodeValidationRange,
	"gte": myErr.ErrExtCodeValidationRange,
	"lte": myErr.ErrExtCodeValidationRange,
	"gt":  myErr.ErrExtCodeValidationRange,
	"lt":  myErr.ErrExtCodeValidationRange,

	// enum class.
	"oneof": myErr.ErrExtCodeValidationEnum,
}
