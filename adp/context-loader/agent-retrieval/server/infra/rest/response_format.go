// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"fmt"
	"reflect"

	"github.com/bytedance/sonic"
	"github.com/toon-format/toon-go"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

const (
	// ContentTypeTOON TOON response Content-Type.
	ContentTypeTOON = "application/toon"
)

// ResponseFormat response format: JSON or TOON.
type ResponseFormat string

const (
	// FormatJSON default JSON.
	FormatJSON ResponseFormat = "json"
	// FormatTOON compression format TOON.
	FormatTOON ResponseFormat = "toon"
)

// ParseResponseFormat parses response_format parameter, illegal value returns error.
func ParseResponseFormat(s string) (ResponseFormat, error) {
	switch s {
	case "", "json":
		return FormatJSON, nil
	case "toon":
		return FormatTOON, nil
	default:
		return FormatJSON, fmt.Errorf("invalid response_format: %q (allowed: json, toon)", s)
	}
}

// MarshalResponse serializes the body according to the specified format and returns the Content-Type and body bytes; no silent degradation is performed when an error occurs.
func MarshalResponse(format ResponseFormat, body interface{}) (contentType string, bodyBytes []byte, err error) {
	if body == nil {
		return ContentTypeJSON, nil, nil
	}
	switch format {
	case FormatJSON:
		bodyBytes, err = sonic.Marshal(body)
		if err != nil {
			return "", nil, err
		}
		return ContentTypeJSON, bodyBytes, nil
	case FormatTOON:
		bodyBytes, err = marshalTOON(body)
		if err != nil {
			return "", nil, err
		}
		return ContentTypeTOON, bodyBytes, nil
	default: // fallback to JSON for unknown values
		bodyBytes, err = sonic.Marshal(body)
		if err != nil {
			return "", nil, err
		}
		return ContentTypeJSON, bodyBytes, nil
	}
}

type toonValueProvider interface {
	TOONValue() any
}

// marshalTOON encodes body as TOON.
// Implement toonValueProvider's response to provide an adapted TOON view;
// Because other structs only have json tags and no toon tags, they need to be converted into maps through JSON and then encoded to ensure that the field names are consistent with the API contract.
func marshalTOON(body interface{}) ([]byte, error) {
	if provider, ok := body.(toonValueProvider); ok {
		return toon.Marshal(provider.TOONValue(), toon.WithLengthMarkers(true))
	}
	v := reflect.ValueOf(body)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		safe, _ := interfaces.TOONSafeValue(body)
		return toon.Marshal(safe, toon.WithLengthMarkers(true))
	}
	// struct → JSON → any → TOON (retain the field name of json tag)
	jsonBytes, err := sonic.Marshal(body)
	if err != nil {
		return nil, err
	}
	var m any
	// UseNumber, not the default configuration: the round-trip is the last place
	// a wide integer can be rounded, and it would undo the precision the driven
	// adapters went out of their way to keep. See openbkn-ai/bkn-studio#464.
	if err := common.UnmarshalPreciseJSON(jsonBytes, &m); err != nil {
		return nil, err
	}
	// toon-go renders json.Number through float64, so anything past the IEEE 754
	// safe range has to become a decimal string before it reaches the encoder.
	safe, _ := interfaces.TOONSafeValue(m)
	return toon.Marshal(safe, toon.WithLengthMarkers(true))
}
