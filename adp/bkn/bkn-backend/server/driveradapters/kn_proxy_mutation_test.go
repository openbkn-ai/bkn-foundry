// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"database/sql"
	"testing"

	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

type knProxyMutationPublisherStub struct {
	calls     int
	changes   *interfaces.KN
	mergeMode string
}

func (s *knProxyMutationPublisherStub) PublishKNChildMutation(ctx context.Context, changes *interfaces.KN,
	mergeMode string, mutate func(context.Context, *sql.Tx) error) error {
	s.calls++
	s.changes = changes
	s.mergeMode = mergeMode
	return mutate(ctx, nil)
}

func assertProxyMutation(t *testing.T, publisher *knProxyMutationPublisherStub, mergeMode string,
	assertChanges func(*interfaces.KN)) {
	t.Helper()
	if publisher.calls != 1 {
		t.Fatalf("proxy publisher calls = %d, want 1", publisher.calls)
	}
	if publisher.mergeMode != mergeMode {
		t.Fatalf("proxy merge mode = %q, want %q", publisher.mergeMode, mergeMode)
	}
	if publisher.changes == nil {
		t.Fatal("proxy mutation changes are nil")
	}
	if publisher.changes.KNID != "kn-1" || publisher.changes.Branch != interfaces.MAIN_BRANCH {
		t.Fatalf("proxy mutation identity = %q/%q", publisher.changes.KNID, publisher.changes.Branch)
	}
	assertChanges(publisher.changes)
}

func TestObjectTypeWritesUseKNProxyPublisher(t *testing.T) {
	objectType := &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-1"},
		KNID:                   "kn-1", Branch: interfaces.MAIN_BRANCH,
	}

	t.Run("create", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := bmock.NewMockObjectTypeService(ctrl)
		publisher := &knProxyMutationPublisherStub{}
		service.EXPECT().CreateObjectTypes(gomock.Any(), nil, []*interfaces.ObjectType{objectType},
			interfaces.ImportMode_Ignore, true, true).Return([]string{"ot-1"}, nil)
		handler := &restHandler{ots: service, knProxyPublisher: publisher}

		ids, err := handler.createObjectTypes(t.Context(), "kn-1", interfaces.MAIN_BRANCH,
			[]*interfaces.ObjectType{objectType}, interfaces.ImportMode_Ignore, true)
		if err != nil || len(ids) != 1 || ids[0] != "ot-1" {
			t.Fatalf("createObjectTypes() = %#v, %v", ids, err)
		}
		assertProxyMutation(t, publisher, interfaces.ImportMode_Ignore, func(changes *interfaces.KN) {
			if len(changes.ObjectTypes) != 1 || changes.ObjectTypes[0] != objectType {
				t.Fatalf("object type changes = %#v", changes.ObjectTypes)
			}
		})
	})

	t.Run("update", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := bmock.NewMockObjectTypeService(ctrl)
		publisher := &knProxyMutationPublisherStub{}
		service.EXPECT().UpdateObjectType(gomock.Any(), nil, objectType, true).Return(nil)
		handler := &restHandler{ots: service, knProxyPublisher: publisher}

		if err := handler.updateObjectType(t.Context(), objectType, true); err != nil {
			t.Fatal(err)
		}
		assertProxyMutation(t, publisher, interfaces.ImportMode_Overwrite, func(changes *interfaces.KN) {
			if len(changes.ObjectTypes) != 1 || changes.ObjectTypes[0] != objectType {
				t.Fatalf("object type changes = %#v", changes.ObjectTypes)
			}
		})
	})

	t.Run("delete", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := bmock.NewMockObjectTypeService(ctrl)
		publisher := &knProxyMutationPublisherStub{}
		service.EXPECT().DeleteObjectTypesByIDs(gomock.Any(), nil, "kn-1", interfaces.MAIN_BRANCH,
			[]string{"ot-1"}).Return(nil)
		handler := &restHandler{ots: service, knProxyPublisher: publisher}

		if err := handler.deleteObjectTypes(t.Context(), "kn-1", interfaces.MAIN_BRANCH, []string{"ot-1"}); err != nil {
			t.Fatal(err)
		}
		assertProxyMutation(t, publisher, interfaces.ImportMode_Normal, func(changes *interfaces.KN) {
			if len(changes.ObjectTypes) != 0 {
				t.Fatalf("delete object type changes = %#v", changes.ObjectTypes)
			}
		})
	})
}

func TestRelationTypeWritesUseKNProxyPublisher(t *testing.T) {
	relationType := &interfaces.RelationType{
		RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "rt-1"},
		KNID:                     "kn-1", Branch: interfaces.MAIN_BRANCH,
	}

	t.Run("create", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := bmock.NewMockRelationTypeService(ctrl)
		publisher := &knProxyMutationPublisherStub{}
		service.EXPECT().CreateRelationTypes(gomock.Any(), nil, []*interfaces.RelationType{relationType},
			interfaces.ImportMode_Ignore, true).Return([]string{"rt-1"}, nil)
		handler := &restHandler{rts: service, knProxyPublisher: publisher}

		ids, err := handler.createRelationTypes(t.Context(), "kn-1", interfaces.MAIN_BRANCH,
			[]*interfaces.RelationType{relationType}, interfaces.ImportMode_Ignore, true)
		if err != nil || len(ids) != 1 || ids[0] != "rt-1" {
			t.Fatalf("createRelationTypes() = %#v, %v", ids, err)
		}
		assertProxyMutation(t, publisher, interfaces.ImportMode_Ignore, func(changes *interfaces.KN) {
			if len(changes.RelationTypes) != 1 || changes.RelationTypes[0] != relationType {
				t.Fatalf("relation type changes = %#v", changes.RelationTypes)
			}
		})
	})

	t.Run("update", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := bmock.NewMockRelationTypeService(ctrl)
		publisher := &knProxyMutationPublisherStub{}
		service.EXPECT().UpdateRelationType(gomock.Any(), nil, relationType, true).Return(nil)
		handler := &restHandler{rts: service, knProxyPublisher: publisher}

		if err := handler.updateRelationType(t.Context(), relationType, true); err != nil {
			t.Fatal(err)
		}
		assertProxyMutation(t, publisher, interfaces.ImportMode_Overwrite, func(changes *interfaces.KN) {
			if len(changes.RelationTypes) != 1 || changes.RelationTypes[0] != relationType {
				t.Fatalf("relation type changes = %#v", changes.RelationTypes)
			}
		})
	})

	t.Run("delete", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := bmock.NewMockRelationTypeService(ctrl)
		publisher := &knProxyMutationPublisherStub{}
		service.EXPECT().DeleteRelationTypesByIDs(gomock.Any(), nil, "kn-1", interfaces.MAIN_BRANCH,
			[]string{"rt-1"}).Return(nil)
		handler := &restHandler{rts: service, knProxyPublisher: publisher}

		if err := handler.deleteRelationTypes(t.Context(), "kn-1", interfaces.MAIN_BRANCH, []string{"rt-1"}); err != nil {
			t.Fatal(err)
		}
		assertProxyMutation(t, publisher, interfaces.ImportMode_Normal, func(changes *interfaces.KN) {
			if len(changes.RelationTypes) != 0 {
				t.Fatalf("delete relation type changes = %#v", changes.RelationTypes)
			}
		})
	})
}

func TestActionTypeWritesUseKNProxyPublisher(t *testing.T) {
	actionType := &interfaces.ActionType{
		ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "at-1"},
		KNID:                   "kn-1", Branch: interfaces.MAIN_BRANCH,
	}

	t.Run("create", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := bmock.NewMockActionTypeService(ctrl)
		publisher := &knProxyMutationPublisherStub{}
		service.EXPECT().CreateActionTypes(gomock.Any(), nil, []*interfaces.ActionType{actionType},
			interfaces.ImportMode_Ignore, true).Return([]string{"at-1"}, nil)
		handler := &restHandler{ats: service, knProxyPublisher: publisher}

		ids, err := handler.createActionTypes(t.Context(), "kn-1", interfaces.MAIN_BRANCH,
			[]*interfaces.ActionType{actionType}, interfaces.ImportMode_Ignore, true)
		if err != nil || len(ids) != 1 || ids[0] != "at-1" {
			t.Fatalf("createActionTypes() = %#v, %v", ids, err)
		}
		assertProxyMutation(t, publisher, interfaces.ImportMode_Ignore, func(changes *interfaces.KN) {
			if len(changes.ActionTypes) != 1 || changes.ActionTypes[0] != actionType {
				t.Fatalf("action type changes = %#v", changes.ActionTypes)
			}
		})
	})

	t.Run("update", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := bmock.NewMockActionTypeService(ctrl)
		publisher := &knProxyMutationPublisherStub{}
		service.EXPECT().UpdateActionType(gomock.Any(), nil, actionType, true).Return(nil)
		handler := &restHandler{ats: service, knProxyPublisher: publisher}

		if err := handler.updateActionType(t.Context(), actionType, true); err != nil {
			t.Fatal(err)
		}
		assertProxyMutation(t, publisher, interfaces.ImportMode_Overwrite, func(changes *interfaces.KN) {
			if len(changes.ActionTypes) != 1 || changes.ActionTypes[0] != actionType {
				t.Fatalf("action type changes = %#v", changes.ActionTypes)
			}
		})
	})

	t.Run("delete", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := bmock.NewMockActionTypeService(ctrl)
		publisher := &knProxyMutationPublisherStub{}
		service.EXPECT().DeleteActionTypesByIDs(gomock.Any(), nil, "kn-1", interfaces.MAIN_BRANCH,
			[]string{"at-1"}).Return(nil)
		handler := &restHandler{ats: service, knProxyPublisher: publisher}

		if err := handler.deleteActionTypes(t.Context(), "kn-1", interfaces.MAIN_BRANCH, []string{"at-1"}); err != nil {
			t.Fatal(err)
		}
		assertProxyMutation(t, publisher, interfaces.ImportMode_Normal, func(changes *interfaces.KN) {
			if len(changes.ActionTypes) != 0 {
				t.Fatalf("delete action type changes = %#v", changes.ActionTypes)
			}
		})
	})
}

func TestMetricWritesUseKNProxyPublisher(t *testing.T) {
	metric := &interfaces.MetricDefinition{ID: "metric-1", KnID: "kn-1", Branch: interfaces.MAIN_BRANCH}

	t.Run("create", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := bmock.NewMockMetricService(ctrl)
		publisher := &knProxyMutationPublisherStub{}
		service.EXPECT().CreateMetrics(gomock.Any(), nil, []*interfaces.MetricDefinition{metric}, true,
			interfaces.ImportMode_Ignore).Return([]string{"metric-1"}, nil)
		handler := &restHandler{ms: service, knProxyPublisher: publisher}

		ids, err := handler.createMetrics(t.Context(), "kn-1", interfaces.MAIN_BRANCH,
			[]*interfaces.MetricDefinition{metric}, true, interfaces.ImportMode_Ignore)
		if err != nil || len(ids) != 1 || ids[0] != "metric-1" {
			t.Fatalf("createMetrics() = %#v, %v", ids, err)
		}
		assertProxyMutation(t, publisher, interfaces.ImportMode_Ignore, func(changes *interfaces.KN) {
			if len(changes.Metrics) != 1 || changes.Metrics[0] != metric {
				t.Fatalf("metric changes = %#v", changes.Metrics)
			}
		})
	})

	t.Run("update", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := bmock.NewMockMetricService(ctrl)
		publisher := &knProxyMutationPublisherStub{}
		service.EXPECT().UpdateMetric(gomock.Any(), nil, metric, true).Return(nil)
		handler := &restHandler{ms: service, knProxyPublisher: publisher}

		if err := handler.updateMetric(t.Context(), metric, true); err != nil {
			t.Fatal(err)
		}
		assertProxyMutation(t, publisher, interfaces.ImportMode_Overwrite, func(changes *interfaces.KN) {
			if len(changes.Metrics) != 1 || changes.Metrics[0] != metric {
				t.Fatalf("metric changes = %#v", changes.Metrics)
			}
		})
	})

	t.Run("delete", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := bmock.NewMockMetricService(ctrl)
		publisher := &knProxyMutationPublisherStub{}
		service.EXPECT().DeleteMetricsByIDs(gomock.Any(), nil, "kn-1", interfaces.MAIN_BRANCH,
			[]string{"metric-1"}).Return(nil)
		handler := &restHandler{ms: service, knProxyPublisher: publisher}

		if err := handler.deleteMetrics(t.Context(), "kn-1", interfaces.MAIN_BRANCH, []string{"metric-1"}); err != nil {
			t.Fatal(err)
		}
		assertProxyMutation(t, publisher, interfaces.ImportMode_Normal, func(changes *interfaces.KN) {
			if len(changes.Metrics) != 0 {
				t.Fatalf("delete metric changes = %#v", changes.Metrics)
			}
		})
	})
}
