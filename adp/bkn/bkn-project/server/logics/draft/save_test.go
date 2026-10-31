// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// savedDraft has accounts keyed by id, with a name, a level using a value type and a phone the
// privacy rules excluded; tickets join accounts by their id.
func savedDraft() *interfaces.Draft {
	return &interfaces.Draft{
		ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime,
		Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{
				{
					ID: "accounts", Name: "Accounts", Description: "Accounts", Adopted: true,
					Table:       interfaces.DraftTable{ResourceID: "r_accounts", Name: "accounts", CatalogID: "c1"},
					PrimaryKeys: []string{"id"}, DisplayKey: "name",
					Key: interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_PRIMARY_KEY},
					Properties: []*interfaces.DraftProperty{
						{Name: "id", DisplayName: "Account ID", Type: "integer", MappedField: "id"},
						{Name: "name", DisplayName: "Account name", Type: "string", MappedField: "name"},
						{Name: "level", DisplayName: "Level", Type: "string", MappedField: "level_code", ValueTypeID: "levels"},
					},
				},
				{ID: "tickets", Name: "Tickets", Adopted: true, PrimaryKeys: []string{"ticket_id"},
					Table:      interfaces.DraftTable{ResourceID: "r_tickets", Name: "tickets", CatalogID: "c1"},
					Properties: []*interfaces.DraftProperty{{Name: "ticket_id", Type: "integer", MappedField: "ticket_id"}}},
			},
			RelationTypes: []*interfaces.DraftRelationType{
				{ID: "tickets_accounts", Name: "Ticket of account", Source: "tickets", Target: "accounts", Adopted: true,
					Mapping: []interfaces.DraftFieldMapping{{SourceProperty: "account_id", TargetProperty: "id"}}},
			},
			ValueTypes: []*interfaces.DraftValueType{{ID: "levels", Name: "Levels", Adopted: true,
				UsedBy: []interfaces.DraftPropertyRef{{ObjectType: "accounts", Property: "level"}}}},
			Excluded: []*interfaces.DraftExcludedField{{ObjectType: "accounts", Field: "phone", Reason: excludedPrivacy, Keyword: "phone"}},
		},
	}
}

func accountsTable() *interfaces.VegaTable {
	return &interfaces.VegaTable{ID: "r_accounts", Name: "accounts", PrimaryKeys: []string{"id"}, Columns: []interfaces.VegaColumn{
		{Name: "id", Type: "integer"}, {Name: "name", Type: "string"}, {Name: "phone", Type: "string"},
		{Name: "level_code", Type: "string"}, {Name: "note", Type: "text", DisplayName: "Note"},
		{Name: "opened_at", Type: "datetime"}, {Name: "区域", Type: "string", APIName: "region"},
		{Name: "location", Type: "other"},
	}}
}

// saveOf saves the draft's accounts as they are, which a test then changes.
func saveOf(draft *interfaces.Draft) interfaces.DraftObjectTypeSave {
	ot := draft.Items.ObjectTypes[0]
	save := interfaces.DraftObjectTypeSave{UpdateTime: readTime, Name: ot.Name, Comment: ot.Description,
		PrimaryKeys: ot.PrimaryKeys, DisplayKey: ot.DisplayKey, IncrementalKey: ot.IncrementalKey}
	for _, p := range ot.Properties {
		save.DataProperties = append(save.DataProperties, interfaces.DraftDataPropertySave{
			Name: p.Name, DisplayName: p.DisplayName, Comment: p.Description, Type: p.Type,
			MappedField: &interfaces.DraftMappedField{Name: p.MappedField},
		})
	}
	return save
}

func field(name string) *interfaces.DraftMappedField { return &interfaces.DraftMappedField{Name: name} }

func Test_SaveObjectType(t *testing.T) {
	Convey("Test saving an object type as a whole\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ira := &fakeIDRegistry{}
		svc := &draftService{setting: testSetting, da: da, ps: ps, va: va, ira: ira}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		draft := savedDraft()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
		va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_accounts"}).Return([]*interfaces.VegaTable{accountsTable()}, nil).AnyTimes()
		var written *interfaces.Draft
		expectWrite := func() {
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).
				DoAndReturn(func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
					written = d
					return true, nil
				})
		}
		codeOf := func(err error) (int, string, any) {
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			return httpErr.HTTPCode, httpErr.BaseError.ErrorCode, httpErr.ErrorDetails
		}

		Convey("Names, properties added, restored and removed are saved together and recorded\n", func() {
			expectWrite()
			save := saveOf(draft)
			save.Name, save.Comment = "Customer accounts", "Who buys"
			save.DataProperties[1].DisplayName = "Customer name"
			// level is removed; note is added under drafting's name, phone restored under a name given.
			save.DataProperties = append(save.DataProperties[:2],
				interfaces.DraftDataPropertySave{MappedField: field("note"), Comment: "Free text"},
				interfaces.DraftDataPropertySave{Name: "phone", DisplayName: "Phone", MappedField: field("phone"), Type: "string"})

			got, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			So(err, ShouldBeNil)
			So(got, ShouldEqual, written)
			ot := got.Items.ObjectTypes[0]
			So(ot.Name, ShouldEqual, "Customer accounts")
			So(ot.Description, ShouldEqual, "Who buys")
			So(findProperty(ot, "name").DisplayName, ShouldEqual, "Customer name")
			So(findProperty(ot, "level"), ShouldBeNil)
			note := findProperty(ot, "note")
			So(note.Type, ShouldEqual, "text")
			So(note.DisplayName, ShouldEqual, "Note")
			So(note.Description, ShouldEqual, "Free text")
			So(findProperty(ot, "phone").MappedField, ShouldEqual, "phone")
			// phone is restored; level_code is excluded now, by the person, and restorable by saving it again.
			So(got.Items.Excluded, ShouldHaveLength, 1)
			So(*got.Items.Excluded[0], ShouldResemble, interfaces.DraftExcludedField{ObjectType: "accounts", Field: "level_code", Reason: excludedByPerson})
			So(got.Items.ValueTypes[0].UsedBy, ShouldBeEmpty)
			actions := []string{}
			for _, d := range got.Items.Decisions {
				actions = append(actions, d.Action)
			}
			So(actions, ShouldResemble, []string{interfaces.DRAFT_DECISION_RENAME, interfaces.DRAFT_DECISION_DESCRIBE, interfaces.DRAFT_DECISION_SAVE})
			saved := got.Items.Decisions[2]
			So(saved.Before.(interfaces.DraftObjectTypeSnapshot).Properties, ShouldHaveLength, 3)
			So(saved.After.(interfaces.DraftObjectTypeSnapshot).Properties[2].MappedField, ShouldEqual, "note")
		})

		Convey("A field named in another script takes its registered id, or registers one\n", func() {
			expectWrite()
			save := saveOf(draft)
			save.DataProperties = append(save.DataProperties, interfaces.DraftDataPropertySave{MappedField: field("区域")})

			got, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			So(err, ShouldBeNil)
			So(ira.recorded, ShouldHaveLength, 1)
			So(ira.recorded[0].ID, ShouldEqual, "region")
			So(findProperty(got.Items.ObjectTypes[0], "region").MappedField, ShouldEqual, "区域")
		})

		Convey("Renaming a property moves its field to the new name, which is not taken out\n", func() {
			expectWrite()
			save := saveOf(draft)
			save.DataProperties[1] = interfaces.DraftDataPropertySave{Name: "full_name", MappedField: field("name")}
			save.DisplayKey = "full_name"

			got, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			So(err, ShouldBeNil)
			ot := got.Items.ObjectTypes[0]
			So(findProperty(ot, "name"), ShouldBeNil)
			So(findProperty(ot, "full_name").MappedField, ShouldEqual, "name")
			for _, e := range got.Items.Excluded {
				So(e.Field, ShouldNotEqual, "name")
			}
		})

		Convey("A name given to one new property is not given to another named as drafting names it\n", func() {
			expectWrite()
			save := saveOf(draft)
			save.DataProperties = append(save.DataProperties,
				interfaces.DraftDataPropertySave{Name: "note", MappedField: field("opened_at")},
				interfaces.DraftDataPropertySave{MappedField: field("note")})

			got, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			So(err, ShouldBeNil)
			ot := got.Items.ObjectTypes[0]
			So(findProperty(ot, "note").MappedField, ShouldEqual, "opened_at")
			So(findProperty(ot, "note_2").MappedField, ShouldEqual, "note")
		})

		Convey("No title sent keeps the title; a title removed gives way to the identity's first property\n", func() {
			expectWrite()
			save := saveOf(draft)
			save.DisplayKey = ""
			save.Comment = "Changed"

			got, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			So(err, ShouldBeNil)
			So(got.Items.ObjectTypes[0].DisplayKey, ShouldEqual, "name")
		})

		Convey("A title removed with its property gives way to the identity's first property\n", func() {
			expectWrite()
			save := saveOf(draft)
			save.DisplayKey = ""
			save.DataProperties = append(save.DataProperties[:1], save.DataProperties[2])

			got, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			So(err, ShouldBeNil)
			So(got.Items.ObjectTypes[0].DisplayKey, ShouldEqual, "id")
		})

		Convey("Saving what is there writes nothing\n", func() {
			got, err := svc.SaveObjectType(ctx, token, "p1", "accounts", saveOf(draft), caller)

			So(err, ShouldBeNil)
			So(got.Items.Decisions, ShouldBeEmpty)
		})

		Convey("Removing a property a relation joins by is refused, naming the relation and its ends\n", func() {
			save := saveOf(draft)
			save.PrimaryKeys = []string{"name"}
			save.DataProperties = save.DataProperties[1:]

			_, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			status, code, details := codeOf(err)
			So(status, ShouldEqual, 409)
			So(code, ShouldEqual, perrors.BknProject_Draft_PropertyInUse)
			So(details, ShouldResemble, []interfaces.DraftPropertyUse{{
				Property: "id",
				Relation: interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_RELATION_TYPE, ID: "tickets_accounts", Name: "Ticket of account"},
				Source:   interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: "tickets", Name: "Tickets"},
				Target:   interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: "accounts", Name: "Accounts"},
			}})
		})

		Convey("A property the table cannot back is refused\n", func() {
			for name, change := range map[string]func(*interfaces.DraftObjectTypeSave){
				"unknown field": func(s *interfaces.DraftObjectTypeSave) { s.DataProperties[2].MappedField = field("nope") },
				"field mapped twice": func(s *interfaces.DraftObjectTypeSave) {
					s.DataProperties = append(s.DataProperties, interfaces.DraftDataPropertySave{Name: "name2", MappedField: field("name")})
				},
				"unsupported type": func(s *interfaces.DraftObjectTypeSave) {
					s.DataProperties = append(s.DataProperties, interfaces.DraftDataPropertySave{MappedField: field("location")})
				},
				"type the field does not map to": func(s *interfaces.DraftObjectTypeSave) {
					s.DataProperties = append(s.DataProperties, interfaces.DraftDataPropertySave{MappedField: field("note"), Type: "integer"})
				},
				"kept property's type changed": func(s *interfaces.DraftObjectTypeSave) { s.DataProperties[1].Type = "text" },
				"no field":                     func(s *interfaces.DraftObjectTypeSave) { s.DataProperties[1].MappedField = nil },
				"name not an id": func(s *interfaces.DraftObjectTypeSave) {
					s.DataProperties = append(s.DataProperties, interfaces.DraftDataPropertySave{Name: "Bad Name", MappedField: field("note")})
				},
				"name taken": func(s *interfaces.DraftObjectTypeSave) {
					s.DataProperties = append(s.DataProperties, interfaces.DraftDataPropertySave{Name: "name", MappedField: field("note")})
				},
				"unknown title": func(s *interfaces.DraftObjectTypeSave) { s.DisplayKey = "nope" },
			} {
				Convey(name+"\n", func() {
					save := saveOf(draft)
					change(&save)

					_, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

					status, code, _ := codeOf(err)
					So(status, ShouldEqual, 400)
					So(code, ShouldEqual, perrors.BknProject_Draft_InvalidProperty)
				})
			}
		})

		Convey("A new identity is counted and chosen as choosing one does, and recorded with the save\n", func() {
			expectWrite()
			va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), gomock.Any()).
				Return([]map[string]any{{"rows": 10, "distinct_values": 10, "nulls": 0}}, nil).AnyTimes()
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return(nil, nil).AnyTimes()
			save := saveOf(draft)
			save.PrimaryKeys = []string{"name"}

			got, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			So(err, ShouldBeNil)
			ot := got.Items.ObjectTypes[0]
			So(ot.PrimaryKeys, ShouldResemble, []string{"name"})
			So(ot.Key.Source, ShouldEqual, interfaces.DRAFT_KEY_CHOSEN)
			last := got.Items.Decisions[len(got.Items.Decisions)-1]
			So(last.Action, ShouldEqual, interfaces.DRAFT_DECISION_SAVE)
			So(last.After.(interfaces.DraftObjectTypeSnapshot).PrimaryKeys, ShouldResemble, []string{"name"})
		})

		Convey("An identity of a type that cannot key, or none, is refused\n", func() {
			for _, keys := range [][]string{{"note_x"}, nil} {
				save := saveOf(draft)
				save.PrimaryKeys = keys

				_, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

				_, code, _ := codeOf(err)
				So(code, ShouldEqual, perrors.BknProject_Draft_InvalidIdentity)
			}
		})

		Convey("An update field of a type that cannot refresh an index is refused\n", func() {
			save := saveOf(draft)
			save.DataProperties = append(save.DataProperties, interfaces.DraftDataPropertySave{MappedField: field("note")})
			save.IncrementalKey = "note"

			_, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			_, code, _ := codeOf(err)
			So(code, ShouldEqual, perrors.BknProject_Draft_InvalidIncrementalKey)
		})

		Convey("An update field of a time type is set\n", func() {
			expectWrite()
			save := saveOf(draft)
			save.DataProperties = append(save.DataProperties, interfaces.DraftDataPropertySave{MappedField: field("opened_at")})
			save.IncrementalKey = "opened_at"

			got, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)

			So(err, ShouldBeNil)
			So(got.Items.ObjectTypes[0].IncrementalKey, ShouldEqual, "opened_at")
		})

		Convey("A draft changed since it was read, or an unknown object type, is refused\n", func() {
			save := saveOf(draft)
			save.UpdateTime = readTime - 1
			_, err := svc.SaveObjectType(ctx, token, "p1", "accounts", save, caller)
			_, code, _ := codeOf(err)
			So(code, ShouldEqual, perrors.BknProject_Draft_Stale)

			_, err = svc.SaveObjectType(ctx, token, "p1", "nope", saveOf(draft), caller)
			status, _, _ := codeOf(err)
			So(status, ShouldEqual, 404)
		})
	})
}
