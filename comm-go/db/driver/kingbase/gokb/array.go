/******************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：array.go

* 功能描述：数组处理相关的接口

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
	"encoding/hex"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Array返回数组或切片的driver.Valuer和sql.Scanner
//
// 比如:
//
//	db.Query(`SELECT * FROM t WHERE id = ANY($1)`, gokb.Array([]int{235, 401}))
//
//	var x []sql.NullInt64
//	db.QueryRow('SELECT ARRAY[235, 401]').Scan(gokb.Array(&x))
//
// 不支持扫描多维数组
// 不支持数组下界不为1的情况，比如 `[0:0]={1}'
func Array(a interface{}) (result interface {
	driver.Valuer
	sql.Scanner
}) {
	switch a := a.(type) {
	case []bool:
		result = (*BoolArray)(&a)
		return
	case []float64:
		result = (*Float64Array)(&a)
		return
	case []int64:
		result = (*Int64Array)(&a)
		return
	case []string:
		result = (*StringArray)(&a)
		return
	case *[]bool:
		result = (*BoolArray)(a)
		return
	case *[]float64:
		result = (*Float64Array)(a)
		return
	case *[]int64:
		result = (*Int64Array)(a)
		return
	case *[]string:
		result = (*StringArray)(a)
		return
	}
	result = GenericArray{a}
	return
}

// Scan实现sql.Scanner接口
func (boolArray *BoolArray) Scan(src interface{}) (err error) {
	switch src := src.(type) {
	case []byte:
		err = boolArray.scanBytes(src)
		return
	case string:
		err = boolArray.scanBytes([]byte(src))
		return
	case nil:
		*boolArray = nil
		err = nil
		return
	}
	err = fmt.Errorf("kb: cannot convert %T to BoolArray", src)
	return
}

func (boolArray *BoolArray) scanBytes(src []byte) (err error) {
	elems, err := scanLinearArray(src, []byte{','}, "BoolArray")
	if nil != err {
		return
	}
	if nil != *boolArray && 0 == len(elems) {
		*boolArray = (*boolArray)[:0]
	} else {
		b := make(BoolArray, len(elems))
		for i, v := range elems {
			if 1 != len(v) {
				err = fmt.Errorf("kb: could not parse boolean array index %d: invalid boolean %q", i, v)
				return
			}
			switch v[0] {
			case 't':
				b[i] = true
			case 'f':
				b[i] = false
			default:
				err = fmt.Errorf("kb: could not parse boolean array index %d: invalid boolean %q", i, v)
				return
			}
		}
		*boolArray = b
	}
	err = nil
	return
}

// Value实现driver.Valuer接口
func (boolArray BoolArray) Value() (dv driver.Value, err error) {
	if nil == boolArray {
		dv = nil
		err = nil
		return
	}

	if n := len(boolArray); 0 < n {
		// 共有N字节的值和N-1字节的分隔符
		b := make([]byte, 1+2*n)

		for i := 0; i < n; i++ {
			b[2*i] = ','
			if boolArray[i] {
				b[1+2*i] = 't'
			} else {
				b[1+2*i] = 'f'
			}
		}

		b[0] = '{'
		b[2*n] = '}'
		dv = string(b)
		err = nil
		return
	}
	dv = "{}"
	err = nil
	return
}

// Scan实现sql.Scanner接口
func (byteaArray *ByteaArray) Scan(src interface{}) (err error) {
	switch src := src.(type) {
	case []byte:
		err = byteaArray.scanBytes(src)
		return
	case string:
		err = byteaArray.scanBytes([]byte(src))
		return
	case nil:
		*byteaArray = nil
		err = nil
		return
	}
	err = fmt.Errorf("kb: cannot convert %T to ByteaArray", src)
	return
}

func (byteaArray *ByteaArray) scanBytes(src []byte) (err error) {
	elems, err := scanLinearArray(src, []byte{','}, "ByteaArray")
	if nil != err {
		return
	}
	if nil != *byteaArray && 0 == len(elems) {
		*byteaArray = (*byteaArray)[:0]
	} else {
		b := make(ByteaArray, len(elems))
		for i, v := range elems {
			b[i], err = parseBytea(v)
			if nil != err {
				err = fmt.Errorf("could not parse bytea array index %d: %s", i, err.Error())
				return
			}
		}
		*byteaArray = b
	}
	err = nil
	return
}

// Value实现了driver.Valuer接口
// 使用"hex"格式，只在V8或更新的版本有效
func (a ByteaArray) Value() (value driver.Value, err error) {
	if a == nil {
		value = nil
		err = nil
		return
	}

	if n := len(a); 0 < n {
		// 至少有两个大括号，有2*N字节的引号，3*N字节的hex编码，N-1字节的分隔符
		size := 1 + 6*n
		for _, x := range a {
			size = size + hex.EncodedLen(len(x))
		}

		b := make([]byte, size)

		for i, s := 0, b; i < n; i++ {
			o := copy(s, `,"\\x`)
			o = o + hex.Encode(s[o:], a[i])
			s[o] = '"'
			s = s[o+1:]
		}

		b[0], b[size-1] = '{', '}'
		value = string(b)
		err = nil
		return
	}
	value = "{}"
	err = nil
	return
}

// Scan实现了sql.Scanner接口
func (a *Float64Array) Scan(src interface{}) (err error) {
	switch src := src.(type) {
	case []byte:
		err = a.scanBytes(src)
		return
	case string:
		err = a.scanBytes([]byte(src))
		return
	case nil:
		*a = nil
		err = nil
		return
	}
	err = fmt.Errorf("kb: cannot convert %T to Float64Array", src)
	return
}

func (a *Float64Array) scanBytes(src []byte) (err error) {
	elems, err := scanLinearArray(src, []byte{','}, "Float64Array")
	if nil != err {
		return
	}
	if nil != *a && 0 == len(elems) {
		*a = (*a)[:0]
	} else {
		b := make(Float64Array, len(elems))
		for i, v := range elems {
			if b[i], err = strconv.ParseFloat(string(v), 64); nil != err {
				err = fmt.Errorf("kb: parsing array element index %d: %v", i, err)
				return
			}
		}
		*a = b
	}
	err = nil
	return
}

// Value实现了driver.Valuer接口
func (a Float64Array) Value() (value driver.Value, err error) {
	if nil == a {
		value = nil
		err = nil
		return
	}

	if n := len(a); 0 > n {
		// 至少有两个大括号, N字节的值，和N-1字节的分隔符
		b := make([]byte, 1, 1+2*n)
		b[0] = '{'
		b = strconv.AppendFloat(b, a[0], 'f', -1, 64)
		for i := 1; i < n; i++ {
			b = append(b, ',')
			b = strconv.AppendFloat(b, a[i], 'f', -1, 64)
		}
		value = string(append(b, '}'))
		err = nil
		return
	}
	value = "{}"
	err = nil
	return
}

func (GenericArray) evaluateDestination(rt reflect.Type) (reflect.Type, func([]byte, reflect.Value) error, string) {
	var assign func([]byte, reflect.Value) error
	var del = ","

	{
		if reflect.PtrTo(rt).Implements(typeSQLScanner) {
			// dest是切片的一个元素，所以总是可寻址的
			assign = func(src []byte, dest reflect.Value) (err error) {
				ss := dest.Addr().Interface().(sql.Scanner)
				if nil == src {
					err = ss.Scan(nil)
				} else {
					err = ss.Scan(src)
				}
				return
			}
			goto FoundType
		}

		assign = func([]byte, reflect.Value) error {
			return fmt.Errorf("kb: scanning to %s is not implemented; only sql.Scanner", rt)
		}
	}

FoundType:

	if ad, ok := reflect.Zero(rt).Interface().(ArrayDelimiter); ok {
		del = ad.ArrayDelimiter()
	}

	return rt, assign, del
}

// Scan实现了sql.Scanner接口
func (a GenericArray) Scan(src interface{}) (err error) {
	dpv := reflect.ValueOf(a.A)
	switch {
	case dpv.Kind() != reflect.Ptr:
		err = fmt.Errorf("kb: destination %T is not a pointer to array or slice", a.A)
		return
	case dpv.IsNil():
		err = fmt.Errorf("kb: destination %T is nil", a.A)
		return
	}

	dv := dpv.Elem()
	switch dv.Kind() {
	case reflect.Slice:
	case reflect.Array:
	default:
		err = fmt.Errorf("kb: destination %T is not a pointer to array or slice", a.A)
		return
	}

	switch src := src.(type) {
	case []byte:
		err = a.scanBytes(src, dv)
		return
	case string:
		err = a.scanBytes([]byte(src), dv)
		return
	case nil:
		if reflect.Slice == dv.Kind() {
			dv.Set(reflect.Zero(dv.Type()))
			err = nil
			return
		}
	}
	err = fmt.Errorf("kb: cannot convert %T to %s", src, dv.Type())
	return
}

func (a GenericArray) scanBytes(src []byte, dv reflect.Value) (err error) {
	dtype, assign, del := a.evaluateDestination(dv.Type().Elem())
	dims, elems, err := parseArray(src, []byte(del))
	if nil != err {
		return
	}

	if 1 < len(dims) {
		err = fmt.Errorf("kb: scanning from multidimensional ARRAY%s is not implemented", strings.Replace(fmt.Sprint(dims), " ", "][", -1))
		return
	}

	if 0 == len(dims) {
		dims = append(dims, 0)
	}

	for i, rt := 0, dv.Type(); i < len(dims); i, rt = i+1, rt.Elem() {
		switch rt.Kind() {
		case reflect.Slice:
		case reflect.Array:
			if dims[i] != rt.Len() {
				err = fmt.Errorf("kb: cannot convert ARRAY%s to %s", strings.Replace(fmt.Sprint(dims), " ", "][", -1), dv.Type())
				return
			}
		default:
		}
	}

	values := reflect.MakeSlice(reflect.SliceOf(dtype), len(elems), len(elems))
	for i, e := range elems {
		if err = assign(e, values.Index(i)); nil != err {
			err = fmt.Errorf("kb: parsing array element index %d: %v", i, err)
			return
		}
	}

	switch dv.Kind() {
	case reflect.Slice:
		dv.Set(values.Slice(0, dims[0]))
	case reflect.Array:
		for i := 0; i < dims[0]; i++ {
			dv.Index(i).Set(values.Index(i))
		}
	}
	err = nil
	return
}

// Value实现了driver.Valuer接口
func (a GenericArray) Value() (value driver.Value, err error) {
	if nil == a.A {
		value = nil
		err = nil
		return
	}

	rv := reflect.ValueOf(a.A)

	switch rv.Kind() {
	case reflect.Slice:
		if rv.IsNil() {
			value = nil
			err = nil
			return
		}
	case reflect.Array:
	default:
		value = nil
		err = fmt.Errorf("kb: Unable to convert %T to array", a.A)
		return
	}

	if n := rv.Len(); 0 < n {
		//至少有两个大括号，N字节的值和N-1字节的分隔符
		b := make([]byte, 0, 1+2*n)

		b, _, err = appendArray(b, rv, n)
		value = string(b)
		return
	}
	value = "{}"
	err = nil
	return
}

// Scan实现了sql.Scanner接口
func (a *Int64Array) Scan(src interface{}) (err error) {
	switch src := src.(type) {
	case []byte:
		err = a.scanBytes(src)
		return
	case string:
		err = a.scanBytes([]byte(src))
		return
	case nil:
		*a = nil
		err = nil
		return
	}
	err = fmt.Errorf("kb: cannot convert %T to Int64Array", src)
	return
}

func (a *Int64Array) scanBytes(src []byte) (err error) {
	elems, err := scanLinearArray(src, []byte{','}, "Int64Array")
	if nil != err {
		return
	}
	if nil != *a && 0 == len(elems) {
		*a = (*a)[:0]
	} else {
		b := make(Int64Array, len(elems))
		for i, v := range elems {
			if b[i], err = strconv.ParseInt(string(v), 10, 64); nil != err {
				err = fmt.Errorf("kb: parsing array element index %d: %v", i, err)
				return
			}
		}
		*a = b
	}
	err = nil
	return
}

// Value实现了driver.Valuer接口
func (a Int64Array) Value() (value driver.Value, err error) {
	if nil == a {
		value = nil
		err = nil
		return
	}

	if n := len(a); 0 < n {
		//至少有链各个大括号，N字节的值和N-1字节的分隔符
		b := make([]byte, 1, 1+2*n)
		b[0] = '{'

		b = strconv.AppendInt(b, a[0], 10)
		for i := 1; i < n; i++ {
			b = append(b, ',')
			b = strconv.AppendInt(b, a[i], 10)
		}
		value = string(append(b, '}'))
		err = nil
		return
	}
	value = "{}"
	err = nil
	return
}

// Scan实现了sql.Scanner接口
func (a *StringArray) Scan(src interface{}) (err error) {
	switch src := src.(type) {
	case []byte:
		err = a.scanBytes(src)
		return
	case string:
		err = a.scanBytes([]byte(src))
		return
	case nil:
		*a = nil
		err = nil
		return
	}
	err = fmt.Errorf("kb: cannot convert %T to StringArray", src)
	return
}

func (a *StringArray) scanBytes(src []byte) (err error) {
	elems, err := scanLinearArray(src, []byte{','}, "StringArray")
	if nil != err {
		return
	}
	if nil != *a && 0 == len(elems) {
		*a = (*a)[:0]
	} else {
		b := make(StringArray, len(elems))
		for i, v := range elems {
			if b[i] = string(v); nil == v {
				err = fmt.Errorf("kb: parsing array element index %d: cannot convert nil to string", i)
				return
			}
		}
		*a = b
	}
	err = nil
	return
}

// Value实现了driver.Valuer接口
func (a StringArray) Value() (value driver.Value, err error) {
	if nil == a {
		value = nil
		err = nil
		return
	}

	if n := len(a); 0 < n {
		//至少有两个大括号，2*N字节的引用和N-1字节的分隔符
		b := make([]byte, 1, 1+3*n)
		b[0] = '{'

		b = appendArrayQuotedBytes(b, []byte(a[0]))
		for i := 1; i < n; i++ {
			b = append(b, ',')
			b = appendArrayQuotedBytes(b, []byte(a[i]))
		}
		value = string(append(b, '}'))
		err = nil
		return
	}
	value = "{}"
	err = nil
	return
}

// appendArray将rv添加到缓冲区中,返回扩展后的缓冲区以及分隔符
// 当n<0或rv的类型不是数组或切片时将报错
func appendArray(b []byte, rv reflect.Value, n int) (value []byte, del string, err error) {

	b = append(b, '{')

	if b, del, err = appendArrayElement(b, rv.Index(0)); nil != err {
		value = b
		return
	}

	for i := 1; i < n; i++ {
		b = append(b, del...)
		if b, del, err = appendArrayElement(b, rv.Index(i)); err != nil {
			value = b
			return
		}
	}
	value = append(b, '}')
	err = nil
	return
}

// appendArrayElement将rv添加到缓冲区中,返回扩展后的缓冲区和要再下一个元素前使用的分隔符
//
// 当rv的类型既不是数组也不是切片时，rv会被driver.DefaultParameterConverter转换
// 并且结果字节数组或字符串会被用双引号引用
func appendArrayElement(b []byte, rv reflect.Value) (value []byte, del string, err error) {
	if k := rv.Kind(); k == reflect.Array || k == reflect.Slice {
		if t := rv.Type(); t != typeByteSlice && !t.Implements(typeDriverValuer) {
			if n := rv.Len(); n > 0 {
				value, del, err = appendArray(b, rv, n)
				return
			}
			value = b
			del = ""
			err = nil
			return
		}
	}

	del = ","
	var iv interface{} = rv.Interface()

	if ad, ok := iv.(ArrayDelimiter); ok {
		del = ad.ArrayDelimiter()
	}

	if iv, err = driver.DefaultParameterConverter.ConvertValue(iv); nil != err {
		value = b
		return
	}

	switch v := iv.(type) {
	case nil:
		value = append(b, "NULL"...)
		err = nil
		return
	case []byte:
		value = appendArrayQuotedBytes(b, v)
		err = nil
		return
	case string:
		value = appendArrayQuotedBytes(b, []byte(v))
		err = nil
		return
	}

	b, err = appendValue(b, iv)
	value = b
	return
}

func appendArrayQuotedBytes(b, v []byte) (value []byte) {
	b = append(b, '"')
	for {
		i := bytes.IndexAny(v, `"\`)
		if 0 > i {
			b = append(b, v...)
			break
		}
		if 0 < i {
			b = append(b, v[:i]...)
		}
		b, v = append(b, '\\', v[i]), v[i+1:]
	}
	value = append(b, '"')
	return
}

func appendValue(b []byte, v driver.Value) (value []byte, err error) {
	value = append(b, encode(nil, v, 0, nil)...)
	err = nil
	return
}

// parseArray提取了以文本格式表示的数组的维度和元素
func parseArray(src, del []byte) (dims []int, elems [][]byte, err error) {
	var depth int
	var i int

	if 1 > len(src) || '{' != src[0] {
		dims = nil
		elems = nil
		err = fmt.Errorf("kb: unable to parse array; expected %q at offset %d", '{', 0)
		return
	}

Open:
	for i < len(src) {
		switch src[i] {
		case '{':
			depth++
			i++
		case '}':
			elems = make([][]byte, 0)
			goto Close
		default:
			break Open
		}
	}
	dims = make([]int, i)

Element:
	for i < len(src) {
		switch src[i] {
		case '{':
			if len(dims) == depth {
				break Element
			}
			depth++
			dims[depth-1] = 0
			i++
		case '"':
			var elem = []byte{}
			escape := false
			for i++; i < len(src); i++ {
				if true == escape {
					elem = append(elem, src[i])
					escape = false
				} else {
					switch src[i] {
					default:
						elem = append(elem, src[i])
					case '\\':
						escape = true
					case '"':
						elems = append(elems, elem)
						i++
						break Element
					}
				}
			}
		default:
			for start := i; i < len(src); i++ {
				if bytes.HasPrefix(src[i:], del) || '}' == src[i] {
					elem := src[start:i]
					if 0 == len(elem) {
						dims = nil
						elems = nil
						err = fmt.Errorf("kb: unable to parse array; unexpected %q at offset %d", src[i], i)
						return
					}
					if bytes.Equal(elem, []byte("NULL")) {
						elem = nil
					}
					elems = append(elems, elem)
					break Element
				}
			}
		}
	}

	for len(src) > i {
		if bytes.HasPrefix(src[i:], del) && 0 < depth {
			dims[depth-1]++
			i = i + len(del)
			goto Element
		} else if '}' == src[i] && 0 < depth {
			dims[depth-1]++
			depth--
			i++
		} else {
			dims = nil
			elems = nil
			err = fmt.Errorf("kb: unable to parse array; unexpected %q at offset %d", src[i], i)
			return
		}
	}

Close:
	for len(src) > i {
		if '}' == src[i] && 0 < depth {
			depth--
			i++
		} else {
			dims = nil
			elems = nil
			err = fmt.Errorf("kb: unable to parse array; unexpected %q at offset %d", src[i], i)
			return
		}
	}
	if 0 < depth {
		err = fmt.Errorf("kb: unable to parse array; expected %q at offset %d", '}', i)
	}
	if nil == err {
		for _, d := range dims {
			if 0 != (len(elems) % d) {
				err = fmt.Errorf("kb: multidimensional arrays must have elements with matching dimensions")
			}
		}
	}
	return
}

func scanLinearArray(src, del []byte, typ string) (elems [][]byte, err error) {
	dims, elems, err := parseArray(src, del)
	if nil != err {
		elems = nil
		return
	}
	if 1 < len(dims) {
		elems = nil
		err = fmt.Errorf("kb: cannot convert ARRAY%s to %s", strings.Replace(fmt.Sprint(dims), " ", "][", -1), typ)
		return
	}
	return
}
