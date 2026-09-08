// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_data_stats

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
	"bkn-backend/logics/object_type"
	"bkn-backend/logics/permission"
)

type objectDataStatsService struct {
	appSetting *common.AppSetting
	ps         interfaces.PermissionService
	ots        interfaces.ObjectTypeService
	vba        interfaces.VegaBackendAccess
}

var (
	serviceOnce sync.Once
	serviceInst interfaces.ObjectDataStatsService
)

// NewObjectDataStatsService creates the object data statistics service.
func NewObjectDataStatsService(appSetting *common.AppSetting) interfaces.ObjectDataStatsService {
	serviceOnce.Do(func() {
		serviceInst = &objectDataStatsService{
			appSetting: appSetting,
			ps:         permission.NewPermissionService(appSetting),
			ots:        object_type.NewObjectTypeService(appSetting),
			vba:        logics.VBA,
		}
	})
	return serviceInst
}

// NewObjectDataStatsServiceWith builds the service from injected dependencies (for testing).
func NewObjectDataStatsServiceWith(ps interfaces.PermissionService, ots interfaces.ObjectTypeService,
	vba interfaces.VegaBackendAccess) interfaces.ObjectDataStatsService {
	return &objectDataStatsService{appSetting: &common.AppSetting{}, ps: ps, ots: ots, vba: vba}
}

// ObjectDataStats counts the data behind two object types, one per side of a comparison.
//
// Comparing the schema of two networks says what the model claims; this says what is actually
// behind it. Two counts is the whole of it on purpose: per-column statistics cost another pass
// over the table for every column, and this runs while someone waits.
func (s *objectDataStatsService) ObjectDataStats(ctx context.Context,
	req interfaces.ObjectDataStatsRequest) (*interfaces.ObjectDataStatsResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "对象类数据统计")
	defer span.End()

	base, err := s.sideStats(ctx, req.Base)
	if err != nil {
		return nil, err
	}
	target, err := s.sideStats(ctx, req.Target)
	if err != nil {
		return nil, err
	}

	result := &interfaces.ObjectDataStatsResult{
		Base:         base,
		Target:       target,
		SameResource: base.ResourceID != "" && base.ResourceID == target.ResourceID,
		Delta: interfaces.ObjectDataStatsDelta{
			RowCount: target.RowCount - base.RowCount,
		},
	}
	if base.PrimaryKeyDistinct != nil && target.PrimaryKeyDistinct != nil {
		delta := *target.PrimaryKeyDistinct - *base.PrimaryKeyDistinct
		result.Delta.PrimaryKeyDistinct = &delta
	}

	span.SetStatus(codes.Ok, "")
	return result, nil
}

// sideStats authorizes, resolves and counts one side.
func (s *objectDataStatsService) sideStats(ctx context.Context,
	ref interfaces.ObjectTypeRef) (*interfaces.ObjectDataStats, error) {
	branch := ref.Branch
	if branch == "" {
		branch = interfaces.MAIN_BRANCH
	}

	// Reading how much data sits behind an object type is reading the data, not the model. It is
	// gated on query_data for that reason; vega then authorizes the resource itself against the
	// same caller, so an object type bound to a resource this caller may not read is stopped there.
	if err := s.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   ref.KNID,
	}, []string{interfaces.OPERATION_TYPE_QUERY_DATA}); err != nil {
		return nil, err
	}

	objectType, err := s.ots.GetObjectTypeByID(ctx, nil, ref.KNID, branch, ref.OTID)
	if err != nil {
		return nil, err
	}
	if objectType == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KNDiff_ObjectTypeNotFound).
			WithErrorDetails(fmt.Sprintf("object type %q not found in knowledge network %q branch %q",
				ref.OTID, ref.KNID, branch))
	}
	if objectType.DataSource == nil || objectType.DataSource.ID == "" {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KNDiff_DataStatsUnavailable).
			WithErrorDetails(fmt.Sprintf("object type %q is not bound to a data resource", ref.OTID))
	}

	stats := &interfaces.ObjectDataStats{
		KNID:       ref.KNID,
		Branch:     branch,
		OTID:       objectType.OTID,
		OTName:     objectType.OTName,
		ResourceID: objectType.DataSource.ID,
	}

	keyColumns, err := primaryKeyColumns(ctx, objectType)
	if err != nil {
		return nil, err
	}
	stats.PrimaryKeys = objectType.PrimaryKeys

	statement, err := buildStatsSQL(ctx, objectType.DataSource.ID, keyColumns)
	if err != nil {
		return nil, err
	}

	response, err := s.vba.RawQuery(ctx, &interfaces.RawQueryRequest{
		Query:        statement,
		QueryFormat:  interfaces.VEGA_QUERY_FORMAT_SQL,
		InputDialect: interfaces.VEGA_DIALECT_MYSQL,
		Paging: interfaces.ResourceDataPagingRequest{
			Mode:  interfaces.VEGA_PAGING_MODE_SINGLE,
			Limit: 1,
		},
		QueryTimeoutSec: interfaces.OBJECT_DATA_STATS_TIMEOUT_SEC,
	})
	if err != nil {
		// The statement names physical tables and columns and the dependency's message may quote
		// it, so only the code travels back.
		logger.Errorf("object data stats query failed: kn_id=%s ot_id=%s err=%s", ref.KNID, ref.OTID, err.Error())
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_KNDiff_DataStatsQueryFailed)
	}
	if len(response.Entries) == 0 {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_KNDiff_DataStatsQueryFailed).
			WithErrorDetails("the aggregate returned no row")
	}

	row := response.Entries[0]
	stats.RowCount = asInt64(row[statsColumnRowCount])
	if len(keyColumns) > 0 {
		distinct := asInt64(row[statsColumnKeyDistinct])
		stats.PrimaryKeyDistinct = &distinct
		duplicates := stats.RowCount - distinct
		stats.DuplicateKeys = &duplicates
	}
	return stats, nil
}

// primaryKeyColumns maps the declared primary key properties to the physical columns they read
// from.
//
// The declared keys are property names, which are the model's own vocabulary; the statement has to
// name the columns underneath them. A key with no mapped column would silently produce a statement
// counting the wrong thing, so it fails instead.
func primaryKeyColumns(ctx context.Context, objectType *interfaces.ObjectType) ([]string, error) {
	if len(objectType.PrimaryKeys) == 0 {
		return nil, nil
	}

	columnByProperty := make(map[string]string, len(objectType.DataProperties))
	for _, property := range objectType.DataProperties {
		if property == nil || property.MappedField == nil {
			continue
		}
		columnByProperty[property.Name] = property.MappedField.Name
	}

	columns := make([]string, 0, len(objectType.PrimaryKeys))
	for _, key := range objectType.PrimaryKeys {
		column, ok := columnByProperty[key]
		if !ok || column == "" {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KNDiff_DataStatsUnavailable).
				WithErrorDetails(fmt.Sprintf("primary key %q has no mapped column", key))
		}
		columns = append(columns, column)
	}
	return columns, nil
}

const (
	statsColumnRowCount    = "row_count"
	statsColumnKeyDistinct = "primary_key_distinct"
)

// buildStatsSQL writes the aggregate for one resource.
//
// A composite key is concatenated with a separator rather than passed as several arguments to
// COUNT(DISTINCT ...): the multi-argument form is MySQL's own, and this statement is transpiled to
// whatever dialect the resource's connector speaks. Unit separator is the separator because it
// cannot appear in a key value that came out of a table column.
func buildStatsSQL(ctx context.Context, resourceID string, keyColumns []string) (string, error) {
	table := "{{." + resourceID + "}}"
	if len(keyColumns) == 0 {
		return fmt.Sprintf("SELECT COUNT(*) AS %s FROM %s", statsColumnRowCount, table), nil
	}

	quoted := make([]string, 0, len(keyColumns))
	for _, column := range keyColumns {
		identifier, err := quoteIdentifier(ctx, column)
		if err != nil {
			return "", err
		}
		quoted = append(quoted, identifier)
	}

	keyExpression := quoted[0]
	if len(quoted) > 1 {
		keyExpression = fmt.Sprintf("CONCAT_WS(CHAR(31), %s)", strings.Join(quoted, ", "))
	}
	return fmt.Sprintf("SELECT COUNT(*) AS %s, COUNT(DISTINCT %s) AS %s FROM %s",
		statsColumnRowCount, keyExpression, statsColumnKeyDistinct, table), nil
}

// quoteIdentifier writes a column name into the statement.
//
// Column names come from the model, which a user edits, so they are not trusted to be harmless
// SQL. Backticks are doubled and a null byte is refused outright rather than truncating the
// statement at the connector.
func quoteIdentifier(ctx context.Context, name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) {
		return "", rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KNDiff_DataStatsUnavailable).
			WithErrorDetails("a mapped column name is empty or contains a null byte")
	}
	return "`" + strings.ReplaceAll(name, "`", "``") + "`", nil
}

// asInt64 reads a count out of a driver-shaped value.
//
// The vega adapter decodes responses with UseNumber, so every number arrives as a json.Number and
// not as a float64. Missing that case is not a rounding problem: the switch falls through to zero,
// and a table with thirty thousand rows is reported as empty with no error anywhere. Connectors
// also hand back counts as int64, float64, a string or raw bytes depending on the driver, so all
// of them are accepted.
func asInt64(value any) int64 {
	switch v := value.(type) {
	case json.Number:
		if parsed, err := v.Int64(); err == nil {
			return parsed
		}
		if parsed, err := v.Float64(); err == nil {
			return int64(parsed)
		}
		return 0
	case int64:
		return v
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case string:
		var parsed int64
		if _, err := fmt.Sscanf(v, "%d", &parsed); err == nil {
			return parsed
		}
	case []byte:
		var parsed int64
		if _, err := fmt.Sscanf(string(v), "%d", &parsed); err == nil {
			return parsed
		}
	}
	return 0
}
