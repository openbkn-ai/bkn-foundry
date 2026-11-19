// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"database/sql"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/driveradapters"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	corelogics "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/action_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/kn_version"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/knowledge_network"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/object_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/relation_type"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
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

// publishableKey marks a context whose draft writes may be published, with the
// version that was running when the draft was found clean.
type publishableKey struct{}

// publishSource tells a version that the platform published it.
var publishSource = &interfaces.KNVersionSource{Type: "platform", Ref: "knevent"}

// begin follows a public direct write on a versioned network: a draft with no
// changes of its own, put back on the running version if that was switched,
// takes the writes, and they are published. The writes carry the draft
// revision seen here, so that a change of anyone else in between fails them
// rather than being published with them.
func (d *coreDefinitions) begin(ctx context.Context, knID string) (context.Context, error) {
	if corelogics.KNVA == nil {
		return ctx, nil
	}
	state, err := corelogics.KNVA.GetState(ctx, nil, knID, false)
	if err != nil || state == nil || state.Mode == interfaces.KNVersioningDisabling || state.RunningVersion == 0 {
		return ctx, err
	}
	if state.DraftHasChanges() {
		versions := kn_version.Current()
		if versions == nil {
			return ctx, nil
		}
		if err := versions.RebaseDraft(ctx, knID); err != nil {
			logger.Warnf("knowledge-network event: put the draft of network %s back on its running version: %v", knID, err)
			return ctx, nil
		}
		if state, err = corelogics.KNVA.GetState(ctx, nil, knID, false); err != nil || state == nil {
			return ctx, err
		}
		if state.DraftHasChanges() {
			return ctx, nil
		}
	}
	revision := state.DraftRevision
	ctx, _ = corelogics.WithDraftWrite(ctx, &revision)
	return context.WithValue(ctx, publishableKey{}, state.RunningVersion), nil
}

func (d *coreDefinitions) publish(ctx context.Context, knID, note string) error {
	running, ok := ctx.Value(publishableKey{}).(int)
	write := corelogics.DraftWriteFrom(ctx)
	versions := kn_version.Current()
	if !ok || write == nil || !write.Written || versions == nil {
		return nil
	}
	revision := write.Revision
	// The writes are committed: the release finishes when the caller has gone.
	_, err := versions.Publish(context.WithoutCancel(ctx), knID, &interfaces.KNPublishRequest{
		ExpectedRunningVersion: &running,
		ExpectedDraftRevision:  &revision,
		Notes:                  note,
		Source:                 publishSource,
	})
	var refused *httperrors.HTTPError
	if errors.As(err, &refused) && refused.ErrorCode == berrors.BknBackend_KNVersion_NothingToPublish {
		return nil
	}
	return err
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
