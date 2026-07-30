/******************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：copy.go

* 功能描述：COPY操作相关的接口

* 其它说明：

* 修改记录：
  1.修改时间：

  2.修改人：

  3.修改内容：

******************************************************************************/

package gokb

import (
	"database/sql/driver"
	"encoding/binary"
	"fmt"
)

// CopyIn创建一个可用Tx.Prepare()处理的'COPY FROM'预备语句
// 目标表需要在当前的search_path下
func CopyIn(table string, columns ...string) (stmt string) {
	stmt = "COPY " + QuoteIdentifier(table) + " ("
	for i, col := range columns {
		if 0 != i {
			stmt += ", "
		}
		stmt += QuoteIdentifier(col)
	}
	stmt += ") FROM STDIN"
	return stmt
}

// CopyInSchema创建一个可用Tx.Prepare()处理的'COPY FROM'预备语句
func CopyInSchema(schema, table string, columns ...string) (stmt string) {
	stmt = "COPY " + QuoteIdentifier(schema) + "." + QuoteIdentifier(table) + " ("
	for i, col := range columns {
		if 0 != i {
			stmt += ", "
		}
		stmt += QuoteIdentifier(col)
	}
	stmt += ") FROM STDIN"
	return stmt
}

func (cn *conn) prepareCopyIn(query string) (_ driver.Stmt, err error) {
	if !cn.isInTransaction() {
		return nil, errCopyNotSupportedOutsideTxn
	}

	ci := &copyin{
		cn:      cn,
		buffer:  make([]byte, 0, ciBufferSize),
		rowData: make(chan []byte),
		done:    make(chan bool, 1),
	}
	// 添加CopyData的标识符和四字节的消息长度
	ci.buffer = append(ci.buffer, 'd', 0, 0, 0, 0)

	wb := cn.writeBuf('Q')
	wb.string(query)
	cn.send(wb)

awaitCopyInResponse:
	for {
		t, r := cn.recv1()
		switch t {
		case 'G':
			if 0 != r.byte() {
				err = errBinaryCopyNotSupported
				break awaitCopyInResponse
			}
			go ci.resploop()
			return ci, nil
		case 'H':
			err = errCopyToNotSupported
			break awaitCopyInResponse
		case 'E':
			err = parseError(r)
		case 'Z':
			if nil == err {
				ci.setBad()
				errorf("unexpected ReadyForQuery in response to COPY")
			}
			cn.processReadyForQuery(r)
			return nil, err
		default:
			ci.setBad()
			errorf("unknown response for copy query: %q", t)
		}
	}

	// 出错，在返回前终止COPY
	wb = cn.writeBuf('f')
	wb.string(err.Error())
	cn.send(wb)

	for {
		t, r := cn.recv1()
		switch t {
		case 'c':
		case 'C':
		case 'E':
		case 'Z':
			// 完成，准备进行新的查询
			cn.processReadyForQuery(r)
			return nil, err
		default:
			ci.setBad()
			errorf("unknown response for CopyFail: %q", t)
		}
	}
}

func (ci *copyin) flush(buf []byte) {
	// 设置报文长度(不含报文标识)
	binary.BigEndian.PutUint32(buf[1:], uint32(len(buf)-1))

	_, err := ci.cn.c.Write(buf)
	if nil != err {
		panic(err)
	}
}

func (ci *copyin) resploop() {
	for {
		var r readBuf
		t, err := ci.cn.recvMessage(&r)
		if nil != err {
			ci.setBad()
			ci.setError(err)
			ci.done <- true
			return
		}
		switch t {
		case 'C': //命令完成
		case 'N':
			if n := ci.cn.noticeHandler; nil != n {
				n(parseError(&r))
			}
		case 'Z':
			ci.cn.processReadyForQuery(&r)
			ci.done <- true
			return
		case 'E':
			err := parseError(&r)
			ci.setError(err)
		default:
			ci.setBad()
			ci.setError(fmt.Errorf("unknown response during CopyIn: %q", t))
			ci.done <- true
			return
		}
	}
}

func (ci *copyin) setBad() {
	ci.Lock()
	ci.cn.bad = true
	ci.Unlock()
}

func (ci *copyin) isBad() (b bool) {
	ci.Lock()
	b = ci.cn.bad
	ci.Unlock()
	return b
}

func (ci *copyin) isErrorSet() (isSet bool) {
	ci.Lock()
	isSet = (nil != ci.err)
	ci.Unlock()
	return isSet
}

// setError()设置ci.err
// 调用者不能持有ci.Mutex.
func (ci *copyin) setError(err error) {
	ci.Lock()
	if nil == ci.err {
		ci.err = err
	}
	ci.Unlock()
}

func (ci *copyin) NumInput() (n int) {
	n = -1
	return
}

func (ci *copyin) Query(v []driver.Value) (r driver.Rows, err error) {
	err = ErrNotSupported
	r = nil
	return
}

// Exec向COPY流中异步地插入数据，并返回先前调用相同COPY stmt时的错误
//
// 需要调用Exec(nil)来同步COPY流
// 因为Stmt.Close()不会返回错误，所以需要从挂起的数据中获取可能出现的错误
func (ci *copyin) Exec(v []driver.Value) (driver.Result, error) {
	if ci.closed {
		return nil, errCopyInClosed
	}

	var err error
	if ci.isBad() {
		return nil, driver.ErrBadConn
	}
	defer ci.cn.errRecover(&err)

	if ci.isErrorSet() {
		return nil, ci.err
	}

	if 0 == len(v) {
		return driver.RowsAffected(0), ci.Close()
	}

	numValues := len(v)
	for i, value := range v {
		ci.buffer = appendEncodedText(&ci.cn.parameterStatus, ci.buffer, value)
		if numValues-1 > i {
			ci.buffer = append(ci.buffer, '\t')
		}
	}

	ci.buffer = append(ci.buffer, '\n')

	if ciBufferFlushSize < len(ci.buffer) {
		ci.flush(ci.buffer)
		//重置缓冲区，为报文标识和长度预留空间
		ci.buffer = ci.buffer[:5]
	}

	return driver.RowsAffected(0), nil
}

func (ci *copyin) Close() error {
	var err error
	if ci.closed {
		return nil
	}
	ci.closed = true

	if ci.isBad() {
		return driver.ErrBadConn
	}
	defer ci.cn.errRecover(&err)

	if 0 < len(ci.buffer) {
		ci.flush(ci.buffer)
	}
	err = ci.cn.sendSimpleMessage('c')
	if nil != err {
		return err
	}

	<-ci.done
	ci.cn.inCopy = false

	if ci.isErrorSet() {
		err = ci.err
		return err
	}
	return nil
}
