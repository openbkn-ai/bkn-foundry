// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

type proxyAccessStub struct {
	mapping        *interfaces.KNProxyAccount
	mappings       []*interfaces.KNProxyAccount
	conflicts      map[string][]string
	lifecycle      string
	lockAcquired   bool
	lockReleased   bool
	syncStatus     string
	syncedVersion  string
	pendingCount   int
	pendingVersion string
	events         *[]string
}

func (s *proxyAccessStub) Get(context.Context, string) (*interfaces.KNProxyAccount, error) {
	return s.mapping, nil
}
func (s *proxyAccessStub) List(context.Context) ([]*interfaces.KNProxyAccount, error) {
	return s.mappings, nil
}
func (s *proxyAccessStub) Ensure(_ context.Context, mapping *interfaces.KNProxyAccount) (*interfaces.KNProxyAccount, bool, error) {
	s.mapping = mapping
	return mapping, true, nil
}

func (s *proxyAccessStub) SetPending(_ context.Context, _ *sql.Tx, _, version, _ string, _ int64) error {
	s.pendingCount++
	s.pendingVersion = version
	return nil
}

func (s *proxyAccessStub) SetSyncResult(_ context.Context, _, _ string, syncStatus, syncedVersion, _ string, _ int64) (bool, error) {
	s.syncStatus = syncStatus
	s.syncedVersion = syncedVersion
	return true, nil
}
func (s *proxyAccessStub) SetLifecycle(_ context.Context, _ string, lifecycle string, _ int64) error {
	s.lifecycle = lifecycle
	if s.events != nil {
		*s.events = append(*s.events, "mapping:"+lifecycle)
	}
	return nil
}
func (s *proxyAccessStub) TryAcquireLock(context.Context, string, string, int64, int64) (bool, error) {
	s.lockAcquired = true
	return true, nil
}
func (s *proxyAccessStub) ReleaseLock(context.Context, string, string, int64) error {
	s.lockReleased = true
	return nil
}
func (s *proxyAccessStub) ListProxyConflicts(context.Context) (map[string][]string, error) {
	return s.conflicts, nil
}

type managedProxyAccessStub struct {
	allowed          bool
	disabled         bool
	disableLifecycle string
	archived         bool
	synced           []interfaces.ProxyGrantSourceSpec
	reconciled       []string
	syncErr          error
	events           *[]string
}

func (s *managedProxyAccessStub) Create(_ context.Context, knID, _ string) (*interfaces.ManagedProxyAccount, bool, error) {
	return &interfaces.ManagedProxyAccount{ProxyAccountID: "proxy-1", AccountType: interfaces.KNProxyAccountTypeApp, ManagedResourceID: knID}, true, nil
}
func (s *managedProxyAccessStub) Disable(context.Context, string) (*interfaces.ManagedProxyAccount, error) {
	s.disabled = true
	if s.events != nil {
		*s.events = append(*s.events, "proxy:disable")
	}
	lifecycle := s.disableLifecycle
	if lifecycle == "" {
		lifecycle = interfaces.KNProxyLifecycleDisabling
	}
	return &interfaces.ManagedProxyAccount{ProxyAccountID: "proxy-1", LifecycleStatus: lifecycle}, nil
}
func (s *managedProxyAccessStub) Archive(context.Context, string) (*interfaces.ManagedProxyAccount, error) {
	s.archived = true
	if s.events != nil {
		*s.events = append(*s.events, "proxy:archive")
	}
	return &interfaces.ManagedProxyAccount{ProxyAccountID: "proxy-1"}, nil
}
func (s *managedProxyAccessStub) CheckGrant(context.Context, string, string, interfaces.ProxyGrantSourceSpec) (interfaces.ProxyGrantCheckResult, error) {
	return interfaces.ProxyGrantCheckResult{Allowed: s.allowed}, nil
}
func (s *managedProxyAccessStub) SyncGrants(_ context.Context, _, _ string, sources []interfaces.ProxyGrantSourceSpec) (interfaces.ProxyGrantSyncResult, error) {
	s.synced = append([]interfaces.ProxyGrantSourceSpec(nil), sources...)
	if s.events != nil {
		*s.events = append(*s.events, "grants:sync")
	}
	return interfaces.ProxyGrantSyncResult{}, s.syncErr
}

func TestPublishKNChildMutationCommitsPendingAndSyncsLatest(t *testing.T) {
	ctrl := gomock.NewController(t)
	kna := bmock.NewMockKNAccess(ctrl)
	cga := bmock.NewMockConceptGroupAccess(ctrl)
	ota := bmock.NewMockObjectTypeAccess(ctrl)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ata := bmock.NewMockActionTypeAccess(ctrl)
	ma := bmock.NewMockMetricAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	db, databaseMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	objectType := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-new"},
	}}
	base := &interfaces.KN{KNID: "kn-1", KNName: "network", Branch: interfaces.MAIN_BRANCH}
	kna.EXPECT().GetKNByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH).Return(base, nil).Times(2)
	cga.EXPECT().ListConceptGroups(gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)
	loadCount := 0
	ota.EXPECT().ListObjectTypes(gomock.Any(), nil, gomock.Any()).DoAndReturn(
		func(context.Context, *sql.Tx, interfaces.ObjectTypesQueryParams) ([]*interfaces.ObjectType, error) {
			loadCount++
			if loadCount == 2 {
				return []*interfaces.ObjectType{objectType}, nil
			}
			return nil, nil
		}).Times(2)
	rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)
	ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)
	ma.EXPECT().ListMetrics(gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)
	databaseMock.ExpectBegin()
	databaseMock.ExpectCommit()

	kpa := &proxyAccessStub{mapping: &interfaces.KNProxyAccount{
		KNID: "kn-1", ProxyAccountID: "proxy-1", LifecycleStatus: interfaces.KNProxyLifecycleActive,
	}}
	mpa := &managedProxyAccessStub{allowed: true}
	service := &knowledgeNetworkService{
		db: db, kna: kna, cga: cga, ota: ota, rta: rta, ata: ata, ma: ma, ps: ps, kpa: kpa, mpa: mpa,
	}
	ctx := context.WithValue(t.Context(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "grantor-1"})
	changes := &interfaces.KN{KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, ObjectTypes: []*interfaces.ObjectType{objectType}}
	mutationCalled := false
	err = service.PublishKNChildMutation(ctx, changes, func(_ context.Context, tx *sql.Tx) error {
		mutationCalled = true
		if tx == nil {
			t.Fatal("mutation transaction is nil")
		}
		if objectType.OTID == "" {
			t.Fatal("object type ID was not prepared before the mutation")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !mutationCalled || kpa.pendingCount != 1 || !kpa.lockReleased {
		t.Fatalf("publication state: mutation=%t pending=%d lock_released=%t",
			mutationCalled, kpa.pendingCount, kpa.lockReleased)
	}
	if len(mpa.synced) != 2 || mpa.synced[0].ResourceID != "resource-new" || mpa.synced[1].ResourceID != "resource-new" {
		t.Fatalf("synchronized sources = %#v, want new resource view and query grants", mpa.synced)
	}
	if kpa.syncStatus != interfaces.KNProxySyncReady || kpa.syncedVersion != kpa.pendingVersion {
		t.Fatalf("sync state = %q %q, pending version %q", kpa.syncStatus, kpa.syncedVersion, kpa.pendingVersion)
	}
	if err := databaseMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPublishKNChildMutationRollsBackPendingWithBusinessFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	kna := bmock.NewMockKNAccess(ctrl)
	cga := bmock.NewMockConceptGroupAccess(ctrl)
	ota := bmock.NewMockObjectTypeAccess(ctrl)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ata := bmock.NewMockActionTypeAccess(ctrl)
	ma := bmock.NewMockMetricAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	db, databaseMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	base := &interfaces.KN{KNID: "kn-1", KNName: "network", Branch: interfaces.MAIN_BRANCH}
	kna.EXPECT().GetKNByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH).Return(base, nil)
	cga.EXPECT().ListConceptGroups(gomock.Any(), gomock.Any()).Return(nil, nil)
	ota.EXPECT().ListObjectTypes(gomock.Any(), nil, gomock.Any()).Return(nil, nil)
	rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(nil, nil)
	ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return(nil, nil)
	ma.EXPECT().ListMetrics(gomock.Any(), gomock.Any()).Return(nil, nil)
	databaseMock.ExpectBegin()
	databaseMock.ExpectRollback()

	kpa := &proxyAccessStub{mapping: &interfaces.KNProxyAccount{
		KNID: "kn-1", ProxyAccountID: "proxy-1", LifecycleStatus: interfaces.KNProxyLifecycleActive,
	}}
	mpa := &managedProxyAccessStub{allowed: true}
	service := &knowledgeNetworkService{
		db: db, kna: kna, cga: cga, ota: ota, rta: rta, ata: ata, ma: ma, ps: ps, kpa: kpa, mpa: mpa,
	}
	ctx := context.WithValue(t.Context(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "grantor-1"})
	wantErr := errors.New("business mutation failed")
	err = service.PublishKNChildMutation(ctx,
		&interfaces.KN{KNID: "kn-1", Branch: interfaces.MAIN_BRANCH},
		func(context.Context, *sql.Tx) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("PublishKNChildMutation() error = %v, want %v", err, wantErr)
	}
	if len(mpa.synced) != 0 || !kpa.lockReleased {
		t.Fatalf("rollback state: synced=%d lock_released=%t", len(mpa.synced), kpa.lockReleased)
	}
	if err := databaseMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFinishProxyPublishReloadsLatestMainModel(t *testing.T) {
	ctrl := gomock.NewController(t)
	kna := bmock.NewMockKNAccess(ctrl)
	cga := bmock.NewMockConceptGroupAccess(ctrl)
	ota := bmock.NewMockObjectTypeAccess(ctrl)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ata := bmock.NewMockActionTypeAccess(ctrl)
	ma := bmock.NewMockMetricAccess(ctrl)
	kpa := &proxyAccessStub{}
	mpa := &managedProxyAccessStub{}

	latestObject := &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: "ot-1", DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-latest"},
	}}
	latest := &interfaces.KN{KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, ObjectTypes: []*interfaces.ObjectType{latestObject}}
	_, latestVersion, err := buildProxyGrantSources(latest)
	if err != nil {
		t.Fatal(err)
	}
	kna.EXPECT().GetKNByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH).
		Return(&interfaces.KN{KNID: "kn-1", Branch: interfaces.MAIN_BRANCH}, nil)
	cga.EXPECT().ListConceptGroups(gomock.Any(), gomock.Any()).Return(nil, nil)
	ota.EXPECT().ListObjectTypes(gomock.Any(), nil, gomock.Any()).Return([]*interfaces.ObjectType{latestObject}, nil)
	rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(nil, nil)
	ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return(nil, nil)
	ma.EXPECT().ListMetrics(gomock.Any(), gomock.Any()).Return(nil, nil)

	service := &knowledgeNetworkService{kna: kna, cga: cga, ota: ota, rta: rta, ata: ata, ma: ma, kpa: kpa, mpa: mpa}
	plan := &proxyPublishPlan{
		mapping:   &interfaces.KNProxyAccount{KNID: "kn-1", ProxyAccountID: "proxy-1"},
		grantorID: "grantor-1", modelVersion: latestVersion,
	}
	if err := service.finishProxyPublish(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if len(mpa.synced) != 2 || mpa.synced[0].ResourceID != "resource-latest" || mpa.synced[1].ResourceID != "resource-latest" {
		t.Fatalf("synchronized sources = %#v, want latest resource", mpa.synced)
	}
	if kpa.syncStatus != interfaces.KNProxySyncReady || kpa.syncedVersion != latestVersion {
		t.Fatalf("sync result = %q %q", kpa.syncStatus, kpa.syncedVersion)
	}
}

func TestFinishProxyPublishFailureRemainsFailClosed(t *testing.T) {
	ctrl := gomock.NewController(t)
	kna := bmock.NewMockKNAccess(ctrl)
	cga := bmock.NewMockConceptGroupAccess(ctrl)
	ota := bmock.NewMockObjectTypeAccess(ctrl)
	rta := bmock.NewMockRelationTypeAccess(ctrl)
	ata := bmock.NewMockActionTypeAccess(ctrl)
	ma := bmock.NewMockMetricAccess(ctrl)
	kpa := &proxyAccessStub{}
	mpa := &managedProxyAccessStub{syncErr: errors.New("safe unavailable")}
	latest := &interfaces.KN{KNID: "kn-1", Branch: interfaces.MAIN_BRANCH}
	_, version, err := buildProxyGrantSources(latest)
	if err != nil {
		t.Fatal(err)
	}
	kna.EXPECT().GetKNByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH).Return(latest, nil)
	cga.EXPECT().ListConceptGroups(gomock.Any(), gomock.Any()).Return(nil, nil)
	ota.EXPECT().ListObjectTypes(gomock.Any(), nil, gomock.Any()).Return(nil, nil)
	rta.EXPECT().ListRelationTypes(gomock.Any(), gomock.Any()).Return(nil, nil)
	ata.EXPECT().ListActionTypes(gomock.Any(), gomock.Any()).Return(nil, nil)
	ma.EXPECT().ListMetrics(gomock.Any(), gomock.Any()).Return(nil, nil)
	service := &knowledgeNetworkService{kna: kna, cga: cga, ota: ota, rta: rta, ata: ata, ma: ma, kpa: kpa, mpa: mpa}
	plan := &proxyPublishPlan{mapping: &interfaces.KNProxyAccount{KNID: "kn-1", ProxyAccountID: "proxy-1"}, grantorID: "grantor-1", modelVersion: version}

	err = service.finishProxyPublish(t.Context(), plan)
	if err == nil {
		t.Fatal("finishProxyPublish() error = nil, want synchronization failure")
	}
	if kpa.syncStatus != interfaces.KNProxySyncFailed || kpa.syncedVersion != "" {
		t.Fatalf("failed synchronization state = %q %q", kpa.syncStatus, kpa.syncedVersion)
	}
}

func (s *managedProxyAccessStub) ReconcileGrants(_ context.Context, proxyAccountID, _ string) (interfaces.ProxyGrantReconcileResult, error) {
	s.reconciled = append(s.reconciled, proxyAccountID)
	return interfaces.ProxyGrantReconcileResult{}, nil
}

func TestGetKNProxyResolvesMappingWithoutBusinessAuthorize(t *testing.T) {
	ctrl := gomock.NewController(t)
	permissionService := bmock.NewMockPermissionService(ctrl)
	mapping := &interfaces.KNProxyAccount{
		KNID:                  "kn-1",
		ProxyAccountID:        "proxy-1",
		LifecycleStatus:       interfaces.KNProxyLifecycleActive,
		SyncStatus:            interfaces.KNProxySyncReady,
		PublishedModelVersion: "model-v1",
		SyncedModelVersion:    "model-v1",
	}
	service := &knowledgeNetworkService{
		kpa: &proxyAccessStub{mapping: mapping},
		ps:  permissionService,
	}

	got, err := service.GetKNProxy(t.Context(), "kn-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != mapping {
		t.Fatalf("GetKNProxy() = %#v, want %#v", got, mapping)
	}
}

func TestReconcileKNProxiesReportsMissingOrphanAndConflict(t *testing.T) {
	ctrl := gomock.NewController(t)
	kna := bmock.NewMockKNAccess(ctrl)
	kna.EXPECT().GetAllMainBranchKNs(gomock.Any()).Return(map[string]*interfaces.KN{
		"kn-live":   {KNID: "kn-live", Branch: interfaces.MAIN_BRANCH},
		"kn-mapped": {KNID: "kn-mapped", Branch: interfaces.MAIN_BRANCH},
	}, nil)
	kpa := &proxyAccessStub{
		mappings: []*interfaces.KNProxyAccount{
			{KNID: "kn-orphan", ProxyAccountID: "proxy-shared", LifecycleStatus: interfaces.KNProxyLifecycleActive},
			{KNID: "kn-mapped", ProxyAccountID: "proxy-mapped", LifecycleStatus: interfaces.KNProxyLifecycleActive},
		},
		conflicts: map[string][]string{"proxy-shared": {"kn-a", "kn-b"}},
	}
	permissionService := bmock.NewMockPermissionService(ctrl)
	permissionService.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   "kn-mapped",
	}, []string{interfaces.OPERATION_TYPE_AUTHORIZE}).Return(nil)
	mpa := &managedProxyAccessStub{}
	service := &knowledgeNetworkService{kna: kna, kpa: kpa, mpa: mpa, ps: permissionService}

	report, err := service.ReconcileKNProxies(t.Context(), "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.MissingMappings) != 1 || report.MissingMappings[0] != "kn-live" {
		t.Fatalf("missing mappings = %#v", report.MissingMappings)
	}
	if len(report.OrphanMappings) != 1 || report.OrphanMappings[0] != "kn-orphan" {
		t.Fatalf("orphan mappings = %#v", report.OrphanMappings)
	}
	if len(report.ConflictingProxy["proxy-shared"]) != 2 {
		t.Fatalf("conflicts = %#v", report.ConflictingProxy)
	}
	if !reflect.DeepEqual(mpa.reconciled, []string{"proxy-mapped"}) {
		t.Fatalf("reconciled proxies = %#v, want live authorized mapping only", mpa.reconciled)
	}
}

func TestReconcileKNProxiesAuthorizesAllLiveMappingsBeforeMutation(t *testing.T) {
	ctrl := gomock.NewController(t)
	kna := bmock.NewMockKNAccess(ctrl)
	kna.EXPECT().GetAllMainBranchKNs(gomock.Any()).Return(map[string]*interfaces.KN{
		"kn-1": {KNID: "kn-1", Branch: interfaces.MAIN_BRANCH},
	}, nil)
	kpa := &proxyAccessStub{mappings: []*interfaces.KNProxyAccount{{
		KNID: "kn-1", ProxyAccountID: "proxy-1", LifecycleStatus: interfaces.KNProxyLifecycleActive,
	}}}
	permissionService := bmock.NewMockPermissionService(ctrl)
	permissionService.EXPECT().CheckPermission(gomock.Any(), gomock.Any(),
		[]string{interfaces.OPERATION_TYPE_AUTHORIZE}).Return(rest.NewHTTPError(t.Context(), http.StatusForbidden, rest.PublicError_Forbidden))
	mpa := &managedProxyAccessStub{}
	service := &knowledgeNetworkService{kna: kna, kpa: kpa, mpa: mpa, ps: permissionService}

	if _, err := service.ReconcileKNProxies(t.Context(), "operator-1"); err == nil {
		t.Fatal("ReconcileKNProxies() error = nil, want authorization failure")
	}
	if len(mpa.reconciled) != 0 {
		t.Fatalf("reconciliation mutated proxies before authorization completed: %#v", mpa.reconciled)
	}
}

func TestProxyDeletionLifecycleIsOrdered(t *testing.T) {
	ctrl := gomock.NewController(t)
	permissionService := bmock.NewMockPermissionService(ctrl)
	events := []string{}
	permissionService.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_KN, []string{"kn-1"}).
		DoAndReturn(func(context.Context, string, []string) error {
			events = append(events, "policy:delete")
			return nil
		})
	kpa := &proxyAccessStub{
		mapping: &interfaces.KNProxyAccount{KNID: "kn-1", ProxyAccountID: "proxy-1", LifecycleStatus: interfaces.KNProxyLifecycleActive},
		events:  &events,
	}
	mpa := &managedProxyAccessStub{events: &events}
	service := &knowledgeNetworkService{kpa: kpa, mpa: mpa, ps: permissionService}
	ctx := context.WithValue(t.Context(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "grantor-1"})

	plan, err := service.prepareProxyDelete(ctx, "kn-1", interfaces.MAIN_BRANCH)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 || mpa.disabled || len(mpa.synced) != 0 || kpa.lifecycle != "" {
		t.Fatalf("prepareProxyDelete() performed external side effects before commit: events=%#v", events)
	}
	events = append(events, "business:delete")
	if err := service.finalizeProxyDelete(ctx, plan); err != nil {
		t.Fatal(err)
	}
	want := []string{"business:delete", "proxy:disable", "mapping:disabling", "grants:sync", "proxy:archive", "mapping:archived", "policy:delete"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("deletion events = %#v, want %#v", events, want)
	}
}

func TestPrepareProxyDeleteAllowsLegacyNetworkWithoutMapping(t *testing.T) {
	service := &knowledgeNetworkService{kpa: &proxyAccessStub{}, mpa: &managedProxyAccessStub{}}
	ctx := context.WithValue(t.Context(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "grantor-1"})

	plan, err := service.prepareProxyDelete(ctx, "legacy-kn", interfaces.MAIN_BRANCH)
	if err != nil {
		t.Fatal(err)
	}
	if plan != nil {
		t.Fatalf("prepareProxyDelete() = %#v, want legacy cleanup plan", plan)
	}
}

func TestFinalizeProxyDeleteRepairsAlreadyArchivedManagedProxy(t *testing.T) {
	ctrl := gomock.NewController(t)
	events := []string{}
	permissionService := bmock.NewMockPermissionService(ctrl)
	permissionService.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_KN, []string{"kn-1"}).
		DoAndReturn(func(context.Context, string, []string) error {
			events = append(events, "policy:delete")
			return nil
		})
	kpa := &proxyAccessStub{events: &events}
	mpa := &managedProxyAccessStub{
		disableLifecycle: interfaces.KNProxyLifecycleArchived,
		events:           &events,
	}
	service := &knowledgeNetworkService{kpa: kpa, mpa: mpa, ps: permissionService}
	plan := &proxyPublishPlan{
		mapping:   &interfaces.KNProxyAccount{KNID: "kn-1", ProxyAccountID: "proxy-1", LifecycleStatus: interfaces.KNProxyLifecycleDisabling},
		grantorID: "grantor-1",
	}

	if err := service.finalizeProxyDelete(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	want := []string{"proxy:disable", "grants:sync", "mapping:archived", "policy:delete"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("recovery events = %#v, want %#v", events, want)
	}
	if mpa.archived {
		t.Fatal("already archived managed proxy was archived twice")
	}
}

func TestDeleteKNRollbackDoesNotDisableProxy(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, databaseMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	databaseMock.ExpectBegin()
	databaseMock.ExpectRollback()

	permissionService := bmock.NewMockPermissionService(ctrl)
	permissionService.EXPECT().CheckPermission(gomock.Any(), gomock.Any(),
		[]string{interfaces.OPERATION_TYPE_DELETE}).Return(nil)
	kna := bmock.NewMockKNAccess(ctrl)
	kna.EXPECT().DeleteKN(gomock.Any(), gomock.Any(), "kn-1", interfaces.MAIN_BRANCH).
		Return(int64(0), errors.New("delete failed"))
	kpa := &proxyAccessStub{mapping: &interfaces.KNProxyAccount{
		KNID: "kn-1", ProxyAccountID: "proxy-1", LifecycleStatus: interfaces.KNProxyLifecycleActive,
	}}
	mpa := &managedProxyAccessStub{}
	service := &knowledgeNetworkService{db: db, ps: permissionService, kna: kna, kpa: kpa, mpa: mpa}
	ctx := context.WithValue(t.Context(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "grantor-1", Type: "user"})

	if err := service.DeleteKN(ctx, &interfaces.KN{KNID: "kn-1", Branch: interfaces.MAIN_BRANCH}); err == nil {
		t.Fatal("DeleteKN() error = nil, want transaction failure")
	}
	if mpa.disabled || mpa.archived || len(mpa.synced) != 0 || kpa.lifecycle != "" {
		t.Fatalf("rolled-back deletion changed proxy: disabled=%v archived=%v synced=%#v lifecycle=%q",
			mpa.disabled, mpa.archived, mpa.synced, kpa.lifecycle)
	}
	if !kpa.lockReleased {
		t.Fatal("rolled-back deletion did not release the proxy lock")
	}
	if err := databaseMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizeKNProxyDeletionAuthorizesArchivedCleanup(t *testing.T) {
	ctrl := gomock.NewController(t)
	events := []string{}
	permissionService := bmock.NewMockPermissionService(ctrl)
	permissionService.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   "kn-1",
	}, []string{interfaces.OPERATION_TYPE_DELETE}).DoAndReturn(func(context.Context, interfaces.PermissionResource, []string) error {
		events = append(events, "permission:check")
		return nil
	})
	permissionService.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_KN, []string{"kn-1"}).
		DoAndReturn(func(context.Context, string, []string) error {
			events = append(events, "policy:delete")
			return nil
		})
	service := &knowledgeNetworkService{
		kpa: &proxyAccessStub{mapping: &interfaces.KNProxyAccount{
			KNID: "kn-1", ProxyAccountID: "proxy-1", LifecycleStatus: interfaces.KNProxyLifecycleArchived,
		}},
		mpa: &managedProxyAccessStub{},
		ps:  permissionService,
	}

	if err := service.FinalizeKNProxyDeletion(t.Context(), "kn-1"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"permission:check", "policy:delete"}) {
		t.Fatalf("finalize events = %#v", events)
	}
}

func TestPrepareProxyPublishDeniedPreflightCompensatesNewProxy(t *testing.T) {
	kpa := &proxyAccessStub{}
	mpa := &managedProxyAccessStub{allowed: false}
	service := &knowledgeNetworkService{kpa: kpa, mpa: mpa}
	ctx := context.WithValue(t.Context(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "grantor-1"})
	kn := &interfaces.KN{
		KNID: "kn-1", KNName: "network", Branch: interfaces.MAIN_BRANCH,
		ObjectTypes: []*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID: "ot-1", DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"},
		}}},
	}

	_, err := service.prepareProxyPublish(ctx, kn)
	if err == nil {
		t.Fatal("prepareProxyPublish() error = nil, want denied preflight")
	}
	httpErr, ok := err.(*rest.HTTPError)
	if !ok || httpErr.HTTPCode != http.StatusForbidden {
		t.Fatalf("prepareProxyPublish() error = %#v, want HTTP 403", err)
	}
	if !kpa.lockAcquired || !kpa.lockReleased {
		t.Fatal("publication lock was not acquired and released")
	}
	if !mpa.disabled || !mpa.archived || kpa.lifecycle != interfaces.KNProxyLifecycleArchived {
		t.Fatalf("new proxy was not compensated: disabled=%v archived=%v lifecycle=%q", mpa.disabled, mpa.archived, kpa.lifecycle)
	}
}
