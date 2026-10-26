// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

const (
	EncodingScalar             OutputEncodingKind = "scalar"
	EncodingExactNumberString  OutputEncodingKind = "exact_number_string"
	EncodingExternalJSONString OutputEncodingKind = "external_json_string"
	EncodingDatasetJSON        OutputEncodingKind = "dataset_json"
	EncodingNativeOther        OutputEncodingKind = "native_other"
	EncodingBinaryMetadata     OutputEncodingKind = "binary_metadata"
	EncodingBinaryContent      OutputEncodingKind = "binary_content"
)

type QueryWindow struct{ Count, Offset int64 }

// SelectBuilder 按 SQL 子句保存结构化节点，同时是查询构造对象和 IR。
// 链式方法返回新对象，复制要修改的切片；表达式和 FROM 节点按只读使用。
// 绑定与规划使用同类型的新对象，解析字段/类型并补齐来源物理名称。
type SelectBuilder struct {
	ColumnsPart  []NamedExpr
	FromPart     Relation
	WherePart    []*Sqlizer
	GroupByPart  []*Sqlizer
	HavingPart   []*Sqlizer
	OrderByPart  []SortKey
	DistinctPart bool
	LimitPart    *QueryWindow
}

type NamedExpr struct {
	Encoding OutputEncoding
	Output   FieldID
	Expr     *Sqlizer
	Alias    string
	Origin   Origin
}

type SortKey struct {
	Nulls      NullOrder
	Expr       *Sqlizer
	Descending bool
	Origin     Origin
}

// FieldID 标识关系中的一个输出槽。两次投影同一字段仍分配不同的输出 ID。
type FieldID uint64

type OutputEncodingKind string

// OutputEncoding 只控制交付表示，不改变 LogicalType 的计算含义。
type OutputEncoding struct {
	Kind       OutputEncodingKind
	PublicType string
}

type OutputColumn struct {
	ID       FieldID
	Name     string
	Type     LogicalType
	Nullable bool
	Lineage  []FieldID
	Encoding OutputEncoding
	Origin   Origin
}

// Schema 按列位置有序；Batch 中每行的单元格位置必须与其一致。
type Schema []OutputColumn

type ColumnMapping struct {
	Input  FieldID
	Output FieldID
}

func As(expr *Sqlizer, alias string) NamedExpr { return NamedExpr{Expr: expr, Alias: alias} }

func Select(columns ...NamedExpr) *SelectBuilder {
	return &SelectBuilder{ColumnsPart: append([]NamedExpr(nil), columns...)}
}

// Clone 复制子句容器；表达式及来源节点按只读共享。
func (q *SelectBuilder) Clone() *SelectBuilder {
	copy := *q
	copy.ColumnsPart = append([]NamedExpr(nil), q.ColumnsPart...)
	copy.WherePart = append([]*Sqlizer(nil), q.WherePart...)
	copy.GroupByPart = append([]*Sqlizer(nil), q.GroupByPart...)
	copy.HavingPart = append([]*Sqlizer(nil), q.HavingPart...)
	copy.OrderByPart = append([]SortKey(nil), q.OrderByPart...)
	if q.LimitPart != nil {
		window := *q.LimitPart
		copy.LimitPart = &window
	}
	return &copy
}

func (q *SelectBuilder) From(source Relation) *SelectBuilder {
	copy := *q
	copy.FromPart = source
	return &copy
}

func (q *SelectBuilder) FromSelect(source *SelectBuilder, alias string) *SelectBuilder {
	return q.From(&Subquery{Query: source, Alias: alias})
}

func (q *SelectBuilder) Columns(columns ...NamedExpr) *SelectBuilder {
	copy := *q
	copy.ColumnsPart = append(append([]NamedExpr(nil), q.ColumnsPart...), columns...)
	return &copy
}

func (q *SelectBuilder) Where(predicates ...*Sqlizer) *SelectBuilder {
	copy := *q
	copy.WherePart = append([]*Sqlizer(nil), q.WherePart...)
	for _, predicate := range predicates {
		if predicate != nil {
			copy.WherePart = append(copy.WherePart, predicate)
		}
	}
	return &copy
}

func (q *SelectBuilder) GroupBy(keys ...*Sqlizer) *SelectBuilder {
	copy := *q
	copy.GroupByPart = append(append([]*Sqlizer(nil), q.GroupByPart...), keys...)
	return &copy
}

func (q *SelectBuilder) Having(predicates ...*Sqlizer) *SelectBuilder {
	copy := *q
	copy.HavingPart = append([]*Sqlizer(nil), q.HavingPart...)
	for _, predicate := range predicates {
		if predicate != nil {
			copy.HavingPart = append(copy.HavingPart, predicate)
		}
	}
	return &copy
}

func (q *SelectBuilder) OrderBy(keys ...SortKey) *SelectBuilder {
	copy := *q
	copy.OrderByPart = append(append([]SortKey(nil), q.OrderByPart...), keys...)
	return &copy
}

func (q *SelectBuilder) Distinct() *SelectBuilder {
	copy := *q
	copy.DistinctPart = true
	return &copy
}

func (q *SelectBuilder) Limit(count, offset int64) *SelectBuilder {
	copy := *q
	copy.LimitPart = &QueryWindow{Count: count, Offset: offset}
	return &copy
}

func (q *SelectBuilder) Join(source Relation, kind JoinKind, on *Sqlizer) *SelectBuilder {
	return q.From(&Join{Left: q.FromPart, Right: source, Kind: kind, On: on})
}

// Schema 从已解析的投影生成输出定义，不在 Builder 中保存第二份列描述。
// 返回的切片、lineage 及时间精度可由调用方独立使用。
func (q *SelectBuilder) Schema() Schema {
	if q == nil {
		return nil
	}
	result := make(Schema, 0, len(q.ColumnsPart))
	for _, column := range q.ColumnsPart {
		c := OutputColumn{ID: column.Output, Name: column.Alias, Encoding: column.Encoding, Origin: column.Origin}
		if column.Expr != nil {
			c.Type = column.Expr.Type
			c.Nullable = column.Expr.Nullable
			c.Lineage = column.Expr.FieldIDs()
			if c.Type.TimePrecision != nil {
				precision := *c.Type.TimePrecision
				c.Type.TimePrecision = &precision
			}
		}
		result = append(result, c)
	}
	return result
}
