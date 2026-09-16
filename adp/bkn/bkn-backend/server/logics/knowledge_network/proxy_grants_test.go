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

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

// projectionFixture is the persisted main model ExportKNForProjection returns.
type projectionFixture struct {
	kn       *interfaces.KN
	bindings []*interfaces.CapabilityBinding
	exports  int
}

func newSkillProjectionFixture() *projectionFixture {
	return &projectionFixture{
		kn: &interfaces.KN{
			KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
			ObjectTypes: []*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID: "ot-1", DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"},
			}}},
		},
		bindings: []*interfaces.CapabilityBinding{{
			ID: "binding-skill-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
			CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-1",
		}},
	}
}

func newProjectionService(t *testing.T, fixture *projectionFixture, kpa interfaces.KNProxyAccess,
	mpa interfaces.ManagedProxyAccess) *knowledgeNetworkService {
	t.Helper()
	ctrl := gomock.NewController(t)
	kna := bmock.NewMockKNAccess(ctrl)
	cga := bmock.NewMockConceptGroupAccess(ctrl)
	ota := bmock.NewMockObjectTypeAccess(ctrl)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ata := bmock.NewMockActionTypeAccess(ctrl)
	ma := bmock.NewMockMetricAccess(ctrl)
	cba := bmock.NewMockCapabilityBindingAccess(ctrl)
	kna.EXPECT().GetKNByID(gomock.Any(), fixture.kn.KNID, interfaces.MAIN_BRANCH).DoAndReturn(
		func(context.Context, string, string) (*interfaces.KN, error) {
			fixture.exports++
			return &interfaces.KN{KNID: fixture.kn.KNID, Branch: interfaces.MAIN_BRANCH}, nil
		}).AnyTimes()
	cga.EXPECT().ListConceptGroups(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	ota.EXPECT().ListObjectTypes(gomock.Any(), nil, gomock.Any()).DoAndReturn(
		func(context.Context, any, interfaces.ObjectTypesQueryParams) ([]*interfaces.ObjectType, error) {
			return fixture.kn.ObjectTypes, nil
		}).AnyTimes()
	rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	ma.EXPECT().ListMetrics(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	cba.EXPECT().ListBindings(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, interfaces.CapabilityBindingsQueryParams) ([]*interfaces.CapabilityBinding, error) {
			return fixture.bindings, nil
		}).AnyTimes()
	return &knowledgeNetworkService{kna: kna, cga: cga, ota: ota, rta: rta, ata: ata, ma: ma, cba: cba, kpa: kpa, mpa: mpa}
}

func fixtureVersion(t *testing.T, fixture *projectionFixture) string {
	t.Helper()
	_, version, err := buildProxyGrantSourcesWithCapabilities(fixture.kn, fixture.bindings)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

// skillAwareSafeStub adds to managedProxyAccessStub what a bkn-safe release
// without Skill sources, or an unavailable one, answers.
type skillAwareSafeStub struct {
	*managedProxyAccessStub
	rejectSkillBatches bool
	checkErr           error
	grantors           []string
	batches            [][]interfaces.ProxyGrantSourceSpec
}

func (s *skillAwareSafeStub) CheckGrants(ctx context.Context, proxyID, grantorID string,
	sources []interfaces.ProxyGrantSourceSpec) (interfaces.ProxyGrantBatchCheckResult, error) {
	s.grantors = append(s.grantors, grantorID)
	s.batches = append(s.batches, append([]interfaces.ProxyGrantSourceSpec(nil), sources...))
	if s.checkErr != nil {
		return interfaces.ProxyGrantBatchCheckResult{}, s.checkErr
	}
	if s.rejectSkillBatches {
		for _, source := range sources {
			if source.ResourceType == interfaces.KNProxyTargetTypeSkill {
				return interfaces.ProxyGrantBatchCheckResult{}, &interfaces.ManagedProxyStatusError{
					Method: http.MethodPost, Path: "/check-batch", StatusCode: http.StatusBadRequest,
				}
			}
		}
	}
	return s.managedProxyAccessStub.CheckGrants(ctx, proxyID, grantorID, sources)
}

func skillAndDataSources() []interfaces.ProxyGrantSourceSpec {
	return []interfaces.ProxyGrantSourceSpec{
		{ResourceType: "resource", ResourceID: "resource-1", Operation: interfaces.OPERATION_TYPE_QUERY_DATA,
			SourceType: interfaces.ProxyGrantSourceTypeKNBinding, SourceID: "source-ot", KNID: "kn-1",
			BindingType: interfaces.MODULE_TYPE_OBJECT_TYPE, BindingID: "ot-1"},
		{ResourceType: interfaces.KNProxyTargetTypeSkill, ResourceID: "skill-1", Operation: interfaces.OPERATION_TYPE_EXECUTE,
			SourceType: interfaces.ProxyGrantSourceTypeKNBinding, SourceID: "source-skill", KNID: "kn-1",
			BindingType: interfaces.KNProxyBindingTypeCapability, BindingID: "binding-skill-1"},
	}
}

func TestPreflightSkipsOnlyTheSkillTheDelegatorCannotGrant(t *testing.T) {
	safe := &skillAwareSafeStub{managedProxyAccessStub: &managedProxyAccessStub{
		allowed: true, deniedResources: map[string]bool{"skill-1": true},
	}}
	service := &knowledgeNetworkService{mpa: safe}
	sources := skillAndDataSources()

	selection, err := service.preflightProxySources(t.Context(), "proxy-1", "editor-1", sources)
	if err != nil {
		t.Fatalf("preflight refused a network because of a skill: %v", err)
	}
	kept := selection.materialized(sources)
	if len(kept) != 1 || kept[0].ResourceType != "resource" {
		t.Fatalf("materialized = %#v, want the data source only", kept)
	}
	skipped := selection.skippedGrants()
	if len(skipped) != 1 || skipped[0].source.ResourceID != "skill-1" || skipped[0].reason != proxySkipReasonDelegatorDenied {
		t.Fatalf("skipped = %#v", skipped)
	}
}

func TestPreflightStillRefusesMissingDataPermissionWithoutListingSkills(t *testing.T) {
	safe := &skillAwareSafeStub{managedProxyAccessStub: &managedProxyAccessStub{
		allowed: true, deniedResources: map[string]bool{"skill-1": true, "resource-1": true},
	}}
	service := &knowledgeNetworkService{mpa: safe}

	_, err := service.preflightProxySources(t.Context(), "proxy-1", "editor-1", skillAndDataSources())
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden ||
		httpErr.BaseError.ErrorCode != berrors.BknBackend_KnowledgeNetwork_ProxyPermissionMissing {
		t.Fatalf("preflight error = %v, want ProxyPermissionMissing", err)
	}
	details, ok := httpErr.BaseError.ErrorDetails.(missingProxyPermissionDetails)
	if !ok || len(details.MissingPermissions) != 1 || details.MissingPermissions[0].ResourceID != "resource-1" {
		t.Fatalf("missing permissions = %#v, want only the data source", httpErr.BaseError.ErrorDetails)
	}
}

func TestPreflightRetriesWithoutSkillsWhenSafePredatesThem(t *testing.T) {
	safe := &skillAwareSafeStub{rejectSkillBatches: true, managedProxyAccessStub: &managedProxyAccessStub{allowed: true}}
	service := &knowledgeNetworkService{mpa: safe}
	sources := skillAndDataSources()

	selection, err := service.preflightProxySources(t.Context(), "proxy-1", "editor-1", sources)
	if err != nil {
		t.Fatalf("an older bkn-safe blocked the network: %v", err)
	}
	if len(safe.batches) != 2 || len(safe.batches[1]) != 1 || safe.batches[1][0].ResourceType != "resource" {
		t.Fatalf("check batches = %#v, want a second batch without skills", safe.batches)
	}
	skipped := selection.skippedGrants()
	if len(skipped) != 1 || skipped[0].reason != proxySkipReasonUnsupported {
		t.Fatalf("skipped = %#v", skipped)
	}
	if kept := selection.materialized(sources); len(kept) != 1 || kept[0].ResourceType != "resource" {
		t.Fatalf("materialized = %#v", kept)
	}
}

func TestPreflightKeepsSkillsWhenSafeIsUnavailable(t *testing.T) {
	safe := &skillAwareSafeStub{
		checkErr:               &interfaces.ManagedProxyStatusError{StatusCode: http.StatusServiceUnavailable},
		managedProxyAccessStub: &managedProxyAccessStub{allowed: true},
	}
	service := &knowledgeNetworkService{mpa: safe}

	_, err := service.preflightProxySources(t.Context(), "proxy-1", "editor-1", skillAndDataSources())
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusServiceUnavailable {
		t.Fatalf("preflight error = %v, want unavailable", err)
	}
	if len(safe.batches) != 1 {
		t.Fatalf("an outage was retried without skills: %d batches", len(safe.batches))
	}
}

func TestFinishProxyPublishDecidesAnUnseenSkillBeforeSync(t *testing.T) {
	fixture := newSkillProjectionFixture()
	kpa := &proxyAccessStub{}
	safe := &skillAwareSafeStub{managedProxyAccessStub: &managedProxyAccessStub{
		allowed: true, deniedResources: map[string]bool{"skill-1": true},
	}}
	service := newProjectionService(t, fixture, kpa, safe)
	version := fixtureVersion(t, fixture)
	// The preflight saw the model before the skill was mounted, as an import
	// that mounts capabilities inside its transaction does.
	sourcesWithoutSkill, _, err := buildProxyGrantSourcesWithCapabilities(fixture.kn, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan := &proxyPublishPlan{
		mapping:     &interfaces.KNProxyAccount{KNID: "kn-1", ProxyAccountID: "proxy-1"},
		delegatorID: "grantor-1", modelVersion: version, grants: newProxyGrantSelection(sourcesWithoutSkill),
	}

	if err := service.finishProxyPublish(t.Context(), plan); err != nil {
		t.Fatalf("finishProxyPublish() error = %v", err)
	}
	for _, source := range safe.synced {
		if source.ResourceType == interfaces.KNProxyTargetTypeSkill {
			t.Fatalf("a skill the delegator cannot grant was synchronized: %#v", safe.synced)
		}
	}
	if len(safe.synced) != 2 || kpa.syncStatus != interfaces.KNProxySyncReady || kpa.syncedVersion != version {
		t.Fatalf("sync = %#v, status %q %q; want the data sources and a ready mapping",
			safe.synced, kpa.syncStatus, kpa.syncedVersion)
	}
}

func TestFinishProxyPublishSynchronizesAGrantableSkill(t *testing.T) {
	fixture := newSkillProjectionFixture()
	kpa := &proxyAccessStub{}
	safe := &skillAwareSafeStub{managedProxyAccessStub: &managedProxyAccessStub{allowed: true}}
	service := newProjectionService(t, fixture, kpa, safe)
	sources, version, err := buildProxyGrantSourcesWithCapabilities(fixture.kn, fixture.bindings)
	if err != nil {
		t.Fatal(err)
	}
	grants, err := service.preflightProxySources(t.Context(), "proxy-1", "grantor-1", sources)
	if err != nil {
		t.Fatal(err)
	}
	plan := &proxyPublishPlan{
		mapping:     &interfaces.KNProxyAccount{KNID: "kn-1", ProxyAccountID: "proxy-1"},
		delegatorID: "grantor-1", modelVersion: version, grants: grants,
	}
	if err := service.finishProxyPublish(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if len(safe.batches) != 1 {
		t.Fatalf("an already checked skill was checked again: %d batches", len(safe.batches))
	}
	skills := 0
	for _, source := range safe.synced {
		if source.ResourceType == interfaces.KNProxyTargetTypeSkill && source.ResourceID == "skill-1" {
			skills++
		}
	}
	if skills != 1 {
		t.Fatalf("synced = %#v, want the mounted skill materialized", safe.synced)
	}
}

func TestResolveKNProxyBindingCacheFollowsSkillMountsThroughMappingVersion(t *testing.T) {
	fixture := newSkillProjectionFixture()
	version := fixtureVersion(t, fixture)
	mapping := &interfaces.KNProxyAccount{
		KNID: "kn-1", ProxyAccountID: "proxy-1", LifecycleStatus: interfaces.KNProxyLifecycleActive,
		SyncStatus: interfaces.KNProxySyncReady, PublishedModelVersion: version, SyncedModelVersion: version, Version: 5,
	}
	service := newProjectionService(t, fixture, &proxyAccessStub{mapping: mapping}, nil)
	skill := func(bindingID, skillID string) interfaces.KNProxyBinding {
		return interfaces.KNProxyBinding{
			ChildType: interfaces.KNProxyBindingTypeCapability, ChildID: bindingID,
			TargetType: interfaces.KNProxyTargetTypeSkill, TargetID: skillID, Operation: interfaces.OPERATION_TYPE_EXECUTE,
		}
	}

	for range 2 {
		if _, err := service.ResolveKNProxyBinding(t.Context(), "kn-1", skill("binding-skill-1", "skill-1")); err != nil {
			t.Fatalf("mounted skill was refused: %v", err)
		}
	}
	if fixture.exports != 1 {
		t.Fatalf("exports = %d, want the second resolve served from the cache", fixture.exports)
	}

	// Another replica releases skill-1 and mounts skill-2. The model version is
	// unchanged; the synchronization that did it advanced the mapping's version.
	fixture.bindings = []*interfaces.CapabilityBinding{{
		ID: "binding-skill-2", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-2",
	}}
	if fixtureVersion(t, fixture) != version {
		t.Fatal("remounting a skill changed the model version")
	}
	mapping.Version = 7
	if _, err := service.ResolveKNProxyBinding(t.Context(), "kn-1", skill("binding-skill-2", "skill-2")); err != nil {
		t.Fatalf("newly mounted skill was refused: %v", err)
	}
	_, err := service.ResolveKNProxyBinding(t.Context(), "kn-1", skill("binding-skill-1", "skill-1"))
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.BaseError.ErrorCode != berrors.BknBackend_KnowledgeNetwork_ProxyBindingInvalid {
		t.Fatalf("released skill resolve error = %v, want ProxyBindingInvalid", err)
	}
}

func TestPublishKNCapabilityMutationRequiresExecuteOnANewlyMountedSkill(t *testing.T) {
	ctx := context.WithValue(t.Context(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "editor-1"})
	newSkill := &interfaces.CapabilityBinding{
		ID: "binding-skill-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-1",
	}
	mutate := func(context.Context, *sql.Tx) (*interfaces.KNCapabilityMutationResult, error) {
		return &interfaces.KNCapabilityMutationResult{Bindings: []*interfaces.CapabilityBinding{newSkill}}, nil
	}
	activeMapping := func() *interfaces.KNProxyAccount {
		return &interfaces.KNProxyAccount{KNID: "kn-1", ProxyAccountID: "proxy-1",
			LifecycleStatus: interfaces.KNProxyLifecycleActive}
	}

	t.Run("new mount is refused", func(t *testing.T) {
		fixture := newSkillProjectionFixture()
		fixture.bindings = nil
		kpa := &proxyAccessStub{mapping: activeMapping()}
		safe := &skillAwareSafeStub{managedProxyAccessStub: &managedProxyAccessStub{
			allowed: true, deniedResources: map[string]bool{"skill-1": true},
		}}
		service := newProjectionService(t, fixture, kpa, safe)
		db, sqlMock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		sqlMock.ExpectBegin()
		sqlMock.ExpectRollback()
		service.db = db

		_, err = service.PublishKNCapabilityMutation(ctx, "kn-1", interfaces.MAIN_BRANCH, nil, mutate)
		var httpErr *rest.HTTPError
		if !errors.As(err, &httpErr) || httpErr.BaseError.ErrorCode != berrors.BknBackend_KnowledgeNetwork_ProxyPermissionMissing {
			t.Fatalf("mount error = %v, want ProxyPermissionMissing", err)
		}
		details, _ := httpErr.BaseError.ErrorDetails.(missingProxyPermissionDetails)
		if len(details.MissingPermissions) != 1 || details.MissingPermissions[0].ResourceType != interfaces.KNProxyTargetTypeSkill {
			t.Fatalf("missing permissions = %#v", httpErr.BaseError.ErrorDetails)
		}
		if kpa.pendingCount != 0 || safe.syncCalls != 0 {
			t.Fatal("a refused mount marked the proxy pending or synchronized it")
		}
		if err := sqlMock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("an already mounted skill stays best effort", func(t *testing.T) {
		fixture := newSkillProjectionFixture()
		kpa := &proxyAccessStub{mapping: activeMapping()}
		safe := &skillAwareSafeStub{managedProxyAccessStub: &managedProxyAccessStub{
			allowed: true, deniedResources: map[string]bool{"skill-1": true},
		}}
		service := newProjectionService(t, fixture, kpa, safe)
		db, sqlMock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		sqlMock.ExpectBegin()
		sqlMock.ExpectCommit()
		service.db = db

		if _, err := service.PublishKNCapabilityMutation(ctx, "kn-1", interfaces.MAIN_BRANCH, nil, mutate); err != nil {
			t.Fatalf("re-attaching a mounted skill failed the network: %v", err)
		}
		if kpa.syncStatus != interfaces.KNProxySyncReady {
			t.Fatalf("sync status = %q, want ready", kpa.syncStatus)
		}
		for _, source := range safe.synced {
			if source.ResourceType == interfaces.KNProxyTargetTypeSkill {
				t.Fatalf("an ungrantable skill was synchronized: %#v", safe.synced)
			}
		}
	})
}

func TestPublishKNCapabilityMutationMountsASkillWhileSafePredatesSkillSources(t *testing.T) {
	ctx := context.WithValue(t.Context(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "editor-1"})
	fixture := newSkillProjectionFixture()
	fixture.bindings = nil
	kpa := &proxyAccessStub{mapping: &interfaces.KNProxyAccount{KNID: "kn-1", ProxyAccountID: "proxy-1",
		LifecycleStatus: interfaces.KNProxyLifecycleActive}}
	safe := &skillAwareSafeStub{rejectSkillBatches: true, managedProxyAccessStub: &managedProxyAccessStub{allowed: true}}
	service := newProjectionService(t, fixture, kpa, safe)
	db, sqlMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sqlMock.ExpectBegin()
	sqlMock.ExpectCommit()
	service.db = db
	mutate := func(context.Context, *sql.Tx) (*interfaces.KNCapabilityMutationResult, error) {
		// The row commits with the mutation; the reload after commit sees it.
		fixture.bindings = []*interfaces.CapabilityBinding{{
			ID: "binding-skill-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
			CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-1",
		}}
		return &interfaces.KNCapabilityMutationResult{Bindings: fixture.bindings}, nil
	}

	if _, err := service.PublishKNCapabilityMutation(ctx, "kn-1", interfaces.MAIN_BRANCH, nil, mutate); err != nil {
		t.Fatalf("an older bkn-safe blocked a skill mount: %v", err)
	}
	if kpa.syncStatus != interfaces.KNProxySyncReady {
		t.Fatalf("sync status = %q, want ready", kpa.syncStatus)
	}
}

func TestPublishKNCapabilityMutationKeepsFunctionGrantType(t *testing.T) {
	ctx := context.WithValue(t.Context(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "editor-1"})
	fixture := newSkillProjectionFixture()
	fixture.bindings = nil
	function := &interfaces.CapabilityBinding{
		ID: "binding-function-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-function", CapabilityID: "tool-function",
	}
	kpa := &proxyAccessStub{mapping: &interfaces.KNProxyAccount{
		KNID: "kn-1", ProxyAccountID: "proxy-1", LifecycleStatus: interfaces.KNProxyLifecycleActive,
	}}
	safe := &skillAwareSafeStub{managedProxyAccessStub: &managedProxyAccessStub{allowed: true}}
	service := newProjectionService(t, fixture, kpa, safe)
	ctrl := gomock.NewController(t)
	aoa := bmock.NewMockAgentOperatorAccess(ctrl)
	aoa.EXPECT().ListBoxTools(gomock.Any(), "box-function").Return([]*interfaces.ToolBrief{{
		ToolID: "tool-function", BoxMetadataType: interfaces.EXEC_BOX_METADATA_TYPE_FUNCTION,
	}}, nil).AnyTimes()
	service.aoa = aoa
	db, sqlMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sqlMock.ExpectBegin()
	sqlMock.ExpectCommit()
	service.db = db
	mutate := func(context.Context, *sql.Tx) (*interfaces.KNCapabilityMutationResult, error) {
		fixture.bindings = []*interfaces.CapabilityBinding{function}
		return &interfaces.KNCapabilityMutationResult{Bindings: fixture.bindings}, nil
	}

	_, publishErr := service.PublishKNCapabilityMutation(ctx, "kn-1", interfaces.MAIN_BRANCH, nil, mutate)
	seenFunction := false
	for _, source := range safe.checked {
		if source.ResourceID != function.OwnerID {
			continue
		}
		if source.ResourceType != "function" {
			t.Fatalf("Function capability checked as %q, want function", source.ResourceType)
		}
		seenFunction = true
	}
	if !seenFunction {
		t.Fatal("Function capability was not checked before synchronization")
	}
	if publishErr != nil {
		t.Fatalf("publishing Function capability: %v", publishErr)
	}
	if err := sqlMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if kpa.syncStatus != interfaces.KNProxySyncReady {
		t.Fatalf("Function proxy sync status = %q, want ready", kpa.syncStatus)
	}
	seenSyncedFunction := false
	for _, source := range safe.synced {
		if source.ResourceID == function.OwnerID {
			if source.ResourceType != "function" || source.Operation != interfaces.OPERATION_TYPE_EXECUTE {
				t.Fatalf("synchronized Function grant = %#v", source)
			}
			seenSyncedFunction = true
		}
	}
	if !seenSyncedFunction {
		t.Fatal("Function grant was not synchronized")
	}
}
