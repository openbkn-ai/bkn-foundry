// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
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
	vba        interfaces.VegaBackendAccess
}

var (
	cypherQueryServiceOnce sync.Once
	cypherQueryServiceInst interfaces.CypherQueryService
)

func NewCypherQueryService(appSetting *common.AppSetting) interfaces.CypherQueryService {
	cypherQueryServiceOnce.Do(func() {
		cypherQueryServiceInst = &cypherQueryService{
			appSetting: appSetting,
			ps:         permission.NewPermissionService(appSetting),
			schema: NewSchemaSource(
				object_type.NewObjectTypeService(appSetting),
				relation_type.NewRelationTypeService(appSetting),
			),
			vba: logics.VBA,
		}
	})
	return cypherQueryServiceInst
}

// Query compiles a Cypher query against one knowledge network and runs the
// resulting statement through vega-backend.
//
// Authorization is checked twice on purpose, and neither check subsumes the
// other. Here it is query_data on the knowledge network, which is what the
// caller asked to read. In vega-backend it is view_detail per resource, using
// the caller's own identity, which is what the statement will actually touch.
// A model that binds an object type to a resource the caller cannot read is
// therefore stopped down there, not here.
func (s *cypherQueryService) Query(ctx context.Context, query interfaces.CypherQuery) (*interfaces.CypherQueryResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Compile and run Cypher query")
	defer span.End()

	if err := validateQueryText(ctx, query.Query); err != nil {
		return nil, err
	}
	if err := s.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   query.KNID,
	}, []string{interfaces.OPERATION_TYPE_QUERY_DATA}); err != nil {
		return nil, err
	}

	sql, rowLimit, err := s.compile(ctx, query)
	if err != nil {
		return nil, err
	}

	// The paging limit repeats the statement's own LIMIT rather than leaving
	// it unset. vega-backend wraps the statement and applies a page of its
	// own, defaulting to 20 rows, so a query asking for more would come back
	// cut down with nothing to say it had been.
	response, err := s.vba.RawQuery(ctx, &interfaces.RawQueryRequest{
		Query:        sql,
		QueryFormat:  interfaces.VEGA_QUERY_FORMAT_SQL,
		InputDialect: interfaces.VEGA_DIALECT_MYSQL,
		Paging: interfaces.ResourceDataPagingRequest{
			Mode:  interfaces.VEGA_PAGING_MODE_SINGLE,
			Limit: rowLimit,
		},
		QueryTimeoutSec: interfaces.CYPHER_DEFAULT_TIMEOUT_SEC,
	})
	if err != nil {
		// The statement carries physical table and column names and the
		// dependency's message may quote it, so only the code travels back.
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_QueryFailed)
	}

	return &interfaces.CypherQueryResult{
		Columns: response.Columns,
		Entries: response.Entries,
	}, nil
}

// compile runs the four stages and turns each stage's own error type into the
// status the caller should see. A query that is malformed, outside the subset,
// or does not fit the model is the caller's to fix, and says so with 400;
// anything else is ours.
func (s *cypherQueryService) compile(ctx context.Context, query interfaces.CypherQuery) (string, int, error) {
	tree, err := Parse(query.Query)
	if err != nil {
		var syntaxErrors SyntaxErrors
		if errors.As(err, &syntaxErrors) {
			return "", 0, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_SyntaxError).
				WithErrorDetails(syntaxErrors.Error())
		}
		return "", 0, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_SyntaxError).
			WithErrorDetails(err.Error())
	}

	analyzed, err := Analyze(tree)
	if err != nil {
		return "", 0, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_Unsupported).
			WithErrorDetails(err.Error())
	}
	if analyzed.Limit != nil && *analyzed.Limit > interfaces.CYPHER_MAX_LIMIT {
		// Refused rather than clamped: a caller who asked for more rows than
		// they will get should be told, not handed a short answer.
		return "", 0, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_LimitExceeded).
			WithErrorDetails(detail(ctx, "LimitExceeded", map[string]any{"max": interfaces.CYPHER_MAX_LIMIT}))
	}

	schema, err := LoadSchema(ctx, s.schema, query.KNID, query.Branch)
	if err != nil {
		return "", 0, err
	}

	plan, err := Compile(analyzed, schema)
	if err != nil {
		return "", 0, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_InvalidQuery).
			WithErrorDetails(err.Error())
	}

	sql, err := Generate(plan, GenerateOptions{
		Dialect:      DialectMySQL,
		DefaultLimit: interfaces.CYPHER_DEFAULT_LIMIT,
	})
	if err != nil {
		common.LogSafeError(ctx, "Cypher SQL generation failed", err)
		return "", 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Cypher_InternalError)
	}

	rowLimit := int64(interfaces.CYPHER_DEFAULT_LIMIT)
	if plan.Limit != nil {
		rowLimit = *plan.Limit
	}
	return sql, int(rowLimit), nil
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
