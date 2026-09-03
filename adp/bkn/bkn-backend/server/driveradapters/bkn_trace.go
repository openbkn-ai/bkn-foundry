// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

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

func bknTraceRequestContext(c *gin.Context, vis hydra.Visitor) (bkntrace.RequestContext, error) {
	requestID := firstNonEmptyHeader(c, headerBKNRequestID, headerLegacyRequestID)
	if requestID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return bkntrace.RequestContext{}, fmt.Errorf("generate request UUIDv7: %w", err)
		}
		requestID = "req_" + id.String()
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
		id, err := uuid.NewV7()
		if err != nil {
			return bkntrace.RequestContext{}, fmt.Errorf("generate interaction UUIDv7: %w", err)
		}
		interactionID = "int_" + id.String()
	}
	operationID := strings.TrimSpace(c.GetHeader(headerBKNOperationID))
	if operationID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return bkntrace.RequestContext{}, fmt.Errorf("generate operation UUIDv7: %w", err)
		}
		operationID = "op_" + id.String()
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
		RequestID:              requestID,
		AccountID:              accountID,
		AccountType:            accountType,
		ApplicationPrincipalID: strings.TrimSpace(os.Getenv("BKN_TRACE_APPLICATION_PRINCIPAL_ID")),
		EffectiveSubjectID:     accountID,
		EffectiveSubjectType:   bknTraceSubjectType(accountType),
		DelegationID:           strings.TrimSpace(c.GetHeader("x-bkn-delegation-id")),
		InteractionID:          interactionID,
		OperationID:            operationID,
		CausationEventID:       strings.TrimSpace(c.GetHeader(headerBKNCausationEventID)),
		ClaimID:                strings.TrimSpace(c.GetHeader(headerBKNClaimID)),
		Attempt:                attempt,
		ObservedAt:             observedAt,
	}, nil
}

func bknTraceSubjectType(accountType string) string {
	if strings.EqualFold(accountType, "service") || strings.EqualFold(accountType, "app") {
		return "service"
	}
	return "user"
}

func emitObjectTypeSchemaRead(ctx context.Context, c *gin.Context, vis hydra.Visitor, operation, knID, branch string, requestedIDs []string, items []*interfaces.ObjectType, total int64) {
	if !bkntrace.EvidenceEnabled() {
		return
	}
	requestContext, err := bknTraceRequestContext(c, vis)
	if err != nil {
		logger.Errorf("build BKN trace request context failed: %v", err)
		return
	}
	eventID := bkntrace.EmitSchemaReadEvents(ctx, requestContext, bkntrace.ReadSubject{
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
	requestContext, err := bknTraceRequestContext(c, vis)
	if err != nil {
		logger.Errorf("build BKN trace request context failed: %v", err)
		return
	}
	eventID := bkntrace.EmitSchemaReadEvents(ctx, requestContext, bkntrace.ReadSubject{
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
	requestContext, err := bknTraceRequestContext(c, vis)
	if err != nil {
		logger.Errorf("build BKN trace request context failed: %v", err)
		return
	}
	eventID := bkntrace.EmitSchemaReadEvents(ctx, requestContext, bkntrace.ReadSubject{
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
	requestContext, err := bknTraceRequestContext(c, vis)
	if err != nil {
		logger.Errorf("build BKN trace request context failed: %v", err)
		return
	}
	eventID := bkntrace.EmitSchemaReadEvents(ctx, requestContext, bkntrace.ReadSubject{
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
