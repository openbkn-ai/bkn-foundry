// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package value_type

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	bknsdk "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
)

// Value types (SPECIFICATION.md §16) have no authorization of their own: reading them needs
// view_detail on the knowledge network, changing them needs modify on it.

var (
	vtsOnce sync.Once
	vts     interfaces.ValueTypeService
)

type valueTypeService struct {
	db  *sql.DB
	vta interfaces.ValueTypeAccess
	ota interfaces.ObjectTypeAccess
	ps  interfaces.PermissionService
	uma interfaces.UserMgmtAccess
}

func NewValueTypeService(appSetting *common.AppSetting) interfaces.ValueTypeService {
	vtsOnce.Do(func() {
		vts = &valueTypeService{
			db:  logics.DB,
			vta: logics.VTA,
			ota: logics.OTA,
			ps:  permission.NewPermissionService(appSetting),
			uma: logics.UMA,
		}
	})
	return vts
}

func detail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.ValueType.InvalidParameter.Detail."+name, templateData)
}

func internalError(ctx context.Context, err error) error {
	return rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ValueType_InternalError).
		WithErrorDetails(err.Error())
}

func (s *valueTypeService) checkKNPermission(ctx context.Context, knID string, operation string) error {
	if permission.KNImportPermissionPrechecked(ctx) {
		return nil
	}
	return s.ps.CheckPermission(ctx, interfaces.PermissionResource{Type: interfaces.RESOURCE_TYPE_KN, ID: knID},
		[]string{operation})
}

// prepare checks a value type with the checks a 3.0 import runs and derives what is stored with it.
func prepare(ctx context.Context, vt *interfaces.ValueType) error {
	vt.ModuleType = interfaces.MODULE_TYPE_VALUE_TYPE
	// What is checked is what is stored: a null enum item would be skipped by the check and kept
	// in the stored definition.
	if slices.Contains(vt.Enum, nil) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ValueType_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("value type %q: an enum item is null", vt.VTID))
	}
	bkn := logics.ToBKNValueType(vt)
	if err := bknsdk.ValidateValueType(bkn); err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ValueType_InvalidParameter).
			WithErrorDetails(err.Error())
	}
	vt.BKNRawContent = bknsdk.SerializeValueType(bkn)
	return nil
}

func accountInfo(ctx context.Context) interfaces.AccountInfo {
	if info, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo); ok {
		return info
	}
	return interfaces.AccountInfo{}
}

// withTx runs fn in tx, or in a transaction of its own when tx is nil.
func (s *valueTypeService) withTx(ctx context.Context, tx *sql.Tx, fn func(tx *sql.Tx) error) (err error) {
	if tx != nil {
		return fn(tx)
	}
	tx, err = s.db.Begin()
	if err != nil {
		return internalError(ctx, err)
	}
	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				otellog.LogError(ctx, "value type transaction rollback failed", rollbackErr)
			}
			return
		}
		if err = tx.Commit(); err != nil {
			err = internalError(ctx, err)
		}
	}()
	return fn(tx)
}

func (s *valueTypeService) CreateValueTypes(ctx context.Context, tx *sql.Tx, valueTypes []*interfaces.ValueType,
	mode string) ([]string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CreateValueTypes")
	defer span.End()

	if len(valueTypes) == 0 {
		return []string{}, nil
	}
	knID, branch := valueTypes[0].KNID, valueTypes[0].Branch
	if err := s.checkKNPermission(ctx, knID, interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	account := accountInfo(ctx)
	for _, vt := range valueTypes {
		if vt.KNID != knID || vt.Branch != branch {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ValueType_InvalidParameter)
		}
		var err error
		if vt.VTID, err = permission.PrepareKNChildResourceID(ctx, vt.VTID); err != nil {
			return nil, err
		}
		if err = prepare(ctx, vt); err != nil {
			return nil, err
		}
		vt.Creator, vt.Updater = account, account
		vt.CreateTime, vt.UpdateTime = now, now
	}

	creates, updates, err := s.splitByImportMode(ctx, mode, valueTypes)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(creates)+len(updates))
	err = s.withTx(ctx, tx, func(tx *sql.Tx) error {
		if len(creates) > 0 {
			if err := s.vta.CreateValueTypes(ctx, tx, creates); err != nil {
				if constraint, duplicate := common.DatabaseUniqueConstraint(err); duplicate {
					code := berrors.BknBackend_ValueType_ValueTypeIDExisted
					if strings.Contains(constraint, "name") {
						code = berrors.BknBackend_ValueType_ValueTypeNameExisted
					}
					return rest.NewHTTPError(ctx, http.StatusBadRequest, code).WithErrorDetails(err.Error())
				}
				return internalError(ctx, err)
			}
			for _, vt := range creates {
				ids = append(ids, vt.VTID)
			}
		}
		for _, vt := range updates {
			if err := s.update(ctx, tx, vt); err != nil {
				return err
			}
			ids = append(ids, vt.VTID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return ids, nil
}

// splitByImportMode sorts value types into those to create and those to overwrite, the rule every
// other kind of definition follows: normal refuses an existing ID or name, ignore skips them,
// overwrite replaces a value type of the same ID and refuses a name another value type holds.
func (s *valueTypeService) splitByImportMode(ctx context.Context, mode string,
	valueTypes []*interfaces.ValueType) (creates, updates []*interfaces.ValueType, err error) {
	if mode != interfaces.ImportMode_Normal && mode != interfaces.ImportMode_Ignore && mode != interfaces.ImportMode_Overwrite {
		return nil, nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ImportMode).
			WithErrorDetails(i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ImportMode", nil))
	}
	ids := make([]string, 0, len(valueTypes))
	names := make([]string, 0, len(valueTypes))
	for _, vt := range valueTypes {
		ids = append(ids, vt.VTID)
		names = append(names, vt.VTName)
	}
	existing, err := s.vta.GetValueTypeIdentitiesByIDsOrNames(ctx, valueTypes[0].KNID, valueTypes[0].Branch, ids, names)
	if err != nil {
		return nil, nil, internalError(ctx, err)
	}
	nameByID := map[string]string{}
	idByName := map[string]string{}
	for _, vt := range existing {
		nameByID[vt.VTID] = vt.VTName
		idByName[vt.VTName] = vt.VTID
	}
	for _, vt := range valueTypes {
		_, idExists := nameByID[vt.VTID]
		holder, nameExists := idByName[vt.VTName]
		nameTaken := nameExists && holder != vt.VTID
		switch {
		case !idExists && !nameExists:
			creates = append(creates, vt)
		case mode == interfaces.ImportMode_Ignore:
		case mode == interfaces.ImportMode_Overwrite && !nameTaken:
			updates = append(updates, vt)
		case idExists && mode == interfaces.ImportMode_Normal:
			return nil, nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ValueType_ValueTypeIDExisted).
				WithErrorDetails(detail(ctx, "ValueTypeIDAlreadyExists", map[string]any{"valueTypeID": vt.VTID}))
		default:
			return nil, nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ValueType_ValueTypeNameExisted).
				WithDescription(map[string]any{"name": vt.VTName}).
				WithErrorDetails(detail(ctx, "ValueTypeNameAlreadyExists", map[string]any{"valueTypeName": vt.VTName}))
		}
	}
	return creates, updates, nil
}

func (s *valueTypeService) ListValueTypes(ctx context.Context, query interfaces.ValueTypesQueryParams) ([]*interfaces.ValueType, int, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListValueTypes")
	defer span.End()

	if err := s.checkKNPermission(ctx, query.KNID, interfaces.OPERATION_TYPE_VIEW_DETAIL); err != nil {
		return nil, 0, err
	}
	all, err := s.vta.ListValueTypes(ctx, query)
	if err != nil {
		return nil, 0, internalError(ctx, err)
	}
	total := len(all)
	page := all
	if query.Offset > 0 {
		page = page[min(query.Offset, len(page)):]
	}
	if query.Limit >= 0 && query.Limit < len(page) {
		page = page[:query.Limit]
	}
	s.fillAccountNames(ctx, page)
	span.SetStatus(codes.Ok, "")
	return page, total, nil
}

func (s *valueTypeService) GetValueTypeByID(ctx context.Context, knID string, branch string, vtID string) (*interfaces.ValueType, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetValueTypeByID")
	defer span.End()

	if err := s.checkKNPermission(ctx, knID, interfaces.OPERATION_TYPE_VIEW_DETAIL); err != nil {
		return nil, err
	}
	vt, err := s.get(ctx, nil, knID, branch, vtID)
	if err != nil {
		return nil, err
	}
	s.fillAccountNames(ctx, []*interfaces.ValueType{vt})
	span.SetStatus(codes.Ok, "")
	return vt, nil
}

func (s *valueTypeService) get(ctx context.Context, tx *sql.Tx, knID, branch, vtID string) (*interfaces.ValueType, error) {
	list, err := s.vta.GetValueTypesByIDs(ctx, tx, knID, branch, []string{vtID})
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(list) != 1 {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ValueType_ValueTypeNotFound).
			WithErrorDetails(fmt.Sprintf("value type %q not found", vtID))
	}
	return list[0], nil
}

func (s *valueTypeService) fillAccountNames(ctx context.Context, list []*interfaces.ValueType) {
	if len(list) == 0 || s.uma == nil {
		return
	}
	accounts := make([]*interfaces.AccountInfo, 0, len(list)*2)
	for _, vt := range list {
		accounts = append(accounts, &vt.Creator, &vt.Updater)
	}
	_ = s.uma.GetAccountNames(ctx, accounts)
}

func (s *valueTypeService) UpdateValueType(ctx context.Context, tx *sql.Tx, vt *interfaces.ValueType) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UpdateValueType")
	defer span.End()

	if err := s.checkKNPermission(ctx, vt.KNID, interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return err
	}
	if err := prepare(ctx, vt); err != nil {
		return err
	}
	vt.Updater = accountInfo(ctx)
	vt.UpdateTime = time.Now().UnixMilli()
	err := s.withTx(ctx, tx, func(tx *sql.Tx) error {
		if _, err := s.get(ctx, tx, vt.KNID, vt.Branch, vt.VTID); err != nil {
			return err
		}
		existing, err := s.vta.GetValueTypeIdentitiesByIDsOrNames(ctx, vt.KNID, vt.Branch, nil, []string{vt.VTName})
		if err != nil {
			return internalError(ctx, err)
		}
		for _, other := range existing {
			if other.VTID != vt.VTID {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ValueType_ValueTypeNameExisted).
					WithDescription(map[string]any{"name": vt.VTName}).
					WithErrorDetails(detail(ctx, "ValueTypeNameAlreadyExists", map[string]any{"valueTypeName": vt.VTName}))
			}
		}
		return s.update(ctx, tx, vt)
	})
	if err != nil {
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// update writes vt over the stored value type of its ID once the properties that reference it
// still fit it: their Type must stay the value type's base_type.
func (s *valueTypeService) update(ctx context.Context, tx *sql.Tx, vt *interfaces.ValueType) error {
	refs, err := s.references(ctx, vt.KNID, vt.Branch, vt.VTID)
	if err != nil {
		return err
	}
	var misfits []interfaces.ValueTypeReference
	for _, ref := range refs {
		if strings.ToLower(strings.TrimSpace(ref.PropertyType)) != vt.BaseType {
			misfits = append(misfits, ref)
		}
	}
	if len(misfits) > 0 {
		return referenced(ctx,
			fmt.Sprintf("value type %q: base_type %s does not fit the Type of the data properties that use it", vt.VTID, vt.BaseType),
			misfits)
	}
	if err = s.vta.UpdateValueType(ctx, tx, vt); err != nil {
		return internalError(ctx, err)
	}
	return nil
}

func (s *valueTypeService) DeleteValueType(ctx context.Context, tx *sql.Tx, knID string, branch string, vtID string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeleteValueType")
	defer span.End()

	if err := s.checkKNPermission(ctx, knID, interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return err
	}
	err := s.withTx(ctx, tx, func(tx *sql.Tx) error {
		if _, err := s.get(ctx, tx, knID, branch, vtID); err != nil {
			return err
		}
		refs, err := s.references(ctx, knID, branch, vtID)
		if err != nil {
			return err
		}
		if len(refs) > 0 {
			return referenced(ctx, fmt.Sprintf("value type %q is used by data properties", vtID), refs)
		}
		if _, err = s.vta.DeleteValueTypesByIDs(ctx, tx, knID, branch, []string{vtID}); err != nil {
			return internalError(ctx, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// references lists the data properties that name vtID. They are read without the caller's object
// type permissions: the list says what blocks the change, and refusing a change without saying
// why would leave the caller no way forward.
func (s *valueTypeService) references(ctx context.Context, knID, branch, vtID string) ([]interfaces.ValueTypeReference, error) {
	byID, err := s.ota.GetAllObjectTypesByKnID(ctx, knID, branch)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	objectTypes := make([]*interfaces.ObjectType, 0, len(byID))
	for _, ot := range byID {
		objectTypes = append(objectTypes, ot)
	}
	sortObjectTypes(objectTypes)
	return logics.ValueTypeReferences(objectTypes, vtID), nil
}

func referenced(ctx context.Context, reason string, refs []interfaces.ValueTypeReference) error {
	return rest.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_ValueType_Referenced).
		WithErrorDetails(map[string]any{"reason": reason, "references": refs})
}

func (s *valueTypeService) GetAllValueTypesByKnID(ctx context.Context, knID string, branch string) ([]*interfaces.ValueType, error) {
	list, err := s.vta.ListValueTypes(ctx, interfaces.ValueTypesQueryParams{KNID: knID, Branch: branch})
	if err != nil {
		return nil, internalError(ctx, err)
	}
	return list, nil
}

func (s *valueTypeService) DeleteValueTypesByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) error {
	if tx == nil {
		return internalError(ctx, fmt.Errorf("missing transaction"))
	}
	if _, err := s.vta.DeleteValueTypesByKnID(ctx, tx, knID, branch); err != nil {
		return internalError(ctx, err)
	}
	return nil
}

func sortObjectTypes(objectTypes []*interfaces.ObjectType) {
	sort.Slice(objectTypes, func(i, j int) bool { return objectTypes[i].OTID < objectTypes[j].OTID })
}
