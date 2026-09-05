// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/audit"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	bknsdk "bkn-backend/bkn-specification/bkn"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
)

const (
	bknBindingPolicyPreserve = "preserve"
	bknBindingPolicyDetach   = "detach"
)

// UploadBKN imports an uploaded BKN tar archive (external endpoint).
func (r *restHandler) UploadBKN(c *gin.Context) {
	logger.Debug("Handler UploadBKN Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	// Verify the access token.
	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	// Store account ID in the context.
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	// Set trace attributes for the API.
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	// Read the uploaded file.
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "UploadedFileReadFailed", nil))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	defer func() { _ = file.Close() }()

	// Validate the file type.
	if header.Header.Get("Content-Type") != "application/octet-stream" {
		// Fall back to the filename extension.
		ext := filepath.Ext(header.Filename)
		if ext != ".tar" && ext != ".tgz" && ext != ".tar.gz" {
			httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
				WithErrorDetails(commonValidationDetail(ctx, "ArchiveFileTypeInvalid", nil))
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}

	// Read form parameters.
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	bindingPolicy := strings.TrimSpace(c.DefaultQuery("binding_policy", bknBindingPolicyPreserve))
	if bindingPolicy != bknBindingPolicyPreserve && bindingPolicy != bknBindingPolicyDetach {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "BindingPolicyInvalid", map[string]any{"value": bindingPolicy}))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	logger.Debugf("Upload BKN: branch=%s, binding_policy=%s, filename=%s, size=%d",
		branch, bindingPolicy, header.Filename, header.Size)

	// Load the network directly from the tar archive in memory.
	bknNetwork, err := bknsdk.LoadNetworkFromTar(file)
	if err != nil {
		logger.Errorf("Failed to load network from tar: %s", err.Error())
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "ArchiveLoadFailed", nil))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	bknNetwork.Branch = branch

	// Import the network.
	kn := logics.ToADPNetWork(bknNetwork)
	otMap := make(map[string]*interfaces.ObjectType)
	for _, bknObj := range bknNetwork.ObjectTypes {
		ot := logics.ToADPObjectType(kn.KNID, kn.Branch, bknObj)
		kn.ObjectTypes = append(kn.ObjectTypes, ot)
		otMap[ot.OTID] = ot
	}
	for _, bknRel := range bknNetwork.RelationTypes {
		rt := logics.ToADPRelationType(kn.KNID, kn.Branch, bknRel)
		kn.RelationTypes = append(kn.RelationTypes, rt)
	}
	for _, bknAct := range bknNetwork.ActionTypes {
		act := logics.ToADPActionType(kn.KNID, kn.Branch, bknAct)
		kn.ActionTypes = append(kn.ActionTypes, act)
	}
	for _, bknRisk := range bknNetwork.RiskTypes {
		risk := logics.ToADPRiskType(kn.KNID, kn.Branch, bknRisk)
		kn.RiskTypes = append(kn.RiskTypes, risk)
	}
	for _, bknCG := range bknNetwork.ConceptGroups {
		cg := logics.ToADPConceptGroup(kn.KNID, kn.Branch, bknCG)
		kn.ConceptGroups = append(kn.ConceptGroups, cg)

		for _, otID := range bknCG.ObjectTypes {
			if ot, ok := otMap[otID]; ok {
				ot.ConceptGroups = append(ot.ConceptGroups, cg)
			}
		}
	}
	for _, bknM := range bknNetwork.Metrics {
		if bknM == nil {
			continue
		}
		kn.Metrics = append(kn.Metrics, logics.ToADPMetricDefinition(kn.KNID, branch, bknM))
	}
	if bindingPolicy == bknBindingPolicyDetach {
		detachBKNExternalBindings(kn)
	}

	// Validate required knowledge network creation fields, lengths, and enum values.
	err = ValidateKN(ctx, kn)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("Validate knowledge network[%s] failed: %s. %v", kn.KNName,
			httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), nil)

		// Set trace attributes for the error.
		span.SetAttributes(attr.Key("kn_name").String(kn.KNName))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Validate each populated object type, relation type, action type, and concept group in the knowledge network.
	if len(kn.ObjectTypes) > 0 {
		err = ValidateObjectTypes(ctx, kn.KNID, kn.ObjectTypes, false)
		if err != nil {
			httpErr := err.(*rest.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	if len(kn.RelationTypes) > 0 {
		err = ValidateRelationTypes(ctx, kn.KNID, kn.RelationTypes, false)
		if err != nil {
			httpErr := err.(*rest.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	if len(kn.ActionTypes) > 0 {
		err = ValidateActionTypes(ctx, kn.KNID, kn.ActionTypes, false)
		if err != nil {
			httpErr := err.(*rest.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	if len(kn.ConceptGroups) > 0 {
		for _, conceptGroup := range kn.ConceptGroups {
			err = ValidateConceptGroup(ctx, conceptGroup)
			if err != nil {
				httpErr := err.(*rest.HTTPError)
				oteltrace.AddHttpAttrs4HttpError(span, httpErr)
				rest.ReplyError(c, httpErr)
				return
			}
		}
	}
	if len(kn.RiskTypes) > 0 {
		err = ValidateRiskTypes(ctx, kn.KNID, kn.RiskTypes)
		if err != nil {
			httpErr := err.(*rest.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	if len(kn.Metrics) > 0 {
		err = ValidateMetricRequests(ctx, kn.Metrics, false)
		if err != nil {
			httpErr := err.(*rest.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}

	// Create the knowledge network.
	knID, err := r.kns.CreateKN(ctx, kn, interfaces.ImportMode_Overwrite, false)
	if err != nil {
		httpErr := err.(*rest.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Record an audit log after successful creation.
	audit.NewInfoLog(audit.OPERATION, audit.CREATE, audit.TransforOperator(visitor),
		interfaces.GenerateKNAuditObject(knID, kn.KNName), "")

	logger.Debugf("Upload BKN completed: kn_id=%s", knID)
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]string{"kn_id": knID})
}

// detachBKNExternalBindings keeps the portable model topology while removing
// environment-local resource, toolbox, and MCP identifiers. The existing
// incremental APIs can bind targets later and publish proxy grants atomically.
func detachBKNExternalBindings(kn *interfaces.KN) {
	if kn == nil {
		return
	}
	detachObjectTypes := func(objectTypes []*interfaces.ObjectType) {
		for _, objectType := range objectTypes {
			if objectType == nil {
				continue
			}
			objectType.DataSource = nil
			for _, property := range objectType.LogicProperties {
				if property != nil {
					property.DataSource = nil
				}
			}
		}
	}
	detachActionTypes := func(actionTypes []*interfaces.ActionType) {
		for _, actionType := range actionTypes {
			if actionType != nil {
				actionType.ActionSource = interfaces.ActionSource{}
			}
		}
	}
	detachRelationTypes := func(relationTypes []*interfaces.RelationType) {
		for _, relationType := range relationTypes {
			if relationType == nil {
				continue
			}
			switch mapping := relationType.MappingRules.(type) {
			case *interfaces.InDirectMapping:
				if mapping != nil {
					mapping.BackingDataSource = nil
				}
			case interfaces.InDirectMapping:
				mapping.BackingDataSource = nil
				relationType.MappingRules = mapping
			case map[string]any:
				if _, exists := mapping["backing_data_source"]; exists {
					mapping["backing_data_source"] = nil
				}
			}
		}
	}
	detachObjectTypes(kn.ObjectTypes)
	detachRelationTypes(kn.RelationTypes)
	detachActionTypes(kn.ActionTypes)
	for _, conceptGroup := range kn.ConceptGroups {
		if conceptGroup == nil {
			continue
		}
		detachObjectTypes(conceptGroup.ObjectTypes)
		detachRelationTypes(conceptGroup.RelationTypes)
		detachActionTypes(conceptGroup.ActionTypes)
	}
}

// DownloadBKN exports a BKN tar archive (external endpoint).
func (r *restHandler) DownloadBKN(c *gin.Context) {
	logger.Debug("Handler DownloadBKN Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	// Verify the access token.
	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	// Store account ID in the context.
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	// Read path parameters.
	kn_id := c.Param("kn_id")
	if kn_id == "" {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(commonValidationDetail(ctx, "KnowledgeNetworkIDRequired", nil))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Read query parameters.
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)

	logger.Debugf("Download BKN: kn_id=%s, branch=%s", kn_id, branch)

	// Export a tar archive through the service.
	tarData, err := r.bs.ExportToTar(ctx, kn_id, branch)
	if err != nil {
		logger.Errorf("Download BKN failed: %s", err.Error())
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_KnowledgeNetwork_InternalError).
			WithErrorDetails(commonValidationDetail(ctx, "InternalRequestFailed", nil))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	filename := kn_id + "-" + branch + ".tar"

	logger.Debugf("Download BKN completed: filename=%s size=%d", filename, len(tarData))

	// Set response headers.
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	c.Data(http.StatusOK, "application/octet-stream", tarData)
}
