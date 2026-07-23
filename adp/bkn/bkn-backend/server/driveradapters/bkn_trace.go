// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/rs/xid"

	"bkn-backend/common/bkntrace"
	"bkn-backend/interfaces"
)

const (
	headerBKNRequestID    = "bkn-request-id"
	headerLegacyRequestID = "x-request-id"
)

func bknTraceRequestContext(c *gin.Context, vis hydra.Visitor) bkntrace.RequestContext {
	requestID := firstNonEmptyHeader(c, headerBKNRequestID, headerLegacyRequestID)
	if requestID == "" {
		requestID = "req_" + xid.New().String()
	}
	accountID := strings.TrimSpace(vis.ID)
	if accountID == "" {
		accountID = strings.TrimSpace(c.GetHeader(interfaces.HTTP_HEADER_ACCOUNT_ID))
	}
	accountType := strings.TrimSpace(string(vis.Type))
	if accountType == "" {
		accountType = strings.TrimSpace(c.GetHeader(interfaces.HTTP_HEADER_ACCOUNT_TYPE))
	}
	return bkntrace.RequestContext{
		RequestID:      requestID,
		AccountID:      accountID,
		AccountType:    accountType,
		BusinessDomain: strings.TrimSpace(c.GetHeader(interfaces.HTTP_HEADER_BUSINESS_DOMAIN)),
	}
}

func emitObjectTypeSchemaRead(ctx context.Context, c *gin.Context, vis hydra.Visitor, operation, knID, branch string, requestedIDs []string, items []*interfaces.ObjectType, total int64) {
	bkntrace.EmitSchemaReadEvents(ctx, bknTraceRequestContext(c, vis), bkntrace.ReadSubject{
		EntityKind:    bkntrace.EntityKindObjectType,
		Operation:     operation,
		KNID:          knID,
		Branch:        branch,
		RequestedIDs:  requestedIDs,
		ReturnedCount: len(items),
		TotalCount:    total,
	}, bkntrace.ObjectTypeRefs(items))
}

func emitRelationTypeSchemaRead(ctx context.Context, c *gin.Context, vis hydra.Visitor, operation, knID, branch string, requestedIDs []string, items []*interfaces.RelationType, total int64) {
	bkntrace.EmitSchemaReadEvents(ctx, bknTraceRequestContext(c, vis), bkntrace.ReadSubject{
		EntityKind:    bkntrace.EntityKindRelationType,
		Operation:     operation,
		KNID:          knID,
		Branch:        branch,
		RequestedIDs:  requestedIDs,
		ReturnedCount: len(items),
		TotalCount:    total,
	}, bkntrace.RelationTypeRefs(items))
}

func emitActionTypeSchemaRead(ctx context.Context, c *gin.Context, vis hydra.Visitor, operation, knID, branch string, requestedIDs []string, items []*interfaces.ActionType, total int64) {
	bkntrace.EmitSchemaReadEvents(ctx, bknTraceRequestContext(c, vis), bkntrace.ReadSubject{
		EntityKind:    bkntrace.EntityKindActionType,
		Operation:     operation,
		KNID:          knID,
		Branch:        branch,
		RequestedIDs:  requestedIDs,
		ReturnedCount: len(items),
		TotalCount:    total,
	}, bkntrace.ActionTypeRefs(items))
}

func emitMetricSchemaRead(ctx context.Context, c *gin.Context, vis hydra.Visitor, operation, knID, branch string, requestedIDs []string, items []*interfaces.MetricDefinition, total int64) {
	bkntrace.EmitSchemaReadEvents(ctx, bknTraceRequestContext(c, vis), bkntrace.ReadSubject{
		EntityKind:    bkntrace.EntityKindMetric,
		Operation:     operation,
		KNID:          knID,
		Branch:        branch,
		RequestedIDs:  requestedIDs,
		ReturnedCount: len(items),
		TotalCount:    total,
	}, bkntrace.MetricRefs(items))
}

func firstNonEmptyHeader(c *gin.Context, names ...string) string {
	for _, name := range names {
		value := strings.TrimSpace(c.GetHeader(name))
		if value != "" {
			return value
		}
	}
	return ""
}
