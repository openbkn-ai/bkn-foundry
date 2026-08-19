// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package utils

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

// TestJSONToObject_Success test JSONToObject success scenario.
func TestJSONToObject_Success(t *testing.T) {
	convey.Convey("TestJSONToObject_Success", t, func() {
		type TestStruct struct {
			Name  string `json:"name"`
			Value int    `json:"value"`
		}

		jsonStr := `{"name": "test", "value": 123}`
		result := JSONToObject[TestStruct](jsonStr)
		convey.So(result.Name, convey.ShouldEqual, "test")
		convey.So(result.Value, convey.ShouldEqual, 123)
	})
}

// TestJSONToObject_EmptyString Test JSONToObject empty string.
func TestJSONToObject_EmptyString(t *testing.T) {
	convey.Convey("TestJSONToObject_EmptyString", t, func() {
		type TestStruct struct {
			Name string `json:"name"`
		}

		result := JSONToObject[TestStruct]("")
		convey.So(result.Name, convey.ShouldEqual, "")
	})
}

// TestJSONToObject_InvalidJSON Test JSONToObject invalid JSON.
func TestJSONToObject_InvalidJSON(t *testing.T) {
	convey.Convey("TestJSONToObject_InvalidJSON", t, func() {
		type TestStruct struct {
			Name string `json:"name"`
		}

		result := JSONToObject[TestStruct]("invalid json")
		convey.So(result.Name, convey.ShouldEqual, "")
	})
}

// TestJSONToObjectWithError_Success test JSONToObjectWithError success scenario.
func TestJSONToObjectWithError_Success(t *testing.T) {
	convey.Convey("TestJSONToObjectWithError_Success", t, func() {
		type TestStruct struct {
			Name string `json:"name"`
		}

		result, err := JSONToObjectWithError[TestStruct](`{"name": "test"}`)
		convey.So(err, convey.ShouldBeNil)
		convey.So(result.Name, convey.ShouldEqual, "test")
	})
}

// TestJSONToObjectWithError_EmptyString Test JSONToObjectWithError empty string.
func TestJSONToObjectWithError_EmptyString(t *testing.T) {
	convey.Convey("TestJSONToObjectWithError_EmptyString", t, func() {
		type TestStruct struct {
			Name string `json:"name"`
		}

		result, err := JSONToObjectWithError[TestStruct]("")
		convey.So(err, convey.ShouldBeNil)
		convey.So(result.Name, convey.ShouldEqual, "")
	})
}

// TestJSONToObjectWithError_InvalidJSON Test JSONToObjectWithError invalid JSON.
func TestJSONToObjectWithError_InvalidJSON(t *testing.T) {
	convey.Convey("TestJSONToObjectWithError_InvalidJSON", t, func() {
		type TestStruct struct {
			Name string `json:"name"`
		}

		_, err := JSONToObjectWithError[TestStruct]("invalid json")
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// TestAnyToObject_Success test AnyToObject success scenario.
func TestAnyToObject_Success(t *testing.T) {
	convey.Convey("TestAnyToObject_Success", t, func() {
		type TestStruct struct {
			Name  string `json:"name"`
			Value int    `json:"value"`
		}

		source := map[string]interface{}{
			"name":  "test",
			"value": 123,
		}

		var result TestStruct
		err := AnyToObject(source, &result)
		convey.So(err, convey.ShouldBeNil)
		convey.So(result.Name, convey.ShouldEqual, "test")
		convey.So(result.Value, convey.ShouldEqual, 123)
	})
}

// TestAnyToObject_SliceToStruct tests AnyToObject array conversion.
func TestAnyToObject_SliceToStruct(t *testing.T) {
	convey.Convey("TestAnyToObject_SliceToStruct", t, func() {
		source := []map[string]interface{}{
			{"name": "item1"},
			{"name": "item2"},
		}

		type Item struct {
			Name string `json:"name"`
		}

		var result []Item
		err := AnyToObject(source, &result)
		convey.So(err, convey.ShouldBeNil)
		convey.So(len(result), convey.ShouldEqual, 2)
		convey.So(result[0].Name, convey.ShouldEqual, "item1")
	})
}
