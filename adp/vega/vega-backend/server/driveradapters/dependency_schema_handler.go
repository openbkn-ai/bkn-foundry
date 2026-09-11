// Copyright openbkn.ai
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"vega-backend/common/visitor"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

// GetResourceSchemaForDependency returns only the schema needed for strict
// model validation. It checks the exact operation declared by the binding
// source and never exposes the unrestricted internal resource read.
func (r *restHandler) GetResourceSchemaForDependency(c *gin.Context) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	operation := strings.TrimSpace(c.Query("operation"))
	if operation != interfaces.OPERATION_TYPE_VIEW_DETAIL && operation != interfaces.OPERATION_TYPE_QUERY_DATA {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	resourceID := strings.TrimSpace(c.Param("id"))
	resource, err := r.rs.InternalGetByID(ctx, nil, resourceID)
	if err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_Resource_InternalError_GetFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if resource == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	caller := visitor.GenerateVisitor(c)
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{
		ID: caller.ID, Type: string(caller.Type),
	})
	if err := r.rs.CheckResourcePermission(ctx, resourceID, operation); err != nil {
		httpErr := httpErrorOrInternal(ctx, err, verrors.VegaBackend_InternalError_CheckPermissionFailed)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	schema := resource.SchemaDefinition
	if schema == nil {
		schema = []*interfaces.Property{}
	}
	emitResourceReadEvidence(c, ctx, "data.resource.schema", []*interfaces.Resource{resource}, 1,
		map[string]string{"resource_id": resource.ID, "operation": operation})
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]any{
		"id": resource.ID, "name": resource.Name, "schema_definition": schema,
	})
}
