// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package concept_group

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
	rootlogics "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// The nested definitions of a concept group are written one kind after another; they are checked
// against the network once all are in, not one kind at a time.
func TestValidateConceptGroupDependenciesChecksNestedDefinitionsTogether(t *testing.T) {
	retired := &interfaces.Lifecycle{Status: interfaces.LIFECYCLE_DEPRECATED, Reason: "retired"}
	warehouse := func() *interfaces.ObjectType {
		ot := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "warehouse", OTName: "warehouse"}}
		ot.Lifecycle = retired
		return ot
	}
	shipsFrom := func(lc *interfaces.Lifecycle) *interfaces.RelationType {
		rt := &interfaces.RelationType{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: "ships_from", RTName: "ships_from", SourceObjectTypeID: "order", TargetObjectTypeID: "warehouse", Type: "direct",
		}}
		rt.Lifecycle = lc
		return rt
	}

	cases := []struct {
		name      string
		relations []*interfaces.RelationType
		wantCode  string
	}{
		// The stored ships_from still in use would fail the object type step on its own.
		{name: "deprecates the object type and its relation type", relations: []*interfaces.RelationType{shipsFrom(retired)}},
		{name: "deprecates only the object type", wantCode: berrors.BknBackend_ConceptGroup_InvalidParameter},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ota := bmock.NewMockObjectTypeAccess(ctrl)
			rta := bmock.NewMockRelationTypeAccess(ctrl)
			ata := bmock.NewMockActionTypeAccess(ctrl)
			ots := bmock.NewMockObjectTypeService(ctrl)
			rts := bmock.NewMockRelationTypeService(ctrl)
			cga := bmock.NewMockConceptGroupAccess(ctrl)
			nr := &rootlogics.NetworkRelations{OTA: ota, RTA: rta, ATA: ata}
			service := &conceptGroupService{cga: cga, ots: ots, rts: rts, nr: nr}

			ota.EXPECT().ListDeprecatedObjectTypes(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).Return(nil, nil).AnyTimes()
			ata.EXPECT().ListDeprecatedActionTypes(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).Return(nil, nil).AnyTimes()
			rta.EXPECT().ListDeprecatedOrFallbackRelationTypeIDs(gomock.Any(), "kn1", interfaces.MAIN_BRANCH).Return(nil, nil).AnyTimes()
			rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).
				Return([]*interfaces.RelationType{shipsFrom(nil)}, nil).AnyTimes()
			cga.EXPECT().CheckConceptGroupExistByID(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "cg1").Return("cg1", true, nil)
			cga.EXPECT().CheckConceptGroupExistByName(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, "cg1").Return("cg1", true, nil)

			// The nested services check their own kind the way the object type and relation type
			// services do.
			ots.EXPECT().ValidateObjectTypes(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, gomock.Any(), true, gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, knID, branch string, ots []*interfaces.ObjectType, _ bool, _ *interfaces.BatchIDIndex, _ string) error {
					return nr.SaveRefusal(ctx, knID, branch, rootlogics.SavedDefinitions{ObjectTypes: ots},
						berrors.BknBackend_ObjectType_InvalidParameter, berrors.BknBackend_ObjectType_InternalError)
				})
			if len(tc.relations) > 0 {
				rts.EXPECT().ValidateRelationTypes(gomock.Any(), "kn1", interfaces.MAIN_BRANCH, gomock.Any(), true, gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, knID, branch string, rts []*interfaces.RelationType, _ bool, _ *interfaces.BatchIDIndex, _ string) error {
						return nr.SaveRefusal(ctx, knID, branch, rootlogics.SavedDefinitions{RelationTypes: rts},
							berrors.BknBackend_RelationType_InvalidParameter, berrors.BknBackend_RelationType_InternalError)
					})
			}

			groups := []*interfaces.ConceptGroup{{
				CGID: "cg1", CGName: "cg1",
				ObjectTypes:   []*interfaces.ObjectType{warehouse()},
				RelationTypes: tc.relations,
			}}
			err := service.validateConceptGroupDependencies(context.Background(), "kn1", interfaces.MAIN_BRANCH,
				groups, true, &interfaces.BatchIDIndex{}, interfaces.ImportMode_Overwrite)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("validateConceptGroupDependencies() error = %v, want nil", err)
				}
				return
			}
			var httpErr *httperrors.HTTPError
			if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest || httpErr.ErrorCode != tc.wantCode {
				t.Fatalf("validateConceptGroupDependencies() error = %v, want 400 %s", err, tc.wantCode)
			}
		})
	}
}
