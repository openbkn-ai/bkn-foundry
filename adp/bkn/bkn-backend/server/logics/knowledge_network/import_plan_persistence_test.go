// Copyright openbkn.ai

package knowledge_network

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	mock_interfaces "bkn-backend/interfaces/mock"
	"bkn-backend/logics/permission"
)

func TestPersistNormalizedImportPlanRestoresOnlyValidMemberships(t *testing.T) {
	ctrl := gomock.NewController(t)
	cgs := mock_interfaces.NewMockConceptGroupService(ctrl)
	cga := mock_interfaces.NewMockConceptGroupAccess(ctrl)
	ots := mock_interfaces.NewMockObjectTypeService(ctrl)
	ota := mock_interfaces.NewMockObjectTypeAccess(ctrl)
	ps := mock_interfaces.NewMockPermissionService(ctrl)
	service := &knowledgeNetworkService{cgs: cgs, cga: cga, ots: ots, ota: ota, ps: ps}
	group := &interfaces.ConceptGroup{CGID: "cg-valid", CGName: "valid", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH}
	objectType := &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-valid", OTName: "valid"},
		KNID:                   "kn-1", Branch: interfaces.MAIN_BRANCH,
		ConceptGroups: []*interfaces.ConceptGroup{group, {CGID: "cg-missing"}},
	}
	plan := &NormalizedImportPlan{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		ConceptGroups: []*interfaces.ConceptGroup{group},
		ObjectTypes:   []*interfaces.ObjectType{objectType},
		GroupMembers: map[string][]string{
			"cg-valid":   {"ot-valid", "ot-missing"},
			"cg-missing": {"ot-valid"},
		},
		groupOrder: []string{"cg-valid", "cg-missing"},
	}

	ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"ot-missing", "ot-existing"}).Return([]*interfaces.ObjectType{{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-existing", OTName: "Existing object"},
		KNID:                   "kn-1", Branch: interfaces.MAIN_BRANCH,
	}}, nil)
	cga.EXPECT().GetConceptIDsGroupedByConceptGroupIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"cg-valid"}, interfaces.MODULE_TYPE_OBJECT_TYPE).Return(map[string][]string{
		"cg-valid": {"ot-existing"},
	}, nil).Times(2)
	cgs.EXPECT().CreateConceptGroups(gomock.Any(), gomock.Any(), plan.ConceptGroups,
		interfaces.ImportMode_Overwrite, true).DoAndReturn(func(_ context.Context, _ *sql.Tx,
		groups []*interfaces.ConceptGroup, _ string, _ bool) ([]string, error) {
		if len(groups) != 1 || !sameImportIDs(groups[0].ObjectTypeIDs, []string{"ot-existing", "ot-valid"}) {
			t.Fatalf("concept group raw members = %v, want [ot-existing ot-valid]", groups[0].ObjectTypeIDs)
		}
		if !strings.Contains(groups[0].BKNRawContent, "Existing object") ||
			!strings.Contains(groups[0].BKNRawContent, "valid") {
			t.Fatalf("concept group raw content lost member details: %s", groups[0].BKNRawContent)
		}
		return []string{"cg-valid"}, nil
	})
	cga.EXPECT().GetConceptGroupsByIDs(gomock.Any(), gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		gomock.Any()).Return([]*interfaces.ConceptGroup{group}, nil)
	ots.EXPECT().CreateObjectTypes(gomock.Any(), gomock.Any(), gomock.Any(), interfaces.ImportMode_Overwrite,
		false, true).DoAndReturn(func(_ context.Context, _ *sql.Tx, objects []*interfaces.ObjectType,
		_ string, _ bool, _ bool) ([]string, error) {
		if len(objects) != 1 || len(objects[0].ConceptGroups) != 1 || objects[0].ConceptGroups[0].CGID != "cg-valid" {
			t.Fatalf("filtered object groups = %#v", objects[0].ConceptGroups)
		}
		return []string{"ot-valid"}, nil
	})
	cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
	ps.EXPECT().RequirePermissions(gomock.Any(), gomock.Len(1)).Return(nil)
	cga.EXPECT().CreateConceptGroupRelation(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ *sql.Tx, relation *interfaces.ConceptGroupRelation) error {
			if relation.CGID != "cg-valid" || relation.ConceptID != "ot-valid" {
				t.Fatalf("restored relation = %#v", relation)
			}
			return nil
		})

	if err := service.prepareNormalizedImportMembers(context.Background(), plan, true); err != nil {
		t.Fatalf("prepareNormalizedImportMembers() error = %v", err)
	}
	if err := service.persistNormalizedImportPlan(context.Background(), nil, plan,
		interfaces.ImportMode_Overwrite, true); err != nil {
		t.Fatalf("persistNormalizedImportPlan() error = %v", err)
	}
}

func TestRestoreImportGroupMembersBatchesAllGroups(t *testing.T) {
	ctrl := gomock.NewController(t)
	cga := mock_interfaces.NewMockConceptGroupAccess(ctrl)
	service := &knowledgeNetworkService{cga: cga}
	plan := &NormalizedImportPlan{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		GroupMembers: map[string][]string{
			"cg-1": {"ot-1", "ot-existing"},
			"cg-2": {"ot-2"},
		},
		groupOrder: []string{"cg-1", "cg-2"},
	}
	validGroups := map[string]struct{}{"cg-1": {}, "cg-2": {}}
	validObjects := map[string]struct{}{"ot-1": {}, "ot-2": {}, "ot-existing": {}}
	cga.EXPECT().GetConceptIDsGroupedByConceptGroupIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"cg-1", "cg-2"}, interfaces.MODULE_TYPE_OBJECT_TYPE).Return(map[string][]string{
		"cg-1": {"ot-existing"},
	}, nil)
	cga.EXPECT().CreateConceptGroupRelations(gomock.Any(), gomock.Any(), gomock.Len(2)).DoAndReturn(
		func(_ context.Context, _ *sql.Tx, relations []*interfaces.ConceptGroupRelation) error {
			if relations[0].CGID != "cg-1" || relations[0].ConceptID != "ot-1" ||
				relations[1].CGID != "cg-2" || relations[1].ConceptID != "ot-2" {
				t.Fatalf("restored relations = %#v", relations)
			}
			return nil
		})

	ctx := permission.WithKNImportPermissionPrechecked(context.Background())
	if err := service.restoreImportGroupMembers(ctx, nil, plan, interfaces.ImportMode_Ignore, validGroups, validObjects); err != nil {
		t.Fatalf("restoreImportGroupMembers() error = %v", err)
	}
	if plan.RestoredMemberCount != 2 {
		t.Fatalf("RestoredMemberCount = %d, want 2", plan.RestoredMemberCount)
	}
}

func TestPrepareNormalizedImportMembersExcludesRemovedObjectGroup(t *testing.T) {
	ctrl := gomock.NewController(t)
	cga := mock_interfaces.NewMockConceptGroupAccess(ctrl)
	ota := mock_interfaces.NewMockObjectTypeAccess(ctrl)
	service := &knowledgeNetworkService{cga: cga, ota: ota}
	group := &interfaces.ConceptGroup{CGID: "cg-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH}
	plan := &NormalizedImportPlan{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		ConceptGroups: []*interfaces.ConceptGroup{group},
		ObjectTypes: []*interfaces.ObjectType{{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-removed"},
		}},
		GroupMembers: map[string][]string{},
	}
	cga.EXPECT().GetConceptIDsGroupedByConceptGroupIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"cg-1"}, interfaces.MODULE_TYPE_OBJECT_TYPE).Return(map[string][]string{
		"cg-1": {"ot-removed", "ot-preserved"},
	}, nil)
	ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"ot-preserved"}).Return([]*interfaces.ObjectType{{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-preserved"},
	}}, nil)
	if err := service.prepareNormalizedImportMembers(context.Background(), plan, true); err != nil {
		t.Fatalf("prepareNormalizedImportMembers() error = %v", err)
	}
	if !sameImportIDs(group.ObjectTypeIDs, []string{"ot-preserved"}) {
		t.Fatalf("prepared group members = %v, want [ot-preserved]", group.ObjectTypeIDs)
	}
}

func TestRestoreImportGroupMembersOverwritePreservesUnmentionedGroupMembers(t *testing.T) {
	ctrl := gomock.NewController(t)
	cga := mock_interfaces.NewMockConceptGroupAccess(ctrl)
	service := &knowledgeNetworkService{cga: cga}
	plan := &NormalizedImportPlan{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		ConceptGroups: []*interfaces.ConceptGroup{{CGID: "cg-1"}},
		GroupMembers:  map[string][]string{},
	}
	ctx := permission.WithKNImportPermissionPrechecked(context.Background())
	if err := service.restoreImportGroupMembers(ctx, nil, plan, interfaces.ImportMode_Overwrite,
		map[string]struct{}{"cg-1": {}}, nil); err != nil {
		t.Fatalf("restoreImportGroupMembers() error = %v", err)
	}
}

func TestRestoreImportGroupMembersOverwriteReplacesObjectGroups(t *testing.T) {
	ctrl := gomock.NewController(t)
	cga := mock_interfaces.NewMockConceptGroupAccess(ctrl)
	service := &knowledgeNetworkService{cga: cga}
	plan := &NormalizedImportPlan{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		ObjectTypes: []*interfaces.ObjectType{{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-1"},
		}},
		GroupMembers: map[string][]string{},
	}
	cga.EXPECT().GetConceptGroupsByOTIDs(gomock.Any(), gomock.Any(), gomock.Any()).Return(
		map[string][]*interfaces.ConceptGroup{"ot-1": {{CGID: "cg-old"}}}, nil)
	cga.EXPECT().GetConceptIDsGroupedByConceptGroupIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH,
		[]string{"cg-old"}, interfaces.MODULE_TYPE_OBJECT_TYPE).Return(map[string][]string{
		"cg-old": {"ot-1", "ot-other"},
	}, nil)
	cga.EXPECT().DeleteObjectTypesFromGroup(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ *sql.Tx, query interfaces.ConceptGroupRelationsQueryParams) (int64, error) {
			if !sameImportIDs(query.CGIDs, []string{"cg-old"}) || !sameImportIDs(query.OTIDs, []string{"ot-1"}) {
				t.Fatalf("deleted relations = %#v", query)
			}
			return 1, nil
		})
	ctx := permission.WithKNImportPermissionPrechecked(context.Background())
	if err := service.restoreImportGroupMembers(ctx, nil, plan, interfaces.ImportMode_Overwrite,
		nil, map[string]struct{}{"ot-1": {}}); err != nil {
		t.Fatalf("restoreImportGroupMembers() error = %v", err)
	}
}
