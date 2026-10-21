// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

// SQLQueryConnector 负责 SQL 链路的 IR 编译和 SQL 执行。
// 关系数据库的 SQL/结构化查询可共用该端口，OpenSearch DSL 和文件服务保留原生路径。
type SQLQueryConnector interface {
	Prepare(ctx context.Context, req querymodel.SQLPrepareRequest) (SQLPreparedQuery, error)
	Execute(ctx context.Context, prepared SQLPreparedQuery,
		execution querymodel.SQLQueryExecutionContext, read querymodel.SQLReadRequest) (SQLResultStream, error)
	Release(ctx context.Context, prepared SQLPreparedQuery) error
}

// SQLPreparedQuery 的 SQL 模板、参数布局和结果解码由具体 SQL Connector 私有实现保存。
// 实现这个接口不代表获得可信执行身份；Execute 仍须校验制品来源与有效性。
type SQLPreparedQuery interface {
	Metadata() querymodel.SQLPreparedMetadata
}

// SQLResultStream 从 Schema 开始约束有序列，Next 以 io.EOF 表示本次读取结束。
// Completion 仅在正常 EOF 后有效；Close 必须释放本次读取资源且可重复调用。
type SQLResultStream interface {
	Schema() querymodel.Schema
	Next(ctx context.Context) (querymodel.Batch, error)
	Completion() querymodel.SQLReadCompletion
	Close() error
}
