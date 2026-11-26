package handler

import (
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"

	"github.com/gin-gonic/gin/binding"
	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
)

func bindJSON(body io.Reader, target any) error {
	if body == nil {
		return io.EOF
	}
	decoder := jsontext.NewDecoder(body)
	// Match existing field spellings regardless of case while preserving underscores.
	if err := jsoncodec.UnmarshalDecode(decoder, target,
		jsonv2.MatchCaseInsensitiveNames(true),
		jsonv1.MatchCaseSensitiveDelimiter(true),
	); err != nil {
		return err
	}
	if _, err := decoder.ReadToken(); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain a single JSON value")
		}
		return err
	}
	return binding.Validator.ValidateStruct(target)
}
