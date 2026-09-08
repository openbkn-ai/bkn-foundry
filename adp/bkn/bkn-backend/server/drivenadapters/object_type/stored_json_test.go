package object_type

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// Columns written before a field existed hold "" rather than "[]". Handing that to the decoder
// failed with "the input json is empty", and the failure surfaced as an internal error on every
// read of the object type — one blank column made a whole knowledge network unopenable.
func Test_unmarshalStoredJSON_EmptyColumnIsUnset(t *testing.T) {
	Convey("An empty JSON column decodes as unset\n", t, func() {
		var properties []*struct{ Name string }

		So(unmarshalStoredJSON([]byte(""), &properties), ShouldBeNil)
		So(properties, ShouldBeNil)

		So(unmarshalStoredJSON([]byte("   "), &properties), ShouldBeNil)
		So(properties, ShouldBeNil)

		So(unmarshalStoredJSON([]byte(`[{"Name":"a"}]`), &properties), ShouldBeNil)
		So(len(properties), ShouldEqual, 1)

		// Malformed content is still an error: only emptiness is treated as "never set".
		So(unmarshalStoredJSON([]byte("{not json"), &properties), ShouldNotBeNil)
	})
}
