// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package table

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
)

type SQLStatement struct {
	Text string
	Args []any
}

type tableSessionConnector interface {
	interfaces.TableConnector
	interfaces.TableReadSessionConnector
}

type tableReadSession struct {
	connector            tableSessionConnector
	tx                   *sql.Tx
	rows                 *sql.Rows
	cancel               context.CancelFunc
	columns              []interfaces.ColumnInfo
	scanColumns          []interfaces.ColumnInfo
	total                *int64
	pending              map[string]any
	eof                  bool
	structured           bool
	fieldTypes           map[string]string
	binaryContentColumns map[string]bool
	localTimeColumns     map[string]bool
	fields               map[string]*interfaces.Property
	binaryMode           *string
}

// OpenReadSession 在连接器提供的同一个读事务中执行可选的计数并打开数据结果集。
// 结构化查询提供资源和查询参数，转换规则在会话打开时确定。
func OpenReadSession(ctx context.Context, connector tableSessionConnector, query SQLStatement,
	count *SQLStatement, resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams,
	localTimeColumns map[string]bool) (interfaces.QueryPageSession, error) {

	tx, err := connector.BeginReadTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("open read transaction: %w", err)
	}
	session := &tableReadSession{connector: connector, tx: tx}
	if params != nil {
		session.structured = true
		session.fieldTypes = queryResultFieldTypes(resource, params)
		session.fields = queryReadFields(resource, params.AggregateOutputAlias())
		session.binaryContentColumns = QueryBinaryContentColumns(resource, params)
		session.localTimeColumns = localTimeColumns
		if params.BinaryMode != nil {
			mode := *params.BinaryMode
			session.binaryMode = &mode
		}
	}
	opened := false
	defer func() {
		if !opened {
			_ = session.Close()
		}
	}()

	if count != nil {
		var total int64
		if err := tx.QueryRowContext(ctx, count.Text, count.Args...).Scan(&total); err != nil {
			return nil, fmt.Errorf("failed to scan total: %w", err)
		}
		session.total = &total
	}

	readCtx, cancel := context.WithCancel(ctx)
	session.cancel = cancel
	session.rows, err = tx.QueryContext(readCtx, query.Text, query.Args...) //nolint:rowserrcheck // Next 检查迭代错误，Close 负责释放结果集。
	if err != nil {
		return nil, fmt.Errorf("failed to execute SQL builder query: %w", err)
	}
	session.scanColumns, err = describeSQLRows(session.rows, connector, true)
	if err != nil {
		return nil, err
	}
	session.columns = make([]interfaces.ColumnInfo, len(session.scanColumns))
	for i, column := range session.scanColumns {
		session.columns[i] = interfaces.ColumnInfo{Name: column.Name, Type: column.Type}
	}
	opened = true
	return session, nil
}

func (s *tableReadSession) ReadPage(ctx context.Context, limit int) (*interfaces.QueryPage, error) {
	if s.rows == nil {
		return nil, sql.ErrTxDone
	}
	if limit <= 0 {
		return nil, errors.New("page limit must be positive")
	}

	stop := context.AfterFunc(ctx, s.cancel)
	defer stop()

	page := &interfaces.QueryPage{
		Columns:    s.columns,
		TotalCount: s.total,
		Entries:    make([]map[string]any, 0, limit),
	}
	if s.pending != nil {
		page.Entries = append(page.Entries, s.pending)
		s.pending = nil
	}
	for !s.eof {
		row, err := s.scanRow()
		if err != nil {
			return nil, err
		}
		if row == nil {
			if err := s.rows.Err(); err != nil {
				return nil, err
			}
			s.eof = true
			break
		}
		if len(page.Entries) == limit {
			s.pending = row
			page.HasMore = true
			break
		}
		page.Entries = append(page.Entries, row)
	}
	// Detach the request before accepting the page. Cancellation during a
	// buffered read can close Rows even when the scan itself returned a row.
	stop()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return page, nil
}

func (s *tableReadSession) scanRow() (map[string]any, error) {
	if !s.structured {
		return s.connector.ScanSQLBuilderRow(s.rows, s.scanColumns)
	}
	row, err := s.connector.ScanTableReadRow(s.rows, s.scanColumns, s.fields, s.binaryMode, s.binaryContentColumns)
	if err != nil {
		return nil, s.connector.ClassifyQueryError(err)
	}
	for name, value := range row {
		if timestamp, ok := value.(time.Time); ok && s.localTimeColumns[name] {
			value = timestamp.Local()
		}
		row[name] = normalizeTableValue(value, s.fieldTypes[name], s.binaryMode != nil)
	}
	return row, nil
}

func (s *tableReadSession) Close() error {
	if s.tx == nil {
		return nil
	}
	if !s.eof && s.cancel != nil {
		// 中途关闭时取消语句，避免驱动为了释放连接而继续读取剩余结果。
		s.cancel()
	}
	var rowsErr error
	if s.rows != nil {
		rowsErr = s.rows.Close()
		s.rows = nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	txErr := s.tx.Rollback()
	s.tx = nil
	if errors.Is(txErr, sql.ErrTxDone) {
		txErr = nil
	}
	return errors.Join(rowsErr, txErr)
}

// DescribeSQLRows 返回结果集列定义并检查列名是否唯一。
func DescribeSQLRows(rows *sql.Rows, connector interfaces.TableConnector) ([]interfaces.ColumnInfo, error) {
	return describeSQLRows(rows, connector, false)
}

func describeSQLRows(rows *sql.Rows, connector interfaces.TableConnector, includeNativeType bool) ([]interfaces.ColumnInfo, error) {
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("failed to read result column types: %w", err)
	}
	columns := make([]interfaces.ColumnInfo, len(types))
	names := make(map[string]bool, len(types))
	for i, column := range types {
		if names[column.Name()] {
			return nil, interfaces.NewConditionBuildError("query result contains duplicate column names; use explicit aliases")
		}
		names[column.Name()] = true
		columns[i] = interfaces.ColumnInfo{
			Name: column.Name(),
			Type: connector.MapType(column.DatabaseTypeName()),
		}
		if includeNativeType {
			columns[i].NativeType = column.DatabaseTypeName()
		}
	}
	return columns, nil
}

func queryReadFields(resource *interfaces.Resource, aggregateAlias string) map[string]*interfaces.Property {
	fields := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
	for _, field := range resource.SchemaDefinition {
		if field != nil && field.Name != aggregateAlias {
			cloned := *field
			fields[field.Name] = &cloned
		}
	}
	return fields
}

// StructuredCountQuery 计算结构化查询分页前的总行数。
func StructuredCountQuery(query *sb.SelectBuilder) *sb.SelectBuilder {
	unpaged := *query
	unpaged.LimitPart = nil
	unpaged.OffsetPart = 0
	unpaged.OrderByPart = nil
	return sb.Select(&sb.Function{
		Name: "COUNT",
		Args: []sb.SqlNode{&sb.Star{}},
	}).FromSelect(&unpaged, "_resource_total")
}

// queryResultFieldTypes 返回可按资源逻辑类型转换的明细查询字段。
func queryResultFieldTypes(resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) map[string]string {
	if params.Aggregation != nil || len(params.GroupBy) > 0 || params.Having != nil {
		return nil
	}
	fieldTypes := make(map[string]string, len(resource.SchemaDefinition))
	for _, field := range resource.SchemaDefinition {
		if field != nil {
			fieldTypes[field.Name] = field.Type
		}
	}
	return fieldTypes
}

// normalizeTableValue 应用结构化读会话的 JSON 和 Binary 输出规则。
func normalizeTableValue(value any, fieldType string, binaryRequested bool) any {
	switch fieldType {
	case interfaces.DataType_Json:
		return common.JSONSourceValueText(value)
	case interfaces.DataType_Binary:
		if !binaryRequested {
			return interfaces.ResourceValue{
				Mode:   interfaces.ResourceValueModeUnavailable,
				Reason: interfaces.ResourceValueReasonNotRequested,
			}
		}
	}
	return value
}
