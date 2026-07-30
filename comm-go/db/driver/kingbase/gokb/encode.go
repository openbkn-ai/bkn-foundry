/******************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：encode.go

* 功能描述：对数据类型进行解析并将字节数组与数据类型相互转换

* 其它说明：

* 修改记录：
  1.修改时间：

  2.修改人：

  3.修改内容：

******************************************************************************/

package gokb

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-comm-go/db/driver/kingbase/gokb/oid"
	"github.com/openbkn-ai/bkn-comm-go/db/driver/kingbase/gokb/oid/oracleOid"

	"github.com/golang-sql/civil"
	"github.com/shopspring/decimal"
)

func binaryEncode(parameterStatus *parameterStatus, x interface{}, cn *conn) (bv []byte) {
	switch v := x.(type) {
	case []byte:
		bv = v
		return
	default:
		bv = encode(parameterStatus, x, cn.allOid.T_unknown, cn)
		return
	}
}

func aliasEncode(parameterStatus *parameterStatus, x interface{}, kbtypOid oid.Oid, allOid oid.AllOid) (value []byte) {
	rv := reflect.ValueOf(x)
	k := (reflect.TypeOf(x)).Kind()
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.AppendInt(nil, rv.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.AppendUint(nil, rv.Uint(), 10)
	case reflect.Float64:
		return strconv.AppendFloat(nil, rv.Float(), 'f', -1, 64)
	case reflect.Float32:
		return strconv.AppendFloat(nil, rv.Float(), 'f', -1, 32)
	case reflect.Bool:
		return strconv.AppendBool(nil, rv.Bool())
	case reflect.String:
		if allOid.T_bytea == kbtypOid {
			return encodeBytea(parameterStatus.serverVersion, []byte(rv.String()))
		} else {
			return []byte(rv.String())
		}
	default:
		errorf("encode: unknown type for %T", rv)
	}
	panic("not reached")
}

func encode(parameterStatus *parameterStatus, x interface{}, kbtypOid oid.Oid, cn *conn) (value []byte) {
	var allOid oid.AllOid
	if cn == nil {
		allOid = oracleOid.OracleOid
	} else {
		allOid = cn.allOid
	}
	rv := reflect.ValueOf(x)
	switch v := x.(type) {
	case int64, int32, int16, int8, int:
		return strconv.AppendInt(nil, rv.Int(), 10)
	case sql.NullInt64:
		return strconv.AppendInt(nil, v.Int64, 10)
	case uint64, uint32, uint16, uint8, uint:
		return strconv.AppendUint(nil, rv.Uint(), 10)
	case float64:
		return strconv.AppendFloat(nil, rv.Float(), 'f', -1, 64)
	case float32:
		return strconv.AppendFloat(nil, rv.Float(), 'f', -1, 32)
	case sql.NullFloat64:
		return strconv.AppendFloat(nil, v.Float64, 'f', -1, 64)
	case decimal.Decimal:
		num, _ := v.Value()
		return []byte(num.(string))
	case []byte:
		if allOid.T_bytea == kbtypOid {
			return encodeBytea(parameterStatus.serverVersion, v)
		} else if allOid.T_binary == kbtypOid || allOid.T_varbinary == kbtypOid {
			hexStr := "0x" + hex.EncodeToString(v)
			return []byte(hexStr)
		} else {
			return v
		}
	case string:
		if allOid.T_bytea == kbtypOid {
			return encodeBytea(parameterStatus.serverVersion, []byte(v))
		} else {
			return []byte(v)
		}
	case VarChar:
		if allOid.T_bytea == kbtypOid {
			return encodeBytea(parameterStatus.serverVersion, []byte(v))
		} else {
			return []byte(v)
		}
	case VarCharMax:
		if allOid.T_bytea == kbtypOid {
			return encodeBytea(parameterStatus.serverVersion, []byte(v))
		} else {
			return []byte(v)
		}
	case NVarCharMax:
		if allOid.T_bytea == kbtypOid {
			return encodeBytea(parameterStatus.serverVersion, []byte(v))
		} else {
			return []byte(v)
		}
	case NChar:
		if allOid.T_bytea == kbtypOid {
			return encodeBytea(parameterStatus.serverVersion, []byte(v))
		} else {
			return []byte(v)
		}
	case sql.NullString:
		if allOid.T_bytea == kbtypOid {
			return encodeBytea(parameterStatus.serverVersion, []byte(v.String))
		} else {
			return []byte(v.String)
		}
	case bool:
		return strconv.AppendBool(nil, v)
	case sql.NullBool:
		return strconv.AppendBool(nil, v.Bool)
	case time.Time:
		return formatTs(time.Time(v))
	case DateTime1:
		return formatTs(time.Time(v)) //datetime
	case civil.Date:
		return formatD(v)
	case civil.Time:
		return formatT(v)
	case CursorString:
		return []byte(v.CursorName)
	case sql.Out: //out参数
		var sBind bindStruct
		var valueInterface interface{}
		valueInterface = v
		sBind.out, sBind.isOut = valueInterface.(sql.Out)
		isBoth := sBind.out.In
		rvOut := reflect.ValueOf(sBind.out.Dest)
		if isBoth {
			return encode(parameterStatus, rvOut.Elem().Interface(), kbtypOid, cn)
		}
		return nil
	default:
		return aliasEncode(parameterStatus, x, kbtypOid, allOid)
	}
	panic("not reached")
}

func decode(parameterStatus *parameterStatus, s []byte, typ oid.Oid, f format, cn conn) (value interface{}) {
	switch f {
	case formatBinary:
		value = binaryDecode(parameterStatus, s, typ, cn)
		return
	case formatText:
		value = textDecode(parameterStatus, s, typ, cn)
		return
	default:
		panic("not reached")
	}
}

func binaryDecode(parameterStatus *parameterStatus, s []byte, typ oid.Oid, cn conn) (value interface{}) {
	switch typ {
	case cn.allOid.T_bytea, cn.allOid.T_longblob, cn.allOid.T_mediumblob, cn.allOid.T_tinyblob, cn.allOid.T_blob, cn.allOid.T_json, cn.allOid.T_bit, cn.allOid.T_varbit:
		value = s
		return
	case cn.allOid.T_int8, cn.allOid.T_bigint:
		value = int64(binary.BigEndian.Uint64(s))
		return
	case cn.allOid.T_int4, cn.allOid.T_int:
		value = int64(int32(binary.BigEndian.Uint32(s)))
		return
	case cn.allOid.T_int2, cn.allOid.T_smallint:
		value = int64(int16(binary.BigEndian.Uint16(s)))
		return
	case cn.allOid.T_uint8:
		s, _ := strconv.ParseUint(string(s), 10, 64)
		value = uint64(s)
	case cn.allOid.T_uint4:
		s, _ := strconv.ParseUint(string(s), 10, 64)
		value = uint32(s)
	case cn.allOid.T_tinyint:
		fillByte := fill64(s)
		if cn.databaseMode == "sqlserver" {
			value = uint8(binary.BigEndian.Uint64(fillByte))
		} else {
			value = int8(binary.BigEndian.Uint64(fillByte))
		}
		return
	case cn.allOid.T_binary, cn.allOid.T_varbinary:
		value = s
		return
	case cn.allOid.T_uuid:
		b, err := decodeUUIDBinary(s)
		if nil != err {
			panic(err)
		}
		value = b
		return
	default:
		errorf("don't know how to decode binary parameter of type %d", uint32(typ))
	}
	panic("not reached")
}

func textDecode(parameterStatus *parameterStatus, s []byte, typ oid.Oid, cn conn) (value interface{}) {
	switch typ {
	case cn.allOid.T_char, cn.allOid.T_varchar, cn.allOid.T_text, cn.allOid.T_longtext, cn.allOid.T_mediumtext, cn.allOid.T_tinytext:
		value = string(s)
		return
	case cn.allOid.T_varcharbyte, cn.allOid.T_nvarchar, cn.allOid.T_bpcharbyte, cn.allOid.T_nchar:
		value = string(s)
		return
	case cn.allOid.T_longblob, cn.allOid.T_mediumblob, cn.allOid.T_tinyblob, cn.allOid.T_blob, cn.allOid.T_json:
		value = s
		return
	case cn.allOid.T_bytea:
		b, err := parseBytea(s)
		if nil != err {
			errorf("%s", err)
		}
		value = b
		return
	case cn.allOid.T_binary, cn.allOid.T_varbinary:
		if len(s) > 2 {
			s = s[2:]
			result := make([]byte, hex.DecodedLen(len(s)))
			_, err := hex.Decode(result, s)
			if nil != err {
				errorf("%s", err)
			}
			value = result
		} else {
			value = s
		}
		return
	case cn.allOid.T_timestamptz:
		value = parseTs(parameterStatus.currentLocation, string(s))
		return
	case cn.allOid.T_timestamp, cn.allOid.T_datetime:
		value = parseTs(parameterStatus.currentLocation, string(s))
		return
	case cn.allOid.T_date:
		if cn.databaseMode == "sqlserver" {
			value = parseCivilDate(string(s))
		} else {
			value = parseTs(parameterStatus.currentLocation, string(s))
		}
		return
	case cn.allOid.T_time:
		if cn.databaseMode == "sqlserver" {
			value = parseCivilTime(string(s))
		} else {
			value = mustParse("15:04:05", typ, s, cn)
		}
		return
	case cn.allOid.T_timetz:
		value = mustParse("15:04:05-07", typ, s, cn)
		return
	case cn.allOid.T_bool:
		value = (s[0] == 't')
		return
	case cn.allOid.T_bit, cn.allOid.T_varbit:
		value = s
		return
	case cn.allOid.T_int8, cn.allOid.T_int4, cn.allOid.T_int2, cn.allOid.T_smallint, cn.allOid.T_int, cn.allOid.T_bigint:
		i, err := strconv.ParseInt(string(s), 10, 64)
		if nil != err {
			errorf("%s", err)
		}
		value = i
		return i
	case cn.allOid.T_uint8:
		s, _ := strconv.ParseUint(string(s), 10, 64)
		value = uint64(s)
		return
	case cn.allOid.T_uint4:
		s, _ := strconv.ParseUint(string(s), 10, 64)
		value = uint32(s)
		return
	case cn.allOid.T_tinyint:
		sVal, _ := strconv.ParseInt(string(s), 10, 64)
		if cn.databaseMode == "sqlserver" {
			value = uint8(sVal)
		} else {
			value = int8(sVal)
		}
		return
	case cn.allOid.T_float4, cn.allOid.T_float8, cn.allOid.T_float, cn.allOid.T_real:
		f, err := strconv.ParseFloat(string(s), 64)
		if nil != err {
			errorf("%s", err)
		}
		value = f
		return
	case cn.allOid.T_numeric: //numeric/decimal
		// num, _ := decimal.NewFromString(string(s))
		// return num
		return s
	case cn.allOid.T_money: //money
		newString := strings.ReplaceAll(string(s), ",", "")
		num, _ := decimal.NewFromString(newString)
		return num
	}
	value = s
	return
}

// appendEncodedText将参数转为文本格式big添加到buf中
func appendEncodedText(parameterStatus *parameterStatus, buf []byte, x interface{}) (value []byte) {
	switch v := x.(type) {
	case int64:
		value = strconv.AppendInt(buf, v, 10)
		return
	case float64:
		value = strconv.AppendFloat(buf, v, 'f', -1, 64)
		return
	case []byte:
		encodedBytea := encodeBytea(parameterStatus.serverVersion, v)
		value = appendEscapedText(buf, string(encodedBytea))
		return
	case string:
		value = appendEscapedText(buf, v)
		return
	case bool:
		value = strconv.AppendBool(buf, v)
		return
	case time.Time:
		value = append(buf, formatTs(v)...)
		return
	case nil:
		value = append(buf, "\\N"...)
		return
	default:
		errorf("encode: unknown type for %T", v)
	}
	panic("not reached")
}

func appendEscapedText(buf []byte, text string) (value []byte) {
	escapeNeeded, startPos := false, 0
	var c byte

	// 检查是否需要转义
	for i := 0; len(text) > i; i++ {
		c = text[i]
		if '\\' == c || '\n' == c || '\r' == c || '\t' == c {
			escapeNeeded, startPos = true, i
			break
		}
	}
	if !escapeNeeded {
		value = append(buf, text...)
		return
	}

	// copy直到第一个需要转义的字符
	result := append(buf, text[:startPos]...)
	for i := startPos; len(text) > i; i++ {
		c = text[i]
		switch c {
		case '\\':
			result = append(result, '\\', '\\')
		case '\n':
			result = append(result, '\\', 'n')
		case '\r':
			result = append(result, '\\', 'r')
		case '\t':
			result = append(result, '\\', 't')
		default:
			result = append(result, c)
		}
	}
	value = result
	return
}

func mustParse(f string, typ oid.Oid, s []byte, cn conn) (pt time.Time) {
	str := string(s)

	if (typ == cn.allOid.T_timestamptz || typ == cn.allOid.T_timetz) &&
		str[len(str)-3] == ':' {
		f += ":00"
	}
	pt, err := time.Parse(f, str)
	if nil != err {
		errorf("decode: %s", err)
	}
	return
}

var errInvalidTimestamp = errors.New("invalid timestamp")

type timestampParser struct {
	err error
}

func (p *timestampParser) expect(str string, char byte, pos int) {
	if nil != p.err {
		return
	}
	if len(str) < pos+1 {
		p.err = errInvalidTimestamp
		return
	}
	if c := str[pos]; char != c && nil == p.err {
		p.err = fmt.Errorf("expected '%v' at position %v; got '%v'", char, pos, c)
	}
}

func (p *timestampParser) mustAtoi(str string, begin int, end int) (result int) {
	if nil != p.err {
		return 0
	}
	if 0 > begin || 0 > end || end < begin || len(str) < end {
		p.err = errInvalidTimestamp
		result = 0
		return
	}
	result, err := strconv.Atoi(str[begin:end])
	if nil != err {
		if nil == p.err {
			p.err = fmt.Errorf("expected number; got '%v'", str)
		}
		result = 0
		return
	}
	return
}

func newLocationCache() (lc *locationCache) {
	lc = &locationCache{cache: make(map[int]*time.Location)}
	return
}

// Returns the cached timezone for the specified offset, creating and caching
// it if necessary.
func (c *locationCache) getLocation(offset int) (location *time.Location) {
	c.lock.Lock()
	defer c.lock.Unlock()

	location, ok := c.cache[offset]
	if !ok {
		location = time.FixedZone("", offset)
		c.cache[offset] = location
	}
	return
}

// 如果EnableInfinityTs没有被调用，"-infinity"和"infinity"将返回[]byte("-infinity")和[]byte("infinity")
// 将其传入time.Time时将导致报错"sql: Scan error on column index 0: unsupported driver -> Scanpair: []uint8 -> *time.Time"
//
// EnableInfinityTs被调用后，所有使用该驱动的连接都将把"-infinity"和"infinity"转为"timestamp",
// "timestamp with time zone"和"date"类型所预指定的最小和最大时间
// 转换time.Time类型的值时，任何等于预指定的最小值将被转为"-infinity"
// 等于或大于最大值将被转为"infinity"
// 当negative >= positive或者被调用多次将会报错
func EnableInfinityTs(negative time.Time, positive time.Time) {
	if infinityTsEnabled {
		panic(infinityTsEnabledAlready)
	}
	if !negative.Before(positive) {
		panic(infinityTsNegativeMustBeSmaller)
	}
	infinityTsNegative = negative
	infinityTsPositive = positive
	infinityTsEnabled = true
}

func disableInfinityTs() {
	infinityTsEnabled = false
}

func parseTs(currentLocation *time.Location, str string) (value interface{}) {
	switch str {
	case "-infinity":
		if infinityTsEnabled {
			return infinityTsNegative
		}
		value = []byte(str)
		return
	case "infinity":
		if infinityTsEnabled {
			return infinityTsPositive
		}
		value = []byte(str)
		return
	}
	value, err := ParseTimestamp(currentLocation, str)
	if nil != err {
		panic(err)
	}
	return
}

func parseTime(currentLocation *time.Location, str string) (value interface{}) {
	switch str {
	case "-infinity":
		if infinityTsEnabled {
			return infinityTsNegative
		}
		value = []byte(str)
		return
	case "infinity":
		if infinityTsEnabled {
			return infinityTsPositive
		}
		value = []byte(str)
		return
	}
	value, err := ParseTime(currentLocation, str)
	if nil != err {
		panic(err)
	}
	return
}

func parseDate(currentLocation *time.Location, str string) (value interface{}) {
	switch str {
	case "-infinity":
		if infinityTsEnabled {
			return infinityTsNegative
		}
		value = []byte(str)
		return
	case "infinity":
		if infinityTsEnabled {
			return infinityTsPositive
		}
		value = []byte(str)
		return
	}
	value, err := ParseDate(currentLocation, str)
	if nil != err {
		panic(err)
	}
	return
}

func parseCivilDate(str string) (value interface{}) {
	date, err := time.Parse("2006-01-02", str)
	if nil != err {
		panic(err)
	}
	t := civil.DateOf(date)
	return t
}

func parseCivilTime(str string) (value interface{}) {
	time, err := time.Parse("15:04:05", str)
	if nil != err {
		panic(err)
	}
	t := civil.TimeOf(time)
	return t
}

// ParseTimestamp将文本格式的字符串解析为time.Time类型
func ParseTimestamp(currentLocation *time.Location, str string) (pt time.Time, err error) {
	p := timestampParser{}

	monSep := strings.IndexRune(str, '-')
	// Gregorian格式，不是ISO格式
	year := p.mustAtoi(str, 0, monSep)
	daySep := monSep + 3
	month := p.mustAtoi(str, monSep+1, daySep)
	p.expect(str, '-', daySep)
	timeSep := daySep + 3
	day := p.mustAtoi(str, daySep+1, timeSep)

	minLen, isBC := monSep+len("01-01")+1, strings.HasSuffix(str, " BC")

	if isBC {
		minLen = minLen + 3
	}

	var hour int
	var minute int
	var second int
	if minLen < len(str) {
		p.expect(str, ' ', timeSep)
		minSep := timeSep + 3
		p.expect(str, ':', minSep)
		hour = p.mustAtoi(str, timeSep+1, minSep)
		secSep := minSep + 3
		p.expect(str, ':', secSep)
		minute = p.mustAtoi(str, minSep+1, secSep)
		secEnd := secSep + 3
		second = p.mustAtoi(str, secSep+1, secEnd)
	}
	remainderIdx := monSep + len("01-01 00:00:00") + 1

	nanoSec, tzOff := 0, 0

	if len(str) > remainderIdx && '.' == str[remainderIdx] {
		fracStart := remainderIdx + 1
		fracOff := strings.IndexAny(str[fracStart:], "-+ ")
		if 0 > fracOff {
			fracOff = len(str) - fracStart
		}
		fracSec := p.mustAtoi(str, fracStart, fracStart+fracOff)
		nanoSec = fracSec * (1000000000 / int(math.Pow(10, float64(fracOff))))

		remainderIdx = remainderIdx + fracOff + 1
	}
	if tzStart := remainderIdx; len(str) > tzStart && ('-' == str[tzStart] || '+' == str[tzStart]) {
		// 时区分隔符为'-' 或'+' (UTC的时区分隔符为+00)
		var tzSign int
		switch c := str[tzStart]; c {
		case '-':
			tzSign = -1
		case '+':
			tzSign = +1
		default:
			return time.Time{}, fmt.Errorf("expected '-' or '+' at position %v; got %v", tzStart, c)
		}
		tzHours := p.mustAtoi(str, tzStart+1, tzStart+3)
		remainderIdx = remainderIdx + 3
		var tzMin, tzSec int
		if len(str) > remainderIdx && ':' == str[remainderIdx] {
			tzMin = p.mustAtoi(str, remainderIdx+1, remainderIdx+3)
			remainderIdx = remainderIdx + 3
		}
		if len(str) > remainderIdx && ':' == str[remainderIdx] {
			tzSec = p.mustAtoi(str, remainderIdx+1, remainderIdx+3)
			remainderIdx = remainderIdx + 3
		}
		tzOff = tzSign * ((tzHours * 60 * 60) + (tzMin * 60) + tzSec)
	}
	var isoYear int

	if isBC {
		isoYear = 1 - year
		remainderIdx = remainderIdx + 3
	} else {
		isoYear = year
	}
	if len(str) > remainderIdx {
		return time.Time{}, fmt.Errorf("expected end of input, got %v", str[remainderIdx:])
	}
	t := time.Date(isoYear, time.Month(month), day, hour, minute, second, nanoSec, globalLocationCache.getLocation(tzOff))

	if nil != currentLocation {
		// lt := t.In(currentLocation)
		// _, newOff := lt.Zone()
		// if tzOff == newOff { t = lt }

		//使用当前数据库时区
		t = time.Date(isoYear, time.Month(month), day, hour, minute, second, nanoSec, currentLocation)
	}
	return t, p.err
}

func ParseTime(currentLocation *time.Location, str string) (pt time.Time, err error) {
	p := timestampParser{}
	var hour, minute, second int
	minSep := strings.IndexRune(str, ':')

	p.expect(str, ':', minSep)
	hour = p.mustAtoi(str, 0, minSep)
	secSep := minSep + 3
	p.expect(str, ':', secSep)
	minute = p.mustAtoi(str, minSep+1, secSep)
	secEnd := secSep + 3
	second = p.mustAtoi(str, secSep+1, secEnd)
	remainderIdx := secEnd

	nanoSec, tzOff := 0, 0

	if len(str) > remainderIdx && '.' == str[remainderIdx] {
		fracStart := remainderIdx + 1
		fracOff := strings.IndexAny(str[fracStart:], "-+ ")
		if 0 > fracOff {
			fracOff = len(str) - fracStart
		}
		fracSec := p.mustAtoi(str, fracStart, fracStart+fracOff)
		nanoSec = fracSec * (1000000000 / int(math.Pow(10, float64(fracOff))))

		remainderIdx = remainderIdx + fracOff + 1
	}
	if tzStart := remainderIdx; len(str) > tzStart && ('-' == str[tzStart] || '+' == str[tzStart]) {
		// 时区分隔符为'-' 或'+' (UTC的时区分隔符为+00)
		var tzSign int
		switch c := str[tzStart]; c {
		case '-':
			tzSign = -1
		case '+':
			tzSign = +1
		default:
			return time.Time{}, fmt.Errorf("expected '-' or '+' at position %v; got %v", tzStart, c)
		}
		tzHours := p.mustAtoi(str, tzStart+1, tzStart+3)
		remainderIdx = remainderIdx + 3
		var tzMin, tzSec int
		if len(str) > remainderIdx && ':' == str[remainderIdx] {
			tzMin = p.mustAtoi(str, remainderIdx+1, remainderIdx+3)
			remainderIdx = remainderIdx + 3
		}
		if len(str) > remainderIdx && ':' == str[remainderIdx] {
			tzSec = p.mustAtoi(str, remainderIdx+1, remainderIdx+3)
			remainderIdx = remainderIdx + 3
		}
		tzOff = tzSign * ((tzHours * 60 * 60) + (tzMin * 60) + tzSec)
	}

	if len(str) > remainderIdx {
		return time.Time{}, fmt.Errorf("expected end of input, got %v", str[remainderIdx:])
	}
	t := time.Date(1, time.Month(1), 1, hour, minute, second, nanoSec, globalLocationCache.getLocation(tzOff))
	if nil != currentLocation {
		lt := t.In(currentLocation)
		_, newOff := lt.Zone()
		if tzOff == newOff {
			t = lt
		}
	}
	pt = t
	err = p.err
	return
}

func ParseDate(currentLocation *time.Location, str string) (dt time.Time, err error) {
	p := timestampParser{}

	monSep := strings.IndexRune(str, '-')
	year := p.mustAtoi(str, 0, monSep)
	daySep := monSep + 3
	month := p.mustAtoi(str, monSep+1, daySep)
	p.expect(str, '-', daySep)
	timeSep := daySep + 3
	day := p.mustAtoi(str, daySep+1, timeSep)

	minLen := monSep + len("01-01") + 1
	isBC := strings.HasSuffix(str, " BC")
	if isBC {
		minLen = minLen + 3
	}

	var isoYear int

	if isBC {
		isoYear = 1 - year
	} else {
		isoYear = year
	}
	t := time.Date(isoYear, time.Month(month), day,
		0, 0, 0, 0,
		globalLocationCache.getLocation(0))
	return t, p.err
}

// formatTs将t格式化为kingbase标准
func formatTs(t time.Time) (ts []byte) {
	if infinityTsEnabled {
		// t <= -infinity : ! (t > -infinity)
		if !t.After(infinityTsNegative) {
			return []byte("-infinity")
		}
		// t >= infinity : ! (!t < infinity)
		if !t.Before(infinityTsPositive) {
			return []byte("infinity")
		}
	}
	ts = FormatTimestamp(t)
	return
}
func formatD(t civil.Date) (ts []byte) {
	date := fmt.Sprintf("%d-%d-%d", t.Year, int(t.Month), t.Day)
	ts = []byte(date)
	return
}
func formatT(t civil.Time) (ts []byte) {
	time := fmt.Sprintf("%d:%d:%d", t.Hour, t.Minute, t.Second)
	ts = []byte(time)
	return
}

// FormatTimestamp将t格式化为kingbase的timestamps的文本格式
func FormatTimestamp(t time.Time) (ts []byte) {
	// 要在0001 A.D.前的日期，用" BC"作为后缀，而不是通过Go来发送负号
	// "0000"在ISO中表示"1 BC", "-0001" is "2 BC"
	bc := false
	if 0 >= t.Year() {
		// 反转年标志, 并加1,比如: "0"为"1","-10"为"11"
		t = t.AddDate((-t.Year())*2+1, 0, 0)
		bc = true
	}
	b := []byte(t.Format("2006-01-02 15:04:05.999999999Z07:00"))

	_, offset := t.Zone()
	offset %= 60
	if 0 != offset {
		if 0 > offset {
			offset = -offset
		}

		b = append(b, ':')
		if 10 > offset {
			b = append(b, '0')
		}
		b = strconv.AppendInt(b, int64(offset), 10)
	}

	if true == bc {
		b = append(b, " BC"...)
	}
	ts = b
	return
}

// 解析从后端接收到的bytea类型的值，"hex"和"escape"格式均支持
func parseBytea(s []byte) (result []byte, err error) {
	if 2 <= len(s) && bytes.Equal(s[:2], []byte("\\x")) {
		// bytea_output = hex
		s = s[2:]
		result = make([]byte, hex.DecodedLen(len(s)))
		_, err := hex.Decode(result, s)
		if nil != err {
			return nil, err
		}
	} else {
		// bytea_output = escape
		for 0 < len(s) {
			if '\\' == s[0] {
				// 转义的 '\\'
				if 2 <= len(s) && '\\' == s[1] {
					result = append(result, '\\')
					s = s[2:]
					continue
				}

				// '\\'后跟着一个八进制数
				if 4 > len(s) {
					return nil, fmt.Errorf("invalid bytea sequence %v", s)
				}
				r, err := strconv.ParseInt(string(s[1:4]), 8, 9)
				if nil != err {
					return nil, fmt.Errorf("could not parse bytea value: %s", err.Error())
				}
				result = append(result, byte(r))
				s = s[4:]
			} else {
				// 遇到未转义字节时尝试尽可能多的读取
				i := bytes.IndexByte(s, '\\')
				if -1 == i {
					result = append(result, s...)
					break
				}
				result = append(result, s[:i]...)
				s = s[i:]
			}
		}
	}
	err = nil
	return
}

func encodeBytea(serverVersion int, v []byte) (result []byte) {
	if 90000 <= serverVersion {
		// 如果服务端支持则使用hex格式
		result = make([]byte, 2+hex.EncodedLen(len(v)))
		result[0] = '\\'
		result[1] = 'x'
		hex.Encode(result[2:], v)
	} else {
		for _, b := range v {
			if '\\' == b {
				result = append(result, '\\', '\\')
			} else if 0x20 > b || 0x7e < b {
				result = append(result, []byte(fmt.Sprintf("\\%03o", b))...)
			} else {
				result = append(result, b)
			}
		}
	}
	return
}

// Scan实现了Scanner接口
func (nt *NullTime) Scan(value interface{}) (err error) {
	nt.Time, nt.Valid = value.(time.Time)
	err = nil
	return
}

// Value实现了driver Valuer接口
func (nt NullTime) Value() (dv driver.Value, err error) {
	if !nt.Valid {
		dv = nil
		err = nil
		return
	}
	dv = nt.Time
	err = nil
	return
}
