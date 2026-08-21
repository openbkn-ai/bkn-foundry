// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"io"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin/binding"
)

var precisionJSON = sonic.Config{UseNumber: true}.Froze()

// DecodePreciseJSON preserves number literals in dynamic JSON fields.
func DecodePreciseJSON(reader io.Reader, target any) error {
	return precisionJSON.NewDecoder(reader).Decode(target)
}

// BindPreciseJSON decodes an HTTP request body and applies Gin's validator.
func BindPreciseJSON(reader io.Reader, target any) error {
	if err := DecodePreciseJSON(reader, target); err != nil {
		return err
	}
	return binding.Validator.ValidateStruct(target)
}

// UnmarshalPreciseJSON preserves number literals in dynamic JSON fields.
func UnmarshalPreciseJSON(data []byte, target any) error {
	return precisionJSON.Unmarshal(data, target)
}
