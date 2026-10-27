// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package sqlbuilder

const (
	Add      ArithmeticOperator = "+"
	Subtract ArithmeticOperator = "-"
	Multiply ArithmeticOperator = "*"
	Divide   ArithmeticOperator = "/"
	Modulo   ArithmeticOperator = "%"
)

const (
	Positive UnaryOperator = "+"
	Negative UnaryOperator = "-"
)

const (
	CastInteger         CastType = "integer"
	CastUnsignedInteger CastType = "unsigned_integer"
	CastDecimal         CastType = "decimal"
	CastString          CastType = "string"
	CastBinary          CastType = "binary"
	CastBoolean         CastType = "boolean"
	CastDate            CastType = "date"
	CastDateTime        CastType = "datetime"
	CastTimestamp       CastType = "timestamp"
	CastTime            CastType = "time"
	CastFloat           CastType = "float"
	CastDouble          CastType = "double"
	CastJSON            CastType = "json"
)

type CastType string

// Column 是字段引用；Qualifier 是来源别名，Name 是单个列名。
// 物理表字段按输入名称保留；View 字段映射由 View 展开逻辑处理。
type Column struct {
	Qualifier string
	Name      string
}

// Param 是单个绑定值，nil 表示 SQL NULL。Value 不作为 SQL 文本解析，
// 也不将其中的 SqlNode 当作子表达式展开。数值保留输入精度，不统一转为 float64。
type Param struct {
	Value any
}

// Star 表示 * 或 alias.*，只能用于允许通配符的位置。
type Star struct {
	Qualifier string
}

type Alias struct {
	Expr SqlNode
	Name string
}

type Eq struct {
	Left, Right SqlNode
}

type NotEq struct {
	Left, Right SqlNode
}

type Gt struct {
	Left, Right SqlNode
}

type GtOrEq struct {
	Left, Right SqlNode
}

type Lt struct {
	Left, Right SqlNode
}

type LtOrEq struct {
	Left, Right SqlNode
}

type And []SqlNode

type Or []SqlNode

type Not struct {
	Expr SqlNode
}

// IsNull 显式表达 IS NULL / IS NOT NULL。
// Eq/NotEq 遵循普通 SQL 比较，不根据参数是否为 nil 改写成 IS NULL。
type IsNull struct {
	Expr    SqlNode
	Negated bool
}

// In 要求 Values 非空；空列表返回错误，不擅自折叠为真假常量。
type In struct {
	Expr    SqlNode
	Values  []SqlNode
	Negated bool
}

type Between struct {
	Expr         SqlNode
	Lower, Upper SqlNode
	Negated      bool
}

// Like 的 Pattern 使用 SQL 通配符；Escape=nil 表示未显式指定转义字符。
type Like struct {
	Expr    SqlNode
	Pattern SqlNode
	Escape  *string
	Negated bool
}

// Contains 表达字面包含，模式中的 % 和 _ 不具有 SQL 通配符含义。
// 与 Like 分开建模，供结构化过滤条件使用。
type Contains struct {
	Expr  SqlNode
	Value SqlNode
}

type ArithmeticOperator string

// Arithmetic 仅表示双目算术；比较和逻辑组合使用各自的具体节点。
type Arithmetic struct {
	Left, Right SqlNode
	Op          ArithmeticOperator
}

type UnaryOperator string

type Unary struct {
	Expr SqlNode
	Op   UnaryOperator
}

// Function 保存函数名称及结构化参数，不接受包含括号或参数的函数 SQL 文本。
// 函数白名单、参数类型及聚合使用位置由准备阶段检查。
type Function struct {
	Name     string
	Args     []SqlNode
	Distinct bool
}

// Case 同时表达简单 CASE 与搜索 CASE；Operand=nil 表示搜索 CASE。
// Else=nil 表示省略 ELSE，显式 ELSE NULL 使用 &Param{Value:nil}。
type Case struct {
	Operand   SqlNode
	WhenParts []When
	Else      SqlNode
}

type When struct {
	Condition SqlNode
	Result    SqlNode
}

// Cast 保存逻辑目标类型及长度、精度/小数位等参数，不携带目标数据库类型文本。
// 具体映射、参数范围和能力检查由目标生成器负责。
type Cast struct {
	Expr       SqlNode
	Type       CastType
	Parameters []uint64
}

func (*Column) sqlNode() {}

func (*Param) sqlNode() {}

func (*Star) sqlNode() {}

func (*Alias) sqlNode() {}

func (*Eq) sqlNode() {}

func (*NotEq) sqlNode() {}

func (*Gt) sqlNode() {}

func (*GtOrEq) sqlNode() {}

func (*Lt) sqlNode() {}

func (*LtOrEq) sqlNode() {}

func (*And) sqlNode() {}

func (*Or) sqlNode() {}

func (*Not) sqlNode() {}

func (*IsNull) sqlNode() {}

func (*In) sqlNode() {}

func (*Between) sqlNode() {}

func (*Like) sqlNode() {}

func (*Contains) sqlNode() {}

func (*Arithmetic) sqlNode() {}

func (*Unary) sqlNode() {}

func (*Function) sqlNode() {}

func (*Case) sqlNode() {}

func (*Cast) sqlNode() {}
