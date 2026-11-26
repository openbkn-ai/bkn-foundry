// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// keptKind keeps the resource "kept-<object type>" for its object types, and a relation type
// from each of them to the object type named by their event subject.
type keptKind struct{}

func (keptKind) Available() bool { return true }
func (keptKind) Complete(context.Context, *interfaces.ObjectType, *interfaces.ObjectType) error {
	return nil
}
func (keptKind) Check(context.Context, *interfaces.ObjectType, *interfaces.ObjectType) error {
	return nil
}
func (keptKind) Saved(context.Context, *sql.Tx, *interfaces.ObjectType, *interfaces.ObjectType) error {
	return nil
}
func (keptKind) Deleting(context.Context, *sql.Tx, *interfaces.ObjectType) error { return nil }
func (keptKind) OwnedRelationTypes(objectType *interfaces.ObjectType) []*interfaces.RelationType {
	return []*interfaces.RelationType{{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
		RTID: objectType.OTID + "_subject", SourceObjectTypeID: objectType.OTID,
		TargetObjectTypeID: objectType.Event.Subject, Type: "direct",
	}}}
}
func (keptKind) PlatformResource(_, objectTypeID, resourceID string) bool {
	return resourceID == "kept-"+objectTypeID
}

func registerKeptKind(t *testing.T) {
	t.Helper()
	logics.RegisterObjectTypeKind("kept", keptKind{})
	t.Cleanup(func() { logics.RegisterObjectTypeKind("kept", nil) })
}

func resourceObjectType(id, resourceID string) *interfaces.ObjectType {
	return &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
		OTID: id, DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: resourceID},
	}, KNID: "kn-1"}
}

func TestBuildProxyGrantSourcesMarksKeptResourcesAndProjectsKeptRelations(t *testing.T) {
	registerKeptKind(t)
	class := resourceObjectType("late_order", "kept-late_order")
	class.Kinds = []string{"kept"}
	class.Event = &interfaces.ObjectTypeEvent{Source: "rule", Subject: "order"}
	// An ordinary object type bound to a resource named as a kept one is not kept: only an
	// object type that lists the kind counts.
	impostor := resourceObjectType("copy", "kept-copy")
	kn := &interfaces.KN{
		KNID:        "kn-1",
		ObjectTypes: []*interfaces.ObjectType{class, resourceObjectType("order", "orders"), impostor},
		// The candidate still holds the relation as it was, to another subject.
		RelationTypes: []*interfaces.RelationType{{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: "late_order_subject", SourceObjectTypeID: "late_order", TargetObjectTypeID: "copy",
		}}},
	}

	sources, _, err := buildProxyGrantSources(kn)
	if err != nil {
		t.Fatalf("buildProxyGrantSources() error = %v", err)
	}
	relationTargets := map[string]bool{}
	for _, source := range sources {
		if source.PlatformKept != (source.ResourceID == "kept-late_order") {
			t.Fatalf("source %s/%s on %s marked PlatformKept=%t", source.BindingType, source.BindingID,
				source.ResourceID, source.PlatformKept)
		}
		if source.BindingType == interfaces.MODULE_TYPE_RELATION_TYPE && source.BindingID == "late_order_subject" {
			relationTargets[source.ResourceID] = true
		}
	}
	if !relationTargets["kept-late_order"] || !relationTargets["orders"] || relationTargets["kept-copy"] {
		t.Fatalf("relation late_order_subject reaches %v, want the class and its subject as the kind writes it",
			relationTargets)
	}
}

func platformKeptSources() []interfaces.ProxyGrantSourceSpec {
	return []interfaces.ProxyGrantSourceSpec{
		{ResourceType: "resource", ResourceID: "kept-late_order", Operation: interfaces.OPERATION_TYPE_QUERY_DATA,
			SourceType: interfaces.ProxyGrantSourceTypeKNBinding, SourceID: "source-class", KNID: "kn-1",
			BindingType: interfaces.MODULE_TYPE_OBJECT_TYPE, BindingID: "late_order", PlatformKept: true},
		{ResourceType: "resource", ResourceID: "orders", Operation: interfaces.OPERATION_TYPE_QUERY_DATA,
			SourceType: interfaces.ProxyGrantSourceTypeKNBinding, SourceID: "source-order", KNID: "kn-1",
			BindingType: interfaces.MODULE_TYPE_OBJECT_TYPE, BindingID: "order"},
	}
}

func TestPreflightProxySourcesGrantsKeptResourcesAsAdministrator(t *testing.T) {
	mpa := &managedProxyAccessStub{allowed: true, deniedResources: map[string]bool{"kept-late_order": true}}
	service := &knowledgeNetworkService{mpa: mpa}

	selection, err := service.preflightProxySources(t.Context(), "proxy-1", "editor-1", platformKeptSources())
	if err != nil {
		t.Fatalf("preflightProxySources() error = %v, want the kept resource granted by the platform", err)
	}
	if len(mpa.grants) != 1 || mpa.grants[0].ResourceID != "kept-late_order" ||
		mpa.grantors[0] != interfaces.ADMIN_ACCOUNT_ID {
		t.Fatalf("grants = %v by %v, want only the kept resource, by the administrator", mpa.grants, mpa.grantors)
	}
	delegators := map[string]string{}
	for _, source := range selection.resolved {
		delegators[source.ResourceID] = source.GrantedBy
	}
	if delegators["kept-late_order"] != interfaces.ADMIN_ACCOUNT_ID || delegators["orders"] != "editor-1" {
		t.Fatalf("resolved delegators = %v, want the administrator for the kept resource only", delegators)
	}
}

func TestPreflightProxySourcesDoesNotGrantWhatTheEditorMayGrant(t *testing.T) {
	mpa := &managedProxyAccessStub{allowed: true}
	service := &knowledgeNetworkService{mpa: mpa}
	if _, err := service.preflightProxySources(t.Context(), "proxy-1", "editor-1", platformKeptSources()); err != nil {
		t.Fatalf("preflightProxySources() error = %v", err)
	}
	if len(mpa.grants) != 0 {
		t.Fatalf("grants = %v, want none when nothing was denied", mpa.grants)
	}
}

func TestPreflightProxySourcesStillRefusesAnOrdinaryDeniedResource(t *testing.T) {
	mpa := &managedProxyAccessStub{allowed: true, deniedResources: map[string]bool{"orders": true}}
	service := &knowledgeNetworkService{mpa: mpa}
	_, err := service.preflightProxySources(t.Context(), "proxy-1", "editor-1", platformKeptSources())
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.ErrorCode != berrors.BknBackend_KnowledgeNetwork_ProxyPermissionMissing {
		t.Fatalf("preflightProxySources() error = %v, want ProxyPermissionMissing", err)
	}
	if len(mpa.grants) != 0 {
		t.Fatalf("grants = %v, want none for a resource the platform does not keep", mpa.grants)
	}
}

func TestPreflightProxySourcesRefusesWhenThePlatformGrantFails(t *testing.T) {
	mpa := &managedProxyAccessStub{allowed: true, deniedResources: map[string]bool{"kept-late_order": true},
		grantErr: errors.New("bkn-safe unavailable")}
	service := &knowledgeNetworkService{mpa: mpa}
	_, err := service.preflightProxySources(t.Context(), "proxy-1", "editor-1", platformKeptSources())
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.ErrorCode != berrors.BknBackend_KnowledgeNetwork_ProxyPermissionMissing {
		t.Fatalf("preflightProxySources() error = %v, want ProxyPermissionMissing", err)
	}
}

func TestPreflightProxyDeltaGrantsKeptResourcesAsAdministrator(t *testing.T) {
	mpa := &managedProxyAccessStub{allowed: true, deniedResources: map[string]bool{"kept-late_order": true}}
	service := &knowledgeNetworkService{mpa: mpa}
	if _, err := service.preflightProxyDelta(t.Context(), "proxy-1", "editor-1",
		&proxyGrantDelta{upserts: platformKeptSources()}); err != nil {
		t.Fatalf("preflightProxyDelta() error = %v", err)
	}
	if len(mpa.grants) != 1 || mpa.grantors[0] != interfaces.ADMIN_ACCOUNT_ID {
		t.Fatalf("grants = %v by %v, want the kept resource by the administrator", mpa.grants, mpa.grantors)
	}
}

func TestGrantUncheckedPlatformKeptSkipsCheckedSources(t *testing.T) {
	mpa := &managedProxyAccessStub{}
	service := &knowledgeNetworkService{mpa: mpa}
	sources := platformKeptSources()
	relation := sources[0]
	relation.BindingType, relation.BindingID, relation.SourceID = interfaces.MODULE_TYPE_RELATION_TYPE,
		"late_order_subject", "source-relation"
	service.grantUncheckedPlatformKept(t.Context(), "proxy-1", newProxyGrantSelection(sources),
		append(sources, relation))
	if len(mpa.grants) != 1 || mpa.grants[0].BindingID != "late_order_subject" {
		t.Fatalf("grants = %v, want only the kept source the preflight never checked", mpa.grants)
	}
}
