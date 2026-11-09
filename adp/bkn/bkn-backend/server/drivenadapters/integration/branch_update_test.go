// Copyright openbkn.ai

//go:build integration

// Package integration runs the data access layer against a real MariaDB. The schema comes
// from migrations/bkn-backend/mariadb/<version>/init.sql. Point the test at a throwaway
// server only, never a shared one:
//
//	BKN_IT_DB_HOST=127.0.0.1 BKN_IT_DB_PORT=13399 BKN_IT_DB_USER=root \
//	BKN_IT_DB_PASSWORD=... BKN_IT_DB_NAME=openbkn \
//	go test -tags=integration ./drivenadapters/integration/
package integration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/action_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/concept_group"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/knowledge_network"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/object_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/relation_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const otherBranch = "dev"

// openDB returns the shared client the access constructors use, or skips the test when no
// database is configured.
func openDB(t *testing.T) (*common.AppSetting, *sql.DB) {
	t.Helper()
	host := os.Getenv("BKN_IT_DB_HOST")
	if host == "" {
		t.Skip("BKN_IT_DB_HOST is not set")
	}
	port, err := strconv.Atoi(os.Getenv("BKN_IT_DB_PORT"))
	if err != nil {
		t.Fatalf("BKN_IT_DB_PORT: %v", err)
	}
	setting := &common.AppSetting{DBSetting: libdb.DBSetting{
		Host:     host,
		Port:     port,
		Username: os.Getenv("BKN_IT_DB_USER"),
		Password: os.Getenv("BKN_IT_DB_PASSWORD"),
		DBName:   os.Getenv("BKN_IT_DB_NAME"),
	}}
	return setting, libdb.NewDB(&setting.DBSetting)
}

// newKN returns a network id unique to this run and removes its rows when the test ends.
func newKN(t *testing.T, db *sql.DB) string {
	t.Helper()
	knID := fmt.Sprintf("it_branch_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		for _, table := range []string{"t_object_type", "t_object_type_status", "t_relation_type",
			"t_action_type", "t_concept_group", "t_knowledge_network"} {
			column := "f_kn_id"
			if table == "t_knowledge_network" {
				column = "f_id"
			}
			if _, err := db.Exec("DELETE FROM "+table+" WHERE "+column+" = ?", knID); err != nil {
				t.Errorf("clean %s: %v", table, err)
			}
		}
	})
	return knID
}

// inTx runs fn in a transaction and commits it.
func inTx(t *testing.T, db *sql.DB, fn func(*sql.Tx) error) error {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// checkUpdate runs the contract every single-row definition UPDATE must keep: it changes only
// its own branch, succeeds when the row already holds the values, and reports a missing row.
func checkUpdate(t *testing.T, db *sql.DB, update func(tx *sql.Tx, branch, id, name string) error,
	read func(branch, id string) string, id string) {
	t.Helper()
	must(t, inTx(t, db, func(tx *sql.Tx) error { return update(tx, otherBranch, id, "changed") }))
	if got := read(otherBranch, id); got != "changed" {
		t.Fatalf("%s on %s = %q, want changed", id, otherBranch, got)
	}
	if got := read(interfaces.MAIN_BRANCH, id); got != "original" {
		t.Fatalf("%s on main = %q after updating %s, want original", id, got, otherBranch)
	}
	// MariaDB counts changed rows only: writing the same values again affects zero rows.
	must(t, inTx(t, db, func(tx *sql.Tx) error { return update(tx, otherBranch, id, "changed") }))
	err := inTx(t, db, func(tx *sql.Tx) error { return update(tx, otherBranch, "missing", "changed") })
	if !errors.Is(err, common.ErrRowNotFound) {
		t.Fatalf("update of a missing row = %v, want ErrRowNotFound", err)
	}
}

func TestObjectTypeUpdateStaysOnItsBranch(t *testing.T) {
	setting, db := openDB(t)
	ctx := context.Background()
	knID := newKN(t, db)
	ota := object_type.NewObjectTypeAccess(setting)
	objectType := func(branch, id, name string) *interfaces.ObjectType {
		return &interfaces.ObjectType{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID: id, OTName: name, DataSource: &interfaces.ResourceInfo{},
				DataProperties: []*interfaces.DataProperty{}, LogicProperties: []*interfaces.LogicProperty{},
				PrimaryKeys: []string{"id"},
			},
			KNID: knID, Branch: branch, UpdateTime: 1,
		}
	}
	for _, branch := range []string{interfaces.MAIN_BRANCH, otherBranch} {
		must(t, inTx(t, db, func(tx *sql.Tx) error {
			ot := objectType(branch, "ot1", "original")
			if err := ota.CreateObjectType(ctx, tx, ot); err != nil {
				return err
			}
			return ota.CreateObjectTypeStatus(ctx, tx, ot)
		}))
	}
	read := func(branch, id string) string {
		ot, err := ota.GetObjectTypeByID(ctx, nil, knID, branch, id)
		must(t, err)
		return ot.OTName
	}

	checkUpdate(t, db, func(tx *sql.Tx, branch, id, name string) error {
		return ota.UpdateObjectType(ctx, tx, objectType(branch, id, name))
	}, read, "ot1")

	// Data properties are an UPDATE of their own.
	ot := objectType(otherBranch, "ot1", "changed")
	ot.DataProperties = []*interfaces.DataProperty{{Name: "only_on_dev"}}
	must(t, inTx(t, db, func(tx *sql.Tx) error { return ota.UpdateDataProperties(ctx, tx, ot) }))
	main, err := ota.GetObjectTypeByID(ctx, nil, knID, interfaces.MAIN_BRANCH, "ot1")
	must(t, err)
	if len(main.DataProperties) != 0 {
		t.Fatalf("main data properties = %v after updating %s", main.DataProperties, otherBranch)
	}
	err = inTx(t, db, func(tx *sql.Tx) error {
		return ota.UpdateDataProperties(ctx, tx, objectType(otherBranch, "missing", "x"))
	})
	if !errors.Is(err, common.ErrRowNotFound) {
		t.Fatalf("data properties update of a missing row = %v, want ErrRowNotFound", err)
	}
}

func TestRelationTypeUpdateStaysOnItsBranch(t *testing.T) {
	setting, db := openDB(t)
	ctx := context.Background()
	knID := newKN(t, db)
	rta := relation_type.NewRelationTypeAccess(setting)
	relationType := func(branch, id, name string) *interfaces.RelationType {
		return &interfaces.RelationType{
			RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
				RTID: id, RTName: name, SourceObjectTypeID: "ot1", TargetObjectTypeID: "ot2",
				Type: interfaces.RELATION_TYPE_DIRECT, MappingRules: []interfaces.Mapping{},
			},
			KNID: knID, Branch: branch, UpdateTime: 1,
		}
	}
	for _, branch := range []string{interfaces.MAIN_BRANCH, otherBranch} {
		must(t, inTx(t, db, func(tx *sql.Tx) error {
			return rta.CreateRelationType(ctx, tx, relationType(branch, "rt1", "original"))
		}))
	}

	checkUpdate(t, db, func(tx *sql.Tx, branch, id, name string) error {
		return rta.UpdateRelationType(ctx, tx, relationType(branch, id, name))
	}, func(branch, id string) string {
		rt, err := rta.GetRelationTypeByID(ctx, knID, branch, id)
		must(t, err)
		return rt.RTName
	}, "rt1")
}

func TestActionTypeUpdateStaysOnItsBranch(t *testing.T) {
	setting, db := openDB(t)
	ctx := context.Background()
	knID := newKN(t, db)
	ata := action_type.NewActionTypeAccess(setting)
	actionType := func(branch, id, name string) *interfaces.ActionType {
		return &interfaces.ActionType{
			ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
				ATID: id, ATName: name, ActionType: interfaces.ACTION_SOURCE_TYPE_TOOL, ObjectTypeID: "ot1",
			},
			KNID: knID, Branch: branch, UpdateTime: 1,
		}
	}
	for _, branch := range []string{interfaces.MAIN_BRANCH, otherBranch} {
		must(t, inTx(t, db, func(tx *sql.Tx) error {
			return ata.CreateActionType(ctx, tx, actionType(branch, "at1", "original"))
		}))
	}

	checkUpdate(t, db, func(tx *sql.Tx, branch, id, name string) error {
		return ata.UpdateActionType(ctx, tx, actionType(branch, id, name))
	}, func(branch, id string) string {
		ats, err := ata.GetActionTypesByIDs(ctx, knID, branch, []string{id})
		must(t, err)
		if len(ats) != 1 {
			t.Fatalf("action type %s on %s: %d rows", id, branch, len(ats))
		}
		return ats[0].ATName
	}, "at1")
}

func TestConceptGroupUpdateStaysOnItsBranch(t *testing.T) {
	setting, db := openDB(t)
	ctx := context.Background()
	knID := newKN(t, db)
	cga := concept_group.NewConceptGroupAccess(setting)
	conceptGroup := func(branch, id, name string) *interfaces.ConceptGroup {
		return &interfaces.ConceptGroup{CGID: id, CGName: name, KNID: knID, Branch: branch, UpdateTime: 1}
	}
	for _, branch := range []string{interfaces.MAIN_BRANCH, otherBranch} {
		must(t, inTx(t, db, func(tx *sql.Tx) error {
			return cga.CreateConceptGroup(ctx, tx, conceptGroup(branch, "cg1", "original"))
		}))
	}

	checkUpdate(t, db, func(tx *sql.Tx, branch, id, name string) error {
		return cga.UpdateConceptGroup(ctx, tx, conceptGroup(branch, id, name))
	}, func(branch, id string) string {
		cg, err := cga.GetConceptGroupByID(ctx, knID, branch, id)
		must(t, err)
		return cg.CGName
	}, "cg1")
}

// Basic info belongs to the network; the raw and skill content belong to one branch.
func TestKNUpdateKeepsContentOnItsBranch(t *testing.T) {
	setting, db := openDB(t)
	ctx := context.Background()
	knID := newKN(t, db)
	kna := knowledge_network.NewKNAccess(setting)
	for _, branch := range []string{interfaces.MAIN_BRANCH, otherBranch} {
		must(t, inTx(t, db, func(tx *sql.Tx) error {
			return kna.CreateKN(ctx, tx, &interfaces.KN{KNID: knID, KNName: knID, Branch: branch,
				CommonInfo: interfaces.CommonInfo{BKNRawContent: "raw-" + branch}, SkillContent: "skill-" + branch})
		}))
	}
	update := func(branch, name string) error {
		return inTx(t, db, func(tx *sql.Tx) error {
			return kna.UpdateKN(ctx, tx, &interfaces.KN{KNID: knID, KNName: name, Branch: branch,
				CommonInfo: interfaces.CommonInfo{BKNRawContent: "raw-new"}, KeepSkillContent: true})
		})
	}
	read := func(branch string) *interfaces.KN {
		kn, err := kna.GetKNByID(ctx, knID, branch)
		must(t, err)
		return kn
	}

	must(t, update(otherBranch, "renamed"))
	main, dev := read(interfaces.MAIN_BRANCH), read(otherBranch)
	if main.KNName != "renamed" || dev.KNName != "renamed" {
		t.Fatalf("names = %q, %q, want renamed on both branches", main.KNName, dev.KNName)
	}
	if main.BKNRawContent != "raw-main" || main.SkillContent != "skill-main" {
		t.Fatalf("main content = %q, %q after updating %s", main.BKNRawContent, main.SkillContent, otherBranch)
	}
	if dev.BKNRawContent != "raw-new" || dev.SkillContent != "skill-dev" {
		t.Fatalf("dev content = %q, %q, want raw-new and the kept skill", dev.BKNRawContent, dev.SkillContent)
	}

	// MariaDB counts changed rows only: writing the same values again affects zero rows.
	must(t, update(otherBranch, "renamed"))

	// A branch the network is not on fails, and the basic-info write rolls back with it.
	if err := update("missing", "lost"); !errors.Is(err, common.ErrRowNotFound) {
		t.Fatalf("update on a missing branch = %v, want ErrRowNotFound", err)
	}
	if got := read(interfaces.MAIN_BRANCH).KNName; got != "renamed" {
		t.Fatalf("main name = %q after the failed update, want renamed", got)
	}
}
