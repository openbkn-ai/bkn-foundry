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

var defaultJSON = sonic.Config{UseNumber: true}.Froze()

// DecodeJSON reads one JSON value and preserves numbers in interface values as json.Number.
func DecodeJSON(reader io.Reader, target any) error {
	return defaultJSON.NewDecoder(reader).Decode(target)
}

// BindJSON decodes an HTTP request body and applies Gin's validator.
func BindJSON(reader io.Reader, target any) error {
	if err := DecodeJSON(reader, target); err != nil {
		return err
	}
	return binding.Validator.ValidateStruct(target)
}

// BindStrictJSON preserves number literals, rejects unknown fields, and
// applies Gin validation. It is used by first-version contracts that do not
// carry backward-compatible request fields.
func BindStrictJSON(reader io.Reader, target any) error {
	// Sonic's stream decoder can report EOF for malformed trailing bytes.
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain a single JSON value")
		}
		return err
	}
	return binding.Validator.ValidateStruct(target)
}

// UnmarshalJSON decodes JSON bytes and preserves numbers in interface values as json.Number.
func UnmarshalJSON(data []byte, target any) error {
	return defaultJSON.Unmarshal(data, target)
}
