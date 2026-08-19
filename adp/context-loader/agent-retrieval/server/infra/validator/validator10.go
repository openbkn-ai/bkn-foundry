// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package validator provides validation utilities and error type mappings.
package validator

import (
	myErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
)

// TagToErrorType Validate tag maps to error category.
var TagToErrorType = map[string]string{
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
