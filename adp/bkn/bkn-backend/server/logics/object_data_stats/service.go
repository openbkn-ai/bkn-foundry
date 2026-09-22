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
	"strconv"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/interfaces/data_type"
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
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Compare object type data stats")
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
	// gated on query_data for that reason, on the object type itself: a grant on the network
	// reaches it through the parent it inherits from, and a grant on this one object type alone
	// is enough, as it is for querying its instances. vega then authorizes the resource against
	// the same caller, so an object type bound to a resource this caller may not read is stopped
	// there.
	if err := s.ps.CheckPermission(ctx,
		interfaces.KNChildPermissionResource(interfaces.RESOURCE_TYPE_OBJECT_TYPE, ref.KNID, ref.OTID),
		[]string{interfaces.OPERATION_TYPE_QUERY_DATA}); err != nil {
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

	where, err := s.statsRowFilter(ctx, ref.KNID, objectType)
	if err != nil {
		return nil, err
	}
	statement, err := buildStatsSQLWithWhere(ctx, objectType.DataSource.ID, keyColumns, where)
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
		keyed := asInt64(row[statsColumnKeyedRows])
		distinct := asInt64(row[statsColumnKeyDistinct])
		missing := stats.RowCount - keyed
		duplicates := keyed - distinct
		stats.PrimaryKeyDistinct = &distinct
		stats.MissingKeys = &missing
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
	statsColumnKeyedRows   = "keyed_rows"
	statsColumnKeyDistinct = "primary_key_distinct"
)

// buildStatsSQL writes the aggregate for one resource.
//
// A composite key is concatenated with a separator rather than passed as several arguments to
// COUNT(DISTINCT ...): the multi-argument form is MySQL's own, and this statement is transpiled to
// whatever dialect the resource's connector speaks. Unit separator is the separator because it
// cannot appear in a key value that came out of a table column.
//
// Only rows whose key is complete take part in the distinct count. COUNT(DISTINCT col) skips a
// NULL on its own, but CONCAT_WS skips a NULL component and keeps the rest, so (a, NULL) and
// (NULL, a) would fold into one key; and either way a row without a key was left in the row count,
// where it read as a duplicate. The rows with a complete key are counted separately so a missing
// key and a repeated key come back as the two different faults they are.
func buildStatsSQL(ctx context.Context, resourceID string, keyColumns []string) (string, error) {
	return buildStatsSQLWithWhere(ctx, resourceID, keyColumns, "")
}

// buildStatsSQLWithWhere builds the aggregate over the caller's visible rows.
// The policy expression is compiler-produced from bkn-safe's restricted
// predicate grammar; it is never copied from a client request.
func buildStatsSQLWithWhere(ctx context.Context, resourceID string, keyColumns []string, where string) (string, error) {
	table := "{{." + resourceID + "}}"
	from := "FROM " + table
	if where != "" {
		from += " WHERE " + where
	}
	if len(keyColumns) == 0 {
		return fmt.Sprintf("SELECT COUNT(*) AS %s %s", statsColumnRowCount, from), nil
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
	present := make([]string, 0, len(quoted))
	for _, column := range quoted {
		present = append(present, column+" IS NOT NULL")
	}
	complete := strings.Join(present, " AND ")
	return fmt.Sprintf("SELECT COUNT(*) AS %s, COUNT(CASE WHEN %s THEN 1 END) AS %s, "+
		"COUNT(DISTINCT CASE WHEN %s THEN %s END) AS %s %s",
		statsColumnRowCount, complete, statsColumnKeyedRows,
		complete, keyExpression, statsColumnKeyDistinct, from), nil
}

// statsRowFilter resolves the effective policy for this side of the comparison
// and translates the small, validated policy grammar to a SQL WHERE clause.
// Data-statistics returns counts, so leaving it unfiltered would still expose
// rows which ordinary object reads hide.
func (s *objectDataStatsService) statsRowFilter(ctx context.Context, knID string,
	objectType *interfaces.ObjectType) (string, error) {
	ref := interfaces.KNChildResourceID(knID, objectType.OTID)
	entries, err := s.ps.ResolveRowFilters(ctx, []string{ref})
	if err != nil {
		return "", err
	}
	if len(entries) != 1 || entries[0].ObjectTypeRef != ref ||
		strings.TrimSpace(entries[0].EffectiveRowFilterDigest) == "" {
		return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KNDiff_DataStatsQueryFailed).WithErrorDetails("invalid row-filter decision")
	}
	where, err := compileStatsRowFilter(ctx, entries[0].Predicate, objectType)
	if err != nil {
		return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KNDiff_DataStatsQueryFailed).WithErrorDetails("invalid row-filter predicate")
	}
	return where, nil
}

func compileStatsRowFilter(ctx context.Context, predicate interfaces.RowFilterPredicate,
	objectType *interfaces.ObjectType) (string, error) {
	switch predicate.Kind {
	case "true":
		return "", nil
	case "false":
		return "1 = 0", nil
	case "in", "not_in":
		property, err := statsRowFilterProperty(objectType, predicate.Property)
		if err != nil {
			return "", err
		}
		column, err := quoteIdentifier(ctx, property.MappedField.Name)
		if err != nil {
			return "", err
		}
		values, err := statsRowFilterValues(predicate.Values, property.Type)
		if err != nil {
			return "", err
		}
		operator := " IN ("
		if predicate.Kind == "not_in" {
			operator = " NOT IN ("
		}
		return column + operator + strings.Join(values, ", ") + ")", nil
	case "gt", "gte", "lt", "lte":
		property, err := statsRowFilterProperty(objectType, predicate.Property)
		if err != nil {
			return "", err
		}
		column, err := quoteIdentifier(ctx, property.MappedField.Name)
		if err != nil {
			return "", err
		}
		values, err := statsRowFilterValues(predicate.Values, property.Type)
		if err != nil || len(values) != 1 {
			if err != nil {
				return "", err
			}
			return "", fmt.Errorf("comparison row-filter requires one value")
		}
		operators := map[string]string{"gt": ">", "gte": ">=", "lt": "<", "lte": "<="}
		return column + " " + operators[predicate.Kind] + " " + values[0], nil
	case "between":
		property, err := statsRowFilterProperty(objectType, predicate.Property)
		if err != nil {
			return "", err
		}
		column, err := quoteIdentifier(ctx, property.MappedField.Name)
		if err != nil {
			return "", err
		}
		values, err := statsRowFilterValues(predicate.Values, property.Type)
		if err != nil || len(values) != 2 {
			if err != nil {
				return "", err
			}
			return "", fmt.Errorf("between row-filter requires two values")
		}
		return "(" + column + " >= " + values[0] + " AND " + column + " <= " + values[1] + ")", nil
	case "and":
		parts := make([]string, 0, len(predicate.Predicates))
		for _, child := range predicate.Predicates {
			compiled, err := compileStatsRowFilter(ctx, child, objectType)
			if err != nil {
				return "", err
			}
			if compiled == "1 = 0" {
				return "1 = 0", nil
			}
			if compiled != "" {
				parts = append(parts, compiled)
			}
		}
		if len(parts) == 0 {
			return "", nil
		}
		if len(parts) == 1 {
			return parts[0], nil
		}
		return "(" + strings.Join(parts, " AND ") + ")", nil
	case "or":
		parts := make([]string, 0, len(predicate.Predicates))
		for _, child := range predicate.Predicates {
			compiled, err := compileStatsRowFilter(ctx, child, objectType)
			if err != nil {
				return "", err
			}
			if compiled == "" { // TRUE makes the whole OR true.
				return "", nil
			}
			if compiled != "1 = 0" {
				parts = append(parts, compiled)
			}
		}
		if len(parts) == 0 {
			return "1 = 0", nil
		}
		if len(parts) == 1 {
			return parts[0], nil
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	default:
		return "", fmt.Errorf("unsupported row-filter predicate %q", predicate.Kind)
	}
}

func statsRowFilterProperty(objectType *interfaces.ObjectType, name string) (*interfaces.DataProperty, error) {
	for _, property := range objectType.DataProperties {
		if property != nil && property.Name == name && property.MappedField != nil && property.MappedField.Name != "" {
			return property, nil
		}
	}
	return nil, fmt.Errorf("row-filter property %q is not a mapped data property", name)
}

func statsRowFilterValues(values []interfaces.RowFilterValue, propertyType string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("row-filter has no values")
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		switch value.Type {
		case "string":
			if value.String == nil || !statsRowFilterValueTypeAllowed(propertyType, value.Type) {
				return nil, fmt.Errorf("string value does not match property type")
			}
			literal, err := statsRowFilterStringLiteral(*value.String)
			if err != nil {
				return nil, err
			}
			result = append(result, literal)
		case "integer":
			if value.Integer == nil || !statsRowFilterValueTypeAllowed(propertyType, value.Type) {
				return nil, fmt.Errorf("integer value does not match property type")
			}
			result = append(result, strconv.FormatInt(*value.Integer, 10))
		case "boolean":
			if value.Boolean == nil || !statsRowFilterValueTypeAllowed(propertyType, value.Type) {
				return nil, fmt.Errorf("boolean value does not match property type")
			}
			if *value.Boolean {
				result = append(result, "TRUE")
			} else {
				result = append(result, "FALSE")
			}
		default:
			return nil, fmt.Errorf("unsupported row-filter value type")
		}
	}
	return result, nil
}

func statsRowFilterStringLiteral(value string) (string, error) {
	if strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("row-filter string value must not contain a null byte")
	}

	// Vega executes this filter as MySQL. Escape backslashes before quotes so a
	// backslash from a policy value cannot escape the following quote literal.
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, "'", "''")
	return "'" + escaped + "'", nil
}

func statsRowFilterValueTypeAllowed(propertyType, valueType string) bool {
	switch valueType {
	case "string":
		return data_type.SimpleTypeMapping[propertyType] == data_type.SimpleChar || data_type.DataType_IsString(propertyType)
	case "integer":
		return data_type.SimpleTypeMapping[propertyType] == data_type.SimpleInt
	case "boolean":
		return propertyType == data_type.DATATYPE_BOOLEAN || data_type.SimpleTypeMapping[propertyType] == data_type.SimpleBool
	default:
		return false
	}
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
