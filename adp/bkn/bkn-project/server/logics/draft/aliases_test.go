// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func aliasesPtr(aliases ...string) *[]string { return &aliases }

// aliasRefusal is the reason and alias an alias error gives, or fails the test for another error.
func aliasRefusal(err error) (string, string, string) {
	var httpErr *httperrors.HTTPError
	So(errors.As(err, &httpErr), ShouldBeTrue)
	So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
	So(httpErr.BaseError.ErrorCode, ShouldEqual, "BknProject.Draft.InvalidAliases")
	details := httpErr.ErrorDetails.(map[string]any)
	other, _ := details["other"].(string)
	return details["reason"].(string), details["alias"].(string), other
}

func Test_EditAliases(t *testing.T) {
	Convey("Test editing the aliases of object and relation types\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, run: func(fn func()) { fn() }}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		draft := editableDraft()
		draft.Items.ObjectTypes[0].Aliases = []string{"Clients"}
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
		var written *interfaces.Draft
		da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).
			DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
				written = d
				return true, nil
			}).AnyTimes()
		editAliases := func(kind string, id string, aliases ...string) error {
			e := edit()
			e.Aliases = &aliases
			_, err := svc.EditDraftItem(ctx, token, "p1", kind, id, e, caller)
			return err
		}

		Convey("Aliases sent replace the item's, trimmed and without empty ones, and are recorded\n", func() {
			err := editAliases(interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", " PO ", "", "Purchase orders")

			So(err, ShouldBeNil)
			So(written.Items.ObjectTypes[1].Aliases, ShouldResemble, []string{"PO", "Purchase orders"})
			last := written.Items.Decisions[len(written.Items.Decisions)-1]
			So(last.Action, ShouldEqual, interfaces.DRAFT_DECISION_ALIASES)
			So(last.Before, ShouldBeNil)
			So(last.After, ShouldResemble, []string{"PO", "Purchase orders"})
		})

		Convey("Sending none clears them\n", func() {
			So(editAliases(interfaces.DRAFT_KIND_OBJECT_TYPES, "customers"), ShouldBeNil)
			So(written.Items.ObjectTypes[0].Aliases, ShouldBeNil)
		})

		Convey("A relation type takes aliases too\n", func() {
			So(editAliases(interfaces.DRAFT_KIND_RELATION_TYPES, "orders_customers", "placed by"), ShouldBeNil)
			So(written.Items.RelationTypes[0].Aliases, ShouldResemble, []string{"placed by"})
		})

		Convey("Aliases BKN refuses are refused, saying which and why\n", func() {
			many := make([]string, interfaces.DRAFT_ALIASES_MAX+1)
			for i := range many {
				many[i] = strings.Repeat("a", i+1)
			}
			for _, c := range []struct {
				aliases []string
				reason  string
				alias   string
				other   string
			}{
				{many, aliasTooMany, "", ""},
				{[]string{strings.Repeat("长", interfaces.DRAFT_ALIAS_MAX_LENGTH+1)}, aliasTooLong, strings.Repeat("长", interfaces.DRAFT_ALIAS_MAX_LENGTH+1), ""},
				{[]string{"PO,order"}, aliasComma, "PO,order", ""},
				{[]string{"订单，单据"}, aliasComma, "订单，单据", ""},
				{[]string{"PO", " PO"}, aliasRepeated, "PO", ""},
				{[]string{"Orders"}, aliasOwnName, "Orders", ""},
				{[]string{"Customers"}, aliasOtherName, "Customers", "Customers"},
			} {
				reason, alias, other := aliasRefusal(editAliases(interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", c.aliases...))
				So(reason, ShouldEqual, c.reason)
				So(alias, ShouldEqual, c.alias)
				So(other, ShouldEqual, c.other)
			}
			So(written, ShouldBeNil)
		})

		Convey("Renaming an object type to another's alias is refused, naming the one that has it\n", func() {
			e := edit()
			e.Name = strPtr("Clients")
			_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", e, caller)

			reason, alias, other := aliasRefusal(err)
			So(reason, ShouldEqual, aliasOfAnother)
			So(alias, ShouldEqual, "Clients")
			So(other, ShouldEqual, "Customers")
		})

		Convey("Renaming an item to one of its aliases leaves that alias out, as it names the item already\n", func() {
			e := edit()
			e.Name = strPtr("Clients")
			_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "customers", e, caller)

			So(err, ShouldBeNil)
			So(written.Items.ObjectTypes[0].Name, ShouldEqual, "Clients")
			So(written.Items.ObjectTypes[0].Aliases, ShouldBeNil)
		})

		Convey("Value types take no aliases\n", func() {
			reason, _, _ := aliasRefusal(editAliases(interfaces.DRAFT_KIND_VALUE_TYPES, "levels", "Grades"))
			So(reason, ShouldEqual, aliasOnDocument)
		})
	})
}

func Test_SaveObjectTypeAliases(t *testing.T) {
	Convey("Test saving the aliases of an object type and its properties\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, va: va, ira: &fakeIDRegistry{}}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		draft := savedDraft()
		draft.Items.ObjectTypes[0].Properties[1].Aliases = []string{"Customer name"}
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
		va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_accounts"}).Return([]*interfaces.VegaTable{accountsTable()}, nil).AnyTimes()
		var written *interfaces.Draft
		da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).
			DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
				written = d
				return true, nil
			}).AnyTimes()

		Convey("The object type's and the properties' aliases are saved; a property sent without keeps its own\n", func() {
			save := saveOf(draft)
			save.Aliases = aliasesPtr("Clients")
			save.DataProperties[0].Aliases = aliasesPtr("Customer number", " CID ")

			_, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			So(err, ShouldBeNil)
			accounts := written.Items.ObjectTypes[0]
			So(accounts.Aliases, ShouldResemble, []string{"Clients"})
			So(accounts.Properties[0].Aliases, ShouldResemble, []string{"Customer number", "CID"})
			So(accounts.Properties[1].Aliases, ShouldResemble, []string{"Customer name"})
			// The property aliases go into the save's record, the object type's into their own.
			actions := []string{}
			for _, d := range written.Items.Decisions {
				actions = append(actions, d.Action)
			}
			So(actions, ShouldContain, interfaces.DRAFT_DECISION_ALIASES)
			So(actions, ShouldContain, interfaces.DRAFT_DECISION_SAVE)
		})

		Convey("A property alias that is another property's name is refused, not left out\n", func() {
			save := saveOf(draft)
			save.DataProperties[0].Aliases = aliasesPtr("Account name")

			_, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			reason, alias, other := aliasRefusal(err)
			So(reason, ShouldEqual, aliasOtherName)
			So(alias, ShouldEqual, "Account name")
			So(other, ShouldEqual, "Account name")
			So(written, ShouldBeNil)
		})

		Convey("Renaming a property to another's alias is refused, which kept the alias silently before\n", func() {
			save := saveOf(draft)
			save.DataProperties[2].DisplayName = "Customer name"

			_, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			reason, alias, _ := aliasRefusal(err)
			So(reason, ShouldEqual, aliasOtherName)
			So(alias, ShouldEqual, "Customer name")
		})

		Convey("A new property's catalog alias that is another property's display name is left out, not refused\n", func() {
			table := accountsTable()
			table.Columns[4].DisplayName, table.Columns[4].Comment = "备注", "Account name"
			va2 := pmock.NewMockVegaAccess(ctrl)
			va2.EXPECT().GetTables(gomock.Any(), caller, []string{"r_accounts"}).Return([]*interfaces.VegaTable{table}, nil).AnyTimes()
			svc.va = va2
			save := saveOf(draft)
			save.DataProperties = append(save.DataProperties, interfaces.DraftDataPropertySave{
				DisplayName: "Remark", MappedField: field("note")})

			_, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			So(err, ShouldBeNil)
			added := written.Items.ObjectTypes[0].Properties[3]
			So(added.DisplayName, ShouldEqual, "Remark")
			So(added.Aliases, ShouldBeNil)
		})

		Convey("Renaming a property to its own alias leaves that alias out\n", func() {
			save := saveOf(draft)
			save.DataProperties[1].DisplayName = "Customer name"

			_, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			So(err, ShouldBeNil)
			So(written.Items.ObjectTypes[0].Properties[1].Aliases, ShouldBeNil)
		})
	})
}

func Test_CompileLeftOutAliases(t *testing.T) {
	Convey("Test compiling reports the aliases BKN refuses instead of leaving them out silently\n", t, func() {
		items := savedDraft().Items
		accounts := items.ObjectTypes[0]
		accounts.Aliases = []string{"Clients", "Contracts"}
		accounts.Properties[1].Aliases = []string{"Customer name"}

		compiled, err := compileDraftBeside(&items, nil, []string{"Contracts"})

		So(err, ShouldBeNil)
		So(compiled.LeftOutAliases, ShouldResemble, []interfaces.DraftLeftOutAlias{{
			Item:  interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: "accounts", Name: "Accounts"},
			Alias: "Contracts",
		}})
		So(string(compiled.Files["object_types/accounts.bkn"]), ShouldContainSubstring, "Clients")
		So(string(compiled.Files["object_types/accounts.bkn"]), ShouldContainSubstring, "Customer name")
	})
}

func Test_AliasesRevision(t *testing.T) {
	Convey("Test checking the aliases Ask AI proposes\n", t, func() {
		ctx := context.Background()
		items := editableDraft().Items
		check := func(kind string, id string, name string, current []string, proposed ...string) (*interfaces.DraftAskRevision, string) {
			return aliasesRevision(ctx, &items, kind, id, name, current, proposed)
		}

		Convey("The whole list is proposed, trimmed, for a person to confirm\n", func() {
			revision, reason := check(interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", "Orders", []string{"PO"}, "PO", " Purchase orders ")
			So(reason, ShouldBeEmpty)
			So(revision.Value, ShouldResemble, []string{"PO", "Purchase orders"})
		})

		Convey("What saving would refuse, or what changes nothing, is left out with why\n", func() {
			for _, proposed := range [][]string{{"Orders"}, {"Customers"}, {"PO,SO"}, {"PO", "PO"}} {
				_, reason := check(interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", "Orders", nil, proposed...)
				So(reason, ShouldEqual, askDroppedInvalidAliases)
			}
			_, reason := check(interfaces.DRAFT_KIND_OBJECT_TYPES, "orders", "Orders", []string{"PO"}, " PO")
			So(reason, ShouldEqual, askDroppedUnchanged)
		})

		Convey("A relation type's alias may be an object type's name\n", func() {
			revision, reason := check(interfaces.DRAFT_KIND_RELATION_TYPES, "orders_customers", "Orders customers", nil, "Customers")
			So(reason, ShouldBeEmpty)
			So(revision.Value, ShouldResemble, []string{"Customers"})
		})
	})
}

func Test_AskSeesUnsavedAliases(t *testing.T) {
	Convey("Test Ask AI reads the aliases the form has not saved yet\n", t, func() {
		items := editableDraft().Items
		svc := &draftService{setting: testSetting}
		relation := items.RelationTypes[0]
		relation.Aliases = []string{"placed by"}

		working := svc.workingRelationType(context.Background(), &items, relation,
			&interfaces.DraftRelationTypeSave{Aliases: aliasesPtr(" ordered by ", "")}, caller)
		So(working.Aliases, ShouldResemble, []string{"ordered by"})
		So(relation.Aliases, ShouldResemble, []string{"placed by"})

		object := workingObjectType(items.ObjectTypes[1], &interfaces.DraftObjectTypeSave{Aliases: aliasesPtr("PO")})
		So(object.Aliases, ShouldResemble, []string{"PO"})
		So(workingObjectType(items.ObjectTypes[1], &interfaces.DraftObjectTypeSave{}).Aliases, ShouldBeNil)
	})
}

func Test_ObjectAskEditable(t *testing.T) {
	Convey("Test a document object type is offered no aliases to revise, whatever its role\n", t, func() {
		table := &interfaces.DraftObjectType{ID: "orders"}
		So(objectAskEditable(table, nil), ShouldContain, interfaces.DRAFT_ASK_REVISION_ALIASES)
		entity := &interfaces.DraftObjectType{ID: "order_doc", Document: &interfaces.DraftDocumentModel{Role: "entity"}}
		So(objectAskEditable(entity, nil), ShouldNotContain, interfaces.DRAFT_ASK_REVISION_ALIASES)
		So(objectAskEditable(entity, nil), ShouldContain, interfaces.DRAFT_ASK_REVISION_PRIMARY_KEYS)
		hooks := []string{interfaces.DRAFT_ASK_REVISION_NAME, interfaces.DRAFT_ASK_REVISION_ALIASES}
		So(objectAskEditable(entity, hooks), ShouldResemble, []string{interfaces.DRAFT_ASK_REVISION_NAME})
	})
}
