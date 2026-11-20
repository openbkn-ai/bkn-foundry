// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// The source files and blocks a knowledge base keeps to locate text hold no business data: the
// data check neither lists them nor the relations records have to them.
func Test_CheckLeavesOutEvidence(t *testing.T) {
	Convey("Test the data check over evidence objects\n", t, func() {
		defer projecthooks.SetForTest(projecthooks.Hooks{Drafts: conceptHooks{}})()
		doc := func(role string) *interfaces.DraftDocumentModel { return &interfaces.DraftDocumentModel{Role: role} }
		items := &interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{
				{ID: "contract", Adopted: true, Document: doc("entity"), PrimaryKeys: []string{"no"}},
				{ID: "file", Adopted: true, Document: doc("evidence_document")},
			},
			RelationTypes: []*interfaces.DraftRelationType{
				{ID: "contract_file", Source: "contract", Target: "file", Adopted: true, Document: doc("evidence_relation")},
			},
		}
		input := checkInputOf(items)
		So(input.ObjectTypes, ShouldHaveLength, 1)
		So(input.ObjectTypes[0].ID, ShouldEqual, "contract")
		So(input.RelationTypes, ShouldBeEmpty)
	})
}
