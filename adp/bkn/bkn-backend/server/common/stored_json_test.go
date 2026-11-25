// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// Columns written before a field existed hold "" rather than "[]". Handing that to the decoder
// failed with "the input json is empty", and the failure surfaced as an internal error on every
// read of the definition — one blank column made a whole knowledge network unopenable.
func Test_UnmarshalStoredJSON_EmptyColumnIsUnset(t *testing.T) {
	Convey("An empty JSON column decodes as unset\n", t, func() {
		var properties []*struct{ Name string }

		So(UnmarshalStoredJSON([]byte(""), &properties), ShouldBeNil)
		So(properties, ShouldBeNil)

		So(UnmarshalStoredJSON([]byte("   "), &properties), ShouldBeNil)
		So(properties, ShouldBeNil)

		So(UnmarshalStoredJSON(nil, &properties), ShouldBeNil)
		So(properties, ShouldBeNil)

		So(UnmarshalStoredJSON([]byte(`[{"Name":"a"}]`), &properties), ShouldBeNil)
		So(len(properties), ShouldEqual, 1)

		// Malformed content is still an error: only emptiness is treated as "never set".
		So(UnmarshalStoredJSON([]byte("{not json"), &properties), ShouldNotBeNil)
	})
}

func TestUnmarshalStoredJSONRetainsExactNumbers(t *testing.T) {
	var data struct {
		Label string         `json:"label"`
		Data  map[string]any `json:"data"`
	}
	raw := []byte(`{"label":"value","data":{"id":9007199254740993}}`)
	if err := UnmarshalStoredJSON(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data.Label != "value" || data.Data["id"] != json.Number("9007199254740993") {
		t.Fatalf("decoded column = %#v", data)
	}
}

func TestUnmarshalStoredJSONRejectsInvalidJSONV2Input(t *testing.T) {
	for _, raw := range []string{
		`{"label":"first","label":"duplicate"}`,
		"{\"label\":\"invalid\xff\"}",
	} {
		var value map[string]any
		if err := UnmarshalStoredJSON([]byte(raw), &value); err == nil {
			t.Errorf("UnmarshalStoredJSON(%q) accepted invalid input", raw)
		}
	}
}
