// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"

	"github.com/gin-gonic/gin/binding"
	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
)

// BindJSON decodes an HTTP request body and applies Gin's validator.
func BindJSON(reader io.Reader, target any) error {
	if err := jsoncodec.UnmarshalDecode(jsontext.NewDecoder(reader), target); err != nil {
		return err
	}
	return binding.Validator.ValidateStruct(target)
}

// BindStrictJSON preserves number literals, rejects unknown fields, and
// applies Gin validation. It is used by first-version contracts that do not
// carry backward-compatible request fields.
func BindStrictJSON(reader io.Reader, target any) error {
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
