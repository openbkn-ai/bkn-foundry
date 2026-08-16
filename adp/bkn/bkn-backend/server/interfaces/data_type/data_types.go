// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package data_type

const (
	KEYWORD_SUFFIX = "keyword"
)

const (
	DATATYPE_KEYWORD = "keyword"
	DATATYPE_TEXT    = "text"
	DATATYPE_BINARY  = "binary"
	DATATYPE_JSON    = "json"
	DATATYPE_VECTOR  = "vector"
	DATATYPE_POINT   = "point"
	DATATYPE_SHAPE   = "shape"
	DATATYPE_IP      = "ip"

	DATATYPE_INTEGER          = "integer"
	DATATYPE_UNSIGNED_INTEGER = "unsigned integer"
	DATATYPE_STRING           = "string"
	DATATYPE_FLOAT            = "float"
	DATATYPE_DECIMAL          = "decimal"
	DATATYPE_DATE             = "date"
	DATATYPE_DATETIME         = "datetime"
	DATATYPE_TIMESTAMP        = "timestamp"
	DATATYPE_TIME             = "time"
	DATATYPE_BOOLEAN          = "boolean"

	// Character types.
	CHAR    = "char"
	VARCHAR = "varchar"
	STRING  = "string"
	// Integer types.
	NUMBER   = "number"
	TINYINT  = "tinyint"
	SMALLINT = "smallint"
	INTEGER  = "integer"
	INT      = "int" // INTEGER alias
	BIGINT   = "bigint"
	// Decimal types.
	REAL            = "real"
	FLOAT           = "float" // REAL alias
	DOUBLE          = "double"
	DOUBLEPRECISION = "double precision" // DOUBLE alias
	// High-precision types.
	DECIMAL = "decimal"
	NUMERIC = "numeric" // DECIMAL alias
	DEC     = "dec"     // DECIMAL alias
	// Boolean types.
	BOOLEAN = "boolean"
	// Date types.
	DATE = "date"
	// Date-time types.
	TIME                     = "time"
	TIME_WITH_TIME_ZONE      = "time with time zone"
	DATETIME                 = "datetime"
	TIMESTAMP                = "timestamp"
	TIMESTAMP_WITH_TIME_ZONE = "timestamp with time zone"

	// region Business-level types.
	SimpleChar     = "char"
	SimpleInt      = "int"
	SimpleFloat    = "float"
	SimpleDecimal  = "decimal"
	SimpleBool     = "bool"
	SimpleDate     = "date"
	SimpleDatetime = "datetime"
	SimpleTime     = "time"
	SimpleBinary   = "binary"
	SimpleOther    = "other"
	// endregion
)

var (
	STRING_TYPES = map[string]struct{}{
		DATATYPE_TEXT:    {},
		DATATYPE_KEYWORD: {},
		DATATYPE_BINARY:  {},
	}

	// NUMBER_TYPES = map[string]struct{}{
	// 	DATATYPE_BYTE:       {},
	// 	DATATYPE_SHORT:      {},
	// 	DATATYPE_INTEGER:    {},
	// 	DATATYPE_LONG:       {},
	// 	DATATYPE_HALF_FLOAT: {},
	// 	DATATYPE_FLOAT:      {},
	// 	DATATYPE_DOUBLE:     {},
	// }

	// BOOLEAN_TYPES = map[string]struct{}{
	// 	DATATYPE_BOOLEAN: {},
	// }

	// DATE_TYPES = map[string]struct{}{
	// 	DATATYPE_DATE: {},
	// }

	// IP_TYPES = map[string]struct{}{
	// 	DATATYPE_IP: {},
	// }

	// GEO_POINT_TYPES = map[string]struct{}{
	// 	DATATYPE_GEO_POINT: {},
	// }

	// GEO_SHAPE_TYPES = map[string]struct{}{
	// 	DATATYPE_GEO_SHAPE: {},
	// }
)

// region Mapping between business-level types and virtual engine types.

var SimpleTypeMapping = map[string]string{
	// region Character types.
	STRING:             SimpleChar,
	CHAR:               SimpleChar,
	VARCHAR:            SimpleChar,
	"json":             SimpleChar,
	"text":             SimpleChar,
	"tinytext":         SimpleChar,
	"mediumtext":       SimpleChar,
	"longtext":         SimpleChar,
	"uuid":             SimpleChar,
	"name":             SimpleChar,
	"jsonb":            SimpleChar,
	"bpchar":           SimpleChar,
	"uniqueidentifier": SimpleChar,
	"xml":              SimpleChar,
	"sysname":          SimpleChar,
	"nvarchar":         SimpleChar,
	"enum":             SimpleChar,
	"set":              SimpleChar,
	"ntext":            SimpleChar,
	"nchar":            SimpleChar,
	"rowid":            SimpleChar,
	"urowid":           SimpleChar,
	"varchar2":         SimpleChar,
	"nvarchar2":        SimpleChar,
	"fixedstring":      SimpleChar,
	"nclob":            SimpleChar,
	"ipaddress":        SimpleChar,
	//endregion

	// region Integer types.
	NUMBER:               SimpleInt,
	TINYINT:              SimpleInt,
	SMALLINT:             SimpleInt,
	INTEGER:              SimpleInt,
	BIGINT:               SimpleInt,
	INT:                  SimpleInt,
	"mediumint":          SimpleInt,
	"int unsigned":       SimpleInt,
	"tinyint unsigned":   SimpleInt,
	"smallint unsigned":  SimpleInt,
	"mediumint unsigned": SimpleInt,
	"bigint unsigned":    SimpleInt,
	"int8":               SimpleInt,
	"int4":               SimpleInt,
	"int2":               SimpleInt,
	"int16":              SimpleInt,
	"int32":              SimpleInt,
	"int64":              SimpleInt,
	"int128":             SimpleInt,
	"int256":             SimpleInt,
	"long":               SimpleInt,

	REAL:            SimpleFloat,
	DOUBLE:          SimpleFloat,
	FLOAT:           SimpleFloat,
	DOUBLEPRECISION: SimpleFloat,
	"float4":        SimpleFloat,
	"float8":        SimpleFloat,
	"float16":       SimpleFloat,
	"float32":       SimpleFloat,
	"float64":       SimpleFloat,
	"binary_double": SimpleFloat,
	"binary_float":  SimpleFloat,

	DECIMAL: SimpleDecimal, NUMERIC: SimpleDecimal, DEC: SimpleDecimal,

	BOOLEAN: SimpleBool, "bit": SimpleBool, "bool": SimpleBool,

	DATE: SimpleDate, "year": SimpleDate,

	DATETIME:                 SimpleDatetime,
	"datetime2":              SimpleDatetime,
	"smalldatetime":          SimpleDatetime,
	TIMESTAMP:                SimpleDatetime,
	"timestamptz":            SimpleDatetime,
	TIMESTAMP_WITH_TIME_ZONE: SimpleDatetime,
	"interval":               SimpleDatetime, // Duration
	"interval year to month": SimpleDatetime, // Year-and-month duration
	"interval day to second": SimpleDatetime, // Day, hour, minute, second, and millisecond duration

	TIME: SimpleTime, "timetz": SimpleTime, TIME_WITH_TIME_ZONE: SimpleTime,

	"binary":      SimpleBinary,
	"blob":        SimpleBinary,
	"tinyblob":    SimpleBinary,
	"mediumblob":  SimpleBinary,
	"longblob":    SimpleBinary,
	"bytea":       SimpleBinary,
	"image":       SimpleBinary,
	"hierarchyid": SimpleBinary,
	"geography":   SimpleBinary,
	"geometry":    SimpleBinary,
	"varbinary":   SimpleBinary,
	"raw":         SimpleBinary,
	"map":         SimpleBinary,
	"array":       SimpleBinary,
	"struct":      SimpleBinary,

	"money":       SimpleOther,
	"smallmoney":  SimpleOther,
	"oid":         SimpleOther,
	"smallserial": SimpleOther,
	"serial4":     SimpleOther,
	"bigserial":   SimpleOther,
	"serial":      SimpleOther,
	"row":         SimpleOther,
	"hyperloglog": SimpleOther,
}

//endregion

func DataType_IsString(t string) bool {
	_, ok := STRING_TYPES[t]
	return ok
}

// func DataType_IsNumber(t string) bool {
// 	_, ok := NUMBER_TYPES[t]
// 	return ok
// }
