// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
)

// SQLBuilderExecutor 在源端执行公共 Builder，旧连接器无需实现此端口。
type SQLBuilderExecutor interface {
	ExecuteSQLBuilder(context.Context, *sqlbuilder.SelectBuilder) (*RawQueryResponse, error)
}

// SQLBuilderRowsExecutor 返回一次执行的结果集，由查询会话负责关闭 Rows 和连接器。
type SQLBuilderRowsExecutor interface {
	OpenSQLBuilder(context.Context, *sqlbuilder.SelectBuilder) (*sql.Rows, []ColumnInfo, error)
}
