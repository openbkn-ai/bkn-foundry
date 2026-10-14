// Copyright openbkn.ai

package concept_group

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	mock_interfaces "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
)

func TestCreateConceptGroupsUsesSharedIdentitySnapshot(t *testing.T) {
	ctrl := gomock.NewController(t)
	cga := mock_interfaces.NewMockConceptGroupAccess(ctrl)
	db, smock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	service := &conceptGroupService{cga: cga, db: db}
	groups := []*interfaces.ConceptGroup{
		{CGID: "cg-1", CGName: "group-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH},
		{CGID: "cg-2", CGName: "group-2", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH},
	}

	cga.EXPECT().GetConceptGroupIdentitiesByIDsOrNames(
		gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"cg-1", "cg-2"}, []string{"group-1", "group-2"},
	).Return([]*interfaces.ConceptGroup{
		{CGID: "cg-1", CGName: "group-1"},
		{CGID: "cg-2", CGName: "group-2"},
	}, nil)

	smock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ctx := permission.WithKNImportPermissionPrechecked(context.Background())
	ids, err := service.CreateConceptGroups(ctx, tx, groups, interfaces.ImportMode_Ignore, false)
	if err != nil {
		t.Fatalf("CreateConceptGroups() error = %v", err)
	}
	if len(ids) != 2 || ids[0] != "cg-1" || ids[1] != "cg-2" {
		t.Fatalf("CreateConceptGroups() ids = %v", ids)
	}
	if err = smock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateConceptGroupsBatchesImportSideEffects(t *testing.T) {
	ctrl := gomock.NewController(t)
	cga := mock_interfaces.NewMockConceptGroupAccess(ctrl)
	ps := mock_interfaces.NewMockPermissionService(ctrl)
	vbs := mock_interfaces.NewMockVegaBackendService(ctrl)
	db, smock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	service := &conceptGroupService{
		appSetting: &common.AppSetting{}, cga: cga, ps: ps, vbs: vbs, db: db,
	}
	groups := []*interfaces.ConceptGroup{
		{CGID: "cg-1", CGName: "group-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
			ObjectTypeIDs: []string{"ot-1"}},
		{CGID: "cg-2", CGName: "group-2", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
			ObjectTypeIDs: []string{"ot-2"}},
	}
	cga.EXPECT().GetConceptGroupIdentitiesByIDsOrNames(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"cg-1", "cg-2"}, []string{"group-1", "group-2"}).Return(nil, nil)
	cga.EXPECT().CreateConceptGroups(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ *sql.Tx, actual []*interfaces.ConceptGroup) error {
			if len(actual) != 2 || !strings.Contains(actual[0].BKNRawContent, "ot-1") ||
				!strings.Contains(actual[1].BKNRawContent, "ot-2") {
				t.Fatalf("batch concept groups did not preserve normalized members: %#v", actual)
			}
			return nil
		})
	ps.EXPECT().UpsertResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
		interfaces.RESOURCE_TYPE_KN, gomock.Len(2)).Return(nil)
	vbs.EXPECT().WriteDatasetDocument(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any(), gomock.Any()).Return(nil).Times(2)

	smock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ctx := permission.WithKNImportPermissionPrechecked(context.Background())
	ids, err := service.CreateConceptGroups(ctx, tx, groups, interfaces.ImportMode_Normal, false)
	if err != nil {
		t.Fatalf("CreateConceptGroups() error = %v", err)
	}
	if len(ids) != 2 || ids[0] != "cg-1" || ids[1] != "cg-2" {
		t.Fatalf("CreateConceptGroups() ids = %v", ids)
	}
	if err = smock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateConceptGroupsBatchesOverwritePermissionAndDatabaseUpdate(t *testing.T) {
	ctrl := gomock.NewController(t)
	cga := mock_interfaces.NewMockConceptGroupAccess(ctrl)
	ps := mock_interfaces.NewMockPermissionService(ctrl)
	vbs := mock_interfaces.NewMockVegaBackendService(ctrl)
	db, smock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	service := &conceptGroupService{
		appSetting: &common.AppSetting{}, cga: cga, ps: ps, vbs: vbs, db: db,
	}
	groups := []*interfaces.ConceptGroup{
		{CGID: "cg-1", CGName: "group-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH},
		{CGID: "cg-2", CGName: "group-2", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH},
	}
	cga.EXPECT().GetConceptGroupIdentitiesByIDsOrNames(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"cg-1", "cg-2"}, []string{"group-1", "group-2"}).Return([]*interfaces.ConceptGroup{
		{CGID: "cg-1", CGName: "group-1"}, {CGID: "cg-2", CGName: "group-2"},
	}, nil)
	ps.EXPECT().RequirePermissions(gomock.Any(), gomock.Len(2)).Return(nil)
	cga.EXPECT().UpdateConceptGroups(gomock.Any(), gomock.Any(), groups).Return(nil)
	vbs.EXPECT().WriteDatasetDocument(gomock.Any(), interfaces.BKN_DATASET_ID, gomock.Any(), gomock.Any()).Return(nil).Times(2)

	smock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ctx := permission.WithKNImportPermissionPrechecked(context.Background())
	if _, err = service.CreateConceptGroups(ctx, tx, groups, interfaces.ImportMode_Overwrite, false); err != nil {
		t.Fatalf("CreateConceptGroups() error = %v", err)
	}
	if err = smock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestConceptGroupIdentityCacheTracksSequentialRename(t *testing.T) {
	cache := newConceptGroupIdentityCache()
	cache.record(&interfaces.ConceptGroup{
		CGID: "cg-1", CGName: "old-name", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
	})
	cache.record(&interfaces.ConceptGroup{
		CGID: "cg-1", CGName: "new-name", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
	})

	oldNameKey := conceptGroupIdentityKey{knID: "kn-1", branch: interfaces.MAIN_BRANCH, value: "old-name"}
	newNameKey := conceptGroupIdentityKey{knID: "kn-1", branch: interfaces.MAIN_BRANCH, value: "new-name"}
	if _, exists := cache.byName[oldNameKey]; exists {
		t.Fatal("old name remained in identity cache after rename")
	}
	if got := cache.byName[newNameKey]; got != "cg-1" {
		t.Fatalf("new name maps to %q, want cg-1", got)
	}
}
