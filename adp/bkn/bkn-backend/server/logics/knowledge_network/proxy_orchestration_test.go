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

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

type proxyAccessStub struct {
	mapping       *interfaces.KNProxyAccount
	mappings      []*interfaces.KNProxyAccount
	conflicts     map[string][]string
	lifecycle     string
	lockAcquired  bool
	lockReleased  bool
	syncStatus    string
	syncedVersion string
	events        *[]string
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
func (s *proxyAccessStub) SetPending(context.Context, *sql.Tx, string, string, string, int64) error {
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
	allowed  bool
	disabled bool
	archived bool
	synced   []interfaces.ProxyGrantSourceSpec
	syncErr  error
	events   *[]string
}

func (s *managedProxyAccessStub) Create(_ context.Context, knID, _ string) (*interfaces.ManagedProxyAccount, bool, error) {
	return &interfaces.ManagedProxyAccount{ProxyAccountID: "proxy-1", AccountType: interfaces.KNProxyAccountTypeApp, ManagedResourceID: knID}, true, nil
}
func (s *managedProxyAccessStub) Disable(context.Context, string) (*interfaces.ManagedProxyAccount, error) {
	s.disabled = true
	if s.events != nil {
		*s.events = append(*s.events, "proxy:disable")
	}
	return &interfaces.ManagedProxyAccount{ProxyAccountID: "proxy-1"}, nil
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

func (s *managedProxyAccessStub) ReconcileGrants(context.Context, string, string) (interfaces.ProxyGrantReconcileResult, error) {
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
	kna.EXPECT().GetAllKNs(gomock.Any()).Return(map[string]*interfaces.KN{
		"kn-live": {KNID: "kn-live", Branch: interfaces.MAIN_BRANCH},
	}, nil)
	kpa := &proxyAccessStub{
		mappings: []*interfaces.KNProxyAccount{{
			KNID: "kn-orphan", ProxyAccountID: "proxy-shared", LifecycleStatus: interfaces.KNProxyLifecycleActive,
		}},
		conflicts: map[string][]string{"proxy-shared": {"kn-a", "kn-b"}},
	}
	service := &knowledgeNetworkService{kna: kna, kpa: kpa, mpa: &managedProxyAccessStub{}}

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
	events = append(events, "business:delete")
	if err := service.finalizeProxyDelete(ctx, plan); err != nil {
		t.Fatal(err)
	}
	want := []string{"proxy:disable", "mapping:disabling", "grants:sync", "business:delete", "proxy:archive", "mapping:archived", "policy:delete"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("deletion events = %#v, want %#v", events, want)
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
