// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package connectortype provides ConnectorType management business logic.
package connector_type

import (
	"context"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/factory"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/permission"
)

var (
	ctServiceOnce sync.Once
	ctService     interfaces.ConnectorTypeService
)

type connectorTypeService struct {
	appSetting *common.AppSetting
	cta        interfaces.ConnectorTypeAccess
	cf         interfaces.ConnectorFactory
	ps         interfaces.PermissionService
}

// NewConnectorTypeService creates a new ConnectorTypeService.
func NewConnectorTypeService(appSetting *common.AppSetting) interfaces.ConnectorTypeService {
	ctServiceOnce.Do(func() {
		ctService = &connectorTypeService{
			appSetting: appSetting,
			cta:        logics.CTA,
			cf:         factory.NewConnectorFactory(appSetting),
			ps:         permission.NewPermissionService(appSetting),
		}
	})
	return ctService
}

// Register register a new ConnectorType.
func (cts *connectorTypeService) Register(ctx context.Context, req *interfaces.ConnectorTypeReq) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Register connector type")
	defer span.End()

	// Determine whether the userid has the permission to create a business knowledge network (policy decision)
	err := cts.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE,
		ID:   interfaces.RESOURCE_ID_ALL,
	}, []string{interfaces.OPERATION_TYPE_CREATE})
	if err != nil {
		return err
	}

	ct := &interfaces.ConnectorType{
		Type:        req.Type,
		Name:        req.Name,
		Description: req.Description,
		Mode:        req.Mode,
		Category:    req.Category,
		Endpoint:    req.Endpoint,
		Enabled:     req.Enabled,
	}
	err = cts.cf.ValidateConnectorTypeRegistration(ct)
	if err != nil {
		otellog.LogError(ctx, "Validate connector type registration failed", err)
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_ConnectorType_BadRequest).
			WithErrorDetails(err.Error())
	}

	err = cts.cta.Create(ctx, ct)
	if err != nil {
		otellog.LogError(ctx, "Register connector type failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_ConnectorType_InternalError_RegisterFailed).
			WithErrorDetails(err.Error())
	}

	err = cts.cf.RegisterConnector(ctx, ct.Type, ct)
	if err != nil {
		otellog.LogError(ctx, "Register connector type failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_ConnectorType_InternalError_RegisterFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// GetByType retrieves a ConnectorType by Type.
func (cts *connectorTypeService) GetByType(ctx context.Context, tp string) (*interfaces.ConnectorType, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get connector type")
	defer span.End()

	ct, err := cts.cta.GetByType(ctx, tp)
	if err != nil {
		span.SetStatus(codes.Error, "Get connector type failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_ConnectorType_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if ct == nil {
		span.SetStatus(codes.Error, "Connector type not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_ConnectorType_NotFound)
	}

	ct.Operations = []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}
	availability := cts.cf.GetConnectorAvailability(ct.Type)
	ct.Available = availability.Available
	ct.RequiredEdition = availability.RequiredEdition
	if !ct.Available {
		span.SetStatus(codes.Ok, "")
		return ct, nil
	}
	ct.FieldConfig, err = cts.cf.GetConnectorFieldConfig(ctx, ct)
	if err != nil {
		span.SetStatus(codes.Error, "Get connector field config failed")
		return nil, rest.NewHTTPError(ctx, http.StatusServiceUnavailable, verrors.VegaBackend_ConnectorType_FieldConfigUnavailable).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return ct, nil
}

// List lists ConnectorTypes with filters.
func (cts *connectorTypeService) List(ctx context.Context, params interfaces.ConnectorTypesQueryParams) ([]*interfaces.ConnectorType, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "List connector types")
	defer span.End()

	connectorTypesArr, _, err := cts.cta.List(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List connector types failed")
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_ConnectorType_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	connectorTypes := make([]*interfaces.ConnectorType, 0)
	for _, c := range connectorTypesArr {
		availability := cts.cf.GetConnectorAvailability(c.Type)
		c.Available = availability.Available
		c.RequiredEdition = availability.RequiredEdition
		c.FieldConfig = nil
		c.Operations = []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}
		if params.Available != nil && c.Available != *params.Available {
			continue
		}
		connectorTypes = append(connectorTypes, c)
	}
	total := int64(len(connectorTypes))

	// If limit = -1, all will be returned
	if params.Limit != -1 {
		// Pagination
		// Check whether the starting position is out of bounds
		if params.Offset < 0 || params.Offset >= len(connectorTypes) {
			span.SetStatus(codes.Ok, "")
			return []*interfaces.ConnectorType{}, total, nil
		}
		// Calculate the end position
		end := params.Offset + params.Limit
		if end > len(connectorTypes) {
			end = len(connectorTypes)
		}

		connectorTypes = connectorTypes[params.Offset:end]
	}

	span.SetStatus(codes.Ok, "")
	return connectorTypes, total, nil
}

// ListAuthResourceEntries lists connector type authorization entries with filters.
func (cts *connectorTypeService) ListAuthResourceEntries(ctx context.Context, params interfaces.AuthResourceQueryParams) ([]*interfaces.AuthResourceEntry, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListAuthResourceEntries")
	defer span.End()

	entries, total, err := cts.cta.ListAuthResourceEntries(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "ListAuthResourceEntries failed")
		return []*interfaces.AuthResourceEntry{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_ConnectorType_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if len(entries) == 0 {
		return []*interfaces.AuthResourceEntry{}, total, nil
	}

	span.SetStatus(codes.Ok, "")
	return entries, total, nil
}

// Update updates a ConnectorType.
func (cts *connectorTypeService) Update(ctx context.Context, ct *interfaces.ConnectorType, req *interfaces.ConnectorTypeReq) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update connector type")
	defer span.End()

	if ct == nil {
		span.SetStatus(codes.Error, "Connector type not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_ConnectorType_NotFound)
	}
	nameModified := req.Name != ct.Name

	// Determine whether the userid has the permission to create a business knowledge network (policy decision)
	err := cts.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE,
		ID:   ct.Type,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}

	// Validate immutable fields before resolving the mutable definition.
	if req.Type != ct.Type {
		span.SetStatus(codes.Error, "can not change connector type")
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_ConnectorType_InvalidParameter_Type)
	}
	if req.Mode != ct.Mode {
		span.SetStatus(codes.Error, "can not change connector mode")
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_ConnectorType_InvalidParameter_Mode)
	}
	if req.Category != ct.Category {
		span.SetStatus(codes.Error, "can not change connector category")
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_ConnectorType_InvalidParameter_Category)
	}

	updated := &interfaces.ConnectorType{
		Type:        req.Type,
		Name:        req.Name,
		Tags:        req.Tags,
		Description: req.Description,
		Mode:        req.Mode,
		Category:    req.Category,
		Endpoint:    req.Endpoint,
		Enabled:     req.Enabled,
	}

	err = cts.cf.ValidateConnectorTypeRegistration(updated)
	if err != nil {
		otellog.LogError(ctx, "Validate connector type update failed", err)
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_ConnectorType_BadRequest).
			WithErrorDetails(err.Error())
	}

	if err := cts.cta.Update(ctx, updated); err != nil {
		span.SetStatus(codes.Error, "Update connector type failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_ConnectorType_InternalError_UpdateFailed).
			WithErrorDetails(err.Error())
	}

	if err := cts.cf.RegisterConnector(ctx, updated.Type, updated); err != nil {
		otellog.LogError(ctx, "Register connector type failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_ConnectorType_InternalError_RegisterFailed).
			WithErrorDetails(err.Error())
	}

	// Request the interface to update the resource name, update the resource name
	if nameModified {
		err = cts.ps.UpdateResource(ctx, interfaces.PermissionResource{
			ID:   updated.Type,
			Type: interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE,
			Name: updated.Name,
		})
		if err != nil {
			return err
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// Delete deletes a ConnectorType.
func (cts *connectorTypeService) DeleteByType(ctx context.Context, tp string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete connector type")
	defer span.End()

	// Determine whether the userid has the permission to be deleted
	err := cts.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE,
		ID:   tp,
	}, []string{interfaces.OPERATION_TYPE_DELETE})
	if err != nil {
		return err
	}

	if err := cts.cta.DeleteByType(ctx, tp); err != nil {
		span.SetStatus(codes.Error, "Delete connector type failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_ConnectorType_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	cts.cf.DeleteConnector(tp)

	//  Clearing resource strategy
	err = cts.ps.DeleteResources(ctx, interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE, []string{tp})
	if err != nil {
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// SetEnabled sets the enabled status of a ConnectorType.
func (cts *connectorTypeService) SetEnabled(ctx context.Context, tp string, enabled bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Set enabled connector type")
	defer span.End()

	// Enabled is part of the connector definition, so changing it uses modify.
	// The permission catalog also requires view_detail for this operation.
	err := cts.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE,
		ID:   tp,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}

	if err := cts.cta.SetEnabled(ctx, tp, enabled); err != nil {
		span.SetStatus(codes.Error, "Set enabled connector type failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_ConnectorType_InternalError_UpdateFailed).
			WithErrorDetails(err.Error())
	}
	cts.cf.SetConnectorEnabled(tp, enabled)

	span.SetStatus(codes.Ok, "")
	return nil
}

// CheckExistByType checks if a ConnectorType exists by Type.
func (cts *connectorTypeService) CheckExistByType(ctx context.Context, tp string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Check connector type exist by Type")
	defer span.End()

	ct, err := cts.cta.GetByType(ctx, tp)
	if err != nil {
		span.SetStatus(codes.Error, "GetByType failed")
		return false, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_ConnectorType_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return ct != nil, nil
}

// CheckExistByName checks if a ConnectorType exists by Name.
func (cts *connectorTypeService) CheckExistByName(ctx context.Context, name string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Check connector type exist by Name")
	defer span.End()

	ct, err := cts.cta.GetByName(ctx, name)
	if err != nil {
		span.SetStatus(codes.Error, "GetByName failed")
		return false, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_ConnectorType_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return ct != nil, nil
}
