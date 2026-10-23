// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	bknsdk "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/capability_binding"
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

	// Archive uploads predate these query parameters and historically behaved as overwrite +
	// non-strict. Keep those omission defaults for existing callers while honoring explicit values.
	mode, strictMode, httpErr := parseKnowledgeNetworkImportOptions(ctx, c,
		interfaces.ImportMode_Overwrite, false)
	if httpErr != nil {
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Read the uploaded file.
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.UploadedFileReadFailed", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		httpErr := httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(localizedText)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	defer func() { _ = file.Close() }()

	// Validate the file type.
	if header.Header.Get("Content-Type") != "application/octet-stream" {
		// Fall back to the filename extension. The suffix is matched on the whole name:
		// filepath.Ext of "net.tar.gz" is ".gz", which never matched ".tar.gz".
		if !hasArchiveSuffix(header.Filename) {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ArchiveFileTypeInvalid", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			httpErr := httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
				WithErrorDetails(localizedText)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}

	// Read form parameters.
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	bindingPolicy := strings.TrimSpace(c.DefaultQuery("binding_policy", bknBindingPolicyPreserve))
	if bindingPolicy != bknBindingPolicyPreserve && bindingPolicy != bknBindingPolicyDetach {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.BindingPolicyInvalid", map[string]any{"value": bindingPolicy})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		httpErr = httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(localizedText)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	logger.Debugf("Upload BKN: branch=%s, import_mode=%s, strict_mode=%t, binding_policy=%s, filename=%s, size=%d",
		branch, mode, strictMode, bindingPolicy, header.Filename, header.Size)

	// Load the network directly from the tar archive in memory.
	// The archive format is read from its first bytes: plain tar as always, tar.gz, or zip.
	bknNetwork, err := bknsdk.LoadNetworkFromArchive(file)
	if err != nil {
		logger.Errorf("Failed to load network from tar: %s", err.Error())
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ArchiveLoadFailed", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		httpErr := httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(localizedText)
		if strings.Contains(err.Error(), "legacy metrics") {
			httpErr = httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
				WithErrorDetails("legacy metrics/*.bkn is unsupported; migrate metrics to object metrics V1 before import")
		}
		// A package refused by its declared bkn_spec says which definition to fix; the generic
		// load failure would leave the author only the server log to find out.
		var specErr *bknsdk.SpecError
		if errors.As(err, &specErr) {
			httpErr = httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
				WithErrorDetails(err.Error())
		}
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	if detail := unstoredDeclaration(bknNetwork); detail != "" {
		httpErr := httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(detail)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	bknNetwork.Branch = branch

	// Import the network.
	kn := logics.ToADPNetWork(bknNetwork)
	kn.ObjectMetrics = make([]*interfaces.ObjectMetricRecordV1, 0, len(bknNetwork.ObjectMetrics))
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
	for _, rawMetric := range bknNetwork.ObjectMetrics {
		var metric interfaces.ObjectMetricRecordV1
		if err := json.Unmarshal(rawMetric, &metric); err != nil {
			httpErr := httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
				WithErrorDetails(err.Error())
			rest.ReplyError(c, httpErr)
			return
		}
		metric.KNID, metric.Branch = kn.KNID, branch
		kn.ObjectMetrics = append(kn.ObjectMetrics, &metric)
	}
	// Validate required knowledge network creation fields, lengths, and enum values.
	err = ValidateKN(ctx, kn)
	if err != nil {
		httpErr := err.(*httperrors.HTTPError)

		// Record the error log.
		otellog.LogError(ctx, fmt.Sprintf("Validate knowledge network[%s] failed: %s. %v", kn.KNName,
			httpErr.ErrorCode, httpErr.ErrorDetails), nil)

		// Set trace attributes for the error.
		span.SetAttributes(attr.Key("kn_name").String(kn.KNName))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Validate each populated object type, relation type, action type, and concept group in the knowledge network.
	if len(kn.ObjectTypes) > 0 {
		err = ValidateObjectTypes(ctx, kn.KNID, kn.ObjectTypes, strictMode)
		if err != nil {
			httpErr := err.(*httperrors.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	if len(kn.RelationTypes) > 0 {
		err = ValidateRelationTypes(ctx, kn.KNID, kn.RelationTypes, strictMode)
		if err != nil {
			httpErr := err.(*httperrors.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	if len(kn.ActionTypes) > 0 {
		err = ValidateActionTypes(ctx, kn.KNID, kn.ActionTypes, strictMode)
		if err != nil {
			httpErr := err.(*httperrors.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	if len(kn.ConceptGroups) > 0 {
		for _, conceptGroup := range kn.ConceptGroups {
			err = ValidateConceptGroup(ctx, conceptGroup)
			if err != nil {
				httpErr := err.(*httperrors.HTTPError)
				oteltrace.AddHttpAttrs4HttpError(span, httpErr)
				rest.ReplyError(c, httpErr)
				return
			}
		}
	}
	if len(kn.RiskTypes) > 0 {
		err = ValidateRiskTypes(ctx, kn.KNID, kn.RiskTypes)
		if err != nil {
			httpErr := err.(*httperrors.HTTPError)
			oteltrace.AddHttpAttrs4HttpError(span, httpErr)
			rest.ReplyError(c, httpErr)
			return
		}
	}
	if bindingPolicy == bknBindingPolicyDetach {
		detachBKNExternalBindings(kn)
	}
	// Detached imports have already been structurally validated above. Persist them without
	// resolving the environment-local dependencies that were removed by the detach policy.
	persistenceStrictMode := strictMode && bindingPolicy != bknBindingPolicyDetach

	// Create the knowledge network.
	knID, err := r.kns.CreateKN(ctx, kn, mode, persistenceStrictMode)
	if err != nil {
		httpErr := err.(*httperrors.HTTPError)

		// Set trace attributes for the error.
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Capability declarations are resolved after the model is in place, and never fail the
	// import: a knowledge network whose capabilities are missing is still a knowledge network.
	// What could not be resolved travels back in the response — a skip that is only logged makes
	// an import look complete while the SKILLs list is quietly empty.
	//
	// detach asks for the topology without this environment's local identifiers, and a Skill or
	// tool id is exactly that. Mounting them anyway would leave the one binding kind the caller
	// asked to drop.
	declaredCapabilities := bknNetwork.Capabilities
	if bindingPolicy == bknBindingPolicyDetach {
		declaredCapabilities = nil
	}
	var capabilityReport *interfaces.CapabilityImportReport
	capResult, capErr := r.publishKNCapabilityMutation(ctx, knID, branch, nil,
		func(mutationCtx context.Context, tx *sql.Tx) (*interfaces.KNCapabilityMutationResult, error) {
			var bindings []*interfaces.CapabilityBinding
			var importErr error
			capabilityReport, bindings, importErr = r.cbs.ImportCapabilitiesTx(
				mutationCtx, tx, knID, branch, declaredCapabilities)
			return &interfaces.KNCapabilityMutationResult{Bindings: bindings}, importErr
		})
	if capErr != nil {
		logger.Errorf("Upload BKN: capability import failed: kn_id=%s, err=%v", knID, capErr)
		capabilityReport = &interfaces.CapabilityImportReport{
			Skipped: []*interfaces.CapabilitySkip{{
				Reason: capability_binding.CapabilitySkipUnreachable,
				Detail: capErr.Error(),
			}},
		}
	}

	if r.auditRecorder != nil {
		name := kn.KNName
		if kn.ImportSkipped {
			// Ignore may still add capability bindings, but the archive's network name was not persisted.
			name = ""
			changed := false
			if capErr == nil && capResult != nil {
				for _, binding := range capResult.Bindings {
					if binding != nil && binding.CreatedByAttach {
						changed = true
						break
					}
				}
			}
			if changed {
				if storedName, exists, lookupErr := r.kns.CheckKNExistByID(ctx, knID, branch); lookupErr == nil && exists {
					name = storedName
				}
			}
		}
		c.Set("operation_audit_skip", name == "")
		if name != "" {
			c.Set("operation_audit_target_names", []string{name})
		}
	}

	logger.Debugf("Upload BKN completed: kn_id=%s", knID)
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, map[string]any{"kn_id": knID, "capabilities": capabilityReport})
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
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.KnowledgeNetworkIDRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		httpErr := httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(localizedText)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Read query parameters.
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	spec := strings.TrimSpace(c.Query("bkn_spec"))
	switch spec {
	case interfaces.BKN_EXPORT_SPEC_AUTO, interfaces.BKN_EXPORT_SPEC_2, interfaces.BKN_EXPORT_SPEC_3:
	default:
		httpErr := httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter_BknSpec).
			WithErrorDetails(fmt.Sprintf("bkn_spec must be %s or %s, or left out, got %q",
				interfaces.BKN_EXPORT_SPEC_2, interfaces.BKN_EXPORT_SPEC_3, spec))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	logger.Debugf("Download BKN: kn_id=%s, branch=%s, bkn_spec=%s", kn_id, branch, spec)

	// Export a tar archive through the service.
	tarData, err := r.bs.ExportToTar(ctx, kn_id, branch, spec)
	if err != nil {
		logger.Errorf("Download BKN failed: %s", err.Error())
		// An error the service already classified travels as it is. Flattening every failure into
		// "internal error" hides the one the caller can act on: an export refused because the
		// execution factory is unreachable is worth retrying, and a generic 500 does not say so.
		var httpErr *httperrors.HTTPError
		if !errors.As(err, &httpErr) {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.InternalRequestFailed", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			httpErr = httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_KnowledgeNetwork_InternalError).
				WithErrorDetails(localizedText)
		}
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

// CheckBKN runs the network check on a knowledge network branch (external endpoint). It is read-only
// and works on any network, 2.x included.
func (r *restHandler) CheckBKN(c *gin.Context) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	})
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	knID := c.Param("kn_id")
	if knID == "" {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.KnowledgeNetworkIDRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		httpErr := httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(localizedText)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)

	result, err := r.bs.CheckNetwork(ctx, knID, branch)
	if err != nil {
		var httpErr *httperrors.HTTPError
		if !errors.As(err, &httpErr) {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.InternalRequestFailed", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			httpErr = httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_KnowledgeNetwork_InternalError).
				WithErrorDetails(localizedText)
		}
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

// archiveSuffixes are the file names an archive upload may carry when it is not sent as
// application/octet-stream. The loader reads the format from the content, not from the name.
var archiveSuffixes = []string{".tar", ".tgz", ".tar.gz", ".zip"}

func hasArchiveSuffix(filename string) bool {
	name := strings.ToLower(filename)
	for _, suffix := range archiveSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// unstoredDeclaration names the first 3.0 declaration in a package that the backend does not store
// yet: a value type, an alias, a lifecycle or a relation declaration. The specification library
// reads them, but the backend stores value types and aliases only from #2281, and relation
// declarations and lifecycles only from #2284, so importing would drop them; the upload refuses the
// package instead. Property declarations (Semantic, Required, ### Property Constraints) are stored.
// It returns "" when there is none, which is always the case for a 2.x package.
func unstoredDeclaration(net *bknsdk.BknNetwork) string {
	if net.BknSpec != bknsdk.BknSpecV3 {
		return ""
	}
	const unsupportedYet = "is not supported by this version yet"
	if len(net.ValueTypes) > 0 {
		return fmt.Sprintf("value type %q: value types are not supported by this version yet", net.ValueTypes[0].ID)
	}
	for _, ot := range net.ObjectTypes {
		if len(ot.Aliases) > 0 {
			return fmt.Sprintf("object type %q: aliases %s", ot.ID, unsupportedYet)
		}
		if ot.Lifecycle != nil {
			return fmt.Sprintf("object type %q: lifecycle %s", ot.ID, unsupportedYet)
		}
		for _, p := range ot.DataProperties {
			if len(p.Aliases) > 0 {
				return fmt.Sprintf("object type %q data property %q: Aliases %s", ot.ID, p.Name, unsupportedYet)
			}
		}
	}
	for _, at := range net.ActionTypes {
		if len(at.Aliases) > 0 {
			return fmt.Sprintf("action type %q: aliases %s", at.ID, unsupportedYet)
		}
		if at.Lifecycle != nil {
			return fmt.Sprintf("action type %q: lifecycle %s", at.ID, unsupportedYet)
		}
	}
	for _, rt := range net.RelationTypes {
		var what string
		switch {
		case len(rt.Aliases) > 0:
			what = "aliases"
		case rt.ReverseName != "":
			what = "reverse_name"
		case rt.Importance != "":
			what = "importance"
		case rt.FallbackFor != "":
			what = "fallback_for"
		case rt.Verification != "":
			what = "verification"
		case rt.Lifecycle != nil:
			what = "lifecycle"
		case rt.Endpoint.Cardinality != "":
			what = "Cardinality"
		case len(rt.RequiredEnds) > 0 || len(rt.Characteristics) > 0:
			what = "### Relation Constraints"
		case len(rt.DataProperties) > 0:
			what = "### Data Properties"
		default:
			continue
		}
		return fmt.Sprintf("relation type %q: %s %s", rt.ID, what, unsupportedYet)
	}
	return ""
}
