// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.

package concept_group

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

func TestGetConceptGroupIdentitiesByIDsOrNames(t *testing.T) {
	cga, smock := MockNewConceptGroupAccess(&common.AppSetting{})
	query := "SELECT f_id, f_name FROM t_concept_group WHERE f_kn_id = ? AND f_branch = ? AND (f_id IN (?,?) OR f_name IN (?,?))"
	smock.ExpectQuery(query).
		WithArgs("kn-1", interfaces.MAIN_BRANCH, "cg-1", "cg-2", "group-1", "group-2").
		WillReturnRows(sqlmock.NewRows([]string{"f_id", "f_name"}).
			AddRow("cg-1", "group-1").
			AddRow("cg-2", "group-2"))

	identities, err := cga.GetConceptGroupIdentitiesByIDsOrNames(testCtx, "kn-1", interfaces.MAIN_BRANCH,
		[]string{"cg-1", "cg-2"}, []string{"group-1", "group-2"})
	if err != nil {
		t.Fatalf("GetConceptGroupIdentitiesByIDsOrNames() error = %v", err)
	}
	if len(identities) != 2 || identities[0].CGID != "cg-1" || identities[1].CGName != "group-2" {
		t.Fatalf("identities = %#v", identities)
	}
	if err := smock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateConceptGroupRelationsBatchesRows(t *testing.T) {
	cga, smock := MockNewConceptGroupAccess(&common.AppSetting{})
	smock.ExpectBegin()
	query := "INSERT INTO t_concept_group_relation (f_id,f_kn_id,f_branch,f_group_id,f_concept_type,f_concept_id,f_create_time) VALUES (?,?,?,?,?,?,?),(?,?,?,?,?,?,?)"
	smock.ExpectExec(query).
		WithArgs(
			"rel-1", "kn-1", interfaces.MAIN_BRANCH, "cg-1", interfaces.MODULE_TYPE_OBJECT_TYPE, "ot-1", int64(1),
			"rel-2", "kn-1", interfaces.MAIN_BRANCH, "cg-1", interfaces.MODULE_TYPE_OBJECT_TYPE, "ot-2", int64(1),
		).
		WillReturnResult(sqlmock.NewResult(0, 2))
	tx, err := cga.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	relations := []*interfaces.ConceptGroupRelation{
		{ID: "rel-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, CGID: "cg-1", ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE, ConceptID: "ot-1", CreateTime: 1},
		{ID: "rel-2", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, CGID: "cg-1", ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE, ConceptID: "ot-2", CreateTime: 1},
	}
	if err = cga.CreateConceptGroupRelations(testCtx, tx, relations); err != nil {
		t.Fatalf("CreateConceptGroupRelations() error = %v", err)
	}
	if err = smock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateConceptGroupsBatchesRows(t *testing.T) {
	cga, smock := MockNewConceptGroupAccess(&common.AppSetting{})
	smock.ExpectBegin()
	query := "INSERT INTO t_concept_group (f_id,f_name,f_tags,f_comment,f_icon,f_color,f_bkn_raw_content,f_kn_id,f_branch,f_creator,f_creator_type,f_create_time,f_updater,f_updater_type,f_update_time) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?),(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)"
	smock.ExpectExec(query).
		WithArgs(
			"cg-1", "group-1", "", "comment-1", "", "", "raw-1", "kn-1", interfaces.MAIN_BRANCH,
			"user-1", "user", int64(1), "user-1", "user", int64(1),
			"cg-2", "group-2", "", "comment-2", "", "", "raw-2", "kn-1", interfaces.MAIN_BRANCH,
			"user-1", "user", int64(1), "user-1", "user", int64(1),
		).
		WillReturnResult(sqlmock.NewResult(0, 2))
	tx, err := cga.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	groups := []*interfaces.ConceptGroup{
		{CGID: "cg-1", CGName: "group-1", CommonInfo: interfaces.CommonInfo{Comment: "comment-1", BKNRawContent: "raw-1"},
			KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, Creator: interfaces.AccountInfo{ID: "user-1", Type: "user"},
			Updater: interfaces.AccountInfo{ID: "user-1", Type: "user"}, CreateTime: 1, UpdateTime: 1},
		{CGID: "cg-2", CGName: "group-2", CommonInfo: interfaces.CommonInfo{Comment: "comment-2", BKNRawContent: "raw-2"},
			KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, Creator: interfaces.AccountInfo{ID: "user-1", Type: "user"},
			Updater: interfaces.AccountInfo{ID: "user-1", Type: "user"}, CreateTime: 1, UpdateTime: 1},
	}
	if err = cga.CreateConceptGroups(testCtx, tx, groups); err != nil {
		t.Fatalf("CreateConceptGroups() error = %v", err)
	}
	if err = smock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateConceptGroupsUsesCaseBatch(t *testing.T) {
	cga, smock := MockNewConceptGroupAccess(&common.AppSetting{})
	smock.ExpectBegin()
	query := "UPDATE t_concept_group SET f_name = CASE f_id WHEN ? THEN ? ELSE f_name END, f_tags = CASE f_id WHEN ? THEN ? ELSE f_tags END, f_comment = CASE f_id WHEN ? THEN ? ELSE f_comment END, f_icon = CASE f_id WHEN ? THEN ? ELSE f_icon END, f_color = CASE f_id WHEN ? THEN ? ELSE f_color END, f_bkn_raw_content = CASE f_id WHEN ? THEN ? ELSE f_bkn_raw_content END, f_updater = CASE f_id WHEN ? THEN ? ELSE f_updater END, f_updater_type = CASE f_id WHEN ? THEN ? ELSE f_updater_type END, f_update_time = CASE f_id WHEN ? THEN ? ELSE f_update_time END WHERE f_kn_id = ? AND f_branch = ? AND f_id IN (?)"
	smock.ExpectExec(query).
		WithArgs(
			"cg-1", "group-1", "cg-1", "", "cg-1", "comment-1", "cg-1", "", "cg-1", "",
			"cg-1", "raw-1", "cg-1", "user-1", "cg-1", "user", "cg-1", int64(2),
			"kn-1", interfaces.MAIN_BRANCH, "cg-1",
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	tx, err := cga.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	groups := []*interfaces.ConceptGroup{{
		CGID: "cg-1", CGName: "group-1", CommonInfo: interfaces.CommonInfo{Comment: "comment-1", BKNRawContent: "raw-1"},
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		Updater: interfaces.AccountInfo{ID: "user-1", Type: "user"}, UpdateTime: 2,
	}}
	if err = cga.UpdateConceptGroups(testCtx, tx, groups); err != nil {
		t.Fatalf("UpdateConceptGroups() error = %v", err)
	}
	if err = smock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
