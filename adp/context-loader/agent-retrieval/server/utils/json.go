// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package utils

import (
	"encoding/json"
	"log"
)

// JSONToObject converts a JSON string into an object of the specified type.
func JSONToObject[T any](jsonStr string) T {
	var result T
	if jsonStr == "" {
		return result
	}

	err := json.Unmarshal([]byte(jsonStr), &result)
	if err != nil {
		log.Printf("JSONToObject error: %v", err)
		return result
	}
	return result
}

// JSONToObjectWithError Converts a JSON string into an object of the specified type and returns error information.
func JSONToObjectWithError[T any](jsonStr string) (T, error) {
	var result T
	if jsonStr == "" {
		return result, nil
	}

	err := json.Unmarshal([]byte(jsonStr), &result)
	if err != nil {
		return result, err
	}
	return result, nil
}

// AnyToObject converts any object to the specified object.
func AnyToObject(anyObj any, obj interface{}) error {
	jsonBytes, err := json.Marshal(anyObj)
	if err != nil {
		return err
	}
	err = json.Unmarshal(jsonBytes, obj)
	if err != nil {
		return err
	}
	return nil
}
