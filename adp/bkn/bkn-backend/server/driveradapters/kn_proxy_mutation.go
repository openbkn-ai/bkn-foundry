// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"database/sql"

	"bkn-backend/interfaces"
)

func (r *restHandler) publishKNChildMutation(ctx context.Context, changes *interfaces.KN, mergeMode string,
	mutate func(context.Context, *sql.Tx) error) error {
	if r.knProxyPublisher == nil {
		return mutate(ctx, nil)
	}
	return r.knProxyPublisher.PublishKNChildMutation(ctx, changes, mergeMode, mutate)
}

func (r *restHandler) createObjectTypes(ctx context.Context, knID, branch string,
	entries []*interfaces.ObjectType, mode string, strictMode bool) (ids []string, err error) {
	changes := &interfaces.KN{KNID: knID, Branch: branch, ObjectTypes: entries}
	err = r.publishKNChildMutation(ctx, changes, mode, func(mutationCtx context.Context, tx *sql.Tx) error {
		ids, err = r.ots.CreateObjectTypes(mutationCtx, tx, entries, mode, true, strictMode)
		return err
	})
	return ids, err
}

func (r *restHandler) updateObjectType(ctx context.Context, objectType *interfaces.ObjectType,
	strictMode bool) error {
	changes := &interfaces.KN{
		KNID: objectType.KNID, Branch: objectType.Branch, ObjectTypes: []*interfaces.ObjectType{objectType},
	}
	return r.publishKNChildMutation(ctx, changes, interfaces.ImportMode_Overwrite, func(mutationCtx context.Context, tx *sql.Tx) error {
		return r.ots.UpdateObjectType(mutationCtx, tx, objectType, strictMode)
	})
}

func (r *restHandler) deleteObjectTypes(ctx context.Context, knID, branch string, ids []string) error {
	changes := &interfaces.KN{KNID: knID, Branch: branch}
	return r.publishKNChildMutation(ctx, changes, interfaces.ImportMode_Normal, func(mutationCtx context.Context, tx *sql.Tx) error {
		return r.ots.DeleteObjectTypesByIDs(mutationCtx, tx, knID, branch, ids)
	})
}

func (r *restHandler) createRelationTypes(ctx context.Context, knID, branch string,
	entries []*interfaces.RelationType, mode string, strictMode bool) (ids []string, err error) {
	changes := &interfaces.KN{KNID: knID, Branch: branch, RelationTypes: entries}
	err = r.publishKNChildMutation(ctx, changes, mode, func(mutationCtx context.Context, tx *sql.Tx) error {
		ids, err = r.rts.CreateRelationTypes(mutationCtx, tx, entries, mode, strictMode)
		return err
	})
	return ids, err
}

func (r *restHandler) updateRelationType(ctx context.Context, relationType *interfaces.RelationType,
	strictMode bool) error {
	changes := &interfaces.KN{
		KNID: relationType.KNID, Branch: relationType.Branch, RelationTypes: []*interfaces.RelationType{relationType},
	}
	return r.publishKNChildMutation(ctx, changes, interfaces.ImportMode_Overwrite, func(mutationCtx context.Context, tx *sql.Tx) error {
		return r.rts.UpdateRelationType(mutationCtx, tx, relationType, strictMode)
	})
}

func (r *restHandler) deleteRelationTypes(ctx context.Context, knID, branch string, ids []string) error {
	changes := &interfaces.KN{KNID: knID, Branch: branch}
	return r.publishKNChildMutation(ctx, changes, interfaces.ImportMode_Normal, func(mutationCtx context.Context, tx *sql.Tx) error {
		return r.rts.DeleteRelationTypesByIDs(mutationCtx, tx, knID, branch, ids)
	})
}

func (r *restHandler) createActionTypes(ctx context.Context, knID, branch string,
	entries []*interfaces.ActionType, mode string, strictMode bool) (ids []string, err error) {
	changes := &interfaces.KN{KNID: knID, Branch: branch, ActionTypes: entries}
	err = r.publishKNChildMutation(ctx, changes, mode, func(mutationCtx context.Context, tx *sql.Tx) error {
		ids, err = r.ats.CreateActionTypes(mutationCtx, tx, entries, mode, strictMode)
		return err
	})
	return ids, err
}

func (r *restHandler) updateActionType(ctx context.Context, actionType *interfaces.ActionType,
	strictMode bool) error {
	changes := &interfaces.KN{
		KNID: actionType.KNID, Branch: actionType.Branch, ActionTypes: []*interfaces.ActionType{actionType},
	}
	return r.publishKNChildMutation(ctx, changes, interfaces.ImportMode_Overwrite, func(mutationCtx context.Context, tx *sql.Tx) error {
		return r.ats.UpdateActionType(mutationCtx, tx, actionType, strictMode)
	})
}

func (r *restHandler) deleteActionTypes(ctx context.Context, knID, branch string, ids []string) error {
	changes := &interfaces.KN{KNID: knID, Branch: branch}
	return r.publishKNChildMutation(ctx, changes, interfaces.ImportMode_Normal, func(mutationCtx context.Context, tx *sql.Tx) error {
		return r.ats.DeleteActionTypesByIDs(mutationCtx, tx, knID, branch, ids)
	})
}

func (r *restHandler) createMetrics(ctx context.Context, knID, branch string,
	entries []*interfaces.MetricDefinition, strictMode bool, mode string) (ids []string, err error) {
	changes := &interfaces.KN{KNID: knID, Branch: branch, Metrics: entries}
	err = r.publishKNChildMutation(ctx, changes, mode, func(mutationCtx context.Context, tx *sql.Tx) error {
		ids, err = r.ms.CreateMetrics(mutationCtx, tx, entries, strictMode, mode)
		return err
	})
	return ids, err
}

func (r *restHandler) updateMetric(ctx context.Context, metric *interfaces.MetricDefinition,
	strictMode bool) error {
	changes := &interfaces.KN{
		KNID: metric.KnID, Branch: metric.Branch, Metrics: []*interfaces.MetricDefinition{metric},
	}
	return r.publishKNChildMutation(ctx, changes, interfaces.ImportMode_Overwrite, func(mutationCtx context.Context, tx *sql.Tx) error {
		return r.ms.UpdateMetric(mutationCtx, tx, metric, strictMode)
	})
}

func (r *restHandler) deleteMetrics(ctx context.Context, knID, branch string, ids []string) error {
	changes := &interfaces.KN{KNID: knID, Branch: branch}
	return r.publishKNChildMutation(ctx, changes, interfaces.ImportMode_Normal, func(mutationCtx context.Context, tx *sql.Tx) error {
		return r.ms.DeleteMetricsByIDs(mutationCtx, tx, knID, branch, ids)
	})
}
