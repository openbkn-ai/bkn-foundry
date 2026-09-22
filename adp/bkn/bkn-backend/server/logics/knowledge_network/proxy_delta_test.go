// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func TestDiffProxyGrantSourcesTouchesRetainedButDoesNotAdvanceVersion(t *testing.T) {
	retained := interfaces.ProxyGrantSourceSpec{
		ResourceType: "resource", ResourceID: "resource-1", Operation: "query_data",
		SourceType: interfaces.ProxyGrantSourceTypeKNBinding, SourceID: "source-1",
		KNID: "kn-1", BindingType: interfaces.MODULE_TYPE_RELATION_TYPE, BindingID: "rt-1",
	}
	upserts, additions, removals := diffProxyGrantSources(
		[]interfaces.ProxyGrantSourceSpec{retained}, []interfaces.ProxyGrantSourceSpec{retained})
	if len(upserts) != 1 || len(additions) != 0 || len(removals) != 0 {
		t.Fatalf("diff = upserts %#v, additions %#v, removals %#v", upserts, additions, removals)
	}
	version, err := proxyGrantTransitionVersion("sha256:base", additions, removals)
	if err != nil || version != "sha256:base" {
		t.Fatalf("proxyGrantTransitionVersion() = (%q, %v)", version, err)
	}
}

func TestDiffProxyGrantSourcesProducesDeterministicReplacementTransition(t *testing.T) {
	old := interfaces.ProxyGrantSourceSpec{
		ResourceType: "resource", ResourceID: "resource-old", Operation: "query_data",
		SourceType: interfaces.ProxyGrantSourceTypeKNBinding, SourceID: "source-1",
		KNID: "kn-1", BindingType: interfaces.MODULE_TYPE_OBJECT_TYPE, BindingID: "ot-1",
	}
	desired := old
	desired.ResourceID = "resource-new"
	upserts, additions, removals := diffProxyGrantSources(
		[]interfaces.ProxyGrantSourceSpec{old}, []interfaces.ProxyGrantSourceSpec{desired})
	if len(upserts) != 1 || len(additions) != 1 || len(removals) != 1 {
		t.Fatalf("replacement diff = upserts %#v, additions %#v, removals %#v", upserts, additions, removals)
	}
	first, err := proxyGrantTransitionVersion("sha256:base", additions, removals)
	if err != nil {
		t.Fatal(err)
	}
	second, err := proxyGrantTransitionVersion("sha256:base",
		[]interfaces.ProxyGrantSourceSpec{desired}, []interfaces.ProxyGrantSourceSpec{old})
	if err != nil || first != second || first == "sha256:base" {
		t.Fatalf("transition versions = %q and %q, err %v", first, second, err)
	}
}

func TestDeleteTombstoneRequiresFullProjection(t *testing.T) {
	changes := &interfaces.KN{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		ObjectTypes: []*interfaces.ObjectType{{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot-1"},
		}},
	}
	if !proxyChangesRequireFullProjection(changes, interfaces.ImportMode_Overwrite) {
		t.Fatal("object deletion tombstone did not select full recovery projection")
	}
}

func TestPrepareProxyChildDeltaForRelationReadsOnlyRelationAndEndpoints(t *testing.T) {
	ctrl := gomock.NewController(t)
	ota := bmock.NewMockObjectTypeAccess(ctrl)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	endpoints := []*interfaces.ObjectType{
		{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID: "ot-1", OTName: "one",
			DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"},
		}},
		{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID: "ot-2", OTName: "two",
			DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-2"},
		}},
	}
	rta.EXPECT().GetRelationTypesByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"rt-new"}).
		Return([]*interfaces.RelationType{}, nil)
	ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), nil, "kn-1", interfaces.MAIN_BRANCH,
		[]string{"ot-1", "ot-2"}).Return(endpoints, nil)

	oldSources, _, err := buildProxyGrantSources(&interfaces.KN{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, ObjectTypes: endpoints,
	})
	if err != nil {
		t.Fatal(err)
	}
	kpa := &proxyAccessStub{published: oldSources}
	service := &knowledgeNetworkService{ota: ota, rta: rta, kpa: kpa}
	plan := &proxyPublishPlan{mapping: &interfaces.KNProxyAccount{
		KNID: "kn-1", SyncStatus: interfaces.KNProxySyncReady,
		PublishedModelVersion: "sha256:base", SyncedModelVersion: "sha256:base",
	}}
	changes := &interfaces.KN{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		RelationTypes: []*interfaces.RelationType{{
			RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
				RTID: "rt-new", RTName: "new", Type: interfaces.RELATION_TYPE_DIRECT,
				SourceObjectTypeID: "ot-1", TargetObjectTypeID: "ot-2",
			},
		}},
	}
	delta, err := service.prepareProxyChildDelta(t.Context(), plan, changes, interfaces.ImportMode_Normal)
	if err != nil {
		t.Fatal(err)
	}
	if delta == nil || len(delta.bindings) != 3 || len(delta.desired) != 6 ||
		len(delta.upserts) != 6 || len(delta.removals) != 0 || delta.targetVersion == delta.baseVersion {
		t.Fatalf("relation delta = %#v", delta)
	}
}

func TestLoadAffectedProxyModelIncludesCurrentAndChangedLogicProperties(t *testing.T) {
	ctrl := gomock.NewController(t)
	ota := bmock.NewMockObjectTypeAccess(ctrl)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ma := bmock.NewMockMetricAccess(ctrl)
	current := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "ot-1", LogicProperties: []*interfaces.LogicProperty{{Name: "old-property"}},
	}}
	changed := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "ot-1", LogicProperties: []*interfaces.LogicProperty{{Name: "new-property"}},
	}}
	rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(nil, nil)
	ma.EXPECT().ListMetrics(gomock.Any(), gomock.Any()).Return(nil, nil)
	ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), nil, "kn-1", interfaces.MAIN_BRANCH,
		[]string{"ot-1"}).Return([]*interfaces.ObjectType{current}, nil)

	service := &knowledgeNetworkService{ota: ota, rta: rta, ma: ma}
	_, bindings, err := service.loadAffectedProxyModel(t.Context(), &interfaces.KN{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, ObjectTypes: []*interfaces.ObjectType{changed},
	})
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{
		interfaces.MODULE_TYPE_OBJECT_TYPE + "\x00ot-1":                                              true,
		"logic_property\x00" + stableProxySourceID("kn-1", "logic_property", "ot-1\x00old-property"): true,
		"logic_property\x00" + stableProxySourceID("kn-1", "logic_property", "ot-1\x00new-property"): true,
	}
	for _, binding := range bindings {
		delete(wanted, binding.BindingType+"\x00"+binding.BindingID)
	}
	if len(bindings) != 3 || len(wanted) != 0 {
		t.Fatalf("affected bindings = %#v, missing %#v", bindings, wanted)
	}
}

func TestDeniedExistingSkillDeltaRemovesOnlyItsMaterializedGrant(t *testing.T) {
	skill := interfaces.ProxyGrantSourceSpec{
		ResourceType: interfaces.KNProxyTargetTypeSkill, ResourceID: "skill-1",
		Operation: interfaces.OPERATION_TYPE_EXECUTE, SourceType: interfaces.ProxyGrantSourceTypeKNBinding,
		SourceID: "source-1", KNID: "kn-1", BindingType: interfaces.KNProxyBindingTypeCapability,
		BindingID: "binding-1",
	}
	delta := &proxyGrantDelta{
		old: []interfaces.ProxyGrantSourceSpec{skill}, desired: []interfaces.ProxyGrantSourceSpec{skill},
		upserts: []interfaces.ProxyGrantSourceSpec{skill}, baseVersion: "sha256:base", targetVersion: "sha256:base",
	}
	service := &knowledgeNetworkService{mpa: &managedProxyAccessStub{
		allowed: false, deniedResources: map[string]bool{"skill-1": true},
	}}
	selection, err := service.preflightProxyDelta(t.Context(), "proxy-1", "editor-1", delta)
	if err != nil {
		t.Fatal(err)
	}
	if err := finalizeProxyGrantDelta(delta, selection); err != nil {
		t.Fatal(err)
	}
	if len(delta.desired) != 0 || len(delta.upserts) != 0 || len(delta.removals) != 1 ||
		delta.targetVersion == delta.baseVersion {
		t.Fatalf("materialized skill delta = %#v", delta)
	}
	err = refuseNewlyMountedSkillsWithoutGrant(t.Context(), selection,
		[]*interfaces.CapabilityBinding{{ID: "binding-1"}},
		[]*interfaces.CapabilityBinding{{ID: "binding-1", CapabilityType: interfaces.CAPABILITY_TYPE_SKILL}})
	if err != nil {
		t.Fatalf("existing best-effort skill was rejected: %v", err)
	}
	err = refuseNewlyMountedSkillsWithoutGrant(t.Context(), selection, nil,
		[]*interfaces.CapabilityBinding{{ID: "binding-1", CapabilityType: interfaces.CAPABILITY_TYPE_SKILL}})
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden {
		t.Fatalf("new best-effort skill error = %#v, want HTTP 403", err)
	}
}

func TestPublishKNChildMutationUsesIncrementalPathForReadyMapping(t *testing.T) {
	ctrl := gomock.NewController(t)
	ota := bmock.NewMockObjectTypeAccess(ctrl)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ma := bmock.NewMockMetricAccess(ctrl)
	db, databaseMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	oldObject := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "ot-1", OTName: "object",
		DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-old"},
	}}
	changedObject := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "ot-1", OTName: "object",
		DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-new"},
	}}
	rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(nil, nil)
	ma.EXPECT().ListMetrics(gomock.Any(), gomock.Any()).Return(nil, nil)
	ota.EXPECT().GetObjectTypesByIDs(gomock.Any(), nil, "kn-1", interfaces.MAIN_BRANCH,
		[]string{"ot-1"}).Return([]*interfaces.ObjectType{oldObject}, nil)
	databaseMock.ExpectBegin()
	databaseMock.ExpectCommit()

	oldSources, _, err := buildProxyGrantSources(&interfaces.KN{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, ObjectTypes: []*interfaces.ObjectType{oldObject},
	})
	if err != nil {
		t.Fatal(err)
	}
	stable := interfaces.ProxyGrantSourceSpec{
		ResourceType: "resource", ResourceID: "resource-stable", Operation: interfaces.OPERATION_TYPE_QUERY_DATA,
		SourceType: interfaces.ProxyGrantSourceTypeKNBinding, SourceID: "stable-source", KNID: "kn-1",
		BindingType: interfaces.MODULE_TYPE_ACTION_TYPE, BindingID: "at-stable",
	}
	kpa := &proxyAccessStub{
		mapping: &interfaces.KNProxyAccount{
			KNID: "kn-1", ProxyAccountID: "proxy-1", LifecycleStatus: interfaces.KNProxyLifecycleActive,
			SyncStatus: interfaces.KNProxySyncReady, PublishedModelVersion: "sha256:base",
			SyncedModelVersion: "sha256:base",
		},
		published: append(oldSources, stable),
	}
	mpa := &managedProxyAccessStub{allowed: true}
	service := &knowledgeNetworkService{db: db, ota: ota, rta: rta, ma: ma, kpa: kpa, mpa: mpa}
	ctx := context.WithValue(t.Context(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "editor-1"})
	mutationCalled := false
	err = service.PublishKNChildMutation(ctx, &interfaces.KN{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, ObjectTypes: []*interfaces.ObjectType{changedObject},
	}, interfaces.ImportMode_Overwrite, func(mutationCtx context.Context, tx *sql.Tx) error {
		mutationCalled = true
		if tx == nil {
			t.Fatal("mutation transaction is nil")
		}
		account, ok := interfaces.VerifiedDependencyAccount(
			interfaces.WithDependencyBindingScope(mutationCtx, "kn-1", interfaces.MODULE_TYPE_OBJECT_TYPE, "ot-1"),
			"resource", "resource-new", interfaces.OPERATION_TYPE_QUERY_DATA)
		if !ok || account.ID != "editor-1" {
			t.Fatalf("verified dependency account = (%+v, %v)", account, ok)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !mutationCalled || mpa.deltaSyncCalls != 1 || mpa.fullSyncCalls != 0 {
		t.Fatalf("publication path: mutation=%t delta=%d full=%d", mutationCalled, mpa.deltaSyncCalls, mpa.fullSyncCalls)
	}
	if len(mpa.synced) != 2 || len(mpa.syncedRemovals) != 2 || len(kpa.published) != 3 {
		t.Fatalf("incremental sources: upserts=%#v removals=%#v published=%#v",
			mpa.synced, mpa.syncedRemovals, kpa.published)
	}
	stableKept := false
	for _, source := range kpa.published {
		if source.SourceID == stable.SourceID {
			stableKept = true
		}
		if source.BindingID == "ot-1" && source.ResourceID != "resource-new" {
			t.Fatalf("stale object source remains: %#v", source)
		}
	}
	if !stableKept {
		t.Fatal("unaffected published source was replaced")
	}
	if err := databaseMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
