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
	aoa        interfaces.AgentOperatorAccess
	ps         interfaces.PermissionService
}

func NewCapabilityBindingService(appSetting *common.AppSetting) interfaces.CapabilityBindingService {
	capabilityBindingServiceOnce.Do(func() {
		capabilityBindingServiceInst = &capabilityBindingService{
			appSetting: appSetting,
			db:         logics.DB,
			cba:        logics.CBA,
			aoa:        logics.AOA,
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
	case interfaces.CAPABILITY_TYPE_FUNCTION, interfaces.CAPABILITY_TYPE_MCP_TOOL:
		// Both name a tool inside a container — a tool box, or an MCP Server — and the id alone
		// does not identify one: tool ids are scoped to their box, tool names to their server.
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

	// Every target is normalised, expanded and validated against the execution factory before a
	// single row is written. Validating inside the write loop would leave a half-mounted request
	// behind when the fourth capability turns out not to exist.
	resolvedEntries, err := cbs.resolveEntries(ctx, entries)
	if err != nil {
		return nil, err
	}

	result := make([]*interfaces.CapabilityBinding, 0, len(resolvedEntries))
	toCreate := make([]*interfaces.CapabilityBinding, 0, len(resolvedEntries))
	// Seen tracks the identities of this request so that a payload repeating the same capability
	// produces one row, not a duplicate-key failure halfway through the batch.
	seen := make(map[string]*interfaces.CapabilityBinding, len(resolvedEntries))

	for _, resolvedEntry := range resolvedEntries {
		capabilityType, ownerID, capabilityID := resolvedEntry.capabilityType, resolvedEntry.ownerID, resolvedEntry.capabilityID
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
			Comment:        resolvedEntry.comment,
			BoundAsBox:     resolvedEntry.boundAsBox,
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
	if metadataType := strings.TrimSpace(query.MetadataType); metadataType != "" &&
		metadataType != interfaces.EXEC_BOX_METADATA_TYPE_OPENAPI &&
		metadataType != interfaces.EXEC_BOX_METADATA_TYPE_FUNCTION {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("unsupported metadata_type: %s", metadataType))
	}

	// metadata_type reaches SQL as a set of tool boxes. Resolving it here rather than filtering
	// the fetched page is what keeps paging honest: the page is drawn from rows that already
	// match, so total_count is the real total and page two exists.
	if metadataType := strings.TrimSpace(query.MetadataType); metadataType != "" {
		boxIDs, boxErr := cbs.boxesOfKind(ctx, query.KNID, query.Branch, metadataType)
		if boxErr != nil {
			return nil, boxErr
		}
		query.OwnerIDs = &boxIDs
		// The kind belongs to a tool box, so this necessarily selects function bindings only.
		// Saying so makes the answer the same whether or not the caller also passed type.
		query.CapabilityType = interfaces.CAPABILITY_TYPE_FUNCTION
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

	backfilled := cbs.backfillMetadata(ctx, query, entries, query.WithDetail)

	span.SetStatus(codes.Ok, "")
	return &interfaces.CapabilityBindingsList{
		Entries:           entries,
		TotalCount:        total,
		Boxes:             backfilled.boxes,
		MetadataAvailable: backfilled.available,
	}, nil
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

	// Split the function count by the kind of box each binding belongs to. The kind is not in
	// these rows — it belongs to the box, in the execution factory — so this costs one call per
	// box that has bindings, not one per binding, and only on the statistics path.
	if totals[interfaces.CAPABILITY_TYPE_FUNCTION] > 0 {
		apis, splitErr := cbs.countAPIBindings(ctx, knID, branch)
		if splitErr != nil {
			// A box that cannot be read leaves its bindings counted as functions, which is the
			// same fallback the count had before this split existed. Failing the whole
			// statistics block over a decoration would be worse than a count that is briefly
			// weighted to one side.
			logger.Warnf("api/function split unavailable for knowledge network[%s]: %v", knID, splitErr)
		} else {
			totals[interfaces.CAPABILITY_TYPE_API] = apis
			totals[interfaces.CAPABILITY_TYPE_FUNCTION] -= apis
		}
	}

	span.SetStatus(codes.Ok, "")
	return totals, nil
}

// boxesOfKind returns the tool boxes of this branch that are of the given kind.
//
// A box that cannot be read counts as a function box, matching countAPIBindings and the meaning
// the single count had before the split. That keeps a dangling binding — one whose tool is gone
// while its box remains — inside the function list rather than vanishing from both, which is
// where its missing marker is meant to be seen.
func (cbs *capabilityBindingService) boxesOfKind(ctx context.Context, knID, branch,
	metadataType string) ([]string, error) {
	perBox, err := cbs.cba.GetFunctionTotalsByOwner(ctx, knID, branch)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_CapabilityBinding_InternalError_ListBindingsFailed).
			WithErrorDetails(err.Error())
	}

	// Non-nil and possibly empty: no box of that kind must select nothing, not everything.
	boxIDs := []string{}
	for boxID := range perBox {
		if boxID == "" {
			continue
		}
		tools, toolsErr := cbs.aoa.ListBoxTools(ctx, boxID)
		if toolsErr != nil {
			return nil, rest.NewHTTPError(ctx, http.StatusBadGateway,
				berrors.BknBackend_CapabilityBinding_ExecutionFactoryUnavailable).
				WithErrorDetails(fmt.Sprintf("tool box lookup failed: box_id=%s", boxID))
		}
		kind := interfaces.EXEC_BOX_METADATA_TYPE_FUNCTION
		if len(tools) > 0 && tools[0].BoxMetadataType != "" {
			kind = tools[0].BoxMetadataType
		}
		if kind == metadataType {
			boxIDs = append(boxIDs, boxID)
		}
	}
	return boxIDs, nil
}

// countAPIBindings counts the function bindings whose tool box is an openapi box.
//
// A box that cannot be read counts as a function rather than failing: that is the pre-split
// behaviour, and the dangling marker on the listing is what makes a missing box visible.
func (cbs *capabilityBindingService) countAPIBindings(ctx context.Context, knID,
	branch string) (int, error) {
	perBox, err := cbs.cba.GetFunctionTotalsByOwner(ctx, knID, branch)
	if err != nil {
		return 0, err
	}

	apis := 0
	for boxID, count := range perBox {
		if boxID == "" {
			continue
		}
		tools, err := cbs.aoa.ListBoxTools(ctx, boxID)
		if err != nil {
			return 0, err
		}
		if len(tools) == 0 {
			continue
		}
		if tools[0].BoxMetadataType == interfaces.EXEC_BOX_METADATA_TYPE_OPENAPI {
			apis += count
		}
	}
	return apis, nil
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

// ResolveCapabilities returns the references bound to one knowledge network branch.
//
// No metadata and no paging. Context Loader uses this to decide what is in scope before asking
// the execution factory to search inside it, so a page of the scope would be a different, smaller
// scope — and the names it would carry are the ones the caller is about to fetch anyway.
func (cbs *capabilityBindingService) ResolveCapabilities(ctx context.Context, knID, branch,
	capabilityType string) (*interfaces.CapabilityReferenceList, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Resolve capabilities")
	defer span.End()

	if err := cbs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   knID,
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}); err != nil {
		return nil, err
	}

	capabilityType = strings.TrimSpace(capabilityType)
	if capabilityType != "" && !interfaces.IsValidCapabilityType(capabilityType) {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_CapabilityBinding_InvalidCapabilityType).
			WithErrorDetails(fmt.Sprintf("unsupported capability_type: %s", capabilityType))
	}

	bindings, err := cbs.cba.ListBindings(ctx, interfaces.CapabilityBindingsQueryParams{
		KNID:           knID,
		Branch:         branch,
		CapabilityType: capabilityType,
	})
	if err != nil {
		logger.Errorf("ResolveCapabilities in knowledge network[%s] error: %v", knID, err)
		span.SetStatus(codes.Error, common.SafeErrorSummary(err))
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_CapabilityBinding_InternalError_ListBindingsFailed).WithErrorDetails(err.Error())
	}

	// An unbound network resolves to an empty list, not an error: "this network has no skills" is
	// an answer, and turning it into a failure would make retrieval report a broken dependency.
	entries := make([]*interfaces.CapabilityReference, 0, len(bindings))
	for _, binding := range bindings {
		entries = append(entries, &interfaces.CapabilityReference{
			CapabilityType: binding.CapabilityType,
			BoxID:          binding.OwnerID,
			CapabilityID:   binding.CapabilityID,
		})
	}
	span.SetStatus(codes.Ok, "")
	return &interfaces.CapabilityReferenceList{Entries: entries}, nil
}
