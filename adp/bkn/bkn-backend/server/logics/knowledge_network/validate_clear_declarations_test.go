// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knowledge_network

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
	rootlogics "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// overwriteImport sets up ValidateKN for an overwrite of an existing network whose object type and
// relation type checks each find declarations the file does not carry. stored are the relation
// types the network keeps besides those the file overwrites.
func overwriteImport(t *testing.T, clears bool, stored []*interfaces.RelationType) (*knowledgeNetworkService, *interfaces.KN, context.Context) {
	ctrl := gomock.NewController(t)
	kna := bmock.NewMockKNAccess(ctrl)
	ots := bmock.NewMockObjectTypeService(ctrl)
	rts := bmock.NewMockRelationTypeService(ctrl)
	ota := bmock.NewMockObjectTypeAccess(ctrl)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ata := bmock.NewMockActionTypeAccess(ctrl)

	kna.EXPECT().CheckKNExistByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH).Return("network", true, nil).AnyTimes()
	kna.EXPECT().CheckKNExistByName(gomock.Any(), "network", interfaces.MAIN_BRANCH).Return("kn-1", true, nil).AnyTimes()
	// Each service refuses nothing itself under a collector: it records what it would clear.
	ots.EXPECT().ValidateObjectTypes(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, gomock.Any(), true, gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _, _ string, _ []*interfaces.ObjectType, _ bool, _ *interfaces.BatchIDIndex, _ string) error {
			if clears {
				interfaces.CollectClearedDeclarations(ctx, []interfaces.ClearedDeclaration{
					{ObjectTypeID: "patient", Property: "id_card", Fields: []string{"semantic"}}})
			}
			return nil
		})
	rts.EXPECT().ValidateRelationTypes(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, gomock.Any(), true, gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, knID, branch string, relations []*interfaces.RelationType, _ bool, _ *interfaces.BatchIDIndex, _ string) error {
			if clears {
				interfaces.CollectClearedDeclarations(ctx, []interfaces.ClearedDeclaration{
					{RelationTypeID: "order_for_patient", Fields: []string{"cardinality"}}})
			}
			// The real service hands what it writes to the deferred network check.
			_, err := rootlogics.NewNetworkRelations().CheckSave(ctx, knID, branch, rootlogics.SavedDefinitions{RelationTypes: relations})
			return err
		})
	ota.EXPECT().ListDeprecatedObjectTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	ata.EXPECT().ListDeprecatedActionTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	rta.EXPECT().ListDeprecatedOrFallbackRelationTypeIDs(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, string, string) ([]string, error) {
			var ids []string
			for _, rt := range stored {
				ids = append(ids, rt.RTID)
			}
			return ids, nil
		}).AnyTimes()
	rta.EXPECT().GetRelationTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(stored, nil).AnyTimes()
	rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	service := &knowledgeNetworkService{
		kna: kna, ots: ots, rts: rts,
		nr: &rootlogics.NetworkRelations{OTA: ota, RTA: rta, ATA: ata},
	}
	kn := &interfaces.KN{
		KNID: "kn-1", KNName: "network", Branch: interfaces.MAIN_BRANCH,
		ObjectTypes: []*interfaces.ObjectType{{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "patient", OTName: "patient"}}},
		RelationTypes: []*interfaces.RelationType{{
			RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
				RTID: "order_for_patient", RTName: "order_for_patient", Type: "direct"}}},
	}
	return service, kn, context.Background()
}

func refusalOf(t *testing.T, err error) (*httperrors.HTTPError, map[string]any) {
	t.Helper()
	refused, ok := err.(*httperrors.HTTPError)
	if !ok {
		t.Fatalf("ValidateKN() error = %v, want an HTTP error", err)
	}
	details, _ := refused.ErrorDetails.(map[string]any)
	return refused, details
}

// An overwrite whose object types and relation types both lose declarations lists all of them in
// one 409, instead of the object types alone.
func TestValidateKNListsClearedDeclarationsOfEveryKind(t *testing.T) {
	service, kn, ctx := overwriteImport(t, true, nil)

	refused, details := refusalOf(t, service.ValidateKN(ctx, kn, true, interfaces.ImportMode_Overwrite))
	if refused.HTTPCode != http.StatusConflict || refused.ErrorCode != berrors.BknBackend_ObjectType_DeclarationsWouldBeCleared {
		t.Fatalf("refusal = %d %s, want 409 %s", refused.HTTPCode, refused.ErrorCode, berrors.BknBackend_ObjectType_DeclarationsWouldBeCleared)
	}
	cleared, _ := details["cleared"].([]interfaces.ClearedDeclaration)
	if len(cleared) != 2 || cleared[0].ObjectTypeID != "patient" || cleared[1].RelationTypeID != "order_for_patient" {
		t.Fatalf("cleared = %+v, want the object type's then the relation type's", cleared)
	}
	if _, listed := details["blocking"]; listed {
		t.Errorf("blocking = %v, want none: nothing else stands in the way", details["blocking"])
	}
}

// A stored relation type that keeps a fallback path the overwritten primary relation cannot carry is
// named in the 409, so the caller does not learn of it only after confirming.
func TestValidateKNNamesWhatStillBlocksAfterConfirming(t *testing.T) {
	stored := []*interfaces.RelationType{{
		RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: "order_for_patient_by_id_card", RTName: "by id card", Type: "direct"},
		RelationDeclarations: interfaces.RelationDeclarations{Importance: "secondary", FallbackFor: "order_for_patient"},
	}}
	service, kn, ctx := overwriteImport(t, true, stored)

	refused, details := refusalOf(t, service.ValidateKN(ctx, kn, true, interfaces.ImportMode_Overwrite))
	if refused.HTTPCode != http.StatusConflict {
		t.Fatalf("refusal = %d, want 409", refused.HTTPCode)
	}
	blocking, _ := details["blocking"].([]string)
	if len(blocking) != 1 || !strings.Contains(blocking[0], `fallback_for "order_for_patient"`) {
		t.Fatalf("blocking = %q, want the fallback_for problem", blocking)
	}
}

// With nothing to clear, the network check refuses as before.
func TestValidateKNRefusesNetworkProblemsWithoutClearedDeclarations(t *testing.T) {
	stored := []*interfaces.RelationType{{
		RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: "order_for_patient_by_id_card", RTName: "by id card", Type: "direct"},
		RelationDeclarations: interfaces.RelationDeclarations{Importance: "secondary", FallbackFor: "order_for_patient"},
	}}
	service, kn, ctx := overwriteImport(t, false, stored)

	refused, details := refusalOf(t, service.ValidateKN(ctx, kn, true, interfaces.ImportMode_Overwrite))
	if refused.HTTPCode != http.StatusBadRequest || refused.ErrorCode != berrors.BknBackend_KnowledgeNetwork_InvalidParameter {
		t.Fatalf("refusal = %d %s, want 400 %s", refused.HTTPCode, refused.ErrorCode, berrors.BknBackend_KnowledgeNetwork_InvalidParameter)
	}
	if details != nil {
		t.Errorf("details = %v, want the problem text alone", details)
	}
}

// Nothing to clear and nothing wrong passes.
func TestValidateKNPassesWithoutClearedDeclarations(t *testing.T) {
	service, kn, ctx := overwriteImport(t, false, nil)
	if err := service.ValidateKN(ctx, kn, true, interfaces.ImportMode_Overwrite); err != nil {
		t.Fatalf("ValidateKN() error = %v", err)
	}
}

// preflightImport sets up the first step of an overwrite import: the object type, relation type and
// action type services each record what the overwrite would clear and the definitions it would
// write, and the stored relation types are what the network check reads.
func preflightImport(t *testing.T, stored []*interfaces.RelationType) (*knowledgeNetworkService, *NormalizedImportPlan, *int) {
	ctrl := gomock.NewController(t)
	ots := bmock.NewMockObjectTypeService(ctrl)
	rts := bmock.NewMockRelationTypeService(ctrl)
	ats := bmock.NewMockActionTypeService(ctrl)
	ota := bmock.NewMockObjectTypeAccess(ctrl)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ata := bmock.NewMockActionTypeAccess(ctrl)
	calls := 0

	ots.EXPECT().CheckOverwriteDeclarations(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, gomock.Any(), interfaces.ImportMode_Overwrite).
		DoAndReturn(func(ctx context.Context, _, _ string, _ []*interfaces.ObjectType, _ string) error {
			calls++
			interfaces.CollectClearedDeclarations(ctx, []interfaces.ClearedDeclaration{{ObjectTypeID: "patient", Fields: []string{"aliases"}}})
			return nil
		}).AnyTimes()
	rts.EXPECT().CheckOverwriteDeclarations(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, gomock.Any(), interfaces.ImportMode_Overwrite).
		DoAndReturn(func(ctx context.Context, knID, branch string, relations []*interfaces.RelationType, _ string) error {
			calls++
			interfaces.CollectClearedDeclarations(ctx, []interfaces.ClearedDeclaration{{RelationTypeID: "order_for_patient", Fields: []string{"cardinality"}}})
			_, err := rootlogics.NewNetworkRelations().CheckSave(ctx, knID, branch, rootlogics.SavedDefinitions{RelationTypes: relations})
			return err
		}).AnyTimes()
	ats.EXPECT().CheckOverwriteDeclarations(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, gomock.Any(), interfaces.ImportMode_Overwrite).
		DoAndReturn(func(ctx context.Context, _, _ string, _ []*interfaces.ActionType, _ string) error {
			calls++
			interfaces.CollectClearedDeclarations(ctx, []interfaces.ClearedDeclaration{{ActionTypeID: "revoke", Fields: []string{"lifecycle"}}})
			return nil
		}).AnyTimes()
	ota.EXPECT().ListDeprecatedObjectTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	ata.EXPECT().ListDeprecatedActionTypes(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	rta.EXPECT().ListDeprecatedOrFallbackRelationTypeIDs(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, string, string) ([]string, error) {
			var ids []string
			for _, rt := range stored {
				ids = append(ids, rt.RTID)
			}
			return ids, nil
		}).AnyTimes()
	rta.EXPECT().GetRelationTypesByIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(stored, nil).AnyTimes()
	rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	service := &knowledgeNetworkService{ots: ots, rts: rts, ats: ats,
		nr: &rootlogics.NetworkRelations{OTA: ota, RTA: rta, ATA: ata}}
	plan := &NormalizedImportPlan{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		ObjectTypes: []*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "patient"}}},
		RelationTypes: []*interfaces.RelationType{{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: "order_for_patient", RTName: "order_for_patient", Type: "direct"}}},
		ActionTypes: []*interfaces.ActionType{{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "revoke"}}},
	}
	return service, plan, &calls
}

// The import refuses before writing, with the declarations of all three kinds of definition in the
// order object types, relation types, action types, and what still blocks the import afterwards.
func TestRefuseClearingDeclarationsListsEveryKindAndWhatStillBlocks(t *testing.T) {
	stored := []*interfaces.RelationType{{
		RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: "order_for_patient_by_id_card", RTName: "by id card", Type: "direct"},
		RelationDeclarations: interfaces.RelationDeclarations{Importance: "secondary", FallbackFor: "order_for_patient"},
	}}
	service, plan, _ := preflightImport(t, stored)

	refused, details := refusalOf(t, service.refuseClearingDeclarations(context.Background(), plan, interfaces.ImportMode_Overwrite))
	if refused.HTTPCode != http.StatusConflict || refused.ErrorCode != berrors.BknBackend_ObjectType_DeclarationsWouldBeCleared {
		t.Fatalf("refusal = %d %s, want 409 %s", refused.HTTPCode, refused.ErrorCode, berrors.BknBackend_ObjectType_DeclarationsWouldBeCleared)
	}
	cleared, _ := details["cleared"].([]interfaces.ClearedDeclaration)
	if len(cleared) != 3 || cleared[0].ObjectTypeID == "" || cleared[1].RelationTypeID == "" || cleared[2].ActionTypeID == "" {
		t.Fatalf("cleared = %+v, want object type, relation type and action type in turn", cleared)
	}
	blocking, _ := details["blocking"].([]string)
	if len(blocking) != 1 || !strings.Contains(blocking[0], `fallback_for "order_for_patient"`) {
		t.Fatalf("blocking = %q, want the fallback_for problem", blocking)
	}
}

// A confirmed request, or a mode that does not overwrite, reads nothing here.
func TestRefuseClearingDeclarationsSkipsWhatItCannotClear(t *testing.T) {
	service, plan, calls := preflightImport(t, nil)
	confirmed := interfaces.WithClearDeclarationsConfirmed(context.Background())
	if err := service.refuseClearingDeclarations(confirmed, plan, interfaces.ImportMode_Overwrite); err != nil {
		t.Fatalf("confirmed: error = %v", err)
	}
	for _, mode := range []string{interfaces.ImportMode_Normal, interfaces.ImportMode_Ignore} {
		if err := service.refuseClearingDeclarations(context.Background(), plan, mode); err != nil {
			t.Fatalf("mode %s: error = %v", mode, err)
		}
	}
	if *calls != 0 {
		t.Fatalf("services were asked %d times, want none", *calls)
	}
}
