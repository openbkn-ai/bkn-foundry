// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
)

// SQLBuilderExecutor 在源端执行公共 Builder，旧连接器无需实现此端口。
type SQLBuilderExecutor interface {
	ExecuteSQLBuilder(context.Context, *sqlbuilder.SelectBuilder) (*RawQueryResponse, error)
}

// SQLQueryConnector 消费标准 Bound IR 查询对象，不产生另一份 Builder 或 SQL 制品。
// 服务层负责解析、条件转换、授权、绑定和目标规划；Connector Prepare 只检查能力。
type SQLQueryConnector interface {
	Prepare(ctx context.Context, query *querymodel.SelectBuilder) error
	Execute(ctx context.Context, query *querymodel.SelectBuilder,
		read querymodel.SQLReadRequest) (SQLResultStream, error)
}

// SQLResultStream 从 Schema 开始约束有序列，Next 以 io.EOF 表示本次读取结束。
// Completion 仅在正常 EOF 后有效；Close 必须释放本次读取资源且可重复调用。
type SQLResultStream interface {
	Schema() querymodel.Schema
	Next(ctx context.Context) (querymodel.Batch, error)
	Completion() querymodel.SQLReadCompletion
	Close() error
}
