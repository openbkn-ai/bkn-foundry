// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mariadb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync/atomic"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

var _ interfaces.SQLQueryConnector = (*MariaDBConnector)(nil)

// sqlPrepared 是 Connector 私有的不可变编译结果，不持有 SQL 服务器句柄。
// owner 及配置引用用于拒绝来自其他 Connector 或重建配置前的制品。
type sqlPrepared struct {
	owner    *MariaDBConnector
	config   *mariadbConfig
	target   qm.ExecutionTarget
	metadata qm.SQLPreparedMetadata
	relation sqlRelation
	released atomic.Bool
}

// Prepare 只检查能力并生成参数化 SQL，不创建连接、不执行源请求。
func (c *MariaDBConnector) Prepare(ctx context.Context, req qm.SQLPrepareRequest) (interfaces.SQLPreparedQuery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Query == nil || req.Query.Root == nil || len(req.Query.OutputSchema) == 0 {
		return nil, sqlUnsupported("缺少完整绑定查询")
	}
	if c.config != nil {
		if charset, ok := c.config.Options["charset"]; ok && charset != "utf8mb4" {
			return nil, sqlUnsupported("当前 SQL 链路要求 utf8mb4 连接字符集")
		}
		if collation, ok := c.config.Options["collation"]; ok {
			name, valid := collation.(string)
			if !valid || !strings.HasPrefix(name, "utf8mb4_") {
				return nil, sqlUnsupported("当前 SQL 链路要求 utf8mb4 连接字符集")
			}
		}
	}
	target := req.TargetBinding
	if target.Source != qm.QuerySourceSource || target.Target.ConnectorType != c.GetType() || target.Target.CatalogID == "" || target.Target.ConnectionProfile == "" || target.Target.EffectiveSourceIdentity == "" || target.Target.ConfigRevision == "" || target.Target.SchemaRevision == "" || target.Target.AuthorizationNamespace == "" || target.Target.PhysicalTargetKey == "" {
		return nil, sqlUnsupported("执行目标与 Connector 不一致")
	}
	fingerprint := target.Fingerprint
	target.Fingerprint = ""
	encoded, err := json.Marshal(target)
	if err != nil {
		return nil, sqlUnsupported("目标无法编码")
	}
	sum := sha256.Sum256(encoded)
	if fingerprint == "" || fingerprint != hex.EncodeToString(sum[:]) {
		return nil, sqlUnsupported("执行目标指纹无效")
	}
	semantics := req.Query.Semantics
	if semantics.Version != qm.CurrentSemanticsVersion || semantics.TypeRulesVersion != qm.CurrentSemanticsVersion || semantics.ConfigFingerprint == "" || semantics.Location == nil || semantics.Comparison.StringRule != "utf8_binary" || !semantics.Comparison.CaseSensitive || !semantics.Comparison.TrailingSpaceSignificant {
		return nil, sqlUnsupported("查询语义版本或比较规则不受支持")
	}
	if len(req.Query.RuntimeSlots) != 0 {
		return nil, sqlUnsupported("稳定函数运行值尚未接入")
	}
	emitter := sqlEmitter{ctx: ctx, target: req.TargetBinding}
	relation, err := emitter.relation(req.Query.Root, 1)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(relation.schema, req.Query.OutputSchema) {
		return nil, sqlUnsupported("查询输出与绑定 Schema 不一致")
	}
	for _, column := range relation.schema {
		if !sqlScalarType(column.Type) {
			return nil, sqlUnsupported("输出类型尚无精确解码支持")
		}
	}
	metadata := qm.SQLPreparedMetadata{TargetFingerprint: fingerprint, OutputSchema: req.Query.OutputSchema, Dependencies: req.Query.Dependencies, Semantics: semantics}
	return &sqlPrepared{owner: c, config: c.config, target: target.Target, metadata: cloneSQLMetadata(metadata), relation: relation}, nil
}

func (p *sqlPrepared) Metadata() qm.SQLPreparedMetadata { return cloneSQLMetadata(p.metadata) }

// Execute 只接受本 Connector 的有效制品及规划时身份，不接受新的查询条件。
// 调用层仍负责在每次执行前复核当前权限、资源状态及依赖版本。
func (c *MariaDBConnector) Execute(ctx context.Context, prepared interfaces.SQLPreparedQuery, execution qm.SQLQueryExecutionContext, read qm.SQLReadRequest) (interfaces.SQLResultStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, ok := prepared.(*sqlPrepared)
	if !ok || p == nil || p.owner != c || p.config != c.config || p.released.Load() || p.target.ConnectorType != c.GetType() {
		return nil, sqlUnsupported("执行制品来源或状态无效")
	}
	encoded, err := json.Marshal(execution.Identity)
	if err != nil {
		return nil, sqlUnsupported("执行身份无效")
	}
	sum := sha256.Sum256(encoded)
	if execution.QueryID == "" || execution.Identity.AuthorizationNamespace != p.target.AuthorizationNamespace || p.target.EffectiveSourceIdentity != p.target.ConnectionProfile+":"+hex.EncodeToString(sum[:]) || len(execution.RuntimeValues) != 0 {
		return nil, sqlUnsupported("执行身份或运行值与准备上下文不一致")
	}
	text, args, schema, err := p.readSQL(read)
	if err != nil {
		return nil, err
	}
	stream := &sqlResultStream{schema: schema, offset: read.Offset}
	if read.Limit != nil {
		stream.limit = new(*read.Limit)
	}
	if read.Kind == qm.SQLReadCount {
		stream.limit = nil
	}
	if read.Limit != nil && *read.Limit == 0 {
		stream.completion = qm.SQLReadCompletion{Kind: qm.CompletionWindowEnd, NextOffset: new(read.Offset)}
		stream.done = true
		return stream, nil
	}
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("SQL 查询连接失败: %w", mariadbQueryError(err))
	}
	rows, err := c.db.QueryContext(ctx, text, args...) //nolint:rowserrcheck // Rows 的错误和正常 EOF 由 sqlResultStream.Next 检查。
	if err != nil {
		return nil, fmt.Errorf("SQL 查询执行失败: %w", mariadbQueryError(err))
	}
	transferred := false
	defer func() {
		if !transferred {
			_ = rows.Close()
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("SQL 查询列读取失败: %w", err)
	}
	if len(columns) != len(schema) {
		return nil, sqlUnsupported("源结果列数与输出 Schema 不一致")
	}
	stream.rows = rows
	transferred = true
	return stream, nil
}

func (c *MariaDBConnector) Release(_ context.Context, prepared interfaces.SQLPreparedQuery) error {
	p, ok := prepared.(*sqlPrepared)
	if !ok || p == nil || p.owner != c {
		return sqlUnsupported("执行制品来源无效")
	}
	p.released.Store(true)
	return nil
}

func (p *sqlPrepared) readSQL(read qm.SQLReadRequest) (string, []any, qm.Schema, error) {
	if read.Offset < 0 || (read.Limit != nil && (*read.Limit < 0 || *read.Limit == math.MaxInt64 || read.Offset > math.MaxInt64-*read.Limit)) {
		return "", nil, nil, sqlUnsupported("读取窗口无效")
	}
	args := append([]any(nil), p.relation.args...)
	if read.Kind == qm.SQLReadCount {
		if read.Offset != 0 || read.Limit != nil {
			return "", nil, nil, sqlUnsupported("count 不接受读取窗口")
		}
		schema := qm.Schema{{ID: 1, Name: "count", Type: qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}, Encoding: qm.OutputEncoding{Kind: qm.EncodingScalar, PublicType: interfaces.DataType_Integer}}}
		return "SELECT COUNT(*) AS `f1` FROM (" + p.relation.text + ") AS q", args, schema, nil
	}
	if read.Kind != qm.SQLReadPage && read.Kind != qm.SQLReadRelation {
		return "", nil, nil, sqlUnsupported("读取类型无效")
	}
	columns, err := sqlColumns(p.metadata.OutputSchema, sqlScope(p.metadata.OutputSchema, "q"))
	if err != nil {
		return "", nil, nil, err
	}
	text := "SELECT " + strings.Join(columns, ", ") + " FROM (" + p.relation.text + ") AS q" + sqlOrderBy(p.relation.order, "q")
	if read.Limit != nil {
		text += " LIMIT ? OFFSET ?"
		args = append(args, *read.Limit+1, read.Offset)
	} else if read.Offset != 0 {
		text += " LIMIT 18446744073709551615 OFFSET ?"
		args = append(args, read.Offset)
	}
	return text, args, cloneSQLMetadata(p.metadata).OutputSchema, nil
}

func cloneSQLMetadata(metadata qm.SQLPreparedMetadata) qm.SQLPreparedMetadata {
	metadata.OutputSchema = append(qm.Schema(nil), metadata.OutputSchema...)
	for i := range metadata.OutputSchema {
		column := &metadata.OutputSchema[i]
		column.Lineage = append([]qm.FieldID(nil), column.Lineage...)
		if column.Type.TimePrecision != nil {
			column.Type.TimePrecision = new(*column.Type.TimePrecision)
		}
	}
	metadata.Dependencies = append([]qm.Dependency(nil), metadata.Dependencies...)
	for i := range metadata.Dependencies {
		metadata.Dependencies[i].Authorization = append([]qm.AuthorizationBasis(nil), metadata.Dependencies[i].Authorization...)
	}
	metadata.RuntimeSlots = append([]qm.RuntimeSlot(nil), metadata.RuntimeSlots...)
	return metadata
}
