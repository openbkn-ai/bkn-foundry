// Copyright openbkn.ai

//go:build integration

package integration

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/knowledge_network"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/object_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// A child counts as present while any branch of its network defines it, and only there.
func TestPresentKNChildIDsLooksAtEveryBranchOfTheNetwork(t *testing.T) {
	setting, db := openDB(t)
	ctx := context.Background()
	knID := newKN(t, db)
	otherKN := newKN(t, db)
	ota := object_type.NewObjectTypeAccess(setting)
	create := func(kn, branch, id string) {
		must(t, inTx(t, db, func(tx *sql.Tx) error {
			ot := &interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: id, OTName: id, DataSource: &interfaces.ResourceInfo{},
					DataProperties: []*interfaces.DataProperty{}, LogicProperties: []*interfaces.LogicProperty{},
					PrimaryKeys: []string{"id"},
				},
				KNID: kn, Branch: branch, UpdateTime: 1,
			}
			if err := ota.CreateObjectType(ctx, tx, ot); err != nil {
				return err
			}
			return ota.CreateObjectTypeStatus(ctx, tx, ot)
		}))
	}
	create(knID, interfaces.VersionBranch(2), "ot_history")
	create(knID, interfaces.DRAFT_BRANCH, "ot_draft")
	create(knID, interfaces.MAIN_BRANCH, "ot_draft")
	create(otherKN, interfaces.MAIN_BRANCH, "ot_elsewhere")

	presence, ok := knowledge_network.NewKNAccess(setting).(interfaces.KNChildPresence)
	if !ok {
		t.Fatal("the network access must check child presence")
	}
	got, err := presence.PresentKNChildIDs(ctx, interfaces.RESOURCE_TYPE_OBJECT_TYPE, knID,
		[]string{"ot_history", "ot_draft", "ot_elsewhere", "ot_never"})
	must(t, err)
	slices.Sort(got)
	if want := []string{"ot_draft", "ot_history"}; !slices.Equal(got, want) {
		t.Fatalf("present = %v, want %v", got, want)
	}
	// Another type with the same id is a different resource.
	got, err = presence.PresentKNChildIDs(ctx, interfaces.RESOURCE_TYPE_RELATION_TYPE, knID, []string{"ot_draft"})
	must(t, err)
	if len(got) != 0 {
		t.Fatalf("relation types present = %v, want none", got)
	}
	if _, err := presence.PresentKNChildIDs(ctx, "unknown", knID, []string{"x"}); err == nil {
		t.Fatal("an unknown type must fail, so its policies stay")
	}
}
