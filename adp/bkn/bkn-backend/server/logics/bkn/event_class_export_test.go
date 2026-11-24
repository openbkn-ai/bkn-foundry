// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bkn

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"go.uber.org/mock/gomock"

	bknsdk "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// subjectKind keeps, for each of its object types, a relation type to the event subject.
type subjectKind struct{}

func (subjectKind) Available() bool { return true }
func (subjectKind) Complete(context.Context, *interfaces.ObjectType, *interfaces.ObjectType) error {
	return nil
}
func (subjectKind) Check(context.Context, *interfaces.ObjectType, *interfaces.ObjectType) error {
	return nil
}
func (subjectKind) Saved(context.Context, *sql.Tx, *interfaces.ObjectType, *interfaces.ObjectType) error {
	return nil
}
func (subjectKind) Deleting(context.Context, *sql.Tx, *interfaces.ObjectType) error { return nil }
func (subjectKind) OwnedRelationTypes(objectType *interfaces.ObjectType) []*interfaces.RelationType {
	return []*interfaces.RelationType{{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
		RTID: objectType.OTID + "_subject", SourceObjectTypeID: objectType.OTID, TargetObjectTypeID: objectType.Event.Subject,
	}}}
}
func (subjectKind) PlatformResource(string, string, string) bool { return false }

func eventClassKN() *interfaces.KN {
	event := &interfaces.ObjectTypeEvent{Source: "report", Subject: "order", Severity: "high", MergeHours: 24, HandlingNote: "联系供应商"}
	return &interfaces.KN{
		KNID: "kn-1", KNName: "kn-1", Branch: interfaces.MAIN_BRANCH,
		ObjectTypes: []*interfaces.ObjectType{
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "late_order", OTName: "订单超期",
				DataSource:  &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "kn_event_x"},
				PrimaryKeys: []string{"event_id"}, DisplayKey: "title"},
				Kinds: []string{interfaces.ObjectTypeKindEvent}, Event: event},
			{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "order", OTName: "订单",
				PrimaryKeys: []string{"order_id"}, DisplayKey: "order_id"}},
		},
		RelationTypes: []*interfaces.RelationType{
			{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "late_order_subject", RTName: "关于"}},
			{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "order_peer", RTName: "同组"}},
		},
	}
}

func registerSubjectKind(t *testing.T) {
	t.Helper()
	logics.RegisterObjectTypeKind(interfaces.ObjectTypeKindEvent, subjectKind{})
	t.Cleanup(func() { logics.RegisterObjectTypeKind(interfaces.ObjectTypeKindEvent, nil) })
}

func objectTypeOf(network *bknsdk.BknNetwork, id string) *bknsdk.BknObjectType {
	for _, ot := range network.ObjectTypes {
		if ot.ID == id {
			return ot
		}
	}
	return nil
}

// The relation the platform keeps for an event class is not exported, since the import makes it
// again.
func TestExportLeavesOutTheRelationsTheKindsKeep(t *testing.T) {
	registerSubjectKind(t)
	svc, mockCtrl, kns := newTestBKNService(t)
	defer mockCtrl.Finish()
	kns.EXPECT().GetKNByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(eventClassKN(), nil)

	network, err := svc.buildNetwork(context.Background(), "kn-1", interfaces.MAIN_BRANCH)
	if err != nil {
		t.Fatal(err)
	}
	if len(network.RelationTypes) != 1 || network.RelationTypes[0].ID != "order_peer" {
		t.Fatalf("exported relation types %+v, want only the network's own", network.RelationTypes)
	}
}

// A network with an event class and nothing only 3.0 holds is exported as 2.1, the first
// specification with event classes, and reads back with its kind and configuration; asked for 2.0,
// it is exported as before.
func TestExportWritesEventClassesAs21(t *testing.T) {
	registerSubjectKind(t)
	want := bknsdk.ObjectTypeEvent{Source: "report", Subject: "order", Severity: "high", MergeHours: 24, HandlingNote: "联系供应商"}
	for _, c := range []struct {
		ask, spec string
		carried   bool
	}{
		{interfaces.BKN_EXPORT_SPEC_AUTO, bknsdk.BknSpecV21, true},
		{interfaces.BKN_EXPORT_SPEC_21, bknsdk.BknSpecV21, true},
		{interfaces.BKN_EXPORT_SPEC_3, bknsdk.BknSpecV3, true},
		{interfaces.BKN_EXPORT_SPEC_2, bknsdk.BknSpecLegacy, false},
	} {
		svc, mockCtrl, kns := newTestBKNService(t)
		kns.EXPECT().GetKNByID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, interfaces.Mode_Export).Return(eventClassKN(), nil)
		data, err := svc.ExportToTar(context.Background(), "kn-1", interfaces.MAIN_BRANCH, c.ask)
		if err != nil {
			t.Fatalf("export as %q: %v", c.ask, err)
		}
		network, err := bknsdk.LoadNetworkFromTar(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("export as %q reads back: %v", c.ask, err)
		}
		class := objectTypeOf(network, "late_order")
		switch {
		case network.BknSpec != c.spec:
			t.Fatalf("export as %q is %q, want %q", c.ask, network.BknSpec, c.spec)
		case c.carried && (!class.IsEventClass() || class.Event == nil || *class.Event != want):
			t.Fatalf("export as %q carries kinds %v event %+v", c.ask, class.Kinds, class.Event)
		case !c.carried && (class.IsEventClass() || class.Event != nil):
			t.Fatalf("export as %q carries kinds %v event %+v", c.ask, class.Kinds, class.Event)
		}
		mockCtrl.Finish()
	}
}
