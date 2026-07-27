// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/rs/xid"

	"bkn-backend/common/bkntrace"
	"bkn-backend/interfaces"
)

const (
	headerBKNRequestID        = "bkn-request-id"
	headerLegacyRequestID     = "x-request-id"
	headerBKNInteractionID    = "bkn-interaction-id"
	headerBKNOperationID      = "bkn-operation-id"
	headerBKNCausationEventID = "bkn-causation-event-id"
	headerBKNClaimID          = "bkn-claim-id"
	headerBKNAttempt          = "bkn-attempt"
	headerBKNEventObservedAt  = "bkn-event-observed-at"
	headerBKNEvidenceEventID  = "bkn-evidence-event-id"
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
	attempt, _ := strconv.Atoi(strings.TrimSpace(c.GetHeader(headerBKNAttempt)))
	if attempt < 1 || attempt > 1000 {
		attempt = 1
	}
	interactionID := strings.TrimSpace(c.GetHeader(headerBKNInteractionID))
	if interactionID == "" {
		interactionID = "int_" + xid.New().String()
	}
	operationID := strings.TrimSpace(c.GetHeader(headerBKNOperationID))
	if operationID == "" {
		operationID = "op_" + xid.New().String()
	}
	observedAt := strings.TrimSpace(c.GetHeader(headerBKNEventObservedAt))
	if observedAt == "" {
		observedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	c.Header(headerBKNRequestID, requestID)
	c.Header(headerBKNInteractionID, interactionID)
	c.Header(headerBKNOperationID, operationID)
	c.Header(headerBKNEventObservedAt, observedAt)
	return bkntrace.RequestContext{
		RequestID:        requestID,
		AccountID:        accountID,
		AccountType:      accountType,
		BusinessDomain:   strings.TrimSpace(c.GetHeader(interfaces.HTTP_HEADER_BUSINESS_DOMAIN)),
		InteractionID:    interactionID,
		OperationID:      operationID,
		CausationEventID: strings.TrimSpace(c.GetHeader(headerBKNCausationEventID)),
		ClaimID:          strings.TrimSpace(c.GetHeader(headerBKNClaimID)),
		Attempt:          attempt,
		ObservedAt:       observedAt,
	}
}

func emitObjectTypeSchemaRead(ctx context.Context, c *gin.Context, vis hydra.Visitor, operation, knID, branch string, requestedIDs []string, items []*interfaces.ObjectType, total int64) {
	if !bkntrace.EvidenceEnabled() {
		return
	}
	eventID := bkntrace.EmitSchemaReadEvents(ctx, bknTraceRequestContext(c, vis), bkntrace.ReadSubject{
		EntityKind:    bkntrace.EntityKindObjectType,
		Operation:     operation,
		KNID:          knID,
		Branch:        branch,
		RequestedIDs:  requestedIDs,
		ReturnedCount: len(items),
		TotalCount:    total,
	}, bkntrace.ObjectTypeRefs(items))
	setEvidenceEventHeader(c, eventID)
}

func emitRelationTypeSchemaRead(ctx context.Context, c *gin.Context, vis hydra.Visitor, operation, knID, branch string, requestedIDs []string, items []*interfaces.RelationType, total int64) {
	if !bkntrace.EvidenceEnabled() {
		return
	}
	eventID := bkntrace.EmitSchemaReadEvents(ctx, bknTraceRequestContext(c, vis), bkntrace.ReadSubject{
		EntityKind:    bkntrace.EntityKindRelationType,
		Operation:     operation,
		KNID:          knID,
		Branch:        branch,
		RequestedIDs:  requestedIDs,
		ReturnedCount: len(items),
		TotalCount:    total,
	}, bkntrace.RelationTypeRefs(items))
	setEvidenceEventHeader(c, eventID)
}

func emitActionTypeSchemaRead(ctx context.Context, c *gin.Context, vis hydra.Visitor, operation, knID, branch string, requestedIDs []string, items []*interfaces.ActionType, total int64) {
	if !bkntrace.EvidenceEnabled() {
		return
	}
	eventID := bkntrace.EmitSchemaReadEvents(ctx, bknTraceRequestContext(c, vis), bkntrace.ReadSubject{
		EntityKind:    bkntrace.EntityKindActionType,
		Operation:     operation,
		KNID:          knID,
		Branch:        branch,
		RequestedIDs:  requestedIDs,
		ReturnedCount: len(items),
		TotalCount:    total,
	}, bkntrace.ActionTypeRefs(items))
	setEvidenceEventHeader(c, eventID)
}

func emitMetricSchemaRead(ctx context.Context, c *gin.Context, vis hydra.Visitor, operation, knID, branch string, requestedIDs []string, items []*interfaces.MetricDefinition, total int64) {
	if !bkntrace.EvidenceEnabled() {
		return
	}
	eventID := bkntrace.EmitSchemaReadEvents(ctx, bknTraceRequestContext(c, vis), bkntrace.ReadSubject{
		EntityKind:    bkntrace.EntityKindMetric,
		Operation:     operation,
		KNID:          knID,
		Branch:        branch,
		RequestedIDs:  requestedIDs,
		ReturnedCount: len(items),
		TotalCount:    total,
	}, bkntrace.MetricRefs(items))
	setEvidenceEventHeader(c, eventID)
}

func setEvidenceEventHeader(c *gin.Context, eventID string) {
	if strings.TrimSpace(eventID) != "" {
		c.Header(headerBKNEvidenceEventID, eventID)
	}
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
