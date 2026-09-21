// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permission

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	mqclient "github.com/openbkn-ai/bkn-foundry/comm-go/mq"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
)

const maxPropertyLevelsPerRequest = 200

const (
	maxRowFilterObjectTypesPerRequest = 100
	maxRowFilterValuesPerPredicate    = 100
)

func localizedPermissionDetail(ctx context.Context, key string) string {
	return i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail."+key, nil)
}

type PermissionServiceImpl struct {
	appSetting *common.AppSetting
	mqClient   mqclient.OpenBKNMQClient
	pa         interfaces.PermissionAccess
}

func NewPermissionServiceImpl(appSetting *common.AppSetting) interfaces.PermissionService {
	mqSetting := appSetting.MQSetting
	client, err := mqclient.NewOpenBKNMQClient(mqSetting.MQHost, mqSetting.MQPort,
		mqSetting.MQHost, mqSetting.MQPort, mqSetting.MQType,
		mqclient.UserInfo(mqSetting.Auth.Username, mqSetting.Auth.Password),
		mqclient.AuthMechanism(mqSetting.Auth.Mechanism),
	)
	if err != nil {
		logger.Fatal("failed to create a openbkn mq client:", err)
	}
	return &PermissionServiceImpl{
		appSetting: appSetting,
		mqClient:   client,
		pa:         logics.PA,
	}
}

func (ps *PermissionServiceImpl) CheckPermission(ctx context.Context, resource interfaces.PermissionResource, ops []string) error {
	requirements := make([]interfaces.PermissionRequirement, 0, len(ops))
	for _, operation := range ops {
		requirements = append(requirements, interfaces.PermissionRequirement{Resource: resource, Operation: operation})
	}
	return ps.RequirePermissions(ctx, requirements)
}

func (ps *PermissionServiceImpl) RequirePermissions(ctx context.Context,
	requirements []interfaces.PermissionRequirement) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CheckPermission")
	defer span.End()

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	if accountInfo.ID == "" || accountInfo.Type == "" {
		httpErr := rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(localizedPermissionDetail(ctx, "AccountInfoMissing"))
		otellog.LogError(ctx, "CheckPermission missing account ID or type", httpErr)
		return httpErr
	}
	if len(requirements) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}

	response, err := ps.pa.CheckPermissions(ctx, interfaces.PermissionChecksRequest{
		AccessorID: accountInfo.ID,
		Checks:     requirements,
	})
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_CheckPermissionFailed).WithErrorDetails(err)
		otellog.LogError(ctx, "CheckPermission failed", httpErr)
		return httpErr
	}
	if !response.Allowed {
		httpErr := rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(localizedPermissionDetail(ctx, "PermissionDenied"))
		otellog.LogError(ctx, "CheckPermission denied", httpErr)
		return httpErr
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// RequireFullPropertyAccess verifies author-time access to every field captured
// by a metric definition. Runtime metric consumers use the persisted metric's
// trusted proxy binding instead of inheriting the author's object-type access.
func (ps *PermissionServiceImpl) RequireFullPropertyAccess(ctx context.Context,
	objectTypeRef string, properties []string) error {
	properties = uniqueSortedProperties(properties)
	if len(properties) == 0 {
		return nil
	}
	full, err := ps.FilterFullPropertyAccess(ctx, objectTypeRef, properties)
	if err != nil {
		return err
	}
	if len(full) != len(properties) {
		return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(localizedPermissionDetail(ctx, "PermissionDenied"))
	}
	return nil
}

// FilterFullPropertyAccess returns only properties whose effective decision is
// full. It is used by authoring UIs as a convenience; mutation services must
// still call RequireFullPropertyAccess as their final security boundary.
func (ps *PermissionServiceImpl) FilterFullPropertyAccess(ctx context.Context,
	objectTypeRef string, properties []string) ([]string, error) {
	return ps.filterPropertyAccess(ctx, objectTypeRef, properties, filterFullPropertyLevelResponse)
}

// FilterVisiblePropertyAccess returns properties that may be exposed in metadata.
// schema, masked, and full properties remain visible; none properties are omitted.
func (ps *PermissionServiceImpl) FilterVisiblePropertyAccess(ctx context.Context,
	objectTypeRef string, properties []string) ([]string, error) {
	return ps.filterPropertyAccess(ctx, objectTypeRef, properties, filterVisiblePropertyLevelResponse)
}

// ResolvePropertyAccessLevels returns the caller's effective level -- full,
// masked, schema or none -- for each named property of one object type. It
// shares the request loop of the filters above, so it is one bkn-safe decision
// per object type, split only where bkn-safe caps one request, and each answer
// is validated exactly as theirs is before any level is kept.
func (ps *PermissionServiceImpl) ResolvePropertyAccessLevels(ctx context.Context,
	objectTypeRef string, properties []string) (map[string]string, error) {
	levels := make(map[string]string, len(properties))
	keepLevels := func(objectTypeRef string, requested []string,
		entries []interfaces.PropertyLevelsDecisionEntry) ([]string, error) {
		batch, err := filterPropertyLevelResponse(objectTypeRef, requested, entries, func(string) bool { return true })
		if err != nil {
			return nil, err
		}
		// The response was just checked to hold exactly the requested
		// properties, each once and at a known level.
		for _, decision := range entries[0].Properties {
			levels[decision.Name] = decision.Level
		}
		return batch, nil
	}
	if _, err := ps.filterPropertyAccess(ctx, objectTypeRef, properties, keepLevels); err != nil {
		return nil, err
	}
	return levels, nil
}

// ResolveRowFilters obtains one complete, ordered row-filter decision from
// bkn-safe for the object types read by a request. A row filter is evaluated
// as the authenticated directory user, never as a proxy or client-credentials
// application account; accepting the latter would make an unbound application
// silently read with no user policy.
func (ps *PermissionServiceImpl) ResolveRowFilters(ctx context.Context,
	objectTypeRefs []string) ([]interfaces.RowFilterDecisionEntry, error) {
	account, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if !ok || account.ID == "" || !rowFilterDirectoryUser(account.Type) {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(localizedPermissionDetail(ctx, "PermissionDenied"))
	}
	refs, err := normalizeRowFilterObjectTypeRefs(objectTypeRefs)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(localizedPermissionDetail(ctx, "PermissionDenied"))
	}
	response, err := ps.pa.ResolveRowFilters(ctx, interfaces.RowFiltersRequest{
		AccessorID:     account.ID,
		ObjectTypeRefs: refs,
	})
	if err != nil {
		return nil, ps.propertyAccessInternalError(ctx, err)
	}
	if err := validateRowFilterResponse(refs, response.Entries); err != nil {
		return nil, ps.propertyAccessInternalError(ctx, err)
	}
	return response.Entries, nil
}

func rowFilterDirectoryUser(accountType string) bool {
	return accountType == interfaces.ACCESSOR_TYPE_USER || accountType == "realname"
}

func normalizeRowFilterObjectTypeRefs(refs []string) ([]string, error) {
	if len(refs) == 0 || len(refs) > maxRowFilterObjectTypesPerRequest {
		return nil, fmt.Errorf("row-filter object type references are required")
	}
	result := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || strings.Count(ref, "/") != 1 {
			return nil, fmt.Errorf("row-filter object type reference is invalid")
		}
		if _, exists := seen[ref]; exists {
			return nil, fmt.Errorf("duplicate row-filter object type reference")
		}
		seen[ref] = struct{}{}
		result = append(result, ref)
	}
	return result, nil
}

func validateRowFilterResponse(request []string, entries []interfaces.RowFilterDecisionEntry) error {
	if len(request) != len(entries) {
		return fmt.Errorf("row-filter response entry count mismatch")
	}
	for index, ref := range request {
		entry := entries[index]
		if entry.ObjectTypeRef != ref || strings.TrimSpace(entry.EffectiveRowFilterDigest) == "" {
			return fmt.Errorf("row-filter response shape mismatch")
		}
		if err := validateRowFilterPredicate(entry.Predicate, 0); err != nil {
			return fmt.Errorf("row-filter response predicate is invalid: %w", err)
		}
	}
	return nil
}

func validateRowFilterPredicate(predicate interfaces.RowFilterPredicate, depth int) error {
	if depth > 8 {
		return fmt.Errorf("row-filter predicate exceeds maximum depth")
	}
	switch predicate.Kind {
	case "true", "false":
		if predicate.Property != "" || len(predicate.Values) != 0 || len(predicate.Predicates) != 0 {
			return fmt.Errorf("row-filter constant predicate has fields")
		}
	case "in":
		if strings.TrimSpace(predicate.Property) == "" || len(predicate.Values) == 0 ||
			len(predicate.Values) > maxRowFilterValuesPerPredicate || len(predicate.Predicates) != 0 {
			return fmt.Errorf("row-filter in predicate is malformed")
		}
		for _, value := range predicate.Values {
			switch value.Type {
			case "string":
				if value.String == nil || value.Integer != nil || value.Boolean != nil {
					return fmt.Errorf("row-filter string value is malformed")
				}
			case "integer":
				if value.String != nil || value.Integer == nil || value.Boolean != nil {
					return fmt.Errorf("row-filter integer value is malformed")
				}
			case "boolean":
				if value.String != nil || value.Integer != nil || value.Boolean == nil {
					return fmt.Errorf("row-filter boolean value is malformed")
				}
			default:
				return fmt.Errorf("row-filter value type is unsupported")
			}
		}
	case "or":
		if predicate.Property != "" || len(predicate.Values) != 0 || len(predicate.Predicates) < 2 {
			return fmt.Errorf("row-filter or predicate is malformed")
		}
		for _, child := range predicate.Predicates {
			if err := validateRowFilterPredicate(child, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("row-filter predicate kind is unsupported")
	}
	return nil
}

func (ps *PermissionServiceImpl) filterPropertyAccess(ctx context.Context, objectTypeRef string,
	properties []string, filter func(string, []string, []interfaces.PropertyLevelsDecisionEntry) ([]string, error)) ([]string, error) {
	properties = uniqueSortedProperties(properties)
	if len(properties) == 0 {
		return []string{}, nil
	}
	accountInfo, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if !ok || accountInfo.ID == "" || accountInfo.Type == "" {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(localizedPermissionDetail(ctx, "AccountInfoMissing"))
	}
	full := make([]string, 0, len(properties))
	for start := 0; start < len(properties); start += maxPropertyLevelsPerRequest {
		end := min(start+maxPropertyLevelsPerRequest, len(properties))
		requested := properties[start:end]
		response, err := ps.pa.ResolvePropertyLevels(ctx, interfaces.PropertyLevelsRequest{
			AccessorID: accountInfo.ID,
			Items: []interfaces.PropertyLevelsRequestItem{{
				ObjectTypeRef: objectTypeRef,
				Properties:    requested,
			}},
		})
		if err != nil {
			return nil, ps.propertyAccessInternalError(ctx, err)
		}
		batch, err := filter(objectTypeRef, requested, response.Entries)
		if err != nil {
			return nil, ps.propertyAccessInternalError(ctx, err)
		}
		full = append(full, batch...)
	}
	return full, nil
}

func (ps *PermissionServiceImpl) propertyAccessInternalError(ctx context.Context, err error) error {
	httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
		berrors.BknBackend_InternalError_CheckPermissionFailed).WithErrorDetails(err)
	otellog.LogError(ctx, "RequireFullPropertyAccess failed", httpErr)
	return httpErr
}

func filterFullPropertyLevelResponse(objectTypeRef string, requested []string,
	entries []interfaces.PropertyLevelsDecisionEntry) ([]string, error) {
	return filterPropertyLevelResponse(objectTypeRef, requested, entries, func(level string) bool {
		return level == "full"
	})
}

func filterVisiblePropertyLevelResponse(objectTypeRef string, requested []string,
	entries []interfaces.PropertyLevelsDecisionEntry) ([]string, error) {
	return filterPropertyLevelResponse(objectTypeRef, requested, entries, func(level string) bool {
		return level != "none"
	})
}

func filterPropertyLevelResponse(objectTypeRef string, requested []string,
	entries []interfaces.PropertyLevelsDecisionEntry, include func(string) bool) ([]string, error) {
	if len(entries) != 1 || entries[0].ObjectTypeRef != objectTypeRef {
		return nil, fmt.Errorf("invalid property-levels object response")
	}
	decisions := make(map[string]string, len(entries[0].Properties))
	for _, decision := range entries[0].Properties {
		if _, duplicate := decisions[decision.Name]; duplicate || strings.TrimSpace(decision.Name) == "" {
			return nil, fmt.Errorf("invalid property-levels property response")
		}
		decisions[decision.Name] = decision.Level
	}
	if len(decisions) != len(requested) {
		return nil, fmt.Errorf("incomplete property-levels response")
	}
	result := make([]string, 0, len(requested))
	for _, property := range requested {
		level, exists := decisions[property]
		if !exists {
			return nil, fmt.Errorf("incomplete property-levels response")
		}
		switch level {
		case "none", "schema", "masked", "full":
		default:
			return nil, fmt.Errorf("invalid property access level")
		}
		if include(level) {
			result = append(result, property)
		}
	}
	return result, nil
}

func uniqueSortedProperties(properties []string) []string {
	seen := make(map[string]struct{}, len(properties))
	result := make([]string, 0, len(properties))
	for _, property := range properties {
		property = strings.TrimSpace(property)
		if property == "" {
			continue
		}
		if _, exists := seen[property]; exists {
			continue
		}
		seen[property] = struct{}{}
		result = append(result, property)
	}
	sort.Strings(result)
	return result
}

// CreateResources creates permission policies for newly created resources.
func (ps *PermissionServiceImpl) CreateResources(ctx context.Context, resources []interfaces.PermissionResource, ops []string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CreatePermissionResources")
	defer span.End()

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	if accountInfo.ID == "" || accountInfo.Type == "" {
		httpErr := rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(localizedPermissionDetail(ctx, "AccountInfoMissing"))
		otellog.LogError(ctx, "CreateResources missing account ID or type", httpErr)
		return httpErr
	}

	allowOps := []interfaces.PermissionOperation{}
	for _, op := range ops {
		allowOps = append(allowOps, interfaces.PermissionOperation{
			Operation: op,
		})
	}

	policies := []interfaces.PermissionPolicy{}
	for _, resource := range resources {
		policies = append(policies, interfaces.PermissionPolicy{
			Accessor: interfaces.PermissionAccessor{
				Type: accountInfo.Type,
				ID:   accountInfo.ID,
			},
			Resource: resource,
			Operations: interfaces.PermissionPolicyOps{
				Allow: allowOps,
				Deny:  []interfaces.PermissionOperation{},
			},
		})
	}

	err := ps.pa.CreateResources(ctx, policies)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_CreateResourcesFailed).WithErrorDetails(err.Error())
		otellog.LogError(ctx, "CreateResources failed", httpErr)
		return httpErr
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteResources deletes permission policies for the supplied resources.
func (ps *PermissionServiceImpl) DeleteResources(ctx context.Context, resourceType string, ids []string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeletePermissionResources")
	defer span.End()

	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}
	// Permission resource deletion is temporarily disabled.

	resources := []interfaces.PermissionResource{}
	for _, id := range ids {
		resources = append(resources, interfaces.PermissionResource{
			Type: resourceType,
			ID:   id,
		})
	}

	err := ps.pa.DeleteResources(ctx, resources)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_DeleteResourcesFailed).WithErrorDetails(err)
		otellog.LogError(ctx, "DeleteResources failed", httpErr)
		return httpErr
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// UpsertResourceParents synchronizes concrete KN child ownership into bkn-safe.
func (ps *PermissionServiceImpl) UpsertResourceParents(ctx context.Context, resourceType, parentType string,
	items []interfaces.PermissionResourceParent) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UpsertPermissionResourceParents")
	defer span.End()

	if len(items) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}
	if err := ps.pa.UpsertResourceParents(ctx, resourceType, parentType, items); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_CreateResourcesFailed).WithErrorDetails(err)
		otellog.LogError(ctx, "UpsertResourceParents failed", httpErr)
		return httpErr
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteResourceParents removes concrete KN child ownership from bkn-safe.
func (ps *PermissionServiceImpl) DeleteResourceParents(ctx context.Context, resourceType string,
	resourceIDs []string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeletePermissionResourceParents")
	defer span.End()

	if len(resourceIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}
	if err := ps.pa.DeleteResourceParents(ctx, resourceType, resourceIDs); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_DeleteResourcesFailed).WithErrorDetails(err)
		otellog.LogError(ctx, "DeleteResourceParents failed", httpErr)
		return httpErr
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// FilterVisibleResources performs pure visibility filtering.
func (ps *PermissionServiceImpl) FilterVisibleResources(ctx context.Context, resourceType string, ids []string,
	visibilityOperations []string) (map[string]interfaces.PermissionResourceOps, error) {
	return ps.filterResources(ctx, resourceType, ids, visibilityOperations, false)
}

// FilterVisibleResourcesWithOperations returns visible resources with their
// complete registry-backed effective operation sets.
func (ps *PermissionServiceImpl) FilterVisibleResourcesWithOperations(ctx context.Context, resourceType string,
	ids []string, visibilityOperations []string) (map[string]interfaces.PermissionResourceOps, error) {
	return ps.filterResources(ctx, resourceType, ids, visibilityOperations, true)
}

func (ps *PermissionServiceImpl) filterResources(ctx context.Context, resourceType string, ids []string,
	visibilityOperations []string, includeOperations bool) (map[string]interfaces.PermissionResourceOps, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "FilterPermissionResources")
	defer span.End()

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	if accountInfo.ID == "" || accountInfo.Type == "" {
		httpErr := rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(localizedPermissionDetail(ctx, "AccountInfoMissing"))
		otellog.LogError(ctx, "FilterResources missing account ID or type", httpErr)
		return nil, httpErr
	}

	resources := []interfaces.PermissionResource{}
	for _, id := range ids {
		resources = append(resources, interfaces.PermissionResource{
			ID:   id,
			Type: resourceType,
		})
	}

	matchResouces, err := ps.pa.FilterResources(ctx, interfaces.PermissionResourcesFilter{
		Accessor: interfaces.PermissionAccessor{
			ID:   accountInfo.ID,
			Type: accountInfo.Type,
		},
		Resources:         resources,
		Operations:        visibilityOperations,
		IncludeOperations: includeOperations,
	})
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_FilterResourcesFailed).WithErrorDetails(err)
		otellog.LogError(ctx, "FilterResources failed", httpErr)
		return nil, httpErr
	}

	// Convert resource IDs to a lookup map.
	requestedIDs := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		requestedIDs[id] = struct{}{}
	}
	idMap := make(map[string]interfaces.PermissionResourceOps, len(matchResouces))
	for key, resourceOps := range matchResouces {
		if _, ok := requestedIDs[key]; !ok || resourceOps.ResourceID != key {
			httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_InternalError_FilterResourcesFailed).
				WithErrorDetails("invalid resource-filter response")
			otellog.LogError(ctx, "FilterResources returned an invalid resource", httpErr)
			return nil, httpErr
		}
		idMap[resourceOps.ResourceID] = resourceOps
	}

	span.SetStatus(codes.Ok, "")
	return idMap, nil
}

// UpdateResource updates a resource name through the authorization event channel.
func (ps *PermissionServiceImpl) UpdateResource(ctx context.Context, resource interfaces.PermissionResource) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UpdatePermissionResource")
	defer span.End()

	bytes, err := sonic.Marshal(resource)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_MarshalDataFailed).WithErrorDetails(err)
		otellog.LogError(ctx, "UpdateResource marshal failed", httpErr)
		return httpErr
	}

	err = ps.mqClient.Pub(interfaces.AUTHORIZATION_RESOURCE_NAME_MODIFY, bytes)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_UpdateResourceFailed).WithErrorDetails(err)
		otellog.LogError(ctx, "UpdateResource publish failed", httpErr)
		return httpErr
	}

	span.SetStatus(codes.Ok, "")
	return nil
}
