/******************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：error.go

* 功能描述：错误处理相关的接口

* 其它说明：


* 修改记录：
  1.修改时间：

  2.修改人：

  3.修改内容：

******************************************************************************/

package gokb

import (
	"database/sql/driver"
	"fmt"
	"io"
	"net"
	"runtime"
)

func (ec ErrorCode) Name() string {
	return errorCodeNames[ec]
}

// Name返回错误类的名称，即标准错误码(最后三个字符为000的错误码)
func (ec ErrorClass) Name() string {
	return errorCodeNames[ErrorCode(ec+"000")]
}

func (ec ErrorCode) Class() ErrorClass {
	return ErrorClass(ec[0:2])
}

// 获取Error信息
func (err *Error) Get(k byte) (value string) {
	switch k {
	case 'S':
		value = err.Severity
		return
	case 'C':
		value = string(err.Code)
		return
	case 'M':
		value = err.Message
		return
	case 'D':
		value = err.Detail
		return
	case 'H':
		value = err.Hint
		return
	case 'P':
		value = err.Position
		return
	case 'p':
		value = err.InternalPosition
		return
	case 'q':
		value = err.InternalQuery
		return
	case 'W':
		value = err.Where
		return
	case 's':
		value = err.Schema
		return
	case 't':
		value = err.Table
		return
	case 'c':
		value = err.Column
		return
	case 'd':
		value = err.DataTypeName
		return
	case 'n':
		value = err.Constraint
		return
	case 'F':
		value = err.File
		return
	case 'L':
		value = err.Line
		return
	case 'R':
		value = err.Routine
		return
	}
	value = ""
	return
}

func parseError(r *readBuf) *Error {
	err := new(Error)
	for t := r.byte(); t != 0; t = r.byte() {
		msg := r.string()
		switch t {
		case 'S':
			err.Severity = msg
		case 'C':
			err.Code = ErrorCode(msg)
		case 'M':
			err.Message = msg
		case 'D':
			err.Detail = msg
		case 'H':
			err.Hint = msg
		case 'P':
			err.Position = msg
		case 'p':
			err.InternalPosition = msg
		case 'q':
			err.InternalQuery = msg
		case 'W':
			err.Where = msg
		case 's':
			err.Schema = msg
		case 't':
			err.Table = msg
		case 'c':
			err.Column = msg
		case 'd':
			err.DataTypeName = msg
		case 'n':
			err.Constraint = msg
		case 'F':
			err.File = msg
		case 'L':
			err.Line = msg
		case 'R':
			err.Routine = msg
		}
	}
	return err
}

// 如果错误等级为fatal则返回true
func (err *Error) Fatal() bool {
	return Efatal == err.Severity
}

func (err Error) Error() (s string) {
	s = "kb: " + err.Message
	return
}

func errorf(s string, args ...interface{}) {
	panic(fmt.Errorf("kb: %s", fmt.Sprintf(s, args...)))
}

func fmterrorf(s string, args ...interface{}) (err error) {
	err = fmt.Errorf("kb: %s", fmt.Sprintf(s, args...))
	return
}

func errRecoverNoErrBadConn(err *error) {
	e := recover()
	if nil == e {
		return
	}
	ok := false
	*err, ok = e.(error)
	if !ok {
		*err = fmt.Errorf("kb: unexpected error: %#v", e)
	}
	return
}

func (cn *conn) errRecover(err *error) {
	e := recover()
	switch v := e.(type) {
	case nil:
	case runtime.Error:
		cn.bad = true
		panic(v)
	case *Error:
		if v.Fatal() {
			*err = driver.ErrBadConn
		} else {
			*err = v
		}
		panic(fmt.Sprintf("kb: unexpected error: %v\n", e))
	case *net.OpError:
		cn.bad = true
		*err = v
	case error:
		if v == io.EOF || v.(error).Error() == "remote error: handshake failure" {
			*err = driver.ErrBadConn
		} else {
			*err = v
		}
	default:
		cn.bad = true
		panic(fmt.Sprintf("unknown error: %#v", e))
	}
	// 返回ErrBadConn时需要标记该连接为坏连接，因为*Tx不会再database/sql中进行标记
	if driver.ErrBadConn == *err {
		cn.bad = true
	}
}
