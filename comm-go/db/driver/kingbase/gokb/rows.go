/******************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：rows.go

* 功能描述：

* 其它说明：

* 修改记录：
  1.修改时间：

  2.修改人：

  3.修改内容：

******************************************************************************/

package gokb

import (
	"database/sql"
	"math"
	"reflect"
	"time"
)

func (fd fieldDesc) Type(cn *conn) (rt reflect.Type) {
	switch fd.OID {
	case cn.allOid.T_int8:
		rt = reflect.TypeOf(int64(0))
		return
	case cn.allOid.T_int4:
		rt = reflect.TypeOf(int32(0))
		return
	case cn.allOid.T_int2:
		rt = reflect.TypeOf(int16(0))
		return
	case cn.allOid.T_tinyint:
		rt = reflect.TypeOf(int8(0))
		return
	case cn.allOid.T_bit, cn.allOid.T_varbit:
		if cn.databaseMode == "sqlserver" {
			var b sql.NullBool
			rt = reflect.TypeOf(b)
		} else {
			rt = reflect.TypeOf([]byte(nil))
		}
		return
	case cn.allOid.T_varchar, cn.allOid.T_text, cn.allOid.T_longtext, cn.allOid.T_mediumtext, cn.allOid.T_tinytext:
		rt = reflect.TypeOf("")
		return
	case cn.allOid.T_bool:
		rt = reflect.TypeOf(false)
		return
	case cn.allOid.T_date, cn.allOid.T_time, cn.allOid.T_timetz, cn.allOid.T_timestamp, cn.allOid.T_timestamptz, cn.allOid.T_datetime, cn.allOid.T_year:
		rt = reflect.TypeOf(time.Time{})
		return
	case cn.allOid.T_bytea, cn.allOid.T_longblob, cn.allOid.T_mediumblob, cn.allOid.T_tinyblob, cn.allOid.T_blob,
		cn.allOid.T_numeric, cn.allOid.T_money,
		cn.allOid.T_json:
		rt = reflect.TypeOf([]byte(nil))
		return
	case cn.allOid.T_float4:
		rt = reflect.TypeOf(float32(0))
		return
	case cn.allOid.T_float8:
		rt = reflect.TypeOf(float64(0))
		return
	default:
		rt = reflect.TypeOf(new(interface{})).Elem()
		return
	}
}

func (fd fieldDesc) Name(cn *conn) (s string) { return cn.TypeName[fd.OID] }

func (fd fieldDesc) Length(cn *conn) (len int64, state bool) {
	if cn.databaseMode == "mysql" {
		switch fd.OID {
		case cn.allOid.T_text, cn.allOid.T_bytea:
			len = math.MaxInt64
			state = true
			return
		case cn.allOid.T_varchar, cn.allOid.T_bpchar, cn.allOid.T_bpcharbyte, cn.allOid.T_varcharbyte, cn.allOid.T_binary, cn.allOid.T_varbinary:
			if fd.Mod == -1 {
				return -1, true
			} else {
				return int64(fd.Mod - headerSize), true
			}
		case cn.allOid.T_numeric:
			if fd.Mod == -1 {
				return 10, true
			} else {
				mod := fd.Mod - headerSize
				precision := int64((mod >> 16) & 0xffff)
				return precision, true
			}
		case cn.allOid.T_tinyint:
			return 3, true // -128到+127
		case cn.allOid.T_int2:
			return 5, true // -32768到+32767
		case cn.allOid.T_int4, cn.allOid.T_uint4, cn.allOid.T_oid:
			return 10, true // -2147483648到+2147483647, cn.allOid:0到4294967295
		case cn.allOid.T_int8:
			return 19, true // -9223372036854775808到+9223372036854775807
		case cn.allOid.T_uint8:
			return 20, true // -9223372036854775808到+9223372036854775807
		case cn.allOid.T_float4:
			return 12, true // 符号+ 9位+小数点+e+符号+ 2位
		case cn.allOid.T_float8:
			return 22, true // 符号+ 18位+小数点+ e+符号+ 3位
		case cn.allOid.T_bool, cn.allOid.T_char:
			return 1, true
		case cn.allOid.T_year:
			return 4, true
		case cn.allOid.T_date:
			return 10, true // "4713-01-01"到"01/01/4713" - "31/12/3276"
		case cn.allOid.T_time, cn.allOid.T_timetz, cn.allOid.T_timestamp, cn.allOid.T_timestamptz, cn.allOid.T_datetime:
			var secondSizeT int
			switch fd.Mod {
			case -1, 0:
				secondSizeT = 0
			case 1:
				//Bizarraely SELECT '0:0:0.1'::time(1); 返回2位.
				secondSizeT = 1 + 1
			default:
				secondSizeT = fd.Mod + 1
			}
			// 我们假设所有这些情况的最坏情况。
			// time = '00:00:00' = 8
			// date = '5874897-12-31' = 13 (尽管在较大的数值下，秒精度会损失)
			// date = '294276-11-20' = 12 --enable-integer-datetimes
			// zone = '+11:30' = 6;
			switch fd.OID {
			case cn.allOid.T_time:
				return int64(8 + secondSizeT), true
			case cn.allOid.T_timetz:
				return int64(8 + secondSizeT + 6), true
			case cn.allOid.T_timestamp, cn.allOid.T_timestamptz, cn.allOid.T_datetime:
				return int64(10 + 1 + 8 + secondSizeT), true
			}
		case cn.allOid.T_bit:
			return int64(fd.Mod), true
		case cn.allOid.T_varbit:
			if fd.Mod == -1 {
				return -1, true
			} else {
				return int64(fd.Mod), true
			}
		case cn.allOid.T_tid:
			return 18, true
		case cn.allOid.T_rowid:
			return 23, true
		case cn.allOid.T_longtext:
			return 2147483647, true
		case cn.allOid.T_mediumtext:
			return 16777215, true
		case cn.allOid.T_tinytext:
			return 255, true
		// 对于longtext、mediumtext、tinytext均返回基类型text，变长字段
		// case cn.allOid.T_text:
		// 	return 65535, true
		case cn.allOid.T_blob:
			return 65535, true
		case cn.allOid.T_tinyblob:
			return 255, true
		case cn.allOid.T_mediumblob:
			return 16777215, true
		case cn.allOid.T_longblob:
			return 2147483647, true
		// 对于longblob、mediumblob、tinyblob均返回基类型blob，变长字段
		// case cn.allOid.T_bytea:
		// 	return 65535, true
		default:
			len = 0
			state = false
			return
		}
	} else {
		switch fd.OID {
		case cn.allOid.T_text, cn.allOid.T_bytea:
			len = math.MaxInt64
			state = true
			return
		case cn.allOid.T_varchar, cn.allOid.T_bpchar, cn.allOid.T_bpcharbyte, cn.allOid.T_varcharbyte, cn.allOid.T_nchar, cn.allOid.T_nvarchar, cn.allOid.T_binary, cn.allOid.T_varbinary:
			if fd.Mod == -1 {
				return -1, true
			} else {
				return int64(fd.Mod - headerSize), true
			}
		default:
			len = 0
			state = false
			return
		}
	}
	return
}

func (fd fieldDesc) PrecisionScale(cn *conn) (precision int64, scale int64, state bool) {
	if cn.databaseMode == "mysql" {
		switch fd.OID {
		case cn.allOid.T_numeric:
			if fd.Mod == -1 {
				return 10, 0, true
			} else {
				mod := fd.Mod - headerSize
				precision = int64((mod >> 16) & 0xffff)
				scale = int64(mod & 0xffff)
				state = true
				return
			}
		case cn.allOid.T_text, cn.allOid.T_bytea:
			return math.MaxInt64, 0, true
		case cn.allOid.T_tinyint:
			return 3, 0, true
		case cn.allOid.T_int2:
			return 5, 0, true
		case cn.allOid.T_int4, cn.allOid.T_uint4, cn.allOid.T_oid:
			return 10, 0, true
		case cn.allOid.T_int8:
			return 19, 0, true
		case cn.allOid.T_uint8:
			return 20, 0, true
		case cn.allOid.T_float4:
			return 12, 0, true
		case cn.allOid.T_float8:
			return 22, 0, true
		case cn.allOid.T_bool, cn.allOid.T_char:
			return 1, 0, true
		case cn.allOid.T_varchar, cn.allOid.T_bpchar, cn.allOid.T_bpcharbyte, cn.allOid.T_varcharbyte, cn.allOid.T_binary, cn.allOid.T_varbinary:
			if fd.Mod == -1 {
				return -1, 0, true
			} else {
				return int64(fd.Mod - headerSize), 0, true
			}
		case cn.allOid.T_year:
			if fd.Mod == -1 {
				scale = 0
			} else {
				scale = int64(fd.Mod)
			}
			return 4, scale, true
		case cn.allOid.T_date:
			if fd.Mod == -1 {
				scale = 0
			} else {
				scale = int64(fd.Mod)
			}
			return 10, scale, true
		case cn.allOid.T_time, cn.allOid.T_timetz, cn.allOid.T_timestamp, cn.allOid.T_timestamptz, cn.allOid.T_datetime:
			var secondSizeT int
			switch fd.Mod {
			case -1, 0:
				secondSizeT = 0
			case 1:
				//Bizarraely SELECT '0:0:0.1'::time(1); 返回2位.
				secondSizeT = 1 + 1
			default:
				secondSizeT = fd.Mod + 1
			}
			// 我们假设所有这些情况的最坏情况。
			// time = '00:00:00' = 8
			// date = '5874897-12-31' = 13 (尽管在较大的数值下，秒精度会损失)
			// date = '294276-11-20' = 12 --enable-integer-datetimes
			// zone = '+11:30' = 6;
			switch fd.OID {
			case cn.allOid.T_time:
				precision = int64(8 + secondSizeT)
			case cn.allOid.T_timetz:
				precision = int64(8 + secondSizeT + 6)
			case cn.allOid.T_timestamp, cn.allOid.T_timestamptz, cn.allOid.T_datetime:
				precision = int64(10 + 1 + 8 + secondSizeT)
			}
			if fd.Mod == -1 {
				scale = 0
			} else {
				scale = int64(fd.Mod)
			}
			return precision, scale, true
		case cn.allOid.T_bit:
			return int64(fd.Mod), 0, true
		case cn.allOid.T_varbit:
			if fd.Mod == -1 {
				return -1, 0, true
			} else {
				return int64(fd.Mod), 0, true
			}
		case cn.allOid.T_tid:
			return 18, 0, true
		case cn.allOid.T_rowid:
			return 23, 0, true
		case cn.allOid.T_longtext:
			return 2147483647, 0, true
		case cn.allOid.T_mediumtext:
			return 16777215, 0, true
		case cn.allOid.T_tinytext:
			return 255, 0, true
		// 对于longtext、mediumtext、tinytext均返回基类型text，变长字段
		// case cn.allOid.T_text:
		// 	return 65535, 0, true
		case cn.allOid.T_blob:
			return 65535, 0, true
		case cn.allOid.T_tinyblob:
			return 255, 0, true
		case cn.allOid.T_mediumblob:
			return 16777215, 0, true
		case cn.allOid.T_longblob:
			return 2147483647, 0, true
		// 对于longblob、mediumblob、tinyblob均返回基类型blob，变长字段
		// case cn.allOid.T_bytea:
		// 	return 65535, 0, true
		default:
			precision = 0
			scale = 0
			state = false
			return
		}
	} else {
		switch fd.OID {
		case cn.allOid.T_numeric:
			if fd.Mod == -1 {
				return 10, 0, true
			} else {
				mod := fd.Mod - headerSize
				precision = int64((mod >> 16) & 0xffff)
				scale = int64(mod & 0xffff)
				state = true
				return
			}
		default:
			precision = 0
			scale = 0
			state = false
			return
		}
	}
}

func (fd fieldDesc) NullAble(cn *conn) (nullable bool, hasNullable bool) {
	switch fd.OID {
	case cn.allOid.T_SIMPLE_INTEGER, cn.allOid.T_SIMPLE_FLOAT, cn.allOid.T_SIMPLE_DOUBLE, cn.allOid.T_positiven, cn.allOid.T_NATURALN,
		cn.allOid.T__SIMPLE_INTEGER, cn.allOid.T__SIMPLE_FLOAT, cn.allOid.T__SIMPLE_DOUBLE, cn.allOid.T__positiven, cn.allOid.T__NATURALN:
		nullable = false
		hasNullable = true
		return
	default:
		nullable = true
		hasNullable = true
		return
	}
}

func (rs *rows) ColumnTypeNullable(index int) (nullable bool, hasNullable bool) {
	return rs.colTyps[index].NullAble(rs.cn)
}

// ColumnTypeScanType返回适合的SCAN数据类型
func (rs *rows) ColumnTypeScanType(index int) (rt reflect.Type) {
	return rs.colTyps[index].Type(rs.cn)
}

// ColumnTypeDatabaseTypeName返回数据的类型名
func (rs *rows) ColumnTypeDatabaseTypeName(index int) (s string) {
	return rs.colTyps[index].Name(rs.cn)
}

// ColumnTypeLength返回列类型的长度
func (rs *rows) ColumnTypeLength(index int) (int64, bool) {
	return rs.colTyps[index].Length(rs.cn)
}

// ColumnTypePrecisionScale返回decimal类型的精度和刻度
func (rs *rows) ColumnTypePrecisionScale(index int) (int64, int64, bool) {
	return rs.colTyps[index].PrecisionScale(rs.cn)
}
