// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"encoding/json/jsontext"
	"errors"
	"io"

	"github.com/gin-gonic/gin/binding"
	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
)

// BindJSON decodes one JSON request body and applies Gin's validator.
func BindJSON(reader io.Reader, target any) error {
	decoder := jsontext.NewDecoder(reader)
	if err := jsoncodec.UnmarshalDecode(decoder, target); err != nil {
		return err
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return binding.Validator.ValidateStruct(target)
}
