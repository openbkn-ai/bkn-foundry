// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package sync_checkpoint owns the batch checkpoint wire format.
package sync_checkpoint

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"vega-backend/interfaces"
)

const ModeBatch = "batch"

// SyncCheckpoint is shared by Task execution progress and the Resource's
// committed incremental baseline.
type SyncCheckpoint struct {
	Mode   string                `json:"mode"`
	Cursor []interfaces.KeyValue `json:"cursor"`
}

// EncodeBatch serializes a batch cursor as a checkpoint. A nil cursor is
// normalized to an empty array so an established empty baseline is never
// confused with the empty string used for no checkpoint.
func EncodeBatch(cursor []interfaces.KeyValue) (string, error) {
	if cursor == nil {
		cursor = make([]interfaces.KeyValue, 0)
	}
	data, err := json.Marshal(SyncCheckpoint{
		Mode:   ModeBatch,
		Cursor: cursor,
	})
	if err != nil {
		return "", fmt.Errorf("encode batch checkpoint: %w", err)
	}
	return string(data), nil
}

// DecodeBatch parses a batch checkpoint. The exact empty string represents
// an absent checkpoint and returns nil; whitespace and legacy cursor arrays are
// invalid rather than being guessed or silently upgraded.
func DecodeBatch(mark string) (*SyncCheckpoint, error) {
	if mark == "" {
		return nil, nil
	}

	var checkpoint SyncCheckpoint
	if err := decodeStrictJSON(strings.NewReader(mark), &checkpoint); err != nil {
		return nil, fmt.Errorf("decode batch checkpoint: %w", err)
	}
	if checkpoint.Mode != ModeBatch {
		return nil, fmt.Errorf("decode batch checkpoint: unsupported mode %q", checkpoint.Mode)
	}
	if checkpoint.Cursor == nil {
		return nil, fmt.Errorf("decode batch checkpoint: cursor must be an array")
	}
	return &checkpoint, nil
}

// ValidateCursor checks a non-empty cursor against the ordered Resource build
// keys and converts JSON numbers to the exact integer types required by the
// schema. An empty cursor is a valid established baseline and starts from the
// first source row without a cursor filter.
func ValidateCursor(checkpoint *SyncCheckpoint, incrementalFields []string, schema []*interfaces.Property) error {
	if checkpoint == nil {
		return fmt.Errorf("validate batch checkpoint: checkpoint is required")
	}
	if len(checkpoint.Cursor) == 0 {
		return nil
	}
	if len(checkpoint.Cursor) != len(incrementalFields) {
		return fmt.Errorf("validate batch checkpoint: expected %d cursor values, got %d", len(incrementalFields), len(checkpoint.Cursor))
	}

	properties := make(map[string]*interfaces.Property, len(schema))
	for _, property := range schema {
		if property == nil || property.Name == "" {
			return fmt.Errorf("validate batch checkpoint: resource schema contains an invalid property")
		}
		if _, exists := properties[property.Name]; exists {
			return fmt.Errorf("validate batch checkpoint: resource schema contains duplicate property %q", property.Name)
		}
		properties[property.Name] = property
	}

	for i, field := range incrementalFields {
		cursorValue := &checkpoint.Cursor[i]
		if cursorValue.Key != field {
			return fmt.Errorf("validate batch checkpoint: expected key %q at position %d, got %q", field, i, cursorValue.Key)
		}
		property, exists := properties[field]
		if !exists {
			return fmt.Errorf("validate batch checkpoint: build key %q is absent from the resource schema", field)
		}
		value, err := convertCursorValue(cursorValue.Value, property.Type)
		if err != nil {
			return fmt.Errorf("validate batch checkpoint key %q: %w", field, err)
		}
		cursorValue.Value = value
	}
	return nil
}

func convertCursorValue(value any, dataType string) (any, error) {
	if value == nil {
		return nil, fmt.Errorf("cursor value must not be null")
	}

	switch dataType {
	case interfaces.DataType_Integer:
		switch number := value.(type) {
		case json.Number:
			parsed, err := strconv.ParseInt(number.String(), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("cursor value %q is not a valid integer", number.String())
			}
			return parsed, nil
		case int64:
			return number, nil
		default:
			return nil, fmt.Errorf("cursor value must be a JSON integer, got %T", value)
		}
	case interfaces.DataType_UnsignedInteger:
		switch number := value.(type) {
		case json.Number:
			parsed, err := strconv.ParseUint(number.String(), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("cursor value %q is not a valid unsigned integer", number.String())
			}
			return parsed, nil
		case uint64:
			return number, nil
		default:
			return nil, fmt.Errorf("cursor value must be a JSON unsigned integer, got %T", value)
		}
	case interfaces.DataType_String,
		interfaces.DataType_Date,
		interfaces.DataType_Time,
		interfaces.DataType_Datetime,
		interfaces.DataType_Timestamp:
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("cursor value must be a JSON string, got %T", value)
		}
		return text, nil
	default:
		return nil, fmt.Errorf("resource type %q is not supported as a build key", dataType)
	}
}

func decodeStrictJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}
