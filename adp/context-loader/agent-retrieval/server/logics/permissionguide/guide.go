// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package permissionguide turns object-type access limits into guidance an
// agent can relay: what was withheld and where the user can request more.
//
// It only restates signals ontology-query already gave the caller (a 403, the
// effective property levels, the row-filter flag). It never infers a limit
// from an empty result or an empty value, and it never names or links an
// object type the caller may not view.
package permissionguide

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

const (
	resourceTypeObjectType = "object_type"
	operationQueryData     = "query_data"

	// visibilityTTL bounds how long a view_detail probe is reused for one
	// caller and object type. A revoked grant can keep a link alive for this
	// long; the link itself grants nothing, Studio checks again.
	visibilityTTL        = time.Minute
	visibilityCacheLimit = 4096
)

// scopeCodes are the Studio requestPermission values for each scope.
var scopeCodes = map[interfaces.PermissionRequestScope]string{
	interfaces.PermissionScopeGrant:          "1",
	interfaces.PermissionScopeRowFilter:      "2",
	interfaces.PermissionScopePropertyGrants: "3",
}

// ObjectTypeReader reads an object type definition without a permission check.
// It is used for the display name only after the caller's view_detail has been
// confirmed.
type ObjectTypeReader interface {
	GetObjectTypeDetail(ctx context.Context, knID string, otIds []string, includeDetail bool) ([]*interfaces.ObjectType, error)
}

// Guide builds PermissionGuidance for object-type tool calls.
type Guide struct {
	disabled     bool
	pathTemplate string
	schemas      interfaces.ObjectSchemaAccess
	objectTypes  ObjectTypeReader
	now          func() time.Time

	mu      sync.Mutex
	visible map[string]visibility
}

type visibility struct {
	viewable bool
	name     string
	expires  time.Time
}

// New returns a Guide. schemas answers whether the caller may view an object
// type (ontology-query's /schema requires view_detail); objectTypes supplies
// its display name.
func New(cfg config.PermissionRequestConfig, schemas interfaces.ObjectSchemaAccess, objectTypes ObjectTypeReader) *Guide {
	return &Guide{
		disabled:     cfg.Disabled,
		pathTemplate: strings.TrimSpace(cfg.PathTemplate),
		schemas:      schemas,
		objectTypes:  objectTypes,
		now:          time.Now,
		visible:      map[string]visibility{},
	}
}

// ForObjectTypeError returns guidance when err is a permission refusal of a
// call that targeted one object type, and nil for every other error.
func (g *Guide) ForObjectTypeError(ctx context.Context, err error, knID, otID string) *interfaces.PermissionGuidance {
	if g == nil || knID == "" || otID == "" {
		return nil
	}
	if status, ok := infraErr.HTTPStatus(err); !ok || status != http.StatusForbidden {
		return nil
	}
	return g.build(ctx, knID, otID, []interfaces.PermissionShortfall{{
		Scope: interfaces.PermissionScopeGrant, Operations: []string{operationQueryData},
	}})
}

// ForObjectQuery returns guidance for a successful query whose result was
// masked or narrowed, and nil when every returned property was full and no
// row filter applied.
func (g *Guide) ForObjectQuery(ctx context.Context, knID, otID string,
	effective map[string]interfaces.PropertyAccessLevel, rowFilterApplied bool) *interfaces.PermissionGuidance {
	if g == nil || knID == "" || otID == "" {
		return nil
	}
	var shortfalls []interfaces.PermissionShortfall
	if restricted := restrictedProperties(effective); len(restricted) > 0 {
		shortfalls = append(shortfalls, interfaces.PermissionShortfall{
			Scope: interfaces.PermissionScopePropertyGrants, Properties: restricted,
		})
	}
	if rowFilterApplied {
		shortfalls = append(shortfalls, interfaces.PermissionShortfall{Scope: interfaces.PermissionScopeRowFilter})
	}
	if len(shortfalls) == 0 {
		return nil
	}
	return g.build(ctx, knID, otID, shortfalls)
}

// restrictedProperties lists properties whose raw value was withheld. A
// property the caller cannot see at all (none) is absent from the map by
// design and stays unmentioned.
func restrictedProperties(effective map[string]interfaces.PropertyAccessLevel) []string {
	var names []string
	for name, level := range effective {
		if level != interfaces.PropertyAccessFull {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (g *Guide) build(ctx context.Context, knID, otID string,
	shortfalls []interfaces.PermissionShortfall) *interfaces.PermissionGuidance {
	guidance := &interfaces.PermissionGuidance{
		Resource: interfaces.PermissionGuidanceResource{
			Type: resourceTypeObjectType, ID: knID + "/" + otID, KnID: knID, OtID: otID,
		},
		Shortfalls: shortfalls,
	}
	linked := false
	if g.canRequest(ctx) {
		// Studio's request dialog needs the object type page; a caller who
		// cannot view it gets no link and no name, only the refusal.
		if seen := g.lookup(ctx, knID, otID); seen.viewable {
			guidance.Resource.Name = seen.name
			// Without a known public origin the link stays relative to the Studio host.
			origin := common.GetPublicOriginFromCtx(ctx)
			for i := range guidance.Shortfalls {
				guidance.Shortfalls[i].RequestPermissionURL = origin +
					g.requestPath(ctx, knID, otID, seen.name, guidance.Shortfalls[i])
			}
			linked = true
		}
	}
	guidance.Message = message(ctx, guidance, linked)
	return guidance
}

// message renders the user-facing sentence: one clause per shortfall, then
// the next step. With a link the user is told to wait for approval before
// rerunning, since a submitted request does not change access by itself.
func message(ctx context.Context, guidance *interfaces.PermissionGuidance, linked bool) string {
	name := guidance.Resource.Name
	if name == "" {
		name = infraErr.LocalizedDetail(ctx, "PermissionGuidanceUnnamedObjectType")
	}
	var parts []string
	for _, shortfall := range guidance.Shortfalls {
		switch shortfall.Scope {
		case interfaces.PermissionScopeGrant:
			parts = append(parts, infraErr.LocalizedDetail(ctx, "PermissionGuidanceGrant", name))
		case interfaces.PermissionScopePropertyGrants:
			separator := infraErr.LocalizedDetail(ctx, "PermissionGuidanceListSeparator")
			parts = append(parts, infraErr.LocalizedDetail(ctx, "PermissionGuidancePropertyGrants",
				name, strings.Join(shortfall.Properties, separator)))
		case interfaces.PermissionScopeRowFilter:
			parts = append(parts, infraErr.LocalizedDetail(ctx, "PermissionGuidanceRowFilter", name))
		}
	}
	if linked {
		parts = append(parts, infraErr.LocalizedDetail(ctx, "PermissionGuidanceRequestHint"))
	} else {
		parts = append(parts, infraErr.LocalizedDetail(ctx, "PermissionGuidanceContactAdmin"))
	}
	separator := ""
	if language := strings.ToLower(string(common.GetLanguageFromCtx(ctx))); strings.HasPrefix(language, "en") {
		separator = " "
	}
	return strings.Join(parts, separator)
}

// canRequest reports whether a request link can help this caller. Studio
// offers the request flow to signed-in users only.
func (g *Guide) canRequest(ctx context.Context) bool {
	if g.disabled || g.pathTemplate == "" || g.schemas == nil {
		return false
	}
	auth, ok := common.GetAccountAuthContextFromCtx(ctx)
	return ok && auth != nil && auth.AccountID != "" && auth.AccountType == interfaces.AccessorTypeUser
}

// requestPath fills the route template and appends the values Studio uses to
// prefill the request dialog: the operations or properties to request, a
// default reason and source=agent for audit.
func (g *Guide) requestPath(ctx context.Context, knID, otID, name string,
	shortfall interfaces.PermissionShortfall) string {
	path := strings.NewReplacer(
		"{kn_id}", url.PathEscape(knID),
		"{ot_id}", url.PathEscape(otID),
		"{scope_code}", scopeCodes[shortfall.Scope],
	).Replace(g.pathTemplate)

	if name == "" {
		name = infraErr.LocalizedDetail(ctx, "PermissionGuidanceUnnamedObjectType")
	}
	var params []string
	switch shortfall.Scope {
	case interfaces.PermissionScopeGrant:
		params = append(params, "operations="+escapedList(shortfall.Operations))
		params = append(params, "reason="+queryEscape(infraErr.LocalizedDetail(ctx, "PermissionGuidanceReasonGrant", name)))
	case interfaces.PermissionScopePropertyGrants:
		params = append(params, "properties="+escapedList(shortfall.Properties))
		params = append(params, "reason="+queryEscape(infraErr.LocalizedDetail(ctx, "PermissionGuidanceReasonPropertyGrants",
			name, strings.Join(shortfall.Properties, infraErr.LocalizedDetail(ctx, "PermissionGuidanceListSeparator")))))
	case interfaces.PermissionScopeRowFilter:
		params = append(params, "reason="+queryEscape(infraErr.LocalizedDetail(ctx, "PermissionGuidanceReasonRowFilter", name)))
	}
	params = append(params, "source=agent")

	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + strings.Join(params, "&")
}

// queryEscape escapes a query value with %20 for spaces, which both
// URLSearchParams and decodeURIComponent read back correctly ("+" is not).
func queryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

// escapedList joins escaped items with a literal comma, the list form Studio
// splits on.
func escapedList(items []string) string {
	escaped := make([]string, len(items))
	for i, item := range items {
		escaped[i] = queryEscape(item)
	}
	return strings.Join(escaped, ",")
}

// lookup probes view_detail through the schema endpoint and caches the answer
// per caller, so a run of queries against one masked object type costs one
// probe rather than one per call.
func (g *Guide) lookup(ctx context.Context, knID, otID string) visibility {
	auth, _ := common.GetAccountAuthContextFromCtx(ctx)
	key := auth.AccountID + "\x00" + knID + "\x00" + otID
	now := g.now()

	g.mu.Lock()
	cached, ok := g.visible[key]
	g.mu.Unlock()
	if ok && now.Before(cached.expires) {
		return cached
	}

	seen := visibility{expires: now.Add(visibilityTTL)}
	if _, err := g.schemas.GetObjectTypeSchema(ctx, knID, otID); err == nil {
		seen.viewable = true
		seen.name = g.displayName(ctx, knID, otID)
	} else if status, ok := infraErr.HTTPStatus(err); !ok || status >= http.StatusInternalServerError {
		// A failed probe is not an answer; do not remember it.
		return seen
	}

	g.mu.Lock()
	if len(g.visible) >= visibilityCacheLimit {
		g.visible = map[string]visibility{}
	}
	g.visible[key] = seen
	g.mu.Unlock()
	return seen
}

func (g *Guide) displayName(ctx context.Context, knID, otID string) string {
	if g.objectTypes == nil {
		return ""
	}
	objectTypes, err := g.objectTypes.GetObjectTypeDetail(ctx, knID, []string{otID}, false)
	if err != nil {
		return ""
	}
	for _, objectType := range objectTypes {
		if objectType != nil && objectType.ID == otID {
			return objectType.Name
		}
	}
	return ""
}
