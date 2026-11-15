// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_ExtractionMethodRevision(t *testing.T) {
	Convey("The extraction method of a document property is revised as its save takes it\n", t, func() {
		ctx := context.Background()
		document := func() *interfaces.DraftObjectType {
			return &interfaces.DraftObjectType{ID: "contract_doc", Name: "合同", Document: &interfaces.DraftDocumentModel{Role: "entity"},
				Properties: []*interfaces.DraftProperty{
					{Name: "price_includes_tax", DisplayName: "价格是否含税", Type: "boolean",
						Extraction: &interfaces.DraftExtractionField{Instruction: "依据原文判断"}},
					{Name: "document_id", DisplayName: "文档", Type: "string", ReadOnly: true},
				}}
		}
		method := func(name, value string) askRevisionOutput {
			return askRevisionOutput{Field: interfaces.DRAFT_ASK_REVISION_DATA_PROPERTY,
				Property: &interfaces.DraftAskPropertyRevision{Name: name, ExtractionMethod: &value}}
		}

		Convey("A field saved without a method reads verbatim, as every property was extracted before\n", func() {
			So((&interfaces.DraftExtractionField{}).MethodOf(), ShouldEqual, interfaces.DRAFT_EXTRACTION_VERBATIM)
			var old interfaces.DraftExtractionField
			So(json.Unmarshal([]byte(`{"instruction":"x","examples":[]}`), &old), ShouldBeNil)
			So(old.MethodOf(), ShouldEqual, interfaces.DRAFT_EXTRACTION_VERBATIM)
			// It is written back as it was read, so an old draft's saved form does not change.
			raw, _ := json.Marshal(old)
			So(string(raw), ShouldEqual, `{"instruction":"x","examples":[]}`)
			So(interfaces.ValidDraftExtractionMethod(""), ShouldBeTrue)
			So(interfaces.ValidDraftExtractionMethod("derived"), ShouldBeFalse)
		})

		Convey("Switching to inferred is kept; proposing the method it has changes nothing\n", func() {
			got, dropped := objectTypeRevision(ctx, document(), method("price_includes_tax", " inferred "))
			So(dropped, ShouldEqual, "")
			value := got.Value.(interfaces.DraftAskPropertyRevision)
			So(*value.ExtractionMethod, ShouldEqual, interfaces.DRAFT_EXTRACTION_INFERRED)
			So(value.DisplayName, ShouldEqual, "价格是否含税")

			_, dropped = objectTypeRevision(ctx, document(), method("price_includes_tax", "verbatim"))
			So(dropped, ShouldEqual, askDroppedUnchanged)
		})

		Convey("An unknown method, a system-kept property and a table property are dropped\n", func() {
			_, dropped := objectTypeRevision(ctx, document(), method("price_includes_tax", "derived"))
			So(dropped, ShouldEqual, askDroppedInvalid)
			_, dropped = objectTypeRevision(ctx, document(), method("document_id", "inferred"))
			So(dropped, ShouldEqual, askDroppedNotEditable)
			table := document()
			table.Document = nil
			table.Properties[0].Extraction = nil
			_, dropped = objectTypeRevision(ctx, table, method("price_includes_tax", "inferred"))
			So(dropped, ShouldEqual, askDroppedNotEditable)
		})

		Convey("The Agent reads each document property's method\n", func() {
			asked := askObjectTypeOf(document())
			So(asked.Properties[0].ExtractionMethod, ShouldEqual, interfaces.DRAFT_EXTRACTION_VERBATIM)
			So(asked.Properties[1].ExtractionMethod, ShouldEqual, "")
			schema, _ := json.Marshal(askResponseFormat([]string{interfaces.DRAFT_ASK_REVISION_DATA_PROPERTY}))
			So(string(schema), ShouldContainSubstring, `"extraction_method":{"enum":["verbatim","inferred"],"type":"string"}`)
		})
	})
}
