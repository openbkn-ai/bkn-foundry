// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
	"bkn-backend/logics/knowledge_network"
	"bkn-backend/logics/object_type"
	"bkn-backend/logics/permission"
	"bkn-backend/logics/relation_type"
)

// MaxQueryLength bounds the submitted text. Parsing is linear, but an
// unbounded body would still be work done before any permission is checked.
const MaxQueryLength = 8192

type cypherQueryService struct {
	appSetting *common.AppSetting
	ps         interfaces.PermissionService
	schema     KNSchemaSource
	// proxies maps a network to its managed proxy account and checks the
	// bindings against the published model; this package only derives them
	// from the compiled plan.
	proxies interfaces.KNProxyBindingResolver
	vba     interfaces.VegaBackendAccess
}

var (
	cypherQueryServiceOnce sync.Once
	cypherQueryServiceInst interfaces.CypherQueryService
)

func NewCypherQueryService(appSetting *common.AppSetting) interfaces.CypherQueryService {
	cypherQueryServiceOnce.Do(func() {
		// Without a resolver every statement runs under the caller's identity.
		proxies, _ := knowledge_network.NewKNService(appSetting).(interfaces.KNProxyBindingResolver)
		cypherQueryServiceInst = &cypherQueryService{
			appSetting: appSetting,
			ps:         permission.NewPermissionService(appSetting),
			schema: NewSchemaSource(
				object_type.NewObjectTypeService(appSetting),
				relation_type.NewRelationTypeService(appSetting),
			),
			proxies: proxies,
			vba:     logics.VBA,
		}
	})
	return cypherQueryServiceInst
}

// compiledQuery is a query ready to run: the statement, the page to ask for,
// the plan it came from, which says what the statement reads, the schema it
// was bound against, which holds the caller's property levels, and the
// semantic descriptor the internal face returns for tracing.
type compiledQuery struct {
	statement       string
	rowLimit        int
	plan            *Plan
	schema          *Schema
	traceDescriptor json.RawMessage
}

// Query compiles a Cypher query against one knowledge network and runs the
// resulting statement through vega-backend.
//
// Every permission the query needs is decided here, on the knowledge network,
// before anything is read. One is query_data on each object type and relation
// type, applied by building the schema out of only what the caller may read,
// so a concept they have no access to is absent rather than refused -- the
// endpoint cannot be used to find out what a model contains. A network-wide
// grant still works, because query_data on a child inherits from query_data on
// its parent network. The other is the caller's level on every property the
// query names. A property at none does not exist for them, and is reported as
// unknown the way a property outside the model is. A compiled statement reads
// columns as they are and nothing masks them, so a property they may see only
// masked or as schema is refused by name rather than read; only full is read.
//
// Past that the caller needs nothing on the vega resources behind those
// concepts. The statement goes to vega-backend's ordinary raw query route
// under the caller's own identity first, exactly as it always has, so a caller
// who holds the resource grants never touches the proxy, whatever the vega
// version. Only when vega-backend refuses that run with 403 is the same
// statement sent again, on the same route, under the network's managed proxy
// account, and vega-backend checks view_detail on each resource it references
// against that account, which holds the grants the published model's bindings
// gave it. The proxy is used only when the network is on main and every
// binding the plan reads through is a current published one, and it cannot be
// steered anywhere else: the statement is generated here from the model, its
// only table references are the plan's resources, and a caller's values reach
// it as escaped literals, never as names.
//
// A refusal that the proxy cannot answer -- no resolver, a branch other than
// main, a binding the published model does not hold, a mapping that is
// missing, not active or not synchronized, or the proxy refused too, as it is
// on an indirect relation's backing resource where it holds only query_data --
// is reported as forbidden, without the statement. Nothing is widened: the
// knowledge-network checks above apply whichever identity reads the data.
func (s *cypherQueryService) Query(ctx context.Context, query interfaces.CypherQuery) (*interfaces.CypherQueryResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Compile and run Cypher query")
	defer span.End()

	if err := validateQueryText(ctx, query.Query); err != nil {
		return nil, err
	}
	compiled, err := s.compile(ctx, query)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProperties(ctx, query.KNID, compiled); err != nil {
		return nil, err
	}

	// The paging limit repeats the statement's own LIMIT rather than leaving
	// it unset. vega-backend wraps the statement and applies a page of its
	// own, defaulting to 20 rows, so a query asking for more would come back
	// cut down with nothing to say it had been.
	response, err := s.execute(ctx, query, compiled.plan, interfaces.RawQueryRequest{
		Query:        compiled.statement,
		QueryFormat:  interfaces.VEGA_QUERY_FORMAT_SQL,
		InputDialect: interfaces.VEGA_DIALECT_MYSQL,
		Paging: interfaces.ResourceDataPagingRequest{
			Mode:  interfaces.VEGA_PAGING_MODE_SINGLE,
			Limit: compiled.rowLimit,
		},
		QueryTimeoutSec: interfaces.CYPHER_DEFAULT_TIMEOUT_SEC,
	})
	if err != nil {
		return nil, executionError(ctx, err)
	}

	return &interfaces.CypherQueryResult{
		Columns:         response.Columns,
		Entries:         response.Entries,
		TraceDescriptor: compiled.traceDescriptor,
	}, nil
}

// compile runs the four stages and turns each stage's own error type into the
// status the caller should see. A query that is malformed, outside the subset,
// or does not fit the model is the caller's to fix, and says so with 400;
// anything else is ours.
func (s *cypherQueryService) compile(ctx context.Context, query interfaces.CypherQuery) (*compiledQuery, error) {
	tree, err := Parse(query.Query)
	if err != nil {
		var syntaxErrors SyntaxErrors
		if errors.As(err, &syntaxErrors) {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_SyntaxError).
				WithErrorDetails(syntaxErrors.Error())
		}
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_SyntaxError).
			WithErrorDetails(err.Error())
	}

	analyzed, err := Analyze(tree)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_Unsupported).
			WithErrorDetails(err.Error())
	}
	if analyzed.Limit != nil && *analyzed.Limit > interfaces.CYPHER_MAX_LIMIT {
		// Refused rather than clamped: a caller who asked for more rows than
		// they will get should be told, not handed a short answer.
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_LimitExceeded).
			WithErrorDetails(detail(ctx, "LimitExceeded", map[string]any{"max": interfaces.CYPHER_MAX_LIMIT}))
	}

	schema, err := LoadSchema(ctx, s.schema, &permissionVisibility{ps: s.ps}, query.KNID, query.Branch)
	if err != nil {
		return nil, err
	}
	if schema.NothingReadable() {
		// The network holds concepts and this caller may read none of them.
		// Saying so is better than reporting every label as unknown, and
		// reveals nothing: they already knew which network they asked about.
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden)
	}
	// Levels are loaded before names are bound, so a property the caller may
	// not know exists is already absent while the query is resolved, and no
	// error or suggestion can mention it.
	if err := s.loadPropertyLevels(ctx, query.KNID, schema, analyzed); err != nil {
		return nil, err
	}

	plan, err := Compile(analyzed, schema, CompileOptions{Parameters: query.Parameters})
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_InvalidQuery).
			WithErrorDetails(err.Error())
	}
	// The semantic descriptor records the caller's query, not policy internals.
	// In particular, a filter-only policy property may be invisible to the
	// caller and must not enter their trace/evidence payload merely because it
	// constrains execution below.
	descriptor, err := BuildSemanticQueryDescriptor(plan, query.Query)
	if err != nil {
		common.LogSafeError(ctx, "Cypher semantic descriptor generation failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
	}
	descriptorJSON, err := json.Marshal(descriptor)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
	}
	if err := s.applyRowFilters(ctx, query.KNID, plan, schema); err != nil {
		var httpErr *rest.HTTPError
		if errors.As(err, &httpErr) {
			return nil, err
		}
		common.LogSafeError(ctx, "Cypher row-filter compilation failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
	}

	sql, err := Generate(plan, GenerateOptions{
		Dialect:      DialectMySQL,
		DefaultLimit: interfaces.CYPHER_DEFAULT_LIMIT,
	})
	if err != nil {
		common.LogSafeError(ctx, "Cypher SQL generation failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
	}

	return &compiledQuery{
		statement: sql, rowLimit: pageSize(plan.Limit), plan: plan, schema: schema,
		traceDescriptor: descriptorJSON,
	}, nil
}

// loadPropertyLevels records the caller's property levels for every object
// type the query's pattern names. A label that does not resolve is left for
// the compiler to report.
func (s *cypherQueryService) loadPropertyLevels(ctx context.Context, knID string, schema *Schema,
	query *Query) error {
	for _, node := range query.Pattern.Nodes {
		objectType, err := schema.ResolveLabel(node.Label)
		if err != nil {
			continue
		}
		if _, err := s.objectTypeLevels(ctx, knID, schema, objectType); err != nil {
			return err
		}
	}
	return nil
}

// objectTypeLevels returns the caller's level for every data property of one
// object type. It asks for all of them, not only those the query names,
// because suggestions are drawn from what the caller may see; and it asks once
// per object type and request, whatever the query does with it.
func (s *cypherQueryService) objectTypeLevels(ctx context.Context, knID string, schema *Schema,
	objectType *interfaces.ObjectType) (map[string]string, error) {
	if levels, ok := schema.PropertyLevels(objectType.OTID); ok {
		return levels, nil
	}
	names := make([]string, 0, len(objectType.DataProperties))
	for _, property := range objectType.DataProperties {
		if property != nil {
			names = append(names, property.Name)
		}
	}
	levels, err := s.ps.ResolvePropertyAccessLevels(ctx, interfaces.KNChildResourceID(knID, objectType.OTID), names)
	if err != nil {
		return nil, err
	}
	schema.SetPropertyLevels(objectType.OTID, levels)
	return levels, nil
}

// authorizeProperties requires full access to every property the query names.
//
// It applies to every caller, whichever identity the statement then runs
// under: a grant on the resource says nothing about the level the model gives
// each property. The levels are the ones loaded before the query was bound,
// so this asks bkn-safe nothing new. A masked or schema property is refused by
// name, the way the query wrote it, by object type and property; the caller
// may know it exists, while the column behind it is no more theirs to learn
// than the table is. A property at none was already reported as unknown.
func (s *cypherQueryService) authorizeProperties(ctx context.Context, knID string, compiled *compiledQuery) error {
	plan := compiled.plan
	byObjectType := map[string][]string{}
	seen := make(map[PlanProperty]bool, len(plan.Properties))
	for _, property := range plan.Properties {
		if seen[property] {
			continue
		}
		seen[property] = true
		byObjectType[property.ObjectTypeID] = append(byObjectType[property.ObjectTypeID], property.Property)
	}
	objectTypeIDs := make([]string, 0, len(byObjectType))
	for objectTypeID := range byObjectType {
		objectTypeIDs = append(objectTypeIDs, objectTypeID)
	}
	sort.Strings(objectTypeIDs)

	var denied []string
	for _, objectTypeID := range objectTypeIDs {
		objectType := compiled.schema.objectTypesByID[objectTypeID]
		if objectType == nil {
			// The plan only names object types it resolved from this schema.
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
		}
		levels, err := s.objectTypeLevels(ctx, knID, compiled.schema, objectType)
		if err != nil {
			return err
		}
		properties := byObjectType[objectTypeID]
		sort.Strings(properties)
		for _, property := range properties {
			switch levels[property] {
			case interfaces.PROPERTY_ACCESS_FULL:
			case interfaces.PROPERTY_ACCESS_NONE:
				// Binding reports this as unknown before a plan exists; should
				// that ever change, the answer must still be the unknown one.
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_InvalidQuery).
					WithErrorDetails(compiled.schema.unknownProperty(objectType, property).Error())
			default:
				// Masked, schema, or a property bkn-safe gave no level for.
				denied = append(denied, objectTypeID+"."+property)
			}
		}
	}
	if len(denied) == 0 {
		return nil
	}
	return rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_Cypher_PropertyForbidden).
		WithErrorDetails(i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.Cypher.PropertyForbidden.Detail.NotFull",
			map[string]any{"properties": strings.Join(denied, ", ")}))
}

// Span attributes saying which identity answered a statement and, when the
// caller was refused and the proxy could not be tried, why. They are recorded
// on the query span.
const (
	spanAttrExecutionPath  = "cypher.execution_path"
	spanAttrFallbackReason = "cypher.fallback_reason"

	executionPathProxy  = "proxy"
	executionPathCaller = "caller"

	fallbackNoResolver          = "no_resolver"
	fallbackNonMainBranch       = "non_main_branch"
	fallbackBindingNotPublished = "binding_not_published"
	fallbackProxyUnavailable    = "proxy_unavailable"
)

// proxyTargetResource is the target type of a binding to a vega resource.
const proxyTargetResource = "resource"

// proxyExecution is what a statement runs under when the knowledge network's
// proxy serves it: the proxy account, and the resources the published bindings
// cover, which are the resources of the plan.
type proxyExecution struct {
	account     interfaces.AccountInfo
	resourceIDs []string
}

// execute runs the statement under the caller's own identity, and runs it
// again under the knowledge network's proxy account only when vega-backend
// refused the caller with 403 and the proxy can serve it. It records on the
// query span which identity the answer came from, and why the proxy was not
// tried when a refusal was left standing.
//
// Both runs go to the same vega-backend raw query route, which checks
// view_detail on each resource the statement references against whoever the
// request names. Any other failure of the caller's run is returned as it is;
// only a refusal is something another identity could answer.
func (s *cypherQueryService) execute(ctx context.Context, query interfaces.CypherQuery, plan *Plan,
	request interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	span := trace.SpanFromContext(ctx)
	callerRequest := request
	response, err := s.vba.RawQuery(ctx, &callerRequest)
	if err == nil || !vegaRefused(err) {
		span.SetAttributes(attribute.String(spanAttrExecutionPath, executionPathCaller))
		return response, err
	}

	proxy, reason := s.resolveProxy(ctx, query, plan)
	if proxy == nil {
		span.SetAttributes(
			attribute.String(spanAttrExecutionPath, executionPathCaller),
			attribute.String(spanAttrFallbackReason, reason),
		)
		return nil, err
	}
	caller, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	logger.Infof("Cypher query on knowledge network %s runs under the knowledge network proxy: "+
		"caller %s, proxy account %s, resources %s",
		query.KNID, caller.ID, proxy.account.ID, strings.Join(proxy.resourceIDs, ","))
	span.SetAttributes(attribute.String(spanAttrExecutionPath, executionPathProxy))
	proxyRequest := request
	return s.vba.RawQueryAs(ctx, proxy.account, &proxyRequest)
}

// vegaRefused reports vega-backend refusing a resource the statement reads.
func vegaRefused(err error) bool {
	var dependencyErr *interfaces.DependencyError
	return errors.As(err, &dependencyErr) && dependencyErr.HTTPStatus == http.StatusForbidden
}

// resolveProxy returns what a refused statement may run under through the
// knowledge network's proxy, or nil and the reason the proxy cannot serve it.
func (s *cypherQueryService) resolveProxy(ctx context.Context, query interfaces.CypherQuery,
	plan *Plan) (*proxyExecution, string) {
	if s.proxies == nil {
		return nil, fallbackNoResolver
	}
	// The proxy holds what the published main model binds. Another branch may
	// bind its concepts differently, so its reads stay on the caller.
	if query.Branch != interfaces.MAIN_BRANCH {
		return nil, fallbackNonMainBranch
	}
	caller, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if !ok || caller.ID == "" || caller.Type == "" {
		return nil, fallbackProxyUnavailable
	}
	bindings := proxyBindings(plan)
	if len(bindings) == 0 {
		return nil, fallbackProxyUnavailable
	}

	mapping, resolved, err := s.proxies.ResolveKNProxyBindings(ctx, query.KNID, bindings)
	if err != nil {
		logger.Infof("Cypher query on knowledge network %s stays refused under the caller's identity: "+
			"the knowledge network proxy is unavailable (%s)", query.KNID, proxyErrorCode(err))
		return nil, fallbackProxyUnavailable
	}
	// The resolver leaves out a binding that is not a current published one.
	// One statement reads through all of its bindings together, so a single
	// one missing means the proxy cannot serve the statement at all.
	if !allResolved(bindings, resolved) {
		logger.Infof("Cypher query on knowledge network %s stays refused under the caller's identity: "+
			"the knowledge network proxy is unavailable (%s)", query.KNID,
			berrors.BknBackend_KnowledgeNetwork_ProxyBindingInvalid)
		return nil, fallbackBindingNotPublished
	}
	if !usableProxy(mapping, query.KNID) {
		logger.Infof("Cypher query on knowledge network %s stays refused under the caller's identity: "+
			"the knowledge network proxy mapping is incomplete", query.KNID)
		return nil, fallbackProxyUnavailable
	}
	return &proxyExecution{
		account:     interfaces.AccountInfo{ID: mapping.ProxyAccountID, Type: mapping.ProxyAccountType},
		resourceIDs: planResourceIDs(plan),
	}, ""
}

// proxyBindings lists the published bindings a statement reads through: each
// object type the pattern matches, bound to its resource, and each relation
// type it joins over, bound to the resources at both of its ends. That is how
// the published model grants the proxy account query_data, so every resource
// the statement references is covered by one of them.
func proxyBindings(plan *Plan) []interfaces.KNProxyBinding {
	seen := map[interfaces.KNProxyBinding]bool{}
	bindings := make([]interfaces.KNProxyBinding, 0, len(plan.Tables)+2*len(plan.Joins))
	add := func(childType, childID, resourceID string) {
		binding := interfaces.KNProxyBinding{
			ChildType:  childType,
			ChildID:    childID,
			TargetType: proxyTargetResource,
			TargetID:   resourceID,
			Operation:  interfaces.OPERATION_TYPE_QUERY_DATA,
		}
		if !seen[binding] {
			seen[binding] = true
			bindings = append(bindings, binding)
		}
	}
	for _, table := range plan.Tables {
		add(interfaces.MODULE_TYPE_OBJECT_TYPE, table.ObjectTypeID, table.ResourceID)
	}
	for _, join := range plan.Joins {
		add(interfaces.MODULE_TYPE_RELATION_TYPE, join.RelationTypeID, plan.Tables[join.Left].ResourceID)
		add(interfaces.MODULE_TYPE_RELATION_TYPE, join.RelationTypeID, plan.Tables[join.Right].ResourceID)
	}
	return bindings
}

// planResourceIDs lists the resources the statement references, each once, in
// the order the plan names them.
func planResourceIDs(plan *Plan) []string {
	seen := make(map[string]bool, len(plan.Tables))
	resourceIDs := make([]string, 0, len(plan.Tables))
	for _, table := range plan.Tables {
		if !seen[table.ResourceID] {
			seen[table.ResourceID] = true
			resourceIDs = append(resourceIDs, table.ResourceID)
		}
	}
	return resourceIDs
}

// allResolved reports whether the resolver returned every binding asked for.
func allResolved(requested, resolved []interfaces.KNProxyBinding) bool {
	returned := make(map[interfaces.KNProxyBinding]bool, len(resolved))
	for _, binding := range resolved {
		returned[binding] = true
	}
	for _, binding := range requested {
		if !returned[binding] {
			return false
		}
	}
	return true
}

// usableProxy repeats the state checks the resolver made. The mapping decides
// whose identity reads the data, so a field that went missing on the way here
// is not something to run a statement under.
func usableProxy(mapping *interfaces.KNProxyAccount, knID string) bool {
	return mapping != nil &&
		mapping.KNID == knID &&
		mapping.ProxyAccountID != "" &&
		mapping.ProxyAccountType == interfaces.KNProxyAccountTypeApp &&
		mapping.Version > 0 &&
		mapping.LifecycleStatus == interfaces.KNProxyLifecycleActive &&
		mapping.SyncStatus == interfaces.KNProxySyncReady &&
		mapping.PublishedModelVersion != "" &&
		mapping.SyncedModelVersion == mapping.PublishedModelVersion
}

func proxyErrorCode(err error) string {
	var httpErr *rest.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.BaseError.ErrorCode
	}
	return "proxy resolution failed"
}

// executionError turns a failed run into what the caller sees. The statement
// names physical tables and columns and the dependency's message may quote it,
// so only a code travels back: forbidden when vega-backend refused a resource
// the statement reads, so a missing grant reads as one, and a query failure
// for anything else.
func executionError(ctx context.Context, err error) error {
	if vegaRefused(err) {
		return rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_Cypher_Forbidden)
	}
	return rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_QueryFailed)
}

// pageSize is how many rows to ask vega-backend for. The statement's own LIMIT
// governs the result; this only has to be wide enough not to cut it short.
//
// The limit is read into a local and bounded against the same local that is
// converted, rather than relying on the ceiling checked several stages
// earlier: int is 32 bits on some targets, and an argument for why a widening
// conversion is safe should not live in another function.
func pageSize(limit *int64) int {
	if limit == nil {
		return interfaces.CYPHER_DEFAULT_LIMIT
	}
	rows := *limit
	if rows <= 0 || rows > interfaces.CYPHER_MAX_LIMIT {
		return interfaces.CYPHER_DEFAULT_LIMIT
	}
	return int(rows)
}

func validateQueryText(ctx context.Context, query string) error {
	if len(query) == 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_InvalidParameter).
			WithErrorDetails(detail(ctx, "QueryRequired", nil))
	}
	if len(query) > MaxQueryLength {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_InvalidParameter).
			WithErrorDetails(detail(ctx, "QueryTooLong", map[string]any{"max": MaxQueryLength}))
	}
	return nil
}

func detail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.Cypher.InvalidParameter.Detail."+name, templateData)
}
