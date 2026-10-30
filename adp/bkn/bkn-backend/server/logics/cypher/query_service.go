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
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/knowledge_network"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/object_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/relation_type"
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
	// bindJoinTimeout bounds one bind-join execution; zero means its share of
	// the request's time.
	bindJoinTimeout time.Duration
	// now reads the clock a request's time is measured on; nil is the wall
	// clock.
	now func() time.Time
	// resourceSources remembers, per resource id, the category and catalog
	// vega-backend gave it; see outsideSQL.
	resourceSources sync.Map
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
	statement string
	rowLimit  int
	// parts are the parts of a UNION, each compiled as the query it would be
	// on its own; the union itself has no statement.
	parts []*compiledQuery
	// graph is how a query that returns graph values reads its rows; such
	// a query has no statement of its own.
	graph             *graphRead
	plan              *Plan
	schema            *Schema
	traceDescriptor   json.RawMessage
	permissionImpacts []interfaces.CypherPermissionImpact
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
	if err := s.validateTimeout(ctx, query.TimeoutSec); err != nil {
		return nil, err
	}
	// Every statement the query runs -- a UNION's parts one after another, or
	// a bind-join and the SQL it falls back to -- shares this one deadline,
	// so the request as a whole stays within its timeout.
	ctx = context.WithValue(ctx, queryDeadlineKey{},
		s.clock().Add(time.Duration(s.requestTimeoutSec(query))*time.Second))
	compiled, err := s.compile(ctx, query)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProperties(ctx, query.KNID, compiled); err != nil {
		return nil, err
	}
	if query.Explain {
		// Everything that decides whether the query may run has run; only
		// the read is left out.
		plan, err := explain(compiled)
		if err != nil {
			common.LogSafeError(ctx, "Cypher explain failed", err)
			return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
		}
		span.SetAttributes(attribute.Bool(spanAttrExplain, true))
		return &interfaces.CypherQueryResult{
			Columns: []interfaces.RawQueryColumn{}, Entries: []map[string]any{}, Plan: plan,
		}, nil
	}
	result, err := s.run(ctx, query, compiled)
	if err != nil {
		return nil, err
	}
	// A query that wrote no LIMIT and filled the default may have more rows;
	// saying so keeps a caller from taking the page for the whole answer.
	if compiled.plan.Limit == nil && len(result.Entries) >= interfaces.CYPHER_DEFAULT_LIMIT {
		result.Limited = true
	}
	return result, nil
}

// run sends a compiled query down the path that answers it.
func (s *cypherQueryService) run(ctx context.Context, query interfaces.CypherQuery,
	compiled *compiledQuery) (*interfaces.CypherQueryResult, error) {
	span := trace.SpanFromContext(ctx)
	if compiled.parts != nil {
		return s.queryUnion(ctx, query, compiled)
	}
	if s.outsideSQL(ctx, compiled.plan) {
		return s.queryOutsideSQL(ctx, query, compiled)
	}
	if compiled.graph != nil {
		span.SetAttributes(attribute.String(spanAttrExecutor, common.CypherExecutorSQL), attribute.Bool(spanAttrGraph, true))
		return s.queryGraph(ctx, query, compiled)
	}
	// The bind-join executor takes no new capabilities; what only the SQL
	// path answers, such as totals over groups or CASE, goes there.
	if s.executor() == common.CypherExecutorBindJoin && !compiled.plan.SQLOnly {
		return s.queryBindJoin(ctx, query, compiled)
	}
	span.SetAttributes(attribute.String(spanAttrExecutor, common.CypherExecutorSQL))
	return s.querySQL(ctx, query, compiled, false)
}

// querySQL runs the compiled MySQL statement through vega-backend's raw query
// route.
//
// afterBindJoin says the bind-join executor already ran the query and gave up
// on its read budget or its time. Resources SQL cannot read then have nothing
// left to fall back to: running the same bind-join again would only double the
// wait and the reads.
func (s *cypherQueryService) querySQL(ctx context.Context, query interfaces.CypherQuery,
	compiled *compiledQuery, afterBindJoin bool) (*interfaces.CypherQueryResult, error) {
	// The paging limit repeats the statement's own LIMIT rather than leaving
	// it unset. vega-backend wraps the statement and applies a page of its
	// own, defaulting to 20 rows, so a query asking for more would come back
	// cut down with nothing to say it had been.
	timeoutSec, err := s.statementTimeoutSec(ctx, query)
	if err != nil {
		return nil, err
	}
	response, err := s.execute(ctx, query, compiled.plan, interfaces.RawQueryRequest{
		Query:       compiled.statement,
		QueryFormat: interfaces.VEGA_QUERY_FORMAT_SQL,
		Paging: interfaces.ResourceDataPagingRequest{
			Mode:  interfaces.VEGA_PAGING_MODE_SINGLE,
			Limit: compiled.rowLimit,
		},
		QueryTimeoutSec: timeoutSec,
	})
	if unsupportedSource(err) && afterBindJoin {
		return nil, outsideSQLTooLarge(ctx)
	}
	if unsupportedSource(err) {
		return s.queryOutsideSQL(ctx, query, compiled)
	}
	if err != nil {
		return nil, executionError(ctx, err)
	}

	conditionsAsBooleans(compiled.plan, response.Columns, response.Entries)
	return &interfaces.CypherQueryResult{
		Columns:           response.Columns,
		Entries:           response.Entries,
		TraceDescriptor:   compiled.traceDescriptor,
		PermissionImpacts: compiled.permissionImpacts,
	}, nil
}

// queryGraph runs a query that returns graph values. Its one statement runs
// on the SQL path whatever the configured executor, which takes no new
// capabilities; the rows it returns are only assembled into values here. A
// read reaching the row cap is refused rather than answered with part of its
// evidence.
func (s *cypherQueryService) queryGraph(ctx context.Context, query interfaces.CypherQuery,
	compiled *compiledQuery) (*interfaces.CypherQueryResult, error) {
	read := compiled.graph
	timeoutSec, err := s.statementTimeoutSec(ctx, query)
	if err != nil {
		return nil, err
	}
	response, err := s.execute(ctx, query, compiled.plan, interfaces.RawQueryRequest{
		Query:       read.statement,
		QueryFormat: interfaces.VEGA_QUERY_FORMAT_SQL,
		Paging: interfaces.ResourceDataPagingRequest{
			Mode:  interfaces.VEGA_PAGING_MODE_SINGLE,
			Limit: read.rowLimit,
		},
		QueryTimeoutSec: timeoutSec,
	})
	if unsupportedSource(err) {
		return s.queryOutsideSQL(ctx, query, compiled)
	}
	if err != nil {
		return nil, executionError(ctx, err)
	}
	if read.evidence && len(response.Entries) >= graphRowCap {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_ReadBudgetExceeded).
			WithErrorDetails(fmt.Sprintf("the evidence of this query reaches %d rows; narrow it with conditions "+
				"or a smaller LIMIT, so no conclusion is returned with part of its evidence", graphRowCap))
	}

	conditionsAsBooleans(compiled.plan, response.Columns, response.Entries)
	return s.graphResult(ctx, compiled, response.Entries)
}

// graphResult builds the graph values of a query from the flat rows its graph
// read returned, whichever executor read them.
func (s *cypherQueryService) graphResult(ctx context.Context, compiled *compiledQuery,
	rows []map[string]any) (*interfaces.CypherQueryResult, error) {
	entries, limited, err := assembleGraphRows(compiled.plan, compiled.graph, rows)
	var missingKey *missingKeyError
	if errors.As(err, &missingKey) {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_InvalidQuery).
			WithErrorDetails(missingKey.Error())
	}
	if err != nil {
		common.LogSafeError(ctx, "Cypher graph value assembly failed", err)
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
	}
	return &interfaces.CypherQueryResult{
		Columns:    bindJoinColumns(compiled.plan, compiled.schema, entries),
		Entries:    entries,
		ValueTypes: graphValueTypes(compiled.plan, compiled.schema),
		Execution: &interfaces.CypherExecution{
			MatchRows: len(rows), MatchComplete: true, ConclusionsLimited: limited,
		},
		TraceDescriptor:   compiled.traceDescriptor,
		PermissionImpacts: compiled.permissionImpacts,
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
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_SyntaxError).
				WithErrorDetails(syntaxErrors.Error())
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_SyntaxError).
			WithErrorDetails(err.Error())
	}

	analyzed, err := Analyze(tree)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_Unsupported).
			WithErrorDetails(err.Error())
	}
	if limitExceeded(analyzed) {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Cypher.InvalidParameter.Detail.LimitExceeded", map[string]any{"max": interfaces.CYPHER_MAX_LIMIT})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		// Refused rather than clamped: a caller who asked for more rows than
		// they will get should be told, not handed a short answer.
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_LimitExceeded).
			WithErrorDetails(localizedText)
	}

	schema, err := LoadSchema(ctx, s.schema, &permissionVisibility{ps: s.ps}, query.KNID, query.Branch)
	if err != nil {
		return nil, err
	}
	if schema.NothingReadable() {
		// The network holds concepts and this caller may read none of them.
		// Saying so is better than reporting every label as unknown, and
		// reveals nothing: they already knew which network they asked about.
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
	}
	// Levels are loaded before names are bound, so a property the caller may
	// not know exists is already absent while the query is resolved, and no
	// error or suggestion can mention it.
	if err := s.loadPropertyLevels(ctx, query.KNID, schema, analyzed); err != nil {
		return nil, err
	}

	plan, err := Compile(analyzed, schema, CompileOptions{
		Parameters: query.Parameters, ObjectProperties: query.ObjectProperties,
	})
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_InvalidQuery).
			WithErrorDetails(err.Error())
	}
	if crossProduct(plan) {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Cypher.InvalidParameter.Detail.CartesianProduct", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_Unsupported).
			WithErrorDetails(localizedText)
	}
	// The semantic descriptor records the caller's query, not policy internals.
	// In particular, a filter-only policy property may be invisible to the
	// caller and must not enter their trace/evidence payload merely because it
	// constrains execution below.
	descriptor, err := BuildSemanticQueryDescriptor(plan, query.Query)
	if errors.Is(err, ErrSemanticDescriptorTooLarge) {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_InvalidQuery).
			WithErrorDetails("the query is too large to record; split a long IN list across several queries")
	}
	if err != nil {
		common.LogSafeError(ctx, "Cypher semantic descriptor generation failed", err)
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
	}
	descriptorJSON, err := json.Marshal(descriptor)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
	}
	rowFilteredObjectTypes, err := s.applyRowFilters(ctx, query.KNID, plan, schema)
	if err != nil {
		var httpErr *httperrors.HTTPError
		if errors.As(err, &httpErr) {
			return nil, err
		}
		common.LogSafeError(ctx, "Cypher row-filter compilation failed", err)
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
	}

	if plan.Union != nil {
		parts := make([]*compiledQuery, 0, len(plan.Union.Parts))
		for _, part := range plan.Union.Parts {
			compiledPart, err := compileStatement(ctx, part, schema)
			if err != nil {
				return nil, err
			}
			compiledPart.traceDescriptor = descriptorJSON
			compiledPart.permissionImpacts = rowFilterPermissionImpacts(rowFilteredObjectTypes)
			parts = append(parts, compiledPart)
		}
		return &compiledQuery{
			parts: parts, rowLimit: pageSize(plan.Limit), plan: plan, schema: schema,
			traceDescriptor:   descriptorJSON,
			permissionImpacts: rowFilterPermissionImpacts(rowFilteredObjectTypes),
		}, nil
	}

	compiled, err := compileStatement(ctx, plan, schema)
	if err != nil {
		return nil, err
	}
	compiled.traceDescriptor = descriptorJSON
	compiled.permissionImpacts = rowFilterPermissionImpacts(rowFilteredObjectTypes)
	return compiled, nil
}

// compileStatement writes what a plan runs as: a graph read when it returns
// graph values, a statement otherwise.
func compileStatement(ctx context.Context, plan *Plan, schema *Schema) (*compiledQuery, error) {
	if plan.HasGraphValues() {
		read, err := planGraphRead(plan, DialectMySQL, interfaces.CYPHER_DEFAULT_LIMIT)
		var planErr *PlanError
		if errors.As(err, &planErr) {
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_InvalidQuery).
				WithErrorDetails(err.Error())
		}
		if err != nil {
			common.LogSafeError(ctx, "Cypher SQL generation failed", err)
			return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
		}
		return &compiledQuery{graph: read, plan: plan, schema: schema}, nil
	}

	sql, err := Generate(plan, GenerateOptions{
		Dialect:      DialectMySQL,
		DefaultLimit: interfaces.CYPHER_DEFAULT_LIMIT,
	})
	if err != nil {
		common.LogSafeError(ctx, "Cypher SQL generation failed", err)
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
	}

	return &compiledQuery{statement: sql, rowLimit: pageSize(plan.Limit), plan: plan, schema: schema}, nil
}

// loadPropertyLevels records the caller's property levels for every object
// type the query's pattern names, a semi-join's earlier stage included: a
// property the caller may not see must read as unknown wherever it is named.
// A label that does not resolve is left for the compiler to report.
func (s *cypherQueryService) loadPropertyLevels(ctx context.Context, knID string, schema *Schema,
	query *Query) error {
	for _, stage := range query.stages() {
		if err := s.loadPropertyLevels(ctx, knID, schema, stage); err != nil {
			return err
		}
	}
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
	impactByObjectType := make(map[string]interfaces.CypherPermissionImpact, len(compiled.permissionImpacts))
	for _, impact := range compiled.permissionImpacts {
		impactByObjectType[impact.ObjectTypeID] = impact
	}
	for _, objectTypeID := range objectTypeIDs {
		objectType := compiled.schema.objectTypesByID[objectTypeID]
		if objectType == nil {
			// The plan only names object types it resolved from this schema.
			return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
		}
		levels, err := s.objectTypeLevels(ctx, knID, compiled.schema, objectType)
		if err != nil {
			return err
		}
		properties := byObjectType[objectTypeID]
		sort.Strings(properties)
		var deniedProperties []string
		for _, property := range properties {
			switch levels[property] {
			case interfaces.PROPERTY_ACCESS_FULL:
			case interfaces.PROPERTY_ACCESS_NONE:
				// Binding reports this as unknown before a plan exists; should
				// that ever change, the answer must still be the unknown one.
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_InvalidQuery).
					WithErrorDetails(compiled.schema.unknownProperty(objectType, property).Error())
			default:
				// Masked, schema, or a property bkn-safe gave no level for.
				denied = append(denied, objectTypeID+"."+property)
				deniedProperties = append(deniedProperties, property)
			}
		}
		if len(deniedProperties) > 0 {
			impact := impactByObjectType[objectTypeID]
			impact.ObjectTypeID = objectTypeID
			impact.Properties = deniedProperties
			impactByObjectType[objectTypeID] = impact
		}
	}
	if len(denied) == 0 {
		return nil
	}
	impactObjectTypeIDs := make([]string, 0, len(impactByObjectType))
	for objectTypeID := range impactByObjectType {
		impactObjectTypeIDs = append(impactObjectTypeIDs, objectTypeID)
	}
	sort.Strings(impactObjectTypeIDs)
	impacts := make([]interfaces.CypherPermissionImpact, 0, len(impactObjectTypeIDs))
	for _, objectTypeID := range impactObjectTypeIDs {
		impacts = append(impacts, impactByObjectType[objectTypeID])
	}
	localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Cypher.PropertyForbidden.Detail.NotFull",
		map[string]any{"properties": strings.Join(denied, ", ")})
	if renderErr != nil {
		logger.Errorf("localized service message failed: %v", renderErr)
	}
	return httperrors.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_Cypher_PropertyForbidden).
		WithErrorDetails(localizedText).
		WithMetadata(map[string]any{"permission_impacts": impacts})
}

func rowFilterPermissionImpacts(objectTypeIDs []string) []interfaces.CypherPermissionImpact {
	impacts := make([]interfaces.CypherPermissionImpact, 0, len(objectTypeIDs))
	for _, objectTypeID := range objectTypeIDs {
		impacts = append(impacts, interfaces.CypherPermissionImpact{
			ObjectTypeID: objectTypeID, RowFilterApplied: true,
		})
	}
	return impacts
}

// Span attributes saying which identity answered a statement and, when the
// caller was refused and the proxy could not be tried, why. They are recorded
// on the query span.
const (
	spanAttrExecutionPath  = "cypher.execution_path"
	spanAttrFallbackReason = "cypher.fallback_reason"
	spanAttrExecutor       = "cypher.executor"
	spanAttrFallback       = "cypher.fallback"
	spanAttrGraph          = "cypher.graph_values"
	spanAttrExplain        = "cypher.explain"

	executionPathProxy  = "proxy"
	executionPathCaller = "caller"

	fallbackNoResolver          = "no_resolver"
	fallbackNonMainBranch       = "non_main_branch"
	fallbackBindingNotPublished = "binding_not_published"
	fallbackProxyUnavailable    = "proxy_unavailable"

	// fallbackSQLReadBudget records a bind-join query that ran as SQL because
	// it would have read past the executor's budget.
	fallbackSQLReadBudget = "sql_read_budget"
	// fallbackSQLTimeout records one that ran as SQL because the executor did
	// not finish within its own timeout.
	fallbackSQLTimeout = "sql_timeout"
	// fallbackNonSQLSource records a query that ran on the bind-join
	// executor because its resources are not in one SQL data source.
	fallbackNonSQLSource = "non_sql_source"
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
	started := time.Now()
	response, err := s.vba.RawQuery(ctx, &callerRequest)
	if err == nil || !vegaRefused(err) {
		span.SetAttributes(attribute.String(spanAttrExecutionPath, executionPathCaller))
		logExecutionFailure(query.KNID, plan, request, time.Since(started), err)
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
	started = time.Now()
	response, err = s.vba.RawQueryAs(ctx, proxy.account, &proxyRequest)
	logExecutionFailure(query.KNID, plan, request, time.Since(started), err)
	return response, err
}

// executor is the configured way to run a compiled query.
func (s *cypherQueryService) executor() string {
	if s.appSetting != nil && s.appSetting.ServerSetting.CypherExecutor == common.CypherExecutorBindJoin {
		return common.CypherExecutorBindJoin
	}
	return common.CypherExecutorSQL
}

// queryBindJoin runs a compiled query through the bind-join executor, which
// reads each table through vega-backend's resource data API. Identity follows
// the same rule as the SQL path: the caller first, and the knowledge network's
// proxy account only when vega-backend refused the caller and the proxy can
// serve every binding the plan reads through. A refusal partway through
// restarts the whole query under the proxy, so no result mixes identities.
//
// What the executor cannot answer -- a query past its read budget, or one it
// cannot finish within its own timeout -- falls back to the compiled MySQL
// statement, so the switch never takes away a query the SQL path answers.
// Nothing else falls back: a refusal is the same under either path, a caller
// that went away is not served by starting over, and any other failure is
// reported rather than masked by a second attempt somewhere else.
func (s *cypherQueryService) queryBindJoin(ctx context.Context, query interfaces.CypherQuery,
	compiled *compiledQuery) (*interfaces.CypherQueryResult, error) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String(spanAttrExecutor, common.CypherExecutorBindJoin))

	columns, entries, err := s.runBindJoin(ctx, query, compiled, compiled.plan, true)
	if errors.Is(err, errQueryOutOfTime) {
		return nil, queryTimeoutError(ctx)
	}
	var budget *ReadBudgetError
	if errors.As(err, &budget) {
		span.SetAttributes(attribute.String(spanAttrFallback, fallbackSQLReadBudget))
		logger.Infof("Cypher query on knowledge network %s falls back to SQL: %s", query.KNID, budget.Error())
		return s.querySQL(ctx, query, compiled, true)
	}
	if errors.Is(err, errBindJoinTimeout) {
		span.SetAttributes(attribute.String(spanAttrFallback, fallbackSQLTimeout))
		logger.Infof("Cypher query on knowledge network %s falls back to SQL: %v", query.KNID, err)
		return s.querySQL(ctx, query, compiled, true)
	}
	if err != nil {
		if !vegaRefused(err) {
			common.LogSafeError(ctx, "Cypher bind-join execution failed", err)
		}
		return nil, executionError(ctx, err)
	}

	return &interfaces.CypherQueryResult{
		Columns:           columns,
		Entries:           entries,
		TraceDescriptor:   compiled.traceDescriptor,
		PermissionImpacts: compiled.permissionImpacts,
	}, nil
}

// runBindJoin executes under the caller and, after a refusal the proxy can
// answer, again under the proxy. The whole execution shares one timeout; see
// bindJoinTimeoutFor.
func (s *cypherQueryService) runBindJoin(ctx context.Context, query interfaces.CypherQuery,
	compiled *compiledQuery, plan *Plan, fallsBack bool) ([]interfaces.RawQueryColumn, []map[string]any, error) {
	span := trace.SpanFromContext(ctx)
	timeout := s.bindJoinTimeoutFor(ctx, query, fallsBack)
	if timeout <= 0 {
		return nil, nil, errQueryOutOfTime
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Its own deadline, while the caller still waits, is the executor running
	// out of time rather than the request ending. A read cut off partway may
	// report it as a transport failure, so the deadline is read off the
	// context rather than off the error.
	timedOut := func(err error) error {
		if err != nil && ctx.Err() == context.DeadlineExceeded && parent.Err() == nil {
			return fmt.Errorf("%w after %s", errBindJoinTimeout, timeout)
		}
		return err
	}
	readAs := func(account interfaces.AccountInfo) dataReader {
		return func(ctx context.Context, resourceID string,
			params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
			return s.vba.QueryResourceDataAs(ctx, account, resourceID, params)
		}
	}

	caller, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	columns, entries, err := runBindJoin(ctx, plan, compiled.schema, readAs(caller),
		defaultBindJoinLimits, interfaces.CYPHER_DEFAULT_LIMIT)
	if err == nil || !vegaRefused(err) {
		span.SetAttributes(attribute.String(spanAttrExecutionPath, executionPathCaller))
		return columns, entries, timedOut(err)
	}

	proxy, reason := s.resolveProxy(ctx, query, compiled.plan)
	if proxy == nil {
		span.SetAttributes(
			attribute.String(spanAttrExecutionPath, executionPathCaller),
			attribute.String(spanAttrFallbackReason, reason),
		)
		return nil, nil, err
	}
	logger.Infof("Cypher query on knowledge network %s runs under the knowledge network proxy: "+
		"caller %s, proxy account %s, resources %s",
		query.KNID, caller.ID, proxy.account.ID, strings.Join(proxy.resourceIDs, ","))
	span.SetAttributes(attribute.String(spanAttrExecutionPath, executionPathProxy))
	columns, entries, err = runBindJoin(ctx, plan, compiled.schema, readAs(proxy.account),
		defaultBindJoinLimits, interfaces.CYPHER_DEFAULT_LIMIT)
	return columns, entries, timedOut(err)
}

// errBindJoinTimeout is the bind-join executor running out of its own time
// while the caller still waits.
var errBindJoinTimeout = errors.New("the bind-join executor ran out of time")

// errQueryOutOfTime is the query's own time already spent before a statement
// could start, as the later parts of a UNION may find it.
var errQueryOutOfTime = errors.New("the query ran out of time")

// vegaRefused reports vega-backend refusing a resource the statement reads.
func vegaRefused(err error) bool {
	var dependencyErr *interfaces.DependencyError
	return errors.As(err, &dependencyErr) && dependencyErr.HTTPStatus == http.StatusForbidden
}

// configuredTimeoutSec is how long one statement may run unless a request asks
// for less, held to what vega-backend accepts.
func (s *cypherQueryService) configuredTimeoutSec() int {
	seconds := 0
	if s.appSetting != nil {
		seconds = s.appSetting.ServerSetting.CypherQueryTimeoutSec
	}
	if seconds <= 0 {
		return interfaces.CYPHER_DEFAULT_TIMEOUT_SEC
	}
	return min(seconds, interfaces.CYPHER_MAX_TIMEOUT_SEC)
}

// requestTimeoutSec is how long one query may take as a whole: what it asked
// for, or the configured timeout. validateTimeout has already held what it
// asked for to the configured one.
func (s *cypherQueryService) requestTimeoutSec(query interfaces.CypherQuery) int {
	if query.TimeoutSec > 0 {
		return query.TimeoutSec
	}
	return s.configuredTimeoutSec()
}

// queryDeadlineKey carries, on the context, when the query's time runs out.
type queryDeadlineKey struct{}

func (s *cypherQueryService) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// remaining is how much of the query's time is left. A context Query did not
// set a deadline on has the whole of it.
func (s *cypherQueryService) remaining(ctx context.Context, query interfaces.CypherQuery) time.Duration {
	if deadline, ok := ctx.Value(queryDeadlineKey{}).(time.Time); ok {
		return deadline.Sub(s.clock())
	}
	return time.Duration(s.requestTimeoutSec(query)) * time.Second
}

// statementTimeoutSec is the query_timeout_sec one statement is sent with:
// what is left of the query's time, rounded up to vega-backend's whole
// seconds. With nothing left the query has timed out, and nothing is sent.
func (s *cypherQueryService) statementTimeoutSec(ctx context.Context, query interfaces.CypherQuery) (int, error) {
	left := s.remaining(ctx, query)
	if left <= 0 {
		return 0, queryTimeoutError(ctx)
	}
	return int((left + time.Second - 1) / time.Second), nil
}

// bindJoinTimeoutFor is how long the bind-join executor may run. When a
// timeout sends the query on to SQL, the executor takes half of what is left
// so that the SQL statement still has time to answer within the request's
// timeout; when nothing follows it, it may take all of it.
func (s *cypherQueryService) bindJoinTimeoutFor(ctx context.Context, query interfaces.CypherQuery,
	fallsBack bool) time.Duration {
	if s.bindJoinTimeout > 0 {
		return s.bindJoinTimeout
	}
	left := s.remaining(ctx, query)
	if fallsBack {
		return left / 2
	}
	return left
}

func queryTimeoutError(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusGatewayTimeout, berrors.BknBackend_Cypher_QueryTimeout)
}

// validateTimeout refuses a timeout the service would not honor rather than
// clamping it, so a caller asking for more time is told it will not get it.
func (s *cypherQueryService) validateTimeout(ctx context.Context, seconds int) error {
	maxSeconds := s.configuredTimeoutSec()
	if seconds >= 0 && seconds <= maxSeconds {
		return nil
	}
	localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Cypher.InvalidParameter.Detail.TimeoutOutOfRange",
		map[string]any{"max": maxSeconds})
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
	}
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_InvalidParameter).
		WithErrorDetails(localizedText)
}

// dependencyTimedOut reports a statement the data source did not finish in
// time: vega-backend stopping it at the statement's timeout, or the request to
// vega-backend itself running out of time.
func dependencyTimedOut(err error) bool {
	var dependencyErr *interfaces.DependencyError
	return errors.As(err, &dependencyErr) && dependencyErr.Kind == interfaces.DependencyTimeout
}

// logExecutionFailure records a statement the data source failed to run, with
// what is needed to tell a slow source from a broken one: the resources it
// reads, how long it ran and how it failed. The statement itself is left out
// since it names physical tables and columns. A refusal is not logged here; it
// is recorded on the span with the proxy decision.
func logExecutionFailure(knID string, plan *Plan, request interfaces.RawQueryRequest,
	elapsed time.Duration, err error) {
	if err == nil || vegaRefused(err) || unsupportedSource(err) {
		return
	}
	kind, status := interfaces.DependencyErrorKind("unknown"), 0
	var dependencyErr *interfaces.DependencyError
	if errors.As(err, &dependencyErr) {
		kind, status = dependencyErr.Kind, dependencyErr.HTTPStatus
	}
	logger.Warnf("Cypher query on knowledge network %s failed after %s: kind=%s status=%d timeout_sec=%d resources=%s",
		knID, elapsed.Round(time.Millisecond), kind, status, request.QueryTimeoutSec,
		strings.Join(planResourceIDs(plan), ","))
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
// type it joins over, bound to the resources at both of its ends and, for an
// indirect relation, to its backing resource. That is how the published model
// grants the proxy account query_data, so every resource the statement
// references is covered by one of them.
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
		if table.Backing {
			// Read through the relation it backs, below.
			continue
		}
		add(interfaces.MODULE_TYPE_OBJECT_TYPE, table.ObjectTypeID, table.ResourceID)
	}
	for _, join := range plan.Joins {
		add(interfaces.MODULE_TYPE_RELATION_TYPE, join.RelationTypeID, plan.Tables[join.Left].ResourceID)
		add(interfaces.MODULE_TYPE_RELATION_TYPE, join.RelationTypeID, plan.Tables[join.Right].ResourceID)
		if join.Backing != nil {
			add(interfaces.MODULE_TYPE_RELATION_TYPE, join.RelationTypeID, plan.Tables[join.Backing.Table].ResourceID)
		}
	}
	for _, sub := range plan.subPlans() {
		for _, binding := range proxyBindings(sub) {
			add(binding.ChildType, binding.ChildID, binding.TargetID)
		}
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
	for _, sub := range plan.subPlans() {
		for _, resourceID := range planResourceIDs(sub) {
			if !seen[resourceID] {
				seen[resourceID] = true
				resourceIDs = append(resourceIDs, resourceID)
			}
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
	var httpErr *httperrors.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.ErrorCode
	}
	return "proxy resolution failed"
}

// executionError turns a failed run into what the caller sees. The statement
// names physical tables and columns and the dependency's message may quote it,
// so only a code travels back: forbidden when vega-backend refused a resource
// the statement reads, so a missing grant reads as one, and a query failure
// for anything else.
// resourceSource is where one resource lives: its category and its catalog.
type resourceSource struct {
	category string
	catalog  string
}

// nonSQLCategories are the resource categories vega-backend cannot read as
// SQL; a table or a logical view is read through a SQL connector.
var nonSQLCategories = map[string]bool{
	"dataset": true, "index": true, "file": true, "fileset": true, "api": true, "metric": true, "topic": true,
}

// outsideSQL reports a plan whose resources are known not to be readable as
// one SQL statement: one of them is not in a SQL data source, or they are in
// more than one. Where a resource lives is read from vega-backend once and
// remembered; it only chooses the path, and the read itself is authorized as
// any other. A resource the caller may not look up stays unknown, and the
// statement is tried as SQL, which refuses it the same way.
func (s *cypherQueryService) outsideSQL(ctx context.Context, plan *Plan) bool {
	ids := planResourceIDs(plan)
	var missing []string
	for _, id := range ids {
		if _, known := s.resourceSources.Load(id); !known {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		resources, err := s.vba.GetResourcesByIDs(ctx, missing)
		if err != nil {
			logger.Infof("Cypher could not look up where %d resources live; trying SQL: %s",
				len(missing), common.SafeErrorSummary(err))
		}
		for _, resource := range resources {
			if resource != nil {
				s.resourceSources.Store(resource.ID, resourceSource{category: resource.Category, catalog: resource.CatalogID})
			}
		}
	}
	catalogs := map[string]bool{}
	for _, id := range ids {
		value, known := s.resourceSources.Load(id)
		if !known {
			continue
		}
		source := value.(resourceSource)
		if nonSQLCategories[source.category] {
			return true
		}
		catalogs[source.catalog] = true
	}
	return len(catalogs) > 1
}

// outsideSQLTooLarge refuses a query the data API path could not finish in
// its read budget or its time. It is the read budget's code, so a caller can
// tell "narrow the query" from "rewrite it".
func outsideSQLTooLarge(ctx context.Context) error {
	localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Cypher.InvalidParameter.Detail.NonSQLSourceTooLarge", nil)
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
	}
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_ReadBudgetExceeded).
		WithErrorDetails(localizedText)
}

// unsupportedSource reports a statement vega-backend could not run as SQL
// because of where its resources live: a dataset, an index, or more than one
// data source.
func unsupportedSource(err error) bool {
	var dependencyErr *interfaces.DependencyError
	return errors.As(err, &dependencyErr) && dependencyErr.Kind == interfaces.DependencyUnsupportedSource
}

// queryOutsideSQL answers a query whose resources vega-backend cannot read as
// SQL through the resource data API the bind-join executor reads, which
// serves datasets and indexes and joins across data sources. That executor
// takes no new capabilities, so what only the SQL path answers -- CASE,
// coalesce, totals over groups, comparing two lists, passing nodes on and
// graph values -- is refused, saying why.
func (s *cypherQueryService) queryOutsideSQL(ctx context.Context, query interfaces.CypherQuery,
	compiled *compiledQuery) (*interfaces.CypherQueryResult, error) {
	if compiled.plan.SQLOnly || !graphOutsideSQL(compiled.graph) {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Cypher.InvalidParameter.Detail.NonSQLSource", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_Unsupported).
			WithErrorDetails(localizedText)
	}
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String(spanAttrExecutor, common.CypherExecutorBindJoin),
		attribute.String(spanAttrFallback, fallbackNonSQLSource),
		attribute.Bool(spanAttrGraph, compiled.graph != nil))
	plan := compiled.plan
	if compiled.graph != nil {
		// The statement a graph value is read through is a plan of plain
		// columns; the values are built from them as the SQL path builds
		// them, so the executor runs nothing it does not already run.
		plan = compiled.graph.flat
	}
	columns, entries, err := s.runBindJoin(ctx, query, compiled, plan, false)
	if errors.Is(err, errQueryOutOfTime) {
		return nil, queryTimeoutError(ctx)
	}
	var budget *ReadBudgetError
	if errors.As(err, &budget) || errors.Is(err, errBindJoinTimeout) {
		return nil, outsideSQLTooLarge(ctx)
	}
	if err != nil {
		if !vegaRefused(err) {
			common.LogSafeError(ctx, "Cypher query outside SQL failed", err)
		}
		return nil, executionError(ctx, err)
	}
	if compiled.graph != nil {
		return s.graphResult(ctx, compiled, entries)
	}
	return &interfaces.CypherQueryResult{
		Columns:           columns,
		Entries:           entries,
		TraceDescriptor:   compiled.traceDescriptor,
		PermissionImpacts: compiled.permissionImpacts,
	}, nil
}

// graphOutsideSQL reports a graph read the bind-join executor can serve: none,
// or one read as flat rows. One with collect selects its conclusions through a
// derived table first, which only the SQL path has.
func graphOutsideSQL(read *graphRead) bool {
	return read == nil || (!read.evidence && read.flat != nil && !read.flat.SQLOnly)
}

func executionError(ctx context.Context, err error) error {
	if vegaRefused(err) {
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_Cypher_Forbidden)
	}
	if dependencyTimedOut(err) {
		return queryTimeoutError(ctx)
	}
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_QueryFailed)
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
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Cypher.InvalidParameter.Detail.QueryRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_InvalidParameter).
			WithErrorDetails(localizedText)
	}
	if len(query) > MaxQueryLength {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Cypher.InvalidParameter.Detail.QueryTooLong", map[string]any{"max": MaxQueryLength})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_InvalidParameter).
			WithErrorDetails(localizedText)
	}
	return nil
}

// conditionsAsBooleans turns the 1, 0 and NULL MySQL returns for a condition
// column back into the true, false and null the executor returns, so the
// answer reads the same whichever path produced it.
func conditionsAsBooleans(plan *Plan, columns []interfaces.RawQueryColumn, entries []map[string]any) {
	if plan.Union != nil {
		// Every part returns the same columns; the first says which are
		// conditions.
		plan = plan.Union.Parts[0]
	}
	for _, column := range plan.Select {
		if _, ok := column.Expression.(PlanConditionValue); !ok {
			continue
		}
		for i := range columns {
			if columns[i].Name == column.Alias {
				columns[i].Type = "boolean"
			}
		}
		for _, entry := range entries {
			value, present := entry[column.Alias]
			if !present || value == nil {
				continue
			}
			if _, isBool := value.(bool); isBool {
				continue
			}
			if n, ok := asNumber(value); ok {
				entry[column.Alias] = n.float() != 0
			}
		}
	}
}

// queryUnion runs each part of a UNION as the query it would be on its own,
// wherever its data lives, and puts the rows together: all of them for UNION
// ALL, each distinct row once for UNION.
func (s *cypherQueryService) queryUnion(ctx context.Context, query interfaces.CypherQuery,
	compiled *compiledQuery) (*interfaces.CypherQueryResult, error) {
	result := &interfaces.CypherQueryResult{
		Entries:           []map[string]any{},
		TraceDescriptor:   compiled.traceDescriptor,
		PermissionImpacts: compiled.permissionImpacts,
	}
	seen := map[string]bool{}
	for i, part := range compiled.parts {
		if len(result.Entries) >= interfaces.CYPHER_MAX_LIMIT {
			// The parts together may hold more rows than one query returns;
			// the rest are not read, and the caller is told.
			result.Limited = true
			break
		}
		got, err := s.run(ctx, query, part)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			result.Columns, result.ValueTypes = got.Columns, got.ValueTypes
		}
		// A part that wrote no LIMIT and filled the default may have more
		// rows, as a query on its own would say.
		if got.Limited || (part.plan.Limit == nil && len(got.Entries) >= interfaces.CYPHER_DEFAULT_LIMIT) {
			result.Limited = true
		}
		for _, entry := range got.Entries {
			if len(result.Entries) >= interfaces.CYPHER_MAX_LIMIT {
				result.Limited = true
				break
			}
			if !compiled.plan.Union.All {
				values := make([]any, len(result.Columns))
				for j, column := range result.Columns {
					values[j] = entry[column.Name]
				}
				key := canonicalTuple(values)
				if seen[key] {
					continue
				}
				seen[key] = true
			}
			result.Entries = append(result.Entries, entry)
		}
	}
	return result, nil
}
