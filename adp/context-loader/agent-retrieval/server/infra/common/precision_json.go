// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin/binding"
)

var precisionJSON = sonic.Config{UseNumber: true}.Froze()

// DecodePreciseJSON decodes business JSON from a reader without rounding dynamic integers.
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

// BindStrictPreciseJSON rejects unknown fields in an HTTP request while
// retaining exact numbers in dynamic values. Use it for contracts that remove
// previously accepted fields, where silently dropping one changes the result.
func BindStrictPreciseJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return binding.Validator.ValidateStruct(target)
}

// UnmarshalPreciseJSON decodes business JSON without rounding dynamic integers.
func UnmarshalPreciseJSON(data []byte, target any) error {
	return precisionJSON.Unmarshal(data, target)
}
