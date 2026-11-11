// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	corelogics "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/action_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/knowledge_network"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/object_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/relation_type"
)

// coreDefinitions writes definitions in process with what the definition
// handlers of bkn-backend use: the same validation, the same services, and
// the same publication to the network's proxy account. A class therefore
// stands in the network exactly as if it had been created through the API.
type coreDefinitions struct {
	networks  interfaces.KNProxyMutationPublisher
	objects   interfaces.ObjectTypeService
	relations interfaces.RelationTypeService
	actions   interfaces.ActionTypeService
}

var _ definitions = (*coreDefinitions)(nil)

func newCoreDefinitions(appSetting *common.AppSetting) *coreDefinitions {
	return &coreDefinitions{
		networks:  knowledge_network.NewKNService(appSetting),
		objects:   object_type.NewObjectTypeService(appSetting),
		relations: relation_type.NewRelationTypeService(appSetting),
		actions:   action_type.NewActionTypeService(appSetting),
	}
}

func (d *coreDefinitions) branch(ctx context.Context, knID string) (string, error) {
	if corelogics.KNVA == nil {
		return interfaces.MAIN_BRANCH, nil
	}
	state, err := corelogics.KNVA.GetState(ctx, nil, knID, false)
	if err != nil {
		return "", err
	}
	if state == nil {
		return interfaces.MAIN_BRANCH, nil
	}
	return interfaces.DRAFT_BRANCH, nil
}

func (d *coreDefinitions) objectTypes(ctx context.Context, knID, branch string, ids []string) (map[string]*interfaces.ObjectType, error) {
	return d.objects.GetObjectTypesMapByIDs(ctx, knID, branch, ids, false)
}

func (d *coreDefinitions) saveObjectType(ctx context.Context, objectType *interfaces.ObjectType) error {
	entries := []*interfaces.ObjectType{objectType}
	if err := driveradapters.ValidateObjectTypes(ctx, objectType.KNID, entries, true); err != nil {
		return err
	}
	changes := &interfaces.KN{KNID: objectType.KNID, Branch: objectType.Branch, ObjectTypes: entries}
	return d.networks.PublishKNChildMutation(ctx, changes, interfaces.ImportMode_Overwrite,
		func(mutationCtx context.Context, tx *sql.Tx) error {
			_, err := d.objects.CreateObjectTypes(mutationCtx, tx, entries, interfaces.ImportMode_Overwrite, true, true)
			return err
		})
}

func (d *coreDefinitions) saveRelationType(ctx context.Context, relationType *interfaces.RelationType) error {
	entries := []*interfaces.RelationType{relationType}
	if err := driveradapters.ValidateRelationTypes(ctx, relationType.KNID, entries, true); err != nil {
		return err
	}
	changes := &interfaces.KN{KNID: relationType.KNID, Branch: relationType.Branch, RelationTypes: entries}
	return d.networks.PublishKNChildMutation(ctx, changes, interfaces.ImportMode_Overwrite,
		func(mutationCtx context.Context, tx *sql.Tx) error {
			_, err := d.relations.CreateRelationTypes(mutationCtx, tx, entries, interfaces.ImportMode_Overwrite, true)
			return err
		})
}

func (d *coreDefinitions) removeRelationType(ctx context.Context, knID, branch, id string) error {
	_, exists, err := d.relations.CheckRelationTypeExistByID(ctx, knID, branch, id)
	if err != nil || !exists {
		return err
	}
	// An unbound entry is how a deletion is put to the proxy publication.
	entries := []*interfaces.RelationType{{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: id}}}
	changes := &interfaces.KN{KNID: knID, Branch: branch, RelationTypes: entries}
	return d.networks.PublishKNChildMutation(ctx, changes, interfaces.ImportMode_Overwrite,
		func(mutationCtx context.Context, tx *sql.Tx) error {
			return d.relations.DeleteRelationTypesByIDs(mutationCtx, tx, knID, branch, []string{id})
		})
}

// usersOf looks for what the definition API looks for before it deletes an
// object type: the relation types bound to it and the action types on it.
func (d *coreDefinitions) usersOf(ctx context.Context, knID, branch, id, except string) ([]string, error) {
	all := interfaces.PaginationQueryParameters{Limit: -1}
	relations, _, err := d.relations.ListRelationTypes(ctx, interfaces.RelationTypesQueryParams{
		KNID: knID, Branch: branch, BoundObjectTypeIDs: []string{id}, PaginationQueryParameters: all,
	})
	if err != nil {
		return nil, err
	}
	users := []string{}
	for _, relation := range relations {
		if relation.RTID != except {
			users = append(users, relation.RTName)
		}
	}
	actions, _, err := d.actions.ListActionTypes(ctx, interfaces.ActionTypesQueryParams{
		KNID: knID, Branch: branch, ObjectTypeIDs: []string{id}, PaginationQueryParameters: all,
	})
	if err != nil {
		return nil, err
	}
	for _, action := range actions {
		users = append(users, action.ATName)
	}
	return users, nil
}

func (d *coreDefinitions) removeObjectType(ctx context.Context, knID, branch, id string) error {
	entries := []*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: id}}}
	changes := &interfaces.KN{KNID: knID, Branch: branch, ObjectTypes: entries}
	return d.networks.PublishKNChildMutation(ctx, changes, interfaces.ImportMode_Overwrite,
		func(mutationCtx context.Context, tx *sql.Tx) error {
			return d.objects.DeleteObjectTypesByIDs(mutationCtx, tx, knID, branch, []string{id})
		})
}
