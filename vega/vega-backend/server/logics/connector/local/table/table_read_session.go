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
	cancelTx             context.CancelFunc
	query                SQLStatement
	count                *SQLStatement
	timeout              time.Duration
	cancelRows           context.CancelFunc
	rows                 *sql.Rows
	columns              []interfaces.ColumnInfo
	scanColumns          []interfaces.ColumnInfo
	total                *int64
	pending              map[string]any
	eof                  bool
	closed               bool
	structured           bool
	detailQuery          bool
	declaredProjection   bool
	binaryContentColumns map[string]bool
	localTimeColumns     map[string]bool
	fields               map[string]*interfaces.Property
	binaryMode           *string
}

// OpenReadSession 保存查询并开启可选读事务。结构化查询可以提供方言确定的完整投影。
// 未提供显式投影时使用资源字段生成已知列，表达式列使用驱动报告的类型。
// 调用方提供事务和查询的执行上下文，并负责让其在需要跨页时保持有效。
func OpenReadSession(ctx context.Context, connector tableSessionConnector, query SQLStatement,
	count *SQLStatement, resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams,
	resultFields map[string]*interfaces.Property, localTimeColumns map[string]bool, timeout time.Duration) (interfaces.QueryPageSession, error) {

	if timeout <= 0 && params != nil {
		timeout = params.Timeout
	}
	session := &tableReadSession{
		connector: connector,
		query:     query,
		count:     count,
		timeout:   timeout,
	}

	if connector.SupportsReadTransaction() {
		if err := session.beginReadTx(ctx); err != nil {
			return nil, fmt.Errorf("open read transaction: %w", err)
		}
	}

	if params != nil {
		session.structured = true
		session.detailQuery = isDetailQuery(params)
		session.declaredProjection = resultFields != nil
		session.fields = resultFields
		if resultFields == nil {
			session.fields = StructuredResultFields(resource, params)
		}
		session.binaryContentColumns = QueryBinaryContentColumns(resource, params)
		session.localTimeColumns = localTimeColumns
		if params.BinaryMode != nil {
			mode := *params.BinaryMode
			session.binaryMode = &mode
		}
	}
	return session, nil
}

func (s *tableReadSession) beginReadTx(ctx context.Context) error {
	var timer *time.Timer
	if s.timeout > 0 {
		// 计时仅覆盖连接和事务开启，事务本身可跨页使用。
		ctx, s.cancelTx = context.WithCancel(ctx)
		timer = time.AfterFunc(s.timeout, s.cancelTx)
	}
	tx, err := s.connector.BeginReadTx(ctx)
	timedOut := timer != nil && !timer.Stop()
	s.tx = tx
	if err != nil {
		return errors.Join(err, s.Close())
	}
	if timedOut {
		s.cancelTx()
		return errors.Join(context.DeadlineExceeded, s.Close())
	}
	return nil
}

// ExecuteSQLBuilder 对一次立即完成的 Builder 查询应用独立超时，覆盖查询和结果读取。
func ExecuteSQLBuilder(ctx context.Context, connector interfaces.TableConnector,
	query *sb.SelectBuilder, timeout time.Duration) (*interfaces.RawQueryResponse, error) {

	if timeout <= 0 {
		return connector.ExecuteSQLBuilder(ctx, query)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := connector.ExecuteSQLBuilder(ctx, query)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *tableReadSession) ReadTotal(ctx context.Context) (*int64, error) {
	if s.closed {
		return nil, sql.ErrTxDone
	}
	if s.count == nil || s.total != nil {
		return s.total, nil
	}
	if s.rows != nil {
		return nil, errors.New("read total before reading table rows")
	}
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	row, err := s.connector.QueryRowContext(ctx, s.tx, s.count.Text, s.count.Args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute count query: %w", s.connector.ClassifyQueryError(err))
	}
	var total int64
	if err := row.Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to scan total: %w", s.connector.ClassifyQueryError(err))
	}
	if err := ctx.Err(); s.timeout > 0 && err != nil {
		return nil, err
	}
	s.total = &total
	return s.total, nil
}

func (s *tableReadSession) ReadPage(ctx context.Context, limit int) (*interfaces.QueryPage, error) {
	if s.closed {
		return nil, sql.ErrTxDone
	}
	if limit <= 0 {
		return nil, errors.New("page limit must be positive")
	}
	readCtx := ctx
	if s.rows == nil {
		// 提前关闭和读页超时都可取消源端结果集。
		readCtx, s.cancelRows = context.WithCancel(ctx)
	}
	if s.timeout <= 0 {
		return s.readPage(readCtx, limit)
	}
	pageCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	stopTimeout := context.AfterFunc(pageCtx, s.cancelRows)
	page, err := s.readPage(readCtx, limit)
	stopTimeout()
	if err != nil {
		return nil, err
	}
	if err := pageCtx.Err(); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return page, nil
}

func (s *tableReadSession) readPage(ctx context.Context, limit int) (*interfaces.QueryPage, error) {
	if s.rows == nil {
		if err := s.openRows(ctx); err != nil {
			return nil, errors.Join(err, s.Close())
		}
	}

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
	return page, nil
}

func (s *tableReadSession) openRows(ctx context.Context) error {
	rows, err := s.connector.QueryContext(ctx, s.tx, s.query.Text, s.query.Args...) //nolint:rowserrcheck // ReadPage 检查迭代错误，Close 释放 Rows。
	if err != nil {
		return fmt.Errorf("failed to execute SQL builder query: %w", s.connector.ClassifyQueryError(err))
	}
	s.rows = rows
	s.scanColumns, err = describeSQLRows(s.rows, s.connector, true)
	if err != nil {
		return err
	}
	s.columns = make([]interfaces.ColumnInfo, len(s.scanColumns))
	for i, column := range s.scanColumns {
		field := s.fields[column.Name]
		if field == nil && s.declaredProjection {
			return errors.New("unexpected structured projection column")
		}
		if field != nil && field.Type != "" {
			column.Type = field.Type
		}
		s.columns[i] = interfaces.ColumnInfo{Name: column.Name, Type: column.Type}
	}
	return nil
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
		if field := s.fields[name]; s.detailQuery && field != nil {
			value = normalizeTableValue(value, field.Type, s.binaryMode != nil)
		}
		row[name] = value
	}
	return row, nil
}

func (s *tableReadSession) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cancelTx != nil {
		defer s.cancelTx()
	}
	if s.cancelRows != nil {
		if s.eof {
			defer s.cancelRows()
		} else {
			// 先取消未读完的查询，避免驱动为了关闭结果集而继续读剩余行。
			s.cancelRows()
		}
	}
	var rowsErr error
	if s.rows != nil {
		rowsErr = s.rows.Close()
		s.rows = nil
	}
	if s.tx == nil {
		return rowsErr
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

// StructuredResultFields 返回实际投影中可直接沿用资源定义的字段。
// 聚合和日历表达式的类型由驱动报告，或由构建表达式的连接器补充。
func StructuredResultFields(resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) map[string]*interfaces.Property {
	fields := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
	var selected map[string]bool
	if !isDetailQuery(params) {
		selected = make(map[string]bool, len(params.GroupBy))
		for _, group := range params.GroupBy {
			if group != nil && group.CalendarInterval == "" {
				selected[group.Property] = true
			}
		}
	} else if len(params.OutputFields) > 0 {
		selected = make(map[string]bool, len(params.OutputFields))
		for _, name := range params.OutputFields {
			selected[name] = true
		}
	}
	for _, field := range resource.SchemaDefinition {
		if field != nil && (selected == nil || selected[field.Name]) {
			cloned := *field
			fields[field.Name] = &cloned
		}
	}
	return fields
}

func isDetailQuery(params *interfaces.ResourceDataQueryParams) bool {
	return params.Aggregation == nil && len(params.GroupBy) == 0 && params.Having == nil
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
