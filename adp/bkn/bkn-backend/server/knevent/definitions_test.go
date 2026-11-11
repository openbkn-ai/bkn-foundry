// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// publisherStub runs a change as the network's proxy publication would, and
// keeps what it was told the change is.
type publisherStub struct {
	interfaces.KNProxyMutationPublisher
	changes []*interfaces.KN
	modes   []string
}

func (p *publisherStub) PublishKNChildMutation(ctx context.Context, changes *interfaces.KN, mode string,
	mutate func(context.Context, *sql.Tx) error) error {
	p.changes = append(p.changes, changes)
	p.modes = append(p.modes, mode)
	return mutate(ctx, nil)
}

type objectTypesStub struct {
	interfaces.ObjectTypeService
	created []*interfaces.ObjectType
	options []any
	deleted []string
}

func (o *objectTypesStub) DeleteObjectTypesByIDs(_ context.Context, _ *sql.Tx, knID, branch string, ids []string) error {
	for _, id := range ids {
		o.deleted = append(o.deleted, definitionKey(knID, branch, id))
	}
	return nil
}

func (o *objectTypesStub) CreateObjectTypes(_ context.Context, _ *sql.Tx, entries []*interfaces.ObjectType, mode string, groups, strict bool) ([]string, error) {
	o.created = append(o.created, entries...)
	o.options = []any{mode, groups, strict}
	return nil, nil
}

type relationTypesStub struct {
	interfaces.RelationTypeService
	exists  bool
	created []*interfaces.RelationType
	options []any
	deleted []string
	queries []interfaces.RelationTypesQueryParams
	bound   []*interfaces.RelationType
}

func (r *relationTypesStub) CheckRelationTypeExistByID(context.Context, string, string, string) (string, bool, error) {
	return "", r.exists, nil
}

func (r *relationTypesStub) CreateRelationTypes(_ context.Context, _ *sql.Tx, entries []*interfaces.RelationType, mode string, dependencies bool) ([]string, error) {
	r.created = append(r.created, entries...)
	r.options = []any{mode, dependencies}
	return nil, nil
}

func (r *relationTypesStub) ListRelationTypes(_ context.Context, query interfaces.RelationTypesQueryParams) ([]*interfaces.RelationType, int, error) {
	r.queries = append(r.queries, query)
	return r.bound, len(r.bound), nil
}

type actionTypesStub struct {
	interfaces.ActionTypeService
	queries []interfaces.ActionTypesQueryParams
	on      []*interfaces.ActionType
}

func (a *actionTypesStub) ListActionTypes(_ context.Context, query interfaces.ActionTypesQueryParams) ([]*interfaces.ActionType, int, error) {
	a.queries = append(a.queries, query)
	return a.on, len(a.on), nil
}

func (r *relationTypesStub) DeleteRelationTypesByIDs(_ context.Context, _ *sql.Tx, knID, branch string, ids []string) error {
	for _, id := range ids {
		r.deleted = append(r.deleted, definitionKey(knID, branch, id))
	}
	return nil
}

// The class is checked by the platform's own validation of definitions, and
// written as the definition API writes: replacing the one of the same ID, and
// told to the network's proxy account.
func TestCoreDefinitionsWriteTheClassAsTheDefinitionAPIDoes(t *testing.T) {
	publisher, objects, relations := &publisherStub{}, &objectTypesStub{}, &relationTypesStub{}
	definitions := &coreDefinitions{networks: publisher, objects: objects, relations: relations}
	ctx := asPlatform(context.Background())
	class := eventClass{objectTypeID: classObjectTypeID(lateOrder.Code), datasetID: classDatasetID(lateOrder.KNID, lateOrder.Code)}

	objectType := classObjectType(lateOrder, class, interfaces.DRAFT_BRANCH)
	if err := definitions.saveObjectType(ctx, objectType); err != nil {
		t.Fatalf("the platform refuses the class: %v", err)
	}
	if len(objects.created) != 1 || objects.created[0] != objectType ||
		!reflect.DeepEqual(objects.options, []any{interfaces.ImportMode_Overwrite, true, true}) {
		t.Fatalf("created %v with %v", objects.created, objects.options)
	}
	told := publisher.changes[0]
	if told.KNID != "kn1" || told.Branch != interfaces.DRAFT_BRANCH || len(told.ObjectTypes) != 1 || told.ObjectTypes[0] != objectType ||
		publisher.modes[0] != interfaces.ImportMode_Overwrite {
		t.Fatalf("published %+v as %s", told, publisher.modes[0])
	}

	relationType := classRelationType(lateOrder, class, interfaces.MAIN_BRANCH, class.objectTypeID+relationSuffix, "order_id")
	if err := definitions.saveRelationType(ctx, relationType); err != nil {
		t.Fatalf("the platform refuses the relation: %v", err)
	}
	if len(relations.created) != 1 || relations.created[0] != relationType ||
		!reflect.DeepEqual(relations.options, []any{interfaces.ImportMode_Overwrite, true}) {
		t.Fatalf("created %v with %v", relations.created, relations.options)
	}
	if told := publisher.changes[1]; told.Branch != interfaces.MAIN_BRANCH || len(told.RelationTypes) != 1 || told.RelationTypes[0] != relationType {
		t.Fatalf("published %+v", told)
	}

	// A relation that is not there is not removed, and nothing is published.
	if err := definitions.removeRelationType(ctx, "kn1", interfaces.MAIN_BRANCH, "order_late_subject"); err != nil || len(publisher.changes) != 2 {
		t.Fatalf("removed a relation that is not there: %v", err)
	}
	relations.exists = true
	if err := definitions.removeRelationType(ctx, "kn1", interfaces.MAIN_BRANCH, "order_late_subject"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(relations.deleted, []string{"kn1/main/order_late_subject"}) {
		t.Fatalf("deleted %v", relations.deleted)
	}
	// The publication learns of a removal from an entry that binds nothing.
	if told := publisher.changes[2]; len(told.RelationTypes) != 1 || told.RelationTypes[0].RTID != "order_late_subject" ||
		told.RelationTypes[0].SourceObjectTypeID != "" {
		t.Fatalf("published %+v", told.RelationTypes)
	}

	// Without version control every network is written on its main branch.
	if branch, err := definitions.branch(ctx, "kn1"); err != nil || branch != interfaces.MAIN_BRANCH {
		t.Fatalf("branch = %s, %v", branch, err)
	}
}

// A class is looked over and deleted as the definition API deletes an object
// type: refused while relation types or action types use it, and told to the
// network's proxy account.
func TestCoreDefinitionsRemoveTheClassAsTheDefinitionAPIDoes(t *testing.T) {
	publisher, objects := &publisherStub{}, &objectTypesStub{}
	relations := &relationTypesStub{bound: []*interfaces.RelationType{
		{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "order_late_subject", RTName: "关于"}},
		{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "escalated_to", RTName: "升级给"}},
	}}
	actions := &actionTypesStub{on: []*interfaces.ActionType{{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATName: "催办"}}}}
	definitions := &coreDefinitions{networks: publisher, objects: objects, relations: relations, actions: actions}
	ctx := asPlatform(context.Background())

	users, err := definitions.usersOf(ctx, "kn1", interfaces.DRAFT_BRANCH, "order_late", "order_late_subject")
	if err != nil || !reflect.DeepEqual(users, []string{"升级给", "催办"}) {
		t.Fatalf("users = %v, %v", users, err)
	}
	if query := relations.queries[0]; query.KNID != "kn1" || query.Branch != interfaces.DRAFT_BRANCH ||
		!reflect.DeepEqual(query.BoundObjectTypeIDs, []string{"order_late"}) || query.Limit != -1 {
		t.Fatalf("relation query %+v", query)
	}
	if query := actions.queries[0]; query.Branch != interfaces.DRAFT_BRANCH ||
		!reflect.DeepEqual(query.ObjectTypeIDs, []string{"order_late"}) || query.Limit != -1 {
		t.Fatalf("action query %+v", query)
	}

	if err := definitions.removeObjectType(ctx, "kn1", interfaces.DRAFT_BRANCH, "order_late"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(objects.deleted, []string{"kn1/draft/order_late"}) {
		t.Fatalf("deleted %v", objects.deleted)
	}
	if told := publisher.changes[0]; told.Branch != interfaces.DRAFT_BRANCH || len(told.ObjectTypes) != 1 ||
		told.ObjectTypes[0].OTID != "order_late" || told.ObjectTypes[0].DataSource != nil ||
		publisher.modes[0] != interfaces.ImportMode_Overwrite {
		t.Fatalf("published %+v as %s", told.ObjectTypes, publisher.modes[0])
	}
}
