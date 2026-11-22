//go:build !garble_compat

// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package jsoncodec

import "github.com/bytedance/sonic"

func Marshal(v any) ([]byte, error)       { return sonic.Marshal(v) }
func MarshalString(v any) (string, error) { return sonic.MarshalString(v) }
func Unmarshal(data []byte, v any) error  { return sonic.Unmarshal(data, v) }
