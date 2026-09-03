// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package capability_binding

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
	"bkn-backend/logics/permission"
)

// Column widths of t_kn_capability_binding, mirrored here so an over-long value is rejected
// with 400 instead of reaching the database.
const (
	maxOwnerIDLength      = 64
	maxCapabilityIDLength = 64
	maxCommentLength      = 255
)

var (
	capabilityBindingServiceOnce sync.Once
	capabilityBindingServiceInst interfaces.CapabilityBindingService
)

type capabilityBindingService struct {
	appSetting *common.AppSetting
	db         *sql.DB
	cba        interfaces.CapabilityBindingAccess
	ps         interfaces.PermissionService
}

func NewCapabilityBindingService(appSetting *common.AppSetting) interfaces.CapabilityBindingService {
	capabilityBindingServiceOnce.Do(func() {
		capabilityBindingServiceInst = &capabilityBindingService{
			appSetting: appSetting,
			db:         logics.DB,
			cba:        logics.CBA,
			ps:         permission.NewPermissionService(appSetting),
		}
	})
	return capabilityBindingServiceInst
}

// normalizeAttachEntry validates one mount item and returns its canonical three-part identity.
//
// A skill has no owning container, so a stray owner_id is dropped rather than stored: keeping it
// would create a second row for the same skill that the unique key cannot collapse, and repeated
// mounts would stop being idempotent.
func normalizeAttachEntry(ctx context.Context, entry *interfaces.AttachCapabilityEntry) (capabilityType, ownerID,
	capabilityID string, err error) {
	capabilityType = strings.TrimSpace(entry.CapabilityType)
	ownerID = strings.TrimSpace(entry.OwnerID)
	capabilityID = strings.TrimSpace(entry.CapabilityID)

	if !interfaces.IsValidCapabilityType(capabilityType) {
		return "", "", "", rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_InvalidCapabilityType).
			WithErrorDetails(fmt.Sprintf("unsupported capability_type: %s", capabilityType))
	}
	if capabilityID == "" {
		return "", "", "", rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_NullParameter_CapabilityID)
	}
	// Column widths are checked here rather than left to the database: an over-long value would
	// otherwise surface as a 500 under strict SQL mode, or be silently truncated into a binding
	// that points at a different capability.
	if len(capabilityID) > maxCapabilityIDLength {
		return "", "", "", rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("capability_id exceeds %d characters", maxCapabilityIDLength))
	}
	if len(ownerID) > maxOwnerIDLength {
		return "", "", "", rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("owner_id exceeds %d characters", maxOwnerIDLength))
	}
	if len(strings.TrimSpace(entry.Comment)) > maxCommentLength {
		return "", "", "", rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("comment exceeds %d characters", maxCommentLength))
	}
	switch capabilityType {
	case interfaces.CAPABILITY_TYPE_SKILL:
		ownerID = ""
	case interfaces.CAPABILITY_TYPE_FUNCTION:
		if ownerID == "" {
			return "", "", "", rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_CapabilityBinding_NullParameter_OwnerID)
		}
	}
	return capabilityType, ownerID, capabilityID, nil
}

func (cbs *capabilityBindingService) AttachCapabilities(ctx context.Context, tx *sql.Tx, knID, branch string,
	entries []*interfaces.AttachCapabilityEntry) ([]*interfaces.CapabilityBinding, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Attach capabilities")
	defer span.End()

	if err := cbs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   knID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY}); err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_InvalidParameter).
			WithErrorDetails("capabilities must not be empty")
	}

	currentTime := time.Now().UnixMilli()
	var accountInfo interfaces.AccountInfo
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}

	result := make([]*interfaces.CapabilityBinding, 0, len(entries))
	toCreate := make([]*interfaces.CapabilityBinding, 0, len(entries))
	// Seen tracks the identities of this request so that a payload repeating the same capability
	// produces one row, not a duplicate-key failure halfway through the batch.
	seen := make(map[string]*interfaces.CapabilityBinding, len(entries))

	for _, entry := range entries {
		capabilityType, ownerID, capabilityID, err := normalizeAttachEntry(ctx, entry)
		if err != nil {
			return nil, err
		}
		identity := strings.Join([]string{capabilityType, ownerID, capabilityID}, "\x00")
		if existing, ok := seen[identity]; ok {
			result = append(result, existing)
			continue
		}

		existing, err := cbs.cba.GetBindingByCapability(ctx, knID, branch, capabilityType, ownerID, capabilityID)
		if err != nil {
			logger.Errorf("GetBindingByCapability in knowledge network[%s] error: %v", knID, err)
			span.SetStatus(codes.Error, common.SafeErrorSummary(err))
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_CapabilityBinding_InternalError_CreateBindingsFailed).WithErrorDetails(err.Error())
		}
		// Mounting an already bound capability returns the existing row. Mounting is a statement
		// about membership, not an event, so repeating it is not an error.
		if existing != nil {
			seen[identity] = existing
			result = append(result, existing)
			continue
		}

		generatedID, generateErr := uuid.NewV7()
		if generateErr != nil {
			span.SetStatus(codes.Error, common.SafeErrorSummary(generateErr))
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_CapabilityBinding_InternalError).WithErrorDetails(generateErr.Error())
		}
		binding := &interfaces.CapabilityBinding{
			ID:             generatedID.String(),
			KNID:           knID,
			Branch:         branch,
			CapabilityType: capabilityType,
			OwnerID:        ownerID,
			CapabilityID:   capabilityID,
			Comment:        strings.TrimSpace(entry.Comment),
			Creator:        accountInfo,
			Updater:        accountInfo,
			CreateTime:     currentTime,
			UpdateTime:     currentTime,
		}
		seen[identity] = binding
		toCreate = append(toCreate, binding)
		result = append(result, binding)
	}

	if len(toCreate) > 0 {
		if err := cbs.cba.CreateBindings(ctx, tx, toCreate); err != nil {
			logger.Errorf("CreateBindings in knowledge network[%s] error: %v", knID, err)
			span.SetStatus(codes.Error, common.SafeErrorSummary(err))
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_CapabilityBinding_InternalError_CreateBindingsFailed).WithErrorDetails(err.Error())
		}
	}

	span.SetStatus(codes.Ok, "")
	return result, nil
}

func (cbs *capabilityBindingService) DetachCapabilities(ctx context.Context, tx *sql.Tx, knID, branch string,
	bindingIDs []string) (int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Detach capabilities")
	defer span.End()

	if err := cbs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   knID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY}); err != nil {
		return 0, err
	}

	rows, err := cbs.cba.DeleteBindingsByIDs(ctx, tx, knID, branch, bindingIDs)
	if err != nil {
		logger.Errorf("DeleteBindingsByIDs in knowledge network[%s] error: %v", knID, err)
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_CapabilityBinding_InternalError_DeleteBindingsFailed).WithErrorDetails(err.Error())
	}
	span.SetStatus(codes.Ok, "")
	return rows, nil
}

func (cbs *capabilityBindingService) ListCapabilities(ctx context.Context,
	query interfaces.CapabilityBindingsQueryParams) (*interfaces.CapabilityBindingsList, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "List capabilities")
	defer span.End()

	if err := cbs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   query.KNID,
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}); err != nil {
		return nil, err
	}

	if query.CapabilityType != "" && !interfaces.IsValidCapabilityType(query.CapabilityType) {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_InvalidCapabilityType).
			WithErrorDetails(fmt.Sprintf("unsupported capability_type: %s", query.CapabilityType))
	}

	entries, err := cbs.cba.ListBindings(ctx, query)
	if err != nil {
		logger.Errorf("ListBindings in knowledge network[%s] error: %v", query.KNID, err)
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_CapabilityBinding_InternalError_ListBindingsFailed).WithErrorDetails(err.Error())
	}
	total, err := cbs.cba.GetBindingsTotal(ctx, query)
	if err != nil {
		logger.Errorf("GetBindingsTotal in knowledge network[%s] error: %v", query.KNID, err)
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_CapabilityBinding_InternalError_GetBindingsTotalFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return &interfaces.CapabilityBindingsList{Entries: entries, TotalCount: total}, nil
}

func (cbs *capabilityBindingService) GetCapabilityTotalsByType(ctx context.Context, knID,
	branch string) (map[string]int, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Count capabilities by type")
	defer span.End()

	totals, err := cbs.cba.GetBindingsTotalByType(ctx, knID, branch)
	if err != nil {
		logger.Errorf("GetBindingsTotalByType in knowledge network[%s] error: %v", knID, err)
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_CapabilityBinding_InternalError_GetBindingsTotalFailed).WithErrorDetails(err.Error())
	}
	span.SetStatus(codes.Ok, "")
	return totals, nil
}

func (cbs *capabilityBindingService) DeleteCapabilitiesByKnID(ctx context.Context, tx *sql.Tx, knID,
	branch string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete capabilities by knowledge network")
	defer span.End()

	if _, err := cbs.cba.DeleteBindingsByKnID(ctx, tx, knID, branch); err != nil {
		logger.Errorf("DeleteBindingsByKnID in knowledge network[%s] error: %v", knID, err)
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_CapabilityBinding_InternalError_DeleteBindingsFailed).WithErrorDetails(err.Error())
	}
	span.SetStatus(codes.Ok, "")
	return nil
}
